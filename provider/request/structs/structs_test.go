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

// TestChatCompletionRequestNativeTools 原生模式请求体：tools 参数 + tool_choice 正确序列化。
func TestChatCompletionRequestNativeTools(t *testing.T) {
	req := ChatCompletionRequest{
		Model: "native-model",
		Messages: []Message{
			{Role: RoleUser, Content: "hi"},
		},
		Stream:     true,
		Tools:      []Tool{{
			Type: "function",
			Function: ToolFunction{
				Name:        "calculator",
				Description: "A calculator",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"expression":{"type":"string"}}}`),
			},
		}},
		ToolChoice: "auto",
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if m["tool_choice"] != "auto" {
		t.Errorf("tool_choice = %v, want auto", m["tool_choice"])
	}
	tools, ok := m["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools missing or wrong: %v", m["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Errorf("tool.type = %v, want function", tool["type"])
	}
	fn := tool["function"].(map[string]any)
	if fn["name"] != "calculator" {
		t.Errorf("function.name = %v, want calculator", fn["name"])
	}
	// parameters 是 JSON Schema 对象，不应二次转义
	if _, ok := fn["parameters"].(map[string]any); !ok {
		t.Errorf("function.parameters should be an object, got %T", fn["parameters"])
	}

	// omitempty：空 Tools/ToolChoice 不输出
	req2 := ChatCompletionRequest{Model: "m", Messages: []Message{{Role: RoleUser, Content: "x"}}, Stream: true}
	data2, _ := json.Marshal(req2)
	var m2 map[string]any
	_ = json.Unmarshal(data2, &m2)
	if _, ok := m2["tools"]; ok {
		t.Error("empty Tools should be omitted")
	}
	if _, ok := m2["tool_choice"]; ok {
		t.Error("empty ToolChoice should be omitted")
	}
}

// TestStreamToolCallDeltaUnmarshal 流式 delta.tool_calls 反序列化（Message.ToolCalls 目标）。
func TestStreamToolCallDeltaUnmarshal(t *testing.T) {
	raw := `{
		"choices": [{
			"index": 0,
			"delta": {
				"role": "assistant",
				"tool_calls": [{
					"index": 0,
					"id": "call_abc",
					"type": "function",
					"function": {"name": "calculator", "arguments": "{\"expression\":"}
				}]
			}
		}]
	}`
	var resp ChatCompletionResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("expected 1 choice, got %d", len(resp.Choices))
	}
	delta := resp.Choices[0].Delta
	if len(delta.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool_call, got %d", len(delta.ToolCalls))
	}
	tc := delta.ToolCalls[0]
	if tc.Index != 0 || tc.ID != "call_abc" || tc.Type != "function" {
		t.Errorf("unexpected tool_call header: %+v", tc)
	}
	if tc.Function == nil || tc.Function.Name != "calculator" || tc.Function.Arguments != `{"expression":` {
		t.Errorf("unexpected function fragment: %+v", tc.Function)
	}
}

// TestMessageToolCallsMarshal 历史回放：assistant 消息 tool_calls 与 role:tool 消息的
// tool_call_id 正确往返序列化。
func TestMessageToolCallsMarshal(t *testing.T) {
	msg := Message{
		Role: RoleAssistant,
		ToolCalls: []StreamToolCall{{
			ID:   "call_1",
			Type: "function",
			Function: &StreamToolCallFunc{
				Name:      "calculator",
				Arguments: `{"expression":"1+1"}`,
			},
		}},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	tcs, ok := m["tool_calls"].([]any)
	if !ok || len(tcs) != 1 {
		t.Fatalf("tool_calls missing: %v", m)
	}
	tc := tcs[0].(map[string]any)
	if tc["id"] != "call_1" || tc["type"] != "function" {
		t.Errorf("tool_call header mismatch: %v", tc)
	}
	// 历史回放（非流式）不应输出 index 字段（omitempty 省略 0）
	if _, ok := tc["index"]; ok {
		t.Error("history replay tool_call should not carry index field")
	}

	// role:tool 消息
	tmsg := Message{Role: RoleTool, Content: `{"result":2}`, ToolCallID: "call_1"}
	dataT, _ := json.Marshal(tmsg)
	var tm map[string]any
	_ = json.Unmarshal(dataT, &tm)
	if tm["role"] != "tool" || tm["tool_call_id"] != "call_1" {
		t.Errorf("tool message mismatch: %v", tm)
	}
}
