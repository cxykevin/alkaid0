package response

import (
	"bytes"
	"encoding/json"

	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/provider/request/build"
	"github.com/cxykevin/alkaid0/storage"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
)

type toolSaveStruct struct {
	Name   string `json:"name"`
	ID     string `json:"id"`
	Return string `json:"return"`
}

// Solver 解析器
type Solver struct {
	parser        *parser.Parser
	toolResponses []toolSaveStruct
	chatID        uint32
}

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
		Return: buf.String(),
	})
	return nil
}

// AddToken 添加token
func (p *Solver) AddToken(token string, thinkingToken string) (string, string, error) {
	delta, reasoningDelta, _, err := p.parser.AddToken(token, thinkingToken)
	return delta, reasoningDelta, err
}

// DoneToken 完成
func (p *Solver) DoneToken() (bool, string, string, error) {
	delta, reasoningDelta, _, err := p.parser.DoneToken()
	if err != nil {
		return true, delta, reasoningDelta, err
	}
	if len(p.toolResponses) == 0 {
		return true, delta, reasoningDelta, nil
	}
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetIndent("", "    ")
	encoder.SetEscapeHTML(false)
	err = encoder.Encode(p.toolResponses)
	if err != nil {
		return true, delta, reasoningDelta, err
	}
	err = storage.DB.Create(&storageStructs.Messages{
		ChatID: p.chatID,
		Delta:  buf.String(),
		Type:   storageStructs.MessagesRoleTool,
	}).Error
	if err != nil {
		return true, delta, reasoningDelta, err
	}

	return !p.parser.CalledTools, delta, reasoningDelta, err
}

// NewSolver 创建解析器
func NewSolver(chatID uint32, agentID string) *Solver {
	obj := &Solver{chatID: chatID}
	obj.parser = parser.NewParser(*build.ToolsSolver(obj.saveToolResponse))
	return obj
}
