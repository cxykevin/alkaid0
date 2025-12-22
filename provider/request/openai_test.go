package request

import (
	"fmt"
	"strings"
	"testing"

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
	baseURL := "http://localhost:56108/v1"
	apiKey := "sk-abc"
	model := "test-chat-flash-thinking"

	body := structs.ChatCompletionRequest{
		Messages: []structs.Message{
			{Role: structs.RoleUser, Content: "Hello, how are you?"},
		},
		Temperature: &[]float32{0.7}[0],
	}

	var responses []structs.ChatCompletionResponse
	err := SimpleOpenAIRequest(baseURL, apiKey, model, body, func(resp structs.ChatCompletionResponse) error {
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
	baseURL := "http://localhost:56108/v1"
	apiKey := "sk-abc"
	model := "test-embedding"

	body := structs.EmbeddingRequest{
		Input:          []string{"Hello world"},
		Model:          model,
		EncodingFormat: "float",
	}

	embeddings, err := SimpleOpenAIEmbedding(baseURL, apiKey, model, body)

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
	baseURL := "http://localhost:56108/v1"
	apiKey := "sk-abc"
	model := "test-chat-flash-thinking"
	body := structs.ChatCompletionRequest{
		Messages:    []structs.Message{},
		Temperature: &[]float32{0.7}[0],
	}
	var responses []structs.ChatCompletionResponse
	err := SimpleOpenAIRequest(baseURL, apiKey, model, body, func(resp structs.ChatCompletionResponse) error {
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
	err := SimpleOpenAIRequest(baseURL, apiKey, model, body, func(resp structs.ChatCompletionResponse) error {
		return nil
	})
	if err == nil {
		t.Fatal("Expected error for invalid baseURL, got nil")
	}
}

// TestCallbackError 测试回调返回 error
func TestCallbackError(t *testing.T) {
	baseURL := "http://localhost:56108/v1"
	apiKey := "sk-abc"
	model := "test-chat-flash-thinking"
	body := structs.ChatCompletionRequest{
		Messages:    []structs.Message{{Role: structs.RoleUser, Content: "test"}},
		Temperature: &[]float32{0.7}[0],
	}
	err := SimpleOpenAIRequest(baseURL, apiKey, model, body, func(resp structs.ChatCompletionResponse) error {
		return fmt.Errorf("callback error")
	})
	if err == nil || !strings.Contains(err.Error(), "callback error") {
		t.Fatalf("Expected callback error, got: %v", err)
	}
}

// TestEmbeddingEmptyInput 测试嵌入空输入
func TestEmbeddingEmptyInput(t *testing.T) {
	baseURL := "http://localhost:56108/v1"
	apiKey := "sk-abc"
	model := "test-embedding"
	body := structs.EmbeddingRequest{
		Input:          []string{},
		Model:          model,
		EncodingFormat: "float",
	}
	embeddings, err := SimpleOpenAIEmbedding(baseURL, apiKey, model, body)
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
	_, err := SimpleOpenAIEmbedding(baseURL, apiKey, model, body)
	if err == nil {
		t.Fatal("Expected error for invalid baseURL, got nil")
	}
}
