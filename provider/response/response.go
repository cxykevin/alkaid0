package response

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/cxykevin/alkaid0/log"

	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/provider/request/build"
	"github.com/cxykevin/alkaid0/storage/structs"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"gorm.io/gorm"
)

// toolSaveStruct 工具响应持久化结构
type toolSaveStruct struct {
	Name   string `json:"name"`
	ID     string `json:"id"`
	Return string `json:"return"`
}

// logger 包级日志对象
var logger *log.LogsObj

func init() {
	logger = log.New("response")
}

// Solver LLM 响应流式解析器，管理 token 的增量解析与工具调用结果的保存
type Solver struct {
	parser        *parser.Parser   // JSON 解析器
	toolResponses []toolSaveStruct // 工具调用响应缓存
	chatID        uint32           // 当前会话 ID
	db            *gorm.DB         // 数据库连接
	session       *structs.Chats   // 当前会话信息
}

// saveToolResponse 将工具调用响应序列化后存入缓存列表
func (p *Solver) saveToolResponse(toolName string, toolID string, response map[string]*any) error {
	// 判断map是否为空
	if len(response) == 0 {
		return nil
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(response)
	if err != nil {
		return err
	}
	p.toolResponses = append(p.toolResponses, toolSaveStruct{
		Name:   toolName,
		ID:     toolID,
		Return: strings.TrimSpace(buf.String()),
	})
	logger.Debug("tool response saved: %s (ID: %s)", toolName, toolID)
	return nil
}

// AddToken 向解析器添加一个 token 进行流式解析。
// 返回过滤掉特殊标签后的增量响应文本和思考内容。
func (p *Solver) AddToken(token string, thinkingToken string) (string, string, error) {
	delta, reasoningDelta, _, err := p.parser.AddToken(token, thinkingToken)
	return delta, reasoningDelta, err
}

// DoneToken 结束解析并返回最终结果。
// 如果解析过程中有工具响应（toolResponses），序列化后以 MessageRoleTool 类型存入数据库。
// 返回的 bool 值表示是否还有更多工具调用待处理（CalledTools=false 表示解析结束）。
func (p *Solver) DoneToken() (bool, string, string, error) {
	delta, reasoningDelta, _, err := p.parser.DoneToken()
	if err != nil {
		return true, delta, reasoningDelta, err
	}
	// 无工具响应时直接返回，无需持久化
	if len(p.toolResponses) == 0 {
		return true, delta, reasoningDelta, nil
	}
	// 将所有工具响应序列化为 JSON 数组并存入 messages 表
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetIndent("", "    ")
	encoder.SetEscapeHTML(false)
	err = encoder.Encode(p.toolResponses)
	if err != nil {
		return true, delta, reasoningDelta, err
	}
	err = p.db.Create(&storageStructs.Messages{
		ChatID:  p.chatID,
		Delta:   buf.String(),
		Type:    storageStructs.MessagesRoleTool,
		AgentID: &p.session.CurrentAgentID,
	}).Error
	if err != nil {
		return true, delta, reasoningDelta, err
	}

	return !p.parser.CalledTools, delta, reasoningDelta, err
}

// GetTools 获取解析器已解决的工具调用列表
func (p *Solver) GetTools() []parser.AIToolsResponse {
	return p.parser.ToolsSolved
}

// GetToolsOrigin 获取工具调用的原始 JSON 字符串，用于调试和日志记录
func (p *Solver) GetToolsOrigin() string {
	return p.parser.ToolOriginString.String()
}

// NewSolver 创建响应解析器。
// 使用 build.ToolsSolver 构建工具求解器列表，并将 saveToolResponse 注册为工具执行回调。
// 每个 Solver 实例绑定到一个会话，用于处理单次 LLM 响应的解析和工具调用管理。
func NewSolver(db *gorm.DB, session *structs.Chats) *Solver {
	obj := &Solver{chatID: session.ID, db: db, session: session}
	obj.parser = parser.NewParser(session, *build.ToolsSolver(session, obj.saveToolResponse))
	return obj
}
