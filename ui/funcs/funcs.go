package funcs

import (
	"context"
	"fmt"
	"slices"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/provider/request"
	"github.com/cxykevin/alkaid0/provider/request/agents"
	"github.com/cxykevin/alkaid0/storage"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/toolobj"
	"github.com/cxykevin/alkaid0/ui/state"
	"gorm.io/gorm"
)

// GetChats 获取所有聊天
func GetChats(db *gorm.DB) ([]structs.Chats, error) {
	chats := []structs.Chats{}
	err := db.Find(&chats).Error
	if err != nil {
		return chats, err
	}

	slices.SortFunc(chats, func(a, b structs.Chats) int {
		return int(a.ID) - int(b.ID)
	})
	return chats, nil
}

// DeleteChat 删除聊天
func DeleteChat(db *gorm.DB, chat *structs.Chats) error {
	return db.Delete(&structs.Chats{}, chat.ID).Error
}

// CreateChat 创建聊天
func CreateChat(db *gorm.DB) (uint32, error) {
	newChat := &structs.Chats{}
	tx := db.Create(newChat)
	if tx.Error != nil {
		return 0, tx.Error
	}
	return newChat.ID, nil
}

// InitChat 加载聊天
func InitChat(db *gorm.DB, chat *structs.Chats) (*structs.Chats, error) {
	session := *chat
	session.DB = db
	session.TemporyDataOfSession = make(map[string]any)
	actions.Load(&session)
	storage.GlobalConfig.LastChatID = session.ID
	err := agents.Load(&session)
	if err != nil {
		return nil, err
	}
	return &session, nil
}

// GetModelName 获取模型名称
func GetModelName(modelID uint32, defaultName string) string {
	if modelID == 0 {
		return defaultName
	}
	if modelInfo, ok := config.GlobalConfig.Model.Models[int32(modelID)]; ok {
		return modelInfo.ModelName
	}
	return defaultName
}

// PendingToolCall 待审批工具调用
// 约定：最近一次带 ToolCallingJSONString 的消息即待审批内容
func PendingToolCall(session *structs.Chats) ([]ToolCall, *structs.Messages, error) {
	if session.State != state.StateWaitApprove {
		return nil, nil, nil
	}
	var msg structs.Messages
	err := session.DB.Where("chat_id = ? AND tool_calling_json_string != ''", session.ID).Order("id DESC").First(&msg).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	if session.TemporyDataOfRequest == nil {
		session.TemporyDataOfRequest = make(map[string]any)
	}
	if err := request.ApplyToolOnHooks(session, msg.ToolCallingJSONString); err != nil {
		return nil, nil, err
	}
	tools, err := request.ParseToolsFromJSON(msg.ToolCallingJSONString)
	if err != nil {
		return nil, nil, err
	}
	return tools, &msg, nil
}

// AutoHandlePendingToolCalls 自动处理待审批工具调用
func AutoHandlePendingToolCalls(session *structs.Chats) (bool, bool, []ToolCall, error) {
	if session.State != state.StateWaitApprove {
		return false, false, nil, nil
	}
	tools, msg, err := PendingToolCall(session)
	if err != nil || msg == nil || len(tools) == 0 {
		return false, false, tools, err
	}
	approved, reason, err := request.CanAutoApprove(session, tools, msg)
	if err != nil {
		return false, false, tools, err
	}
	if approved {
		_, err = request.ExecuteToolCalls(session, msg.ToolCallingJSONString)
		return true, true, nil, err
	}
	if session.CurrentAgentID != "" || session.NowAgent != "" {
		err = request.RejectToolCallsNoDeactivate(session, reason, nil)
		return true, false, nil, err
	}
	return false, false, tools, nil
}

// ApproveToolCalls 允许执行待审批工具调用
func ApproveToolCalls(session *structs.Chats) error {
	if session.State != state.StateWaitApprove {
		return nil
	}
	var msg structs.Messages
	err := session.DB.Where("chat_id = ? AND tool_calling_json_string != ''", session.ID).Order("id DESC").First(&msg).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil
		}
		return err
	}
	if session.TemporyDataOfRequest == nil {
		session.TemporyDataOfRequest = make(map[string]any)
	}
	_, err = request.ExecuteToolCalls(session, msg.ToolCallingJSONString)
	return err
}

// ToolCall 工具调用
// 仅用于待审批展示
type ToolCall = request.ToolCall

// ModelList 模型列表
type ModelList struct {
	ID     int32
	Config cfgStructs.ModelConfig
}

// GetModels 获取所有模型
func GetModels() []ModelList {
	models := config.GlobalConfig.Model.Models
	modelsObj := make([]ModelList, len(models))
	idx := 0
	for id, model := range models {
		modelsObj[idx] = ModelList{ID: id, Config: model}
		idx++
	}
	slices.SortFunc(modelsObj, func(a, b ModelList) int {
		return int(a.ID) - int(b.ID)
	})
	return modelsObj
}

// GetModelInfo 获取模型信息
func GetModelInfo(modelID int32) (cfgStructs.ModelConfig, error) {
	modelInfo, ok := config.GlobalConfig.Model.Models[modelID]
	if !ok {
		return cfgStructs.ModelConfig{}, fmt.Errorf("model %d not found", modelID)
	}
	return modelInfo, nil
}

// SelectModel 选择模型
func SelectModel(session *structs.Chats, modelID int32) error {
	session.LastModelID = uint32(modelID)
	return session.DB.Save(&session).Error
}

// SummarySession 汇总会话
func SummarySession(ctx context.Context, session *structs.Chats) (string, error) {
	return request.SummarySession(context.Background(), session)
}

// GetHistory 获取历史消息
func GetHistory(session *structs.Chats) ([]structs.Messages, error) {
	chatMsgs := []structs.Messages{}
	err := session.DB.Where("chat_id = ?", session.ID).Order("id ASC").Find(&chatMsgs).Error
	return chatMsgs, err
}

// AgentTagsList 代理标签列表
type AgentTagsList struct {
	ID    string
	Agent cfgStructs.AgentConfig
}

// GetAgentTags 获取代理标签列表
func GetAgentTags() []AgentTagsList {
	agents := config.GlobalConfig.Agent.Agents
	agentsObj := make([]AgentTagsList, len(agents))
	idx := 0
	for i, agent := range agents {
		agentsObj[idx] = AgentTagsList{ID: i, Agent: agent}
		idx++
	}
	slices.SortFunc(agentsObj, func(a, b AgentTagsList) int {
		if a.ID < b.ID {
			return -1
		}
		return 1
	})
	return agentsObj
}

// GetAgents 获取代理实例
func GetAgents(session *structs.Chats) ([]structs.SubAgents, error) {
	return agents.ListAgents(session.DB)
}

// AddAgent 添加代理
func AddAgent(session *structs.Chats, agentCode string, agentID string, path string) error {
	return agents.AddAgent(session, agentCode, agentID, path)
}

// ActivateAgent 激活代理
func ActivateAgent(session *structs.Chats, agentCode string, prompt string) error {
	return agents.ActivateAgent(session, agentCode, prompt)
}

// DeactivateAgent 停用代理
func DeactivateAgent(session *structs.Chats, agentID string) error {
	return agents.DeactivateAgent(session, agentID)
}

// Scopes 作用域
type Scopes struct {
	ID     string
	Prompt string
}

// GetScopes 获取作用域
func GetScopes() []Scopes {
	obj := make([]Scopes, len(toolobj.Scopes))
	idx := 0
	for k, scope := range toolobj.Scopes {
		obj[idx] = Scopes{ID: k, Prompt: scope}
		idx++
	}
	slices.SortFunc(obj, func(a, b Scopes) int {
		if a.ID < b.ID {
			return -1
		}
		return 1
	})
	return obj
}

// EnableScope 启用作用域
func EnableScope(session *structs.Chats, scope string) error {
	return actions.EnableScope(session, scope)
}

// DisableScope 禁用作用域
func DisableScope(session *structs.Chats, scope string) error {
	return actions.DisableScope(session, scope)
}

// UserAddMsg 用户添加消息
func UserAddMsg(session *structs.Chats, msg string, refers *structs.MessagesReferList) error {
	return request.UserAddMsg(session, msg, refers)
}

// SendRequest 发送请求
func SendRequest(ctx context.Context, session *structs.Chats, callback func(string, string) error) (bool, error) {
	return request.SendRequest(ctx, session, callback)
}
