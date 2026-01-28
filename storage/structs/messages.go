package structs

import "encoding/gob"

// MessagesReferType 消息引用类型
type MessagesReferType uint8

// 消息引用类型
const (
	MessagesReferTypeFile MessagesReferType = iota
	MessagesReferTypeText
	MessagesReferTypeImage
	// MessagesReferTypeAudio
	// MessagesReferTypeVideo
)

// MessagesRole 消息类型
type MessagesRole uint8

// 消息引用类型
const (
	MessagesRoleUser MessagesRole = iota
	MessagesRoleAgent
	MessagesRoleTool
	MessagesRoleCommunicate
	// 注： Tool Calling 在记录时被归到 Agent 响应，但是用户展示时使用 Tool 中内容
)

// MessagesRefer 消息引用
type MessagesRefer struct {
	FilePath     string
	FileType     MessagesReferType
	FileFromLine int32
	FileFromCol  int32
	FileToLine   int32
	FileToCol    int32
	Origin       []byte
}

// MessagesReferList 消息引用
type MessagesReferList []MessagesRefer

// 使用gob注册消息引用列表
func init() {
	gob.Register(MessagesReferList{})
}

// Messages 消息列表
type Messages struct {
	ID                    uint64 `gorm:"primaryKey;autoIncrement"`
	ChatID                uint32
	AgentID               *string
	Delta                 string            `gorm:"type:text"`
	Summary               string            `gorm:"type:text"`
	ThinkingDelta         string            `gorm:"type:text"`
	Chats                 Chats             `gorm:"foreignKey:ChatID;constraints:OnDelete:RESTRICT;OnUpdate:CASCADE"`
	SubAgents             SubAgents         `gorm:"foreignKey:AgentID;constraints:OnDelete:RESTRICT;OnUpdate:CASCADE"`
	Refers                MessagesReferList `gorm:"type:bytes;serialize:gob"`
	ToolCallingJSONString string            `gorm:"type:text"`
	// ToolFinished          bool              `gorm:"default:false"`
	Time      uint64 `gorm:"autoCreateTime"`
	ModelName string
	ModelID   uint32
	Type      MessagesRole
}
