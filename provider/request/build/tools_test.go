package build

import (
	"os"
	"testing"

	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/storage"
	"github.com/cxykevin/alkaid0/tools/toolobj"
)

func initTestEnv() {
	os.Setenv("ALKAID_DEBUG_PROJECTPATH", "../../debug_config/dot_alkaid")
	os.Remove("../../debug_config/dot_alkaid/db.sqlite")
	storage.InitStorage()
}

// TestMap2Slice 测试 map 转 slice 的泛型函数
func TestMap2Slice(t *testing.T) {
	initTestEnv()
	tests := []struct {
		name      string
		input     map[string]int
		filter    func(string, int) *string
		expected  []string
		wantLen   int
		checkVals []string
	}{
		{
			name:      "空 map",
			input:     map[string]int{},
			filter:    func(k string, v int) *string { return &k },
			expected:  []string{},
			wantLen:   0,
			checkVals: []string{},
		},
		{
			name:  "单个元素",
			input: map[string]int{"a": 1},
			filter: func(k string, v int) *string {
				return &k
			},
			wantLen:   1,
			checkVals: []string{"a"},
		},
		{
			name:  "多个元素全部通过过滤",
			input: map[string]int{"a": 1, "b": 2, "c": 3},
			filter: func(k string, v int) *string {
				return &k
			},
			wantLen:   3,
			checkVals: []string{"a", "b", "c"},
		},
		{
			name:  "部分元素通过过滤器",
			input: map[string]int{"a": 1, "b": 2, "c": 3},
			filter: func(k string, v int) *string {
				// 只返回值大于 1 的元素
				if v > 1 {
					return &k
				}
				return nil
			},
			wantLen:   2,
			checkVals: []string{"b", "c"},
		},
		{
			name:  "都不通过过滤器",
			input: map[string]int{"a": 1, "b": 2},
			filter: func(k string, v int) *string {
				// 都返回 nil
				return nil
			},
			wantLen:   0,
			checkVals: []string{},
		},
		{
			name:  "使用过滤器改变值",
			input: map[string]int{"a": 1, "b": 2, "c": 3},
			filter: func(k string, v int) *string {
				result := k + "_" + string(rune('0'+v))
				return &result
			},
			wantLen: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := map2Slice(tt.input, tt.filter)

			if len(result) != tt.wantLen {
				t.Errorf("期望长度 %d，得到 %d", tt.wantLen, len(result))
			}

			// 检查期望的值是否存在
			if len(tt.checkVals) > 0 {
				resultMap := make(map[string]bool)
				for _, v := range result {
					resultMap[v] = true
				}

				for _, expected := range tt.checkVals {
					if !resultMap[expected] {
						t.Errorf("期望值 %s 不在结果中", expected)
					}
				}
			}
		})
	}
}

// TestTools 测试 Tools 函数
func TestTools(t *testing.T) {
	initTestEnv()
	// 保存原始值
	originalScopes := toolobj.Scopes
	originalToolsList := toolobj.ToolsList
	originalEnableScopes := toolobj.EnableScopes

	defer func() {
		toolobj.Scopes = originalScopes
		toolobj.ToolsList = originalToolsList
		toolobj.EnableScopes = originalEnableScopes
	}()

	tests := []struct {
		name              string
		setupScopes       func()
		setupTools        func()
		setupEnableScopes func()
		checkResult       func(t *testing.T, scopeStr string, traceStr string, toolsDef *[]*parser.ToolsDefine)
	}{
		{
			name: "空的工具列表和命名空间",
			setupScopes: func() {
				toolobj.Scopes = make(map[string]string)
				toolobj.Scopes[""] = "Global"
			},
			setupTools: func() {
				toolobj.ToolsList = make(map[string]*toolobj.Tools)
				toolobj.ToolsList[""] = &toolobj.Tools{
					Name:  "Global",
					ID:    "",
					Hooks: make([]toolobj.Hook, 0),
				}
			},
			setupEnableScopes: func() {
				toolobj.EnableScopes = make(map[string]bool)
				toolobj.EnableScopes[""] = true
			},
			checkResult: func(t *testing.T, scopeStr string, traceStr string, toolsDef *[]*parser.ToolsDefine) {
				if scopeStr == "" {
					t.Error("期望获得作用域字符串，但为空")
				}
				if traceStr == "" {
					t.Error("期望获得追踪字符串，但为空")
				}
				if toolsDef == nil {
					t.Error("期望获得工具定义，但为 nil")
				}
				// Global 工具不包含在总工具表中
				if len(*toolsDef) != 0 {
					t.Errorf("期望空的工具列表，得到 %d 个工具", len(*toolsDef))
				}
			},
		},
		{
			name: "单个启用的作用域和工具",
			setupScopes: func() {
				toolobj.Scopes = map[string]string{
					"":           "Global",
					"test_scope": "测试命名空间",
				}
			},
			setupTools: func() {
				toolobj.ToolsList = map[string]*toolobj.Tools{
					"": {
						Name:  "Global",
						ID:    "",
						Hooks: make([]toolobj.Hook, 0),
					},
					"test_tool": {
						Scope: "test_scope",
						Name:  "测试工具",
						ID:    "test_tool",
						Hooks: make([]toolobj.Hook, 0),
					},
				}
			},
			setupEnableScopes: func() {
				toolobj.EnableScopes = map[string]bool{
					"":           true,
					"test_scope": true,
				}
			},
			checkResult: func(t *testing.T, scopeStr string, traceStr string, toolsDef *[]*parser.ToolsDefine) {
				if scopeStr == "" {
					t.Error("期望获得作用域字符串，但为空")
				}
				if toolsDef == nil {
					t.Error("期望获得工具定义，但为 nil")
				}
				// 期望只有 test_tool（Global 工具不包含在总工具表）
				if len(*toolsDef) != 1 {
					t.Errorf("期望 1 个工具，得到 %d 个", len(*toolsDef))
				}
				if len(*toolsDef) > 0 && (*toolsDef)[0].Name != "test_tool" {
					t.Errorf("期望工具名为 'test_tool'，得到 '%s'", (*toolsDef)[0].Name)
				}
			},
		},
		{
			name: "禁用的作用域应该被过滤",
			setupScopes: func() {
				toolobj.Scopes = map[string]string{
					"":               "Global",
					"enabled_scope":  "启用的命名空间",
					"disabled_scope": "禁用的命名空间",
				}
			},
			setupTools: func() {
				toolobj.ToolsList = map[string]*toolobj.Tools{
					"": {
						Name:  "Global",
						ID:    "",
						Hooks: make([]toolobj.Hook, 0),
					},
					"enabled_tool": {
						Scope: "enabled_scope",
						Name:  "启用的工具",
						ID:    "enabled_tool",
						Hooks: make([]toolobj.Hook, 0),
					},
					"disabled_tool": {
						Scope: "disabled_scope",
						Name:  "禁用的工具",
						ID:    "disabled_tool",
						Hooks: make([]toolobj.Hook, 0),
					},
				}
			},
			setupEnableScopes: func() {
				toolobj.EnableScopes = map[string]bool{
					"":               true,
					"enabled_scope":  true,
					"disabled_scope": false,
				}
			},
			checkResult: func(t *testing.T, scopeStr string, traceStr string, toolsDef *[]*parser.ToolsDefine) {
				if toolsDef == nil {
					t.Error("期望获得工具定义，但为 nil")
				}
				// 期望只有 enabled_tool（disabled_tool 被过滤，Global 工具不包含）
				if len(*toolsDef) != 1 {
					t.Errorf("期望 1 个启用的工具，得到 %d 个", len(*toolsDef))
				}
				// 检查是否包含 enabled_tool，且不包含 disabled_tool
				hasEnabledTool := false
				hasDisabledTool := false
				for _, tool := range *toolsDef {
					if tool.Name == "enabled_tool" {
						hasEnabledTool = true
					}
					if tool.Name == "disabled_tool" {
						hasDisabledTool = true
					}
				}
				if !hasEnabledTool {
					t.Error("期望找到 'enabled_tool'")
				}
				if hasDisabledTool {
					t.Error("期望不包含 'disabled_tool'")
				}
			},
		},
		{
			name: "多个启用的作用域和工具",
			setupScopes: func() {
				toolobj.Scopes = map[string]string{
					"":       "Global",
					"scope1": "作用域 1",
					"scope2": "作用域 2",
					"scope3": "作用域 3",
				}
			},
			setupTools: func() {
				toolobj.ToolsList = map[string]*toolobj.Tools{
					"": {
						Name:  "Global",
						ID:    "",
						Hooks: make([]toolobj.Hook, 0),
					},
					"tool1": {
						Scope: "scope1",
						Name:  "工具 1",
						ID:    "tool1",
						Hooks: make([]toolobj.Hook, 0),
					},
					"tool2": {
						Scope: "scope2",
						Name:  "工具 2",
						ID:    "tool2",
						Hooks: make([]toolobj.Hook, 0),
					},
					"tool3": {
						Scope: "scope1",
						Name:  "工具 3",
						ID:    "tool3",
						Hooks: make([]toolobj.Hook, 0),
					},
					"tool4": {
						Scope: "scope3",
						Name:  "工具 4",
						ID:    "tool4",
						Hooks: make([]toolobj.Hook, 0),
					},
				}
			},
			setupEnableScopes: func() {
				toolobj.EnableScopes = map[string]bool{
					"":       true,
					"scope1": true,
					"scope2": true,
					"scope3": false,
				}
			},
			checkResult: func(t *testing.T, scopeStr string, traceStr string, toolsDef *[]*parser.ToolsDefine) {
				if toolsDef == nil {
					t.Error("期望获得工具定义，但为 nil")
				}
				// 期望 3 个工具（tool1, tool2, tool3）, tool4 因为 scope3 被禁用而过滤
				if len(*toolsDef) != 3 {
					t.Errorf("期望 3 个工具，得到 %d 个", len(*toolsDef))
				}

				toolNames := make(map[string]bool)
				for _, tool := range *toolsDef {
					toolNames[tool.Name] = true
				}

				// ToolsDefine 中的 Name 字段实际是工具 ID（k 值）
				expectedTools := []string{"tool1", "tool2", "tool3"}
				for _, name := range expectedTools {
					if !toolNames[name] {
						t.Errorf("期望找到 '%s'", name)
					}
				}

				if toolNames["tool4"] {
					t.Error("期望不包含 'tool4'（scope3 已禁用）")
				}
			},
		},
		{
			name: "所有作用域都禁用",
			setupScopes: func() {
				toolobj.Scopes = map[string]string{
					"":       "Global",
					"scope1": "作用域 1",
					"scope2": "作用域 2",
				}
			},
			setupTools: func() {
				toolobj.ToolsList = map[string]*toolobj.Tools{
					"": {
						Name:  "Global",
						ID:    "",
						Hooks: make([]toolobj.Hook, 0),
					},
					"tool1": {
						Scope: "scope1",
						Name:  "工具 1",
						ID:    "tool1",
						Hooks: make([]toolobj.Hook, 0),
					},
					"tool2": {
						Scope: "scope2",
						Name:  "工具 2",
						ID:    "tool2",
						Hooks: make([]toolobj.Hook, 0),
					},
				}
			},
			setupEnableScopes: func() {
				toolobj.EnableScopes = map[string]bool{
					"":       true,
					"scope1": false,
					"scope2": false,
				}
			},
			checkResult: func(t *testing.T, scopeStr string, traceStr string, toolsDef *[]*parser.ToolsDefine) {
				if toolsDef == nil {
					t.Error("期望获得工具定义，但为 nil")
				}
				// 所有工具都被禁用
				if len(*toolsDef) != 0 {
					t.Errorf("期望 0 个工具，得到 %d 个", len(*toolsDef))
				}
			},
		},
		{
			name: "工具 ID 和名称验证",
			setupScopes: func() {
				toolobj.Scopes = map[string]string{
					"":       "Global",
					"scope1": "测试作用域",
				}
			},
			setupTools: func() {
				toolobj.ToolsList = map[string]*toolobj.Tools{
					"": {
						Name:  "Global",
						ID:    "",
						Hooks: make([]toolobj.Hook, 0),
					},
					"run_code": {
						Scope: "scope1",
						Name:  "run_code",
						ID:    "run_code",
						Hooks: make([]toolobj.Hook, 0),
					},
					"file_read": {
						Scope: "scope1",
						Name:  "file_read",
						ID:    "file_read",
						Hooks: make([]toolobj.Hook, 0),
					},
				}
			},
			setupEnableScopes: func() {
				toolobj.EnableScopes = map[string]bool{
					"":       true,
					"scope1": true,
				}
			},
			checkResult: func(t *testing.T, scopeStr string, traceStr string, toolsDef *[]*parser.ToolsDefine) {
				if toolsDef == nil {
					t.Error("期望获得工具定义，但为 nil")
				}
				if len(*toolsDef) != 2 {
					t.Errorf("期望 2 个工具，得到 %d 个", len(*toolsDef))
				}

				// 验证工具 ID（Name 字段存储的是 ID）
				toolIDs := make(map[string]bool)
				for _, tool := range *toolsDef {
					toolIDs[tool.Name] = true
				}

				expectedIDs := []string{"run_code", "file_read"}
				for _, id := range expectedIDs {
					if !toolIDs[id] {
						t.Errorf("期望找到 ID '%s'", id)
					}
				}
			},
		},
		{
			name: "大量工具的情况",
			setupScopes: func() {
				scopes := map[string]string{
					"":       "Global",
					"scope1": "作用域 1",
				}
				toolobj.Scopes = scopes
			},
			setupTools: func() {
				tools := map[string]*toolobj.Tools{
					"": {
						Name:  "Global",
						ID:    "",
						Hooks: make([]toolobj.Hook, 0),
					},
				}
				// 添加 50 个工具
				for i := range 50 {
					toolID := "tool_" + string(rune('0'+(i%10))) + "_" + string(rune('0'+(i/10)))
					tools[toolID] = &toolobj.Tools{
						Scope: "scope1",
						Name:  toolID,
						ID:    toolID,
						Hooks: make([]toolobj.Hook, 0),
					}
				}
				toolobj.ToolsList = tools
			},
			setupEnableScopes: func() {
				toolobj.EnableScopes = map[string]bool{
					"":       true,
					"scope1": true,
				}
			},
			checkResult: func(t *testing.T, scopeStr string, traceStr string, toolsDef *[]*parser.ToolsDefine) {
				if toolsDef == nil {
					t.Error("期望获得工具定义，但为 nil")
				}
				// 期望 50 个工具（Global 不计）
				if len(*toolsDef) != 50 {
					t.Errorf("期望 50 个工具，得到 %d 个", len(*toolsDef))
				}
			},
		},
		{
			name: "单个作用域多个工具",
			setupScopes: func() {
				toolobj.Scopes = map[string]string{
					"":       "Global",
					"scope1": "编程工具",
				}
			},
			setupTools: func() {
				toolobj.ToolsList = map[string]*toolobj.Tools{
					"": {
						Name:  "Global",
						ID:    "",
						Hooks: make([]toolobj.Hook, 0),
					},
					"python_exec": {
						Scope: "scope1",
						Name:  "python_exec",
						ID:    "python_exec",
						Hooks: make([]toolobj.Hook, 0),
					},
					"bash_exec": {
						Scope: "scope1",
						Name:  "bash_exec",
						ID:    "bash_exec",
						Hooks: make([]toolobj.Hook, 0),
					},
					"node_exec": {
						Scope: "scope1",
						Name:  "node_exec",
						ID:    "node_exec",
						Hooks: make([]toolobj.Hook, 0),
					},
				}
			},
			setupEnableScopes: func() {
				toolobj.EnableScopes = map[string]bool{
					"":       true,
					"scope1": true,
				}
			},
			checkResult: func(t *testing.T, scopeStr string, traceStr string, toolsDef *[]*parser.ToolsDefine) {
				if toolsDef == nil {
					t.Error("期望获得工具定义，但为 nil")
				}
				if len(*toolsDef) != 3 {
					t.Errorf("期望 3 个工具，得到 %d 个", len(*toolsDef))
				}

				// 验证所有工具都来自 scope1
				// Name 字段存储的是工具 ID
				toolIDs := make(map[string]bool)
				for _, tool := range *toolsDef {
					toolIDs[tool.Name] = true
					// 验证 Description 不为空（包含来自 hook 的信息）
					if tool.Description == "" {
						t.Errorf("工具 %s 的描述为空", tool.Name)
					}
				}

				expectedIDs := []string{"python_exec", "bash_exec", "node_exec"}
				for _, id := range expectedIDs {
					if !toolIDs[id] {
						t.Errorf("期望找到 ID '%s'", id)
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 初始化测试数据
			tt.setupScopes()
			tt.setupTools()
			tt.setupEnableScopes()

			// 执行被测试函数
			scopeStr, traceStr, toolsDef := Tools()

			// 验证结果
			if scopeStr == "" {
				t.Error("期望获得作用域字符串")
			}
			if traceStr == "" {
				t.Error("期望获得追踪字符串")
			}
			if toolsDef == nil {
				t.Error("期望获得工具定义指针")
			}

			// 执行自定义检查
			if tt.checkResult != nil {
				tt.checkResult(t, scopeStr, traceStr, toolsDef)
			}
		})
	}
}

// BenchmarkMap2Slice map 转 slice 的性能测试
func BenchmarkMap2Slice(b *testing.B) {
	initTestEnv()
	// 创建一个包含 1000 个元素的 map
	largeMap := make(map[string]int)
	for i := range 1000 {
		largeMap[string(rune(i))] = i
	}

	filter := func(k string, v int) *string {
		if v%2 == 0 {
			return &k
		}
		return nil
	}

	for b.Loop() {
		map2Slice(largeMap, filter)
	}
}

// BenchmarkTools Tools 函数的性能测试
func BenchmarkTools(b *testing.B) {
	initTestEnv()
	// 保存原始值
	originalScopes := toolobj.Scopes
	originalToolsList := toolobj.ToolsList
	originalEnableScopes := toolobj.EnableScopes

	defer func() {
		toolobj.Scopes = originalScopes
		toolobj.ToolsList = originalToolsList
		toolobj.EnableScopes = originalEnableScopes
	}()

	// 设置测试数据
	toolobj.Scopes = map[string]string{
		"":       "Global",
		"scope1": "作用域 1",
		"scope2": "作用域 2",
		"scope3": "作用域 3",
	}

	toolobj.ToolsList = make(map[string]*toolobj.Tools)
	toolobj.ToolsList[""] = &toolobj.Tools{
		Name:  "Global",
		ID:    "",
		Hooks: make([]toolobj.Hook, 0),
	}
	for i := range 10 {
		scope := "scope1"
		if i%2 == 0 {
			scope = "scope2"
		}
		toolobj.ToolsList[string(rune(65+i))] = &toolobj.Tools{
			Scope: scope,
			Name:  "工具",
			ID:    string(rune(65 + i)),
			Hooks: make([]toolobj.Hook, 0),
		}
	}

	toolobj.EnableScopes = map[string]bool{
		"":       true,
		"scope1": true,
		"scope2": true,
		"scope3": false,
	}

	for b.Loop() {
		Tools()
	}
}
