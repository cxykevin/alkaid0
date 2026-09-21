package openai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/server/apikey"
	"github.com/cxykevin/alkaid0/stats"
)

// newTestMux 构造带 OpenAI 兼容路由的 mux 与一个有效 API key。
// 同时把用量统计写到临时目录：代理转发带 usage 的响应时会调用 stats.AddUsage，
// 默认会往当前目录写 usage.json，污染源码树（此前在 server/openai/ 下留过残留文件）。
func newTestMux(t *testing.T) (*http.ServeMux, string) {
	t.Helper()
	stats.ResetForTest()
	stats.SetFilePath(filepath.Join(t.TempDir(), "usage.json"))

	key, err := apikey.New(10)
	if err != nil {
		t.Fatalf("apikey.New failed: %v", err)
	}
	mux := http.NewServeMux()
	NewHandler().Register(mux)
	return mux, key
}

// swapModelConfig 用给定的模型表替换全局配置，并在用例结束时恢复。
func swapModelConfig(t *testing.T, models map[int32]structs.ModelConfig) {
	t.Helper()
	cfg := structs.Config{}
	cfg.Model.Models = models
	restore := config.GlobalConfigSwap(cfg)
	t.Cleanup(restore)
}

// TestAuthorizeRejectsInvalidKeys 回归：OpenAI 兼容端点（server/openai）此前零测试，
// 鉴权、模型解析与转发都没有回归保护。这里覆盖缺失/非法/过期凭据必须 401。
func TestAuthorizeRejectsInvalidKeys(t *testing.T) {
	mux, _ := newTestMux(t)

	expired, err := apikey.New(0)
	if err != nil {
		t.Fatalf("apikey.New(0) failed: %v", err)
	}

	cases := []struct {
		name string
		auth string
	}{
		{"missing header", ""},
		{"wrong scheme", "Basic YWJj"},
		{"scheme only", "Bearer"},
		{"empty token", "Bearer "},
		{"unknown token", "Bearer 0123456789abcdef"},
		{"expired token", "Bearer " + expired},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, apiPrefix+"/models", nil)
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestModelsListsVisibleModels 验证模型列表只暴露未隐藏且配置了 ModelID 的模型。
func TestModelsListsVisibleModels(t *testing.T) {
	swapModelConfig(t, map[int32]structs.ModelConfig{
		1: {ModelID: "test-model", ModelName: "test-model-name", ProviderURL: "http://127.0.0.1:1"},
		2: {ModelID: "hidden-model", ModelName: "hidden-model-name", ProviderURL: "http://127.0.0.1:1", Hide: true},
		3: {ModelID: "embed-model", ModelName: "embed-model-name", ProviderURL: "http://127.0.0.1:1", Type: structs.ModelTypeEmbedding},
	})

	mux, key := newTestMux(t)
	req := httptest.NewRequest(http.MethodGet, apiPrefix+"/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("invalid response json: %v", err)
	}
	ids := map[string]bool{}
	if data, ok := out["data"].([]any); ok {
		for _, item := range data {
			if m, ok := item.(map[string]any); ok {
				if id, ok := m["id"].(string); ok {
					ids[id] = true
				}
			}
		}
	}
	if !ids["test-model"] {
		t.Errorf("expected test-model in model list, got %v", ids)
	}
	if ids["hidden-model"] {
		t.Errorf("hidden model must not be listed, got %v", ids)
	}
}

// TestChatCompletionsValidatesInput 覆盖方法/参数/模型解析等错误分支。
func TestChatCompletionsValidatesInput(t *testing.T) {
	swapModelConfig(t, map[int32]structs.ModelConfig{
		1: {ModelID: "test-model", ModelName: "test-model-name", ProviderURL: "http://127.0.0.1:1"},
		2: {ModelID: "no-url-model", ModelName: "no-url-model"},
	})

	mux, key := newTestMux(t)
	do := func(method, path, body string) *httptest.ResponseRecorder {
		var r *http.Request
		if body == "" {
			r = httptest.NewRequest(method, path, nil)
		} else {
			r = httptest.NewRequest(method, path, strings.NewReader(body))
		}
		r.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, r)
		return rec
	}

	if rec := do(http.MethodGet, apiPrefix+"/chat/completions", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d, want 405", rec.Code)
	}
	if rec := do(http.MethodPost, apiPrefix+"/chat/completions", "{not json"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad json status = %d, want 400", rec.Code)
	}
	if rec := do(http.MethodPost, apiPrefix+"/chat/completions", "{}"); rec.Code != http.StatusBadRequest {
		t.Errorf("empty body status = %d, want 400", rec.Code)
	}
	if rec := do(http.MethodPost, apiPrefix+"/chat/completions",
		"{\"model\":\"does-not-exist\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown model status = %d, want 404", rec.Code)
	}
	// 模型存在但没有配置 ProviderURL 时必须是 502，而不是静默成功
	if rec := do(http.MethodPost, apiPrefix+"/chat/completions",
		"{\"model\":\"no-url-model\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}"); rec.Code != http.StatusBadGateway {
		t.Errorf("missing provider url status = %d, want 502; body=%s", rec.Code, rec.Body.String())
	}
}

// TestChatCompletionsProxiesToUpstream 端到端验证非流式转发：
// 上游返回 SSE 增量，代理必须累积成标准 chat.completion 响应。
func TestChatCompletionsProxiesToUpstream(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-test\",\"created\":1,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello \"},\"finish_reason\":\"\"}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-test\",\"created\":1,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"world\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	swapModelConfig(t, map[int32]structs.ModelConfig{
		1: {ModelID: "test-model", ModelName: "test-model-name", ProviderURL: upstream.URL, ProviderKey: "upstream-key"},
	})

	mux, key := newTestMux(t)
	req := httptest.NewRequest(http.MethodPost, apiPrefix+"/chat/completions",
		strings.NewReader("{\"model\":\"test-model\",\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}"))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if gotPath != "/chat/completions" {
		t.Errorf("upstream path = %q, want /chat/completions", gotPath)
	}
	if gotAuth != "Bearer upstream-key" {
		t.Errorf("upstream auth = %q, want provider key", gotAuth)
	}
	if gotBody["model"] != "test-model" {
		t.Errorf("upstream model = %v, want test-model", gotBody["model"])
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid response json: %v", err)
	}
	choices, _ := resp["choices"].([]any)
	if len(choices) != 1 {
		t.Fatalf("choices = %v, want exactly one", resp["choices"])
	}
	choice, _ := choices[0].(map[string]any)
	message, _ := choice["message"].(map[string]any)
	if message["content"] != "hello world" {
		t.Errorf("message content = %v, want hello world", message["content"])
	}
	if choice["finish_reason"] != "stop" {
		t.Errorf("finish_reason = %v, want stop", choice["finish_reason"])
	}
	if resp["model"] != "test-model" {
		t.Errorf("response model = %v, want test-model", resp["model"])
	}
}

// TestChatCompletionsStreaming 验证 stream=true 时按 SSE 逐条转发并以 [DONE] 结束。
func TestChatCompletionsStreaming(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-s\",\"created\":1,\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"streamed\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	swapModelConfig(t, map[int32]structs.ModelConfig{
		1: {ModelID: "test-model", ModelName: "test-model-name", ProviderURL: upstream.URL, ProviderKey: "k"},
	})

	mux, key := newTestMux(t)
	req := httptest.NewRequest(http.MethodPost, apiPrefix+"/chat/completions",
		strings.NewReader("{\"model\":\"test-model\",\"stream\":true,\"messages\":[{\"role\":\"user\",\"content\":\"hi\"}]}"))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "streamed") {
		t.Errorf("stream body missing content: %q", body)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Errorf("stream body missing [DONE] terminator: %q", body)
	}
}
