package parser

import (
	"strings"
	"unicode/utf8"

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
	TokenCache  string // 缓存正在解析中的标签名
	Mode        int16  // 状态机主模式
	KeyMode     int16  // 当前所处的逻辑区域
	atLineStart bool   // 当前是否位于行首
}

// AddToken 流式传入 token 并解析其中的 <think> 标签。
// 只返回（正文增量, 思考增量, error）；工具调用不经由此通道。
func (p *Parser) AddToken(token string, tokenThinking string) (string, string, error) {
	var response strings.Builder
	var responseThinking strings.Builder
	responseThinking.WriteString(tokenThinking)
	for i := 0; i < len(token); {
		char, size := utf8.DecodeRuneInString(token[i:])
		i += size
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
			if char == '<' || char == '\n' {
				// 标签名不可能包含 '<' 或换行：说明这里的 '<' 不是有效标签起始。
				// 先原样回吐已缓存的候选文本，再把当前字符按“标签外文本”重新处理；
				// 否则换行/后续真正的行首标签会被吞进候选并当成普通文本输出，
				// 行首状态停在 false，导致 '\n<think>' 这类标签再也不被识别。
				response.WriteString("<" + p.TokenCache)
				p.TokenCache = ""
				p.Mode = ModeOutside
				p.atLineStart = false
				i -= size
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
	return response.String(), responseThinking.String(), nil
}

// DoneToken 传入结束 token，回吐尚未输出的尾部文本。
func (p *Parser) DoneToken() (string, string, error) {
	switch p.Mode {
	case ModeOutside:
		return "", "", nil
	case ModeEnterTag:
		return "<" + p.TokenCache, "", nil
	case ModeInTag:
		if p.KeyMode == KeyModeThink {
			return "", "", nil
		}
	case ModePossibleEnd:
		if p.KeyMode == KeyModeThink {
			return "", "<", nil
		}
	case ModeEndTagName:
		if p.KeyMode == KeyModeThink {
			return "", "</" + p.TokenCache, nil
		}
	}
	return "", "", nil
}

// NewParser 创建解析器。工具调用由 NativeToolCallAccumulator 独立累积，
// 本解析器只负责正文与 <think>；同时重置会话的本轮临时数据容器
// （部分工具的 PreHook/OnHook 会往 TemporyDataOfRequest 里写内容）。
func NewParser(session *structs.Chats) *Parser {
	if session != nil {
		session.TemporyDataOfRequest = make(map[string]any)
	}
	return &Parser{atLineStart: true}
}
