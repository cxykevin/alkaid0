package helper

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// chunkedReader 每次 Read 最多返回 chunk 字节，用于模拟 stdin 在任意边界被切分读取。
type chunkedReader struct {
	data  []byte
	chunk int
}

func (r *chunkedReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := len(p)
	if n > r.chunk {
		n = r.chunk
	}
	if n > len(r.data) {
		n = len(r.data)
	}
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

// bigJSONRequest 生成一条超过 64KiB 的 JSON-RPC 请求（模拟 fs 写入上限 1MiB 的大请求）。
func bigJSONRequest() string {
	return `{"jsonrpc":"2.0","id":1,"method":"fs/write","params":{"content":"` +
		strings.Repeat("x", 70*1024) + `"}}`
}

// startFramesCollector 启动一个只接收 TextMessage 的测试 WebSocket 服务端。
func startFramesCollector(t *testing.T) (string, <-chan []byte) {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	frames := make(chan []byte, 32)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			frames <- msg
		}
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http"), frames
}

// TestCopyStdinToWSFramesOneJSONValuePerFrame 验证 stdin 转发严格按完整 JSON 值分帧：
// 一条超过 64KiB 的消息必须只占一帧，同一行内的多条消息必须分别成帧。
func TestCopyStdinToWSFramesOneJSONValuePerFrame(t *testing.T) {
	wsURL, frames := startFramesCollector(t)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	big := bigJSONRequest()
	small := `{"jsonrpc":"2.0","id":2,"method":"ping"}`
	input := big + "\n" + small + "\n"

	// 用临时文件伪装 stdin，覆盖 copyStdinToWS 的真实调用路径
	stdinFile, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stdinFile.WriteString(input); err != nil {
		t.Fatal(err)
	}
	if _, err := stdinFile.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	origStdin := os.Stdin
	os.Stdin = stdinFile
	t.Cleanup(func() { os.Stdin = origStdin })

	if err := copyStdinToWS(conn); !errors.Is(err, io.EOF) {
		t.Fatalf("copyStdinToWS() error = %v, want io.EOF", err)
	}

	var got [][]byte
	deadline := time.After(3 * time.Second)
	for len(got) < 2 {
		select {
		case msg := <-frames:
			got = append(got, msg)
		case <-deadline:
			t.Fatalf("收到 %d 帧，期望 2 帧：大消息被拆帧或消息被合并", len(got))
		}
	}
	select {
	case msg := <-frames:
		t.Fatalf("收到多余帧（长度 %d），帧边界错误", len(msg))
	case <-time.After(300 * time.Millisecond):
	}

	if len(got[0]) <= 65536 {
		t.Fatalf("第一帧长度 %d，未包含完整的大消息", len(got[0]))
	}
	if string(got[0]) != big {
		t.Fatalf("第一帧不是完整大消息：长度 %d，期望 %d", len(got[0]), len(big))
	}
	if string(got[1]) != small {
		t.Fatalf("第二帧 = %q，期望 %q", string(got[1]), small)
	}
}

// TestReadJSONMessages 覆盖分帧函数在各类输入边界上的行为。
func TestReadJSONMessages(t *testing.T) {
	big := bigJSONRequest()
	if len(big) <= 65536 {
		t.Fatalf("测试数据仅 %d 字节，未超过 64KiB，无法覆盖分帧缺陷", len(big))
	}
	pretty := `{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "ping"
}`
	tests := []struct {
		name    string
		input   string
		chunk   int // >0 时按 chunk 字节切分 Read，模拟跨读取边界
		want    []string
		wantErr bool
	}{
		{
			name:  "empty input",
			input: "",
		},
		{
			name:  "pure whitespace",
			input: " \n\t\r\n  \n",
		},
		{
			name:  "single message",
			input: `{"jsonrpc":"2.0","id":1,"method":"ping"}`,
			want:  []string{`{"jsonrpc":"2.0","id":1,"method":"ping"}`},
		},
		{
			name:  "multiple messages in one line",
			input: `{"id":1}{"id":2} {"id":3}`,
			want:  []string{`{"id":1}`, `{"id":2}`, `{"id":3}`},
		},
		{
			name: "messages separated by newlines",
			input: `{"id":1}
{"id":2}
`,
			want: []string{`{"id":1}`, `{"id":2}`},
		},
		{
			name:  "pretty multiline message",
			input: pretty + "\n",
			want:  []string{pretty},
		},
		{
			name:  "message larger than 64KiB",
			input: big + "\n",
			chunk: 4096,
			want:  []string{big},
		},
		{
			name:  "message split byte by byte",
			input: `{"id":12345,"method":"ping","params":{}}`,
			chunk: 1,
			want:  []string{`{"id":12345,"method":"ping","params":{}}`},
		},
		{
			name:    "invalid json only",
			input:   `not-json`,
			wantErr: true,
		},
		{
			name:    "valid message then invalid json",
			input:   `{"id":1}{"id":2`,
			want:    []string{`{"id":1}`},
			wantErr: true,
		},
		{
			name:    "truncated object at EOF",
			input:   `{"id":1`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r io.Reader = strings.NewReader(tt.input)
			if tt.chunk > 0 {
				r = &chunkedReader{data: []byte(tt.input), chunk: tt.chunk}
			}
			var got []string
			err := readJSONMessages(r, func(msg []byte) error {
				if !json.Valid(msg) {
					t.Errorf("回调收到非法 JSON 帧: %q", msg)
				}
				got = append(got, string(msg))
				return nil
			})

			if tt.wantErr {
				if err == nil {
					t.Fatalf("error = nil, want 非法 JSON 错误")
				}
				if errors.Is(err, io.EOF) {
					t.Fatalf("error = io.EOF, want 非法 JSON 错误")
				}
				if !strings.Contains(err.Error(), "invalid JSON message") {
					t.Fatalf("error = %v, 错误信息不清晰", err)
				}
			} else if !errors.Is(err, io.EOF) {
				t.Fatalf("error = %v, want io.EOF", err)
			}

			if len(got) != len(tt.want) {
				t.Fatalf("帧数 = %d (%q), want %d (%q)", len(got), got, len(tt.want), tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("第 %d 帧 = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestReadJSONMessagesEmitError 验证写回调失败时错误立即向上传播，不被吞掉。
func TestReadJSONMessagesEmitError(t *testing.T) {
	sentinel := errors.New("write failed")
	calls := 0
	err := readJSONMessages(strings.NewReader("{\"id\":1}\n{\"id\":2}\n"), func([]byte) error {
		calls++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error = %v, want %v", err, sentinel)
	}
	if calls != 1 {
		t.Fatalf("emit 调用 %d 次, want 1（回调出错后应立即停止）", calls)
	}
}
