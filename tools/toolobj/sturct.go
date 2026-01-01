package toolobj

import "github.com/cxykevin/alkaid0/provider/parser"

// PreHookFunction 钩子函数
type PreHookFunction struct {
	Func     func() (string, error)
	Priority int32
}

// PostHookFunction 钩子函数
type PostHookFunction struct {
	Func     func(map[string]any, []*any) (bool, []*any, map[string]any, error)
	Priority int32
}

// OnHookFunction 钩子函数
type OnHookFunction struct {
	Func     func(map[string]any, []*any) (bool, []*any, error)
	Priority int32
}

// Hook 钩子对象
type Hook struct {
	Scope      string
	Parameters *map[string]parser.ToolParameters
	// 注入提示词
	PreHook PreHookFunction
	// UI 渲染（bool：是否传递）
	OnHook OnHookFunction
	// 执行操作（bool：是否传递）
	PostHook PostHookFunction
}

// Tools 工具调用
type Tools struct {
	Scope           string
	Name            string
	ID              string
	UserDescription string
	Parameters      map[string]parser.ToolParameters
	Hooks           []Hook
}
