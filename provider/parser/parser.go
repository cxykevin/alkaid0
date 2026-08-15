package parser

import (
	"errors"
	"strings"

	"github.com/cxykevin/alkaid0/log"
	structs "github.com/cxykevin/alkaid0/storage/structs"
)

// logger 包级日志对象
var logger = log.New("parser")

// ToolsDefine 工具接口
type ToolsDefine struct {
	Name        string                                    `json:"name"`
	Description string                                    `json:"description"`
	Parameters  map[string]ToolParameters                 `json:"parameters"`
	Func        func(string, map[string]*any, bool) error `json:"-"`
}

// AIToolsResponse 工具返回接口
type AIToolsResponse struct {
	Name       string          `json:"name"`
	ID         string          `json:"id"`
	Parameters map[string]*any `json:"parameters"`
}

// ToolType 工具参数类型枚举
type ToolType string

const (
	ToolTypeString  ToolType = "string"
	ToolTypeNumber  ToolType = "number"
	ToolTypeBoolean ToolType = "boolean"
	ToolTypeArray   ToolType = "array"
	ToolTypeObject  ToolType = "object"
)

// ToolParameters 工具参数
type ToolParameters struct {
	Type        ToolType `json:"type"`
	Required    bool     `json:"required"`
	Description string   `json:"description,omitempty"`
}

const maxTagLen = 6

// 状态机主模式常量
const (
	ModeOutside     int16 = iota // 0-标签外
	ModeEnterTag                 // 1-进入标签起始
	ModeInTag                    // 2-标签内容
	ModePossibleEnd              // 3-可能的结束标签起始
	ModeEndTagName               // 4-结束标签名解析
)

// KeyMode 逻辑区域常量
const (
	KeyModeNormal int16 = iota // 0-普通文本
	KeyModeThink               // 1-思考(think)
)

// Parser 流式解析器，仅负责从 AI 响应流中提取 <think> 标签内容。
// 原生 tool_calls 由 NativeToolCallAccumulator 独立累积。
type Parser struct {
	Session     *structs.Chats
	TokenCache  string // 缓存正在解析中的标签名
	Mode        int16  // 状态机主模式
	KeyMode     int16  // 当前所处的逻辑区域
	Stop        bool   // 发生错误时停止解析
	atLineStart bool   // 当前是否位于行首
}

// AddToken 流式传入 token 并解析其中的 <think> 标签。
func (p *Parser) AddToken(token string, tokenThinking string) (string, string, *any, error) {
	if p.Stop {
		return "", "", nil, errors.New("parser stop")
	}
	var response strings.Builder
	var responseThinking strings.Builder
	responseThinking.WriteString(tokenThinking)
	for _, char := range token {
		solveThink := func(tokens string) {
			if p.KeyMode == KeyModeThink {
				responseThinking.WriteString(tokens)
			} else {
				response.WriteString(tokens)
				if strings.HasSuffix(tokens, "\n") {
					p.atLineStart = true
				} else {
					p.atLineStart = false
				}
			}
		}
		switch p.Mode {
		case ModeOutside:
			if char == '\n' {
				response.WriteRune(char)
				p.atLineStart = true
				continue
			}
			if p.atLineStart && char == '<' {
				p.Mode = ModeEnterTag
				p.TokenCache = ""
				p.atLineStart = false
				continue
			}
			response.WriteRune(char)
			p.atLineStart = false
		case ModeEnterTag:
			if char == '>' {
				if p.TokenCache == "think" {
					logger.Debug("entering think mode")
					p.KeyMode = KeyModeThink
					p.TokenCache = ""
					p.Mode = ModeInTag
					continue
				}
				response.WriteString("<" + p.TokenCache + ">")
				p.TokenCache = ""
				p.Mode = ModeOutside
				p.atLineStart = false
				continue
			}
			p.TokenCache += string(char)
			if len(p.TokenCache) >= maxTagLen {
				response.WriteString("<" + p.TokenCache)
				p.TokenCache = ""
				p.Mode = ModeOutside
				p.atLineStart = false
			}
		case ModeInTag:
			if char == '<' {
				p.Mode = ModePossibleEnd
				continue
			}
			solveThink(string(char))
		case ModePossibleEnd:
			if char == '/' {
				p.Mode = ModeEndTagName
				p.TokenCache = ""
				continue
			}
			p.Mode = ModeInTag
			solveThink("<" + string(char))
		case ModeEndTagName:
			if char == '>' {
				if p.KeyMode == KeyModeThink && p.TokenCache == "think" {
					logger.Debug("exiting think mode")
					p.KeyMode = KeyModeNormal
					p.Mode = ModeOutside
					p.TokenCache = ""
					p.atLineStart = false
					continue
				}
				solveThink("</" + p.TokenCache + ">")
				p.TokenCache = ""
				p.Mode = ModeInTag
				continue
			}
			p.TokenCache += string(char)
			if len(p.TokenCache) >= maxTagLen {
				solveThink("</" + p.TokenCache)
				p.TokenCache = ""
				p.Mode = ModeInTag
			}
		}
	}
	return response.String(), responseThinking.String(), nil, nil
}

// DoneToken 传入结束 token。
func (p *Parser) DoneToken() (string, string, *[]AIToolsResponse, error) {
	switch p.Mode {
	case ModeOutside:
		return "", "", nil, nil
	case ModeEnterTag:
		return "<" + p.TokenCache, "", nil, nil
	case ModeInTag:
		if p.KeyMode == KeyModeThink {
			return "", "", nil, nil
		}
	case ModePossibleEnd:
		if p.KeyMode == KeyModeThink {
			return "", "<", nil, nil
		}
	case ModeEndTagName:
		if p.KeyMode == KeyModeThink {
			return "", "</" + p.TokenCache, nil, nil
		}
	}
	return "", "", nil, nil
}

// NewParser 创建解析器。tools 参数保留以兼容现有构造调用；工具累积由 native.go 负责。
func NewParser(session *structs.Chats, _ []*ToolsDefine) *Parser {
	if session != nil {
		session.TemporyDataOfRequest = make(map[string]any)
	}
	return &Parser{Session: session, atLineStart: true}
}
