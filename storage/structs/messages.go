package structs

import (
	"bytes"
	"database/sql/driver"
	"encoding/gob"
	"encoding/json"

	"gorm.io/gorm"
)

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
	gob.Register(MessagesRefer{})
	gob.Register(MessagesReferList{})
}

// Value 实现 driver.Valuer 接口，用于 GORM 写入数据库
func (m MessagesReferList) Value() (driver.Value, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(m)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Scan 实现 sql.Scanner 接口，用于 GORM 从数据库读取
func (m *MessagesReferList) Scan(src any) error {
	switch v := src.(type) {
	case []byte:
		dec := gob.NewDecoder(bytes.NewReader(v))
		return dec.Decode(m)
	case string:
		dec := gob.NewDecoder(bytes.NewReader([]byte(v)))
		return dec.Decode(m)
	case nil:
		*m = MessagesReferList{}
		return nil
	default:
		*m = MessagesReferList{}
		return nil
	}
}

// Messages 消息列表
type Messages struct {
	ID uint64 `gorm:"primaryKey;autoIncrement"`
	// ParentID 树形消息的父节点 ID（NULL = 根节点）。
	// 线性历史迁移时按「同会话内 id 小于当前行的最大 id」回填；fork/rewind 分支的
	// 祖先指针，递归查询依赖 idx_parent_id 索引。
	ParentID *uint64 `gorm:"index:idx_parent_id"`
	// ChatID 所属会话。回填 SQL 与后续按会话查树的查询都依赖该索引。
	ChatID        uint32 `gorm:"index:idx_chat_id"`
	AgentID       *string
	Delta         string `gorm:"type:text"`
	Summary       string `gorm:"type:text"`
	ThinkingDelta string `gorm:"type:text"`
	// 设计注记：本表与会话之间有意不建立外键/关联——deleteChat 只删会话表、保留聊天
	// 记录（见 ui/funcs.DeleteChat），子表数据既不应阻止会话行删除、也不应被级联删除。
	// 历史库遗留的 fk_messages_chats 外键由 storage/migrate.MigrateRemoveChatForeignKeys
	// 一次性移除；全新库不再生成。ChatID 仍是普通索引列（idx_chat_id）。
	// SubAgents             SubAgents         `gorm:"foreignKey:AgentID;constraints:OnDelete:RESTRICT;OnUpdate:CASCADE"`
	Refers                MessagesReferList `gorm:"type:bytes;serialize:gob"`
	ToolCallingJSONString string            `gorm:"type:text"`
	// ToolCallingContent 工具调用展示内容（ACP v2 tool_call_update 的 content 数组 JSON）。
	// 格式：{"<工具调用ID>": [content...]}，含编辑后的 ACP v2 Diffs 段，会话还原时按工具 ID 重放。
	ToolCallingContent string `gorm:"type:text"`
	// ToolFinished          bool              `gorm:"default:false"`
	Time             uint64 `gorm:"autoCreateTime"`
	ModelName        string
	ModelID          uint32
	Type             MessagesRole
	PromptTokens     uint32
	CompletionTokens uint32
	TotalTokens      uint32
	CachedTokens     uint32
}

// SaveToolCallingContent 把单个工具调用的展示 content 按工具调用 ID 合并写入消息的
// tool_calling_content 列（格式 {"<toolID>": [content...]}），供 session/resume 历史回放
// 按 ID 重放 content（含 alk.cxykevin.top/calling_info 参数）。db 为空、messageID 为 0
// 或 toolID 为空时静默跳过，返回 nil。
func SaveToolCallingContent(db *gorm.DB, messageID uint64, toolID string, content any) error {
	if db == nil || messageID == 0 || toolID == "" || content == nil {
		return nil
	}
	var msg Messages
	if err := db.First(&msg, messageID).Error; err != nil {
		return err
	}
	contentMap := map[string]any{}
	if msg.ToolCallingContent != "" {
		if err := json.Unmarshal([]byte(msg.ToolCallingContent), &contentMap); err != nil {
			// 旧数据损坏时丢弃，避免单个工具调用写坏整列。
			contentMap = map[string]any{}
		}
	}
	contentMap[toolID] = content
	b, err := json.Marshal(contentMap)
	if err != nil {
		return err
	}
	return db.Model(&Messages{}).Where("id = ?", messageID).Update("tool_calling_content", string(b)).Error
}
