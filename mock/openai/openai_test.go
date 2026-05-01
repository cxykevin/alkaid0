package openai

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleChatCompletion(t *testing.T) {
	reqBody := `{"model":"test-chat","messages":[{"role":"user","content":"Hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte(reqBody)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handleChatCompletion(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp ChatCompletionResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Model != "test-chat" {
		t.Errorf("expected model test-chat, got %s", resp.Model)
	}

	if len(resp.Choices) == 0 || resp.Choices[0].Delta.Content == "" {
		t.Errorf("expected non-empty response content")
	}
}

func TestHandleChatCompletion_MethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	w := httptest.NewRecorder()

	handleChatCompletion(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", w.Code)
	}
}

func TestHandleChatCompletion_InvalidJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte("invalid")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handleChatCompletion(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
}

func TestHandleEmbedding(t *testing.T) {
	reqBody := `{"model":"test-embedding","input":["Hello world"]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", bytes.NewReader([]byte(reqBody)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handleEmbedding(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp EmbeddingResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Data) != 1 {
		t.Errorf("expected 1 embedding, got %d", len(resp.Data))
	}

	if len(resp.Data[0].Embedding) != 512 {
		t.Errorf("expected 512-dim embedding, got %d", len(resp.Data[0].Embedding))
	}
}

func TestHandleModels(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()

	handleModels(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var resp ModelsResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Data) == 0 {
		t.Errorf("expected models, got none")
	}
}

func TestHandleModels_MethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/models", nil)
	w := httptest.NewRecorder()

	handleModels(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", w.Code)
	}
}

func TestCalculateTokens(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"Hello world", 2},
		{"", 0},
		{"Single", 1},
		{"One two three four", 4},
	}

	for _, tt := range tests {
		got := calculateTokens(tt.input)
		if got != tt.expected {
			t.Errorf("calculateTokens(%q) = %d, want %d", tt.input, got, tt.expected)
		}
	}
}

func TestGenerateID(t *testing.T) {
	id := generateID("test")
	if !strings.HasPrefix(id, "test-") {
		t.Errorf("generateID should start with prefix, got %s", id)
	}
}

func TestGenerateEmbedding(t *testing.T) {
	emb := generateEmbedding()
	if len(emb) != 512 {
		t.Errorf("expected 512 dimensions, got %d", len(emb))
	}
	for _, v := range emb {
		if v < -1 || v > 1 {
			t.Errorf("embedding value out of range: %f", v)
		}
	}
}
