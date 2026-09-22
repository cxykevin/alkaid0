package connect

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/gorilla/websocket"
)

// freeTCPSlot 申请一个当前空闲的 TCP 端口（立刻释放，交给 StartWs 监听）。
func freeTCPSlot(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// dialWS 在服务端监听就绪前重试拨号。
func dialWS(t *testing.T, url string, header http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var (
		conn *websocket.Conn
		resp *http.Response
		err  error
	)
	for time.Now().Before(deadline) {
		conn, resp, err = websocket.DefaultDialer.Dial(url, header)
		if err == nil {
			return conn, resp, nil
		}
		// 只有连接层失败（监听尚未就绪）才重试；像 401 这样的握手失败直接返回
		if resp != nil {
			return nil, resp, err
		}
		time.Sleep(50 * time.Millisecond)
	}
	return conn, resp, err
}

// TestStartWs_TransportEndToEnd：真实起一个 WebSocket 服务端，覆盖
// 无 key 拒绝升级、Authorization 头鉴权、一帧一条消息原样交给 handler、
// 响应按帧写回、断连触发 closeConn。
func TestStartWs_TransportEndToEnd(t *testing.T) {
	port := freeTCPSlot(t)
	const path = "/jsonrpc"
	restoreCfg := config.GlobalConfigSwap(cfgStructs.Config{
		Server: cfgStructs.RPCConfig{Host: "127.0.0.1", Port: uint16(port), Path: path, Key: "test-key"},
	})
	defer restoreCfg()

	oldServer := wsHTTPServer
	defer func() { wsHTTPServer = oldServer }()

	gotMsg := make(chan string, 4)
	handler := func(msg string, _ func(string) error, _ uint64) (string, bool) {
		gotMsg <- msg
		return "{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"ok\":true}}", false
	}
	closed := make(chan uint64, 4)
	if err := StartWs(handler, func(id uint64) { closed <- id }); err != nil {
		t.Fatalf("StartWs failed: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = ShutdownWs(ctx)
	})

	url := "ws://127.0.0.1:" + strconv.Itoa(port) + path

	// 1) 无 key：必须拒绝升级
	conn, resp, err := dialWS(t, url, nil)
	if err == nil {
		conn.Close()
		t.Fatal("缺少 key 时不应升级成功")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("缺少 key 应返回 401，got resp=%v err=%v", resp, err)
	}

	// 2) 错误 key：同样拒绝
	conn, resp, err = dialWS(t, url+"?token=wrong-key", nil)
	if err == nil {
		conn.Close()
		t.Fatal("错误 key 不应升级成功")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("错误 key 应返回 401，got resp=%v err=%v", resp, err)
	}

	// 3) Authorization: Bearer <key>：升级成功（与查询参数同一套鉴权）
	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer test-key")
	conn, _, err = dialWS(t, url, hdr)
	if err != nil {
		t.Fatalf("Authorization 头携带正确 key 时应升级成功: %v", err)
	}

	// 4) 一帧一条消息：handler 收到的应是完整的原始 JSON 文本
	msg := "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"ping\",\"params\":{\"n\":1}}"
	if err := conn.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
		conn.Close()
		t.Fatalf("写入消息失败: %v", err)
	}
	select {
	case got := <-gotMsg:
		if got != msg {
			t.Errorf("handler 应收到原样的整帧文本:\n got %q\nwant %q", got, msg)
		}
	case <-time.After(10 * time.Second):
		conn.Close()
		t.Fatal("handler 未收到消息")
	}

	// 5) 响应按帧写回
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, reply, err := conn.ReadMessage()
	if err != nil {
		conn.Close()
		t.Fatalf("读取响应失败: %v", err)
	}
	if !strings.Contains(string(reply), "\"ok\":true") {
		t.Errorf("响应内容不对: %q", reply)
	}

	// 6) 断连触发 closeConn（连接清理）
	conn.Close()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("断开连接后未调用 closeConn")
	}
}
