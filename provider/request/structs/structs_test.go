package structs

import (
	"encoding/json"
	"testing"
)

func TestRoleConstants(t *testing.T) {
	if RoleUser != "user" {
		t.Errorf("Expected RoleUser = 'user', got %s", RoleUser)
	}
	if RoleAssistant != "assistant" {
		t.Errorf("Expected RoleAssistant = 'assistant', got %s", RoleAssistant)
	}
	if RoleSystem != "system" {
		t.Errorf("Expected RoleSystem = 'system', got %s", RoleSystem)
	}
}

func TestChatCompletionRequestMarshal(t *testing.T) {
	req := ChatCompletionRequest{
		Model: "gpt-4",
		Messages: []Message{
			{Role: RoleUser, Content: "Hello"},
		},
		Stream: true,
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var unmarshaled ChatCompletionRequest
	err = json.Unmarshal(data, &unmarshaled)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if unmarshaled.Model != "gpt-4" {
		t.Errorf("Expected model 'gpt-4', got %s", unmarshaled.Model)
	}
	if len(unmarshaled.Messages) != 1 {
		t.Errorf("Expected 1 message, got %d", len(unmarshaled.Messages))
	}
}

func TestMessageMarshal(t *testing.T) {
	msg := Message{
		Role:    RoleAssistant,
		Content: "Response",
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var unmarshaled Message
	err = json.Unmarshal(data, &unmarshaled)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if unmarshaled.Role != RoleAssistant {
		t.Errorf("Expected role 'assistant', got %s", unmarshaled.Role)
	}
	if unmarshaled.Content != "Response" {
		t.Errorf("Expected content 'Response', got %s", unmarshaled.Content)
	}
}
