package response

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/cxykevin/alkaid0/log"

	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/provider/request/build"
	"github.com/cxykevin/alkaid0/provider/request/structs"
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

// Solver LLM 响应流式解析器，管理 token 的增量解析与原生工具调用结果的保存。
// parser 负责普通文本和 <think> 内容，nativeAcc 负责原生 tool_calls 增量。
type Solver struct {
	parser         *parser.Parser                    // 普通文本与 <think> 解析器
	nativeAcc      *parser.NativeToolCallAccumulator // 原生 tool_calls 累积器
	toolResponses  []toolSaveStruct                  // 工具调用响应缓存
	responsesSaved bool                              // 防止 DoneToken 重复落库
	chatID         uint32                            // 当前会话 ID
	db             *gorm.DB                          // 数据库连接
	session        *storageStructs.Chats             // 当前会话信息
}

// saveToolResponse 将工具调用响应序列化后存入缓存列表
func (p *Solver) saveToolResponse(toolName string, toolID string, response map[string]*any) error {
	// 空结果也生成一条记录（序列化为 {}），否则 LLM 下一轮看不到任何工具返回，
	// 会误以为工具未被调用而重复调用或产生幻觉。
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	err := encoder.Encode(response)
	if err != nil {
		return err
	}
	content := strings.TrimSpace(buf.String())
	if content == "" {
		content = "{}"
	}
	p.toolResponses = append(p.toolResponses, toolSaveStruct{
		Name:   toolName,
		ID:     toolID,
		Return: content,
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

// AddNativeToolCallDelta 原生模式：喂入一个流式 delta.tool_calls 增量（可含多个 index）。
func (p *Solver) AddNativeToolCallDelta(deltas []structs.StreamToolCall) error {
	if p.nativeAcc == nil {
		return nil
	}
	for i := range deltas {
		d := &deltas[i]
		var name, arguments string
		if d.Function != nil {
			name = d.Function.Name
			arguments = d.Function.Arguments
		}
		if err := p.nativeAcc.AddDelta(d.Index, d.ID, name, arguments); err != nil {
			return err
		}
	}
	return nil
}

// DoneToken 结束解析并返回最终结果。
// 如果解析过程中有工具响应（toolResponses），序列化后以 MessageRoleTool 类型存入数据库。
// 返回的 bool 值表示是否还有更多工具调用待处理（CalledTools=false 表示解析结束）。
func (p *Solver) DoneToken() (bool, string, string, error) {
	if p.nativeAcc != nil {
		if err := p.nativeAcc.DoneToken(); err != nil {
			return true, "", "", err
		}
	}
	delta, reasoningDelta, _, err := p.parser.DoneToken()
	if err != nil {
		return true, delta, reasoningDelta, err
	}
	if len(p.toolResponses) > 0 && !p.responsesSaved {
		var buf bytes.Buffer
		encoder := json.NewEncoder(&buf)
		encoder.SetIndent("", "    ")
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(p.toolResponses); err != nil {
			return true, delta, reasoningDelta, err
		}
		if err := p.db.Create(&storageStructs.Messages{
			ChatID:  p.chatID,
			Delta:   buf.String(),
			Type:    storageStructs.MessagesRoleTool,
			AgentID: &p.session.CurrentAgentID,
		}).Error; err != nil {
			return true, delta, reasoningDelta, err
		}
		p.responsesSaved = true
	}
	if p.nativeAcc == nil {
		return true, delta, reasoningDelta, nil
	}
	return !p.nativeAcc.HasTools(), delta, reasoningDelta, nil
}

func (p *Solver) GetTools() []parser.AIToolsResponse {
	if p.nativeAcc == nil {
		return nil
	}
	return p.nativeAcc.GetTools()
}

// GetToolsOrigin 获取原生工具调用的内部 JSON 表示，用于调试和日志记录。
func (p *Solver) GetToolsOrigin() string {
	if p.nativeAcc == nil {
		return ""
	}
	return p.nativeAcc.Origin()
}

// NewSolver 创建响应解析器。
// 使用 build.ToolsSolver 构建工具求解器列表，并将 saveToolResponse 注册为工具执行回调。
// 每个 Solver 实例绑定到一个会话，用于处理单次 LLM 响应的解析和工具调用管理。
func NewSolver(db *gorm.DB, session *storageStructs.Chats) *Solver {
	obj := &Solver{chatID: session.ID, db: db, session: session}
	obj.parser = parser.NewParser(session, *build.ToolsSolver(session, obj.saveToolResponse))
	return obj
}

// NewNativeSolver 创建原生 tool_calls 模式的 Solver。
// parser 仅处理普通文本与 <think>，tool_calls 由 nativeAcc 累积。
func NewNativeSolver(db *gorm.DB, session *storageStructs.Chats) *Solver {
	obj := NewSolver(db, session)
	obj.nativeAcc = parser.NewNativeToolCallAccumulator(session, *build.ToolsSolver(session, obj.saveToolResponse))
	return obj
}
