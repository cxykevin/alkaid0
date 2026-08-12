package toolobj

import "sync"

// ToolsList 工具列表
var ToolsList map[string]*Tools = make(map[string]*Tools)

// Scopes 工具命名空间
var Scopes map[string]string = make(map[string]string)

// ToolsMu 保护 ToolsList 的并发读写
var ToolsMu sync.RWMutex

// ScopesMu 保护 Scopes 的并发读写
var ScopesMu sync.RWMutex

// // EnableScopes 启用的命名空间
// var EnableScopes map[string]bool = make(map[string]bool)

// SetScope 线程安全设置 Scope
func SetScope(name, prompt string) {
	ScopesMu.Lock()
	Scopes[name] = prompt
	ScopesMu.Unlock()
}

// GetScope 线程安全读取 Scope
func GetScope(name string) (string, bool) {
	ScopesMu.RLock()
	v, ok := Scopes[name]
	ScopesMu.RUnlock()
	return v, ok
}

// SetTool 线程安全注册工具
func SetTool(tool *Tools) {
	ToolsMu.Lock()
	// 统一以 Name 作为键（与 GetTool/GetToolHooks/AppendToolHook 按 Name 查找一致），
	// 避免 ID!=Name 的工具被静默不可达。Global 工具（ID==""）保留空键专用槽位，
	// 供构建工具表时识别并跳过。
	key := tool.Name
	if tool.ID == "" {
		key = ""
	}
	ToolsList[key] = tool
	ToolsMu.Unlock()
}

// GetTool 线程安全读取工具
func GetTool(name string) *Tools {
	ToolsMu.RLock()
	t := ToolsList[name]
	ToolsMu.RUnlock()
	return t
}

// GetToolHooks 线程安全获取工具的钩子列表（返回副本以避免竞态）
func GetToolHooks(name string) []Hook {
	ToolsMu.RLock()
	t := ToolsList[name]
	ToolsMu.RUnlock()
	if t == nil {
		return nil
	}
	hooks := make([]Hook, len(t.Hooks))
	copy(hooks, t.Hooks)
	return hooks
}

// AppendToolHook 线程安全为工具追加钩子
func AppendToolHook(name string, hook Hook) bool {
	ToolsMu.Lock()
	t := ToolsList[name]
	if t == nil {
		ToolsMu.Unlock()
		return false
	}
	t.Hooks = append(t.Hooks, hook)
	ToolsMu.Unlock()
	return true
}

// SearchTool 搜索工具
func SearchTool(name string, scope string) *Tools {
	for _, tool := range ToolsList {
		if tool.Name == name && tool.Scope == scope {
			return tool
		}
	}
	return nil
}
