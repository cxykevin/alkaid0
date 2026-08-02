package request

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	configStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/provider/mask"
	"github.com/cxykevin/alkaid0/provider/request/structs"
	"github.com/cxykevin/alkaid0/storage"
)

func e2eJWT() string {
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"1234567890","name":"John"}`))
	sig := base64.RawURLEncoding.EncodeToString([]byte("a-very-long-signature-0123456789"))
	return hdr + "." + payload + "." + sig
}

// TestMaskE2E 验证：出站 payload 已脱敏（不含原秘密），响应经流式还原后恢复原文。
// 用自建 httptest 服务器捕获请求体，并以 3 字节小块 SSE 回显，覆盖跨 chunk 流式还原。
func TestMaskE2E(t *testing.T) {
	restore := config.GlobalConfigSwap(configStructs.Config{
		DataMask: configStructs.DataMaskConfig{
			Enable:      true,
			MaskAPIKey:  true,
			MaskPhone:   true,
			MaskIP:      true,
			MaskSession: true,
			MaskJWT:     true,
		},
	})
	defer restore()

	db, err := storage.InitDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if d, e := db.DB(); e == nil {
			_ = d.Close()
		}
	}()

	eng := mask.NewEngine(db)
	if eng == nil {
		t.Fatal("engine is nil")
	}

	secrets := []string{"sk-or-v1-abc123def456ghi789", "13800138000", "45.45.45.45", "abcdef12345678"}
	jwt := e2eJWT()

	var received []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, _ := io.ReadAll(r.Body)
		received = payload

		var req structs.ChatCompletionRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			t.Errorf("unmarshal request: %v", err)
		}
		content := ""
		if len(req.Messages) > 0 {
			content = req.Messages[len(req.Messages)-1].Content
		}

		w.Header().Set("Content-Type", "text/event-stream")
		// 以 3 字节小块回显，确保假值被切分到多个 chunk
		b := []byte(content)
		for i := 0; i < len(b); i += 3 {
			end := min(i+3, len(b))
			chunk := structs.ChatCompletionResponse{
				ID: "x", Model: "echo",
				Choices: []structs.Choice{{Index: 0, Delta: structs.Message{Role: structs.RoleAssistant, Content: string(b[i:end])}}},
			}
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", data)
		}
		fmt.Fprintf(w, "data: %s\n\n", SSEDoneMarker)
	}))
	defer srv.Close()

	original := "key sk-or-v1-abc123def456ghi789 phone 13800138000 ip 45.45.45.45 session=abcdef12345678 jwt " + jwt
	body := structs.ChatCompletionRequest{
		Messages: []structs.Message{{Role: structs.RoleUser, Content: original}},
	}
	var out strings.Builder
	err = SimpleOpenAIRequest(context.Background(), srv.URL, "mock-key", "echo", body, eng, func(resp structs.ChatCompletionResponse) error {
		if len(resp.Choices) > 0 {
			out.WriteString(resp.Choices[0].Delta.Content)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	// 1. 传输层脱敏：出站 payload 不含任何原秘密
	for _, s := range secrets {
		if bytes.Contains(received, []byte(s)) {
			t.Errorf("outbound payload leaked secret %q", s)
		}
	}
	if bytes.Contains(received, []byte(jwt)) {
		t.Errorf("outbound payload leaked jwt")
	}

	// 2. 响应还原：回调累积内容包含全部原秘密
	result := out.String()
	for _, s := range secrets {
		if !strings.Contains(result, s) {
			t.Errorf("restored response missing %q: %s", s, result)
		}
	}
	if !strings.Contains(result, jwt) {
		t.Errorf("restored response missing jwt")
	}
}
