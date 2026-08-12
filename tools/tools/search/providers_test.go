package search

import (
	"testing"
)

// TestProvidersParameterInternal 验证 providers 参数已正确添加到工具定义中
func TestProvidersParameterInternal(t *testing.T) {
	// 验证 providers 参数存在
	param, exists := paras["providers"]
	if !exists {
		t.Fatal("providers parameter not found in tool parameters")
	}

	// 验证参数类型
	if param.Type != "string" {
		t.Errorf("expected providers type to be string, got %s", param.Type)
	}

	// 验证参数不是必需的
	if param.Required {
		t.Error("providers parameter should not be required")
	}

	// 验证描述包含关键信息
	desc := param.Description
	if desc == "" {
		t.Error("providers parameter should have a description")
	}
	t.Logf("providers parameter description: %s", desc)
}

// TestGetStringParamDefaultInternal 验证 getStringParamDefault 辅助函数
func TestGetStringParamDefaultInternal(t *testing.T) {
	tests := []struct {
		name     string
		mp       map[string]*any
		key      string
		def      string
		expected string
	}{
		{
			name:     "empty map returns default",
			mp:       map[string]*any{},
			key:      "providers",
			def:      "default",
			expected: "default",
		},
		{
			name: "nil value returns default",
			mp: map[string]*any{
				"providers": nil,
			},
			key:      "providers",
			def:      "default",
			expected: "default",
		},
		{
			name: "valid string value",
			mp: func() map[string]*any {
				v := any("bing,tavily")
				return map[string]*any{"providers": &v}
			}(),
			key:      "providers",
			def:      "default",
			expected: "bing,tavily",
		},
		{
			name: "empty string value",
			mp: func() map[string]*any {
				v := any("")
				return map[string]*any{"providers": &v}
			}(),
			key:      "providers",
			def:      "default",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := getStringParamDefault(tt.mp, tt.key, tt.def)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}
