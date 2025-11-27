package storage

// ChatType 聊天类型枚举
type ChatType uint8

// 枚举值
const (
	ChatTypeUser ChatType = iota
	ChatTypeAssistant
	ChatTypeToolCalling
)

// ChatMessageObject 聊天消息
type ChatMessageObject struct {
	ID        int32
	Reasoning string
	Message   string
	Type      ChatType
	Resources []byte
	Model     string
	ModelName string
}

// ChatHistory 聊天历史
type ChatHistory struct {
	Summary              string
	SummayID             int32
	Content              []*ChatMessageObject
	LastID               int32
	CurrentAgentID       int32
	CurrentModelID       int32
	PromptTokensCost     int64
	CompletionTokensCost int64
	TotalTokensCost      int64
}
