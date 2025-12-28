package tools

// Hook 钩子对象
type Hook struct {
	Scope string
	// 注入提示词
	PreHook (func() (string, error))
	// UI 渲染（bool：是否传递）
	OnHook (func(map[string]any, []*any) (bool, []*any, error))
	// 执行操作（bool：是否传递）
	PostHook (func(map[string]any, []*any) (bool, []*any, map[string]any, error))
}

// Tools 工具调用
type Tools struct {
	Name            string
	ID              string
	UserDescription string
	Hooks           []Hook
}
