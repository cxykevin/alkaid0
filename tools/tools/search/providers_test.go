package search

import (
	"strings"
	"testing"
)

// TestProvidersParameterInternal 验证 providers 功能已正确挂载到工具定义中。
// providers 参数由 path 参数承载：path 在 online=true 时作为逗号分隔的 providers 列表，
// 其描述在 load/UpdateToolPathDesp 时被替换为 pathDesp + 可用 providers 列表。
func TestProvidersParameterInternal(t *testing.T) {
	// 验证 path 参数存在（providers 功能由其承载）
	param, exists := paras["path"]
	if !exists {
		t.Fatal("path parameter not found in tool parameters")
	}

	// 验证参数类型
	if param.Type != "string" {
		t.Errorf("expected path type to be string, got %s", param.Type)
	}

	// 验证参数不是必需的
	if param.Required {
		t.Error("path parameter should not be required")
	}

	// 验证描述包含 providers 关键信息（load 时替换为实际可用列表）
	UpdateToolPathDesp()
	desc := paras["path"].Description
	if desc == "" {
		t.Error("path parameter should have a description")
	}
	if !strings.Contains(desc, "providers") {
		t.Errorf("path description should mention providers, got: %q", desc)
	}
	t.Logf("path parameter description: %s", desc)
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
