package request

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/mock/openai"
	"github.com/cxykevin/alkaid0/provider/request/structs"
)

// setupMockServer 在测试前启动mock server的hook函数
func setupMockServer() {
	openai.StartServerTask()
}

func TestMain(m *testing.M) {
	// 在所有测试前启动mock server
	setupMockServer()

	// 运行测试
	m.Run()
}

func TestSimpleOpenAIRequest(t *testing.T) {
	baseURL := openai.BaseURL
	apiKey := "sk-abc"
	model := "test-chat-flash-thinking"

	body := structs.ChatCompletionRequest{
		Messages: []structs.Message{
			{Role: structs.RoleUser, Content: "Hello, how are you?"},
		},
		Temperature: &[]float32{0.7}[0],
	}

	var responses []structs.ChatCompletionResponse
	err := SimpleOpenAIRequest(context.Background(), baseURL, apiKey, model, body, nil, func(resp structs.ChatCompletionResponse) error {
		responses = append(responses, resp)
		return nil
	})

	if err != nil {
		t.Fatalf("SimpleOpenAIRequest failed: %v", err)
	}

	if len(responses) == 0 {
		t.Fatal("No responses received")
	}

	for i, resp := range responses {
		if resp.ID == "" {
			t.Errorf("Response %d has empty ID", i)
		}
		if resp.Model == "" {
			t.Errorf("Response %d has empty model", i)
		}
	}
}

func TestSimpleOpenAIEmbedding(t *testing.T) {
	baseURL := openai.BaseURL
	apiKey := "sk-abc"
	model := "test-embedding"

	body := structs.EmbeddingRequest{
		Input:          []string{"Hello world"},
		Model:          model,
		EncodingFormat: "float",
	}

	embeddings, err := SimpleOpenAIEmbedding(context.Background(), baseURL, apiKey, model, body)

	if err != nil {
		t.Fatalf("SimpleOpenAIEmbedding failed: %v", err)
	}

	if len(embeddings) == 0 {
		t.Fatal("No embeddings returned")
	}

	for i, emb := range embeddings {
		if len(emb) == 0 {
			t.Errorf("Embedding %d is empty", i)
		}
	}
}

// TestEmptyMessages 测试空消息输入
func TestEmptyMessages(t *testing.T) {
	baseURL := openai.BaseURL
	apiKey := "sk-abc"
	model := "test-chat-flash-thinking"
	body := structs.ChatCompletionRequest{
		Messages:    []structs.Message{},
		Temperature: &[]float32{0.7}[0],
	}
	var responses []structs.ChatCompletionResponse
	err := SimpleOpenAIRequest(context.Background(), baseURL, apiKey, model, body, nil, func(resp structs.ChatCompletionResponse) error {
		responses = append(responses, resp)
		return nil
	})
	if err != nil {
		t.Fatalf("SimpleOpenAIRequest with empty messages failed: %v", err)
	}
}

// TestInvalidBaseURL 测试错误 baseURL
func TestInvalidBaseURL(t *testing.T) {
	baseURL := "http://localhost:99999/v1" // 错误端口
	apiKey := "sk-abc"
	model := "test-chat-flash-thinking"

	body := structs.ChatCompletionRequest{
		Messages:    []structs.Message{{Role: structs.RoleUser, Content: "test"}},
		Temperature: &[]float32{0.7}[0],
	}
	err := SimpleOpenAIRequest(context.Background(), baseURL, apiKey, model, body, nil, func(resp structs.ChatCompletionResponse) error {
		return nil
	})
	if err == nil {
		t.Fatal("Expected error for invalid baseURL, got nil")
	}
}

// TestCallbackError 测试回调返回 error
func TestCallbackError(t *testing.T) {
	baseURL := openai.BaseURL
	apiKey := "sk-abc"
	model := "test-chat-flash-thinking"
	body := structs.ChatCompletionRequest{
		Messages:    []structs.Message{{Role: structs.RoleUser, Content: "test"}},
		Temperature: &[]float32{0.7}[0],
	}
	err := SimpleOpenAIRequest(context.Background(), baseURL, apiKey, model, body, nil, func(resp structs.ChatCompletionResponse) error {
		return fmt.Errorf("callback error")
	})
	if err == nil || !strings.Contains(err.Error(), "callback error") {
		t.Fatalf("Expected callback error, got: %v", err)
	}
}

// TestEmbeddingEmptyInput 测试嵌入空输入
func TestEmbeddingEmptyInput(t *testing.T) {
	baseURL := openai.BaseURL
	apiKey := "sk-abc"
	model := "test-embedding"
	body := structs.EmbeddingRequest{
		Input:          []string{},
		Model:          model,
		EncodingFormat: "float",
	}
	embeddings, err := SimpleOpenAIEmbedding(context.Background(), baseURL, apiKey, model, body)
	if err != nil {
		t.Fatalf("SimpleOpenAIEmbedding with empty input failed: %v", err)
	}
	if len(embeddings) != 0 {
		t.Errorf("Expected 0 embeddings, got %d", len(embeddings))
	}
}

// TestEmbeddingInvalidBaseURL 测试嵌入错误 baseURL
func TestEmbeddingInvalidBaseURL(t *testing.T) {
	baseURL := "http://localhost:99999/v1"
	apiKey := "sk-abc"
	model := "test-embedding"
	body := structs.EmbeddingRequest{
		Input:          []string{"test"},
		Model:          model,
		EncodingFormat: "float",
	}
	_, err := SimpleOpenAIEmbedding(context.Background(), baseURL, apiKey, model, body)
	if err == nil {
		t.Fatal("Expected error for invalid baseURL, got nil")
	}
}

// TestConcurrentRequests 测试并发请求
func TestConcurrentRequests(t *testing.T) {
	baseURL := openai.BaseURL
	apiKey := "sk-abc"
	model := "test-chat-flash-thinking"

	const numGoroutines = 5
	done := make(chan bool, numGoroutines)

	for i := range numGoroutines {
		go func(id int) {
			body := structs.ChatCompletionRequest{
				Messages: []structs.Message{
					{Role: structs.RoleUser, Content: fmt.Sprintf("Hello from goroutine %d", id)},
				},
				Temperature: &[]float32{0.7}[0],
			}

			var responses []structs.ChatCompletionResponse
			err := SimpleOpenAIRequest(context.Background(), baseURL, apiKey, model, body, nil, func(resp structs.ChatCompletionResponse) error {
				responses = append(responses, resp)
				return nil
			})

			if err != nil {
				t.Errorf("Goroutine %d failed: %v", id, err)
			}

			if len(responses) == 0 {
				t.Errorf("Goroutine %d received no responses", id)
			}

			done <- true
		}(i)
	}

	for range numGoroutines {
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("Timeout waiting for concurrent requests")
		}
	}
}

// ---- 回归测试：忽略 stream=true 的网关（非流式 JSON 回退） ----
//
// 背景：真实 OpenAI 的非流式响应把内容放在 choices[].message（流式才用 delta），
// 而响应消费方统一只读 delta。此前没有 message→delta 的归一化，于是这类网关
// 表现为"空回复"；mock 服务器也错误地使用 delta 构造非流式响应，掩盖了该缺陷。

func TestNormalizeNonStreamChoices(t *testing.T) {
	resp := structs.ChatCompletionResponse{
		Choices: []structs.Choice{
			{Index: 0, Message: structs.Message{Role: "assistant", Content: "hello from message"}},
		},
	}
	normalizeNonStreamChoices(&resp)
	if resp.Choices[0].Delta.Content != "hello from message" {
		t.Errorf("message content should be moved into delta, got %q", resp.Choices[0].Delta.Content)
	}

	// delta 已有内容时不得被覆盖（真正的流式增量）
	resp2 := structs.ChatCompletionResponse{
		Choices: []structs.Choice{
			{Index: 0, Delta: structs.Message{Content: "streamed"}, Message: structs.Message{Content: "stale"}},
		},
	}
	normalizeNonStreamChoices(&resp2)
	if resp2.Choices[0].Delta.Content != "streamed" {
		t.Errorf("existing delta must win, got %q", resp2.Choices[0].Delta.Content)
	}

	// 两者都为空时不产生任何变化
	resp3 := structs.ChatCompletionResponse{Choices: []structs.Choice{{Index: 0}}}
	normalizeNonStreamChoices(&resp3)
	if resp3.Choices[0].Delta.Content != "" {
		t.Error("empty message must not produce content")
	}

	normalizeNonStreamChoices(nil) // 不得 panic
}

// TestSimpleOpenAIRequest_NonStreamMessageField 端到端：网关忽略 stream=true、
// 直接返回标准非流式 JSON（choices[].message）时，回调必须收到内容。
func TestSimpleOpenAIRequest_NonStreamMessageField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"non-stream reply"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer srv.Close()

	var got string
	err := SimpleOpenAIRequest(context.Background(), srv.URL, "sk-test", "m",
		structs.ChatCompletionRequest{Messages: []structs.Message{{Role: structs.RoleUser, Content: "hi"}}},
		nil,
		func(resp structs.ChatCompletionResponse) error {
			if len(resp.Choices) > 0 {
				got += resp.Choices[0].Delta.Content
			}
			return nil
		})
	if err != nil {
		t.Fatalf("SimpleOpenAIRequest failed: %v", err)
	}
	if got != "non-stream reply" {
		t.Errorf("callback must receive the non-stream message content, got %q", got)
	}
}
