package actions

import "github.com/cxykevin/alkaid0/storage/structs"

// Add 添加Agent
type Add struct {
	Session   *structs.Chats
	AgentCode string
	AgentID   string
	Path      string
}

// Update 更新Agent
type Update struct {
	Session   *structs.Chats
	AgentCode string
	AgentID   string
	Path      string
}

// Del 删除Agent
type Del struct {
	Session   *structs.Chats
	AgentCode string
}

// List 获取Agent列表
type List struct {
	Session *structs.Chats
}

// Activate 激活Agent
type Activate struct {
	Session   *structs.Chats
	AgentCode string
	Prompt    string
}

// Deactivate 退出SubAgent
type Deactivate struct {
	Session *structs.Chats
	Prompt  string
}
