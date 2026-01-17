package parser

import (
	"errors"
	"strings"

	"github.com/cxykevin/alkaid0/library/json"
)

// import "github.com/cxykevin/alkaid0/log"

// var logger *log.LogsObj

// func init() {
// 	logger = log.New("parser")
// }

// ToolsResponse 工具返回类
type ToolsResponse struct {
	Name       string
	ID         string
	Parameters map[string]any
}

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

// 类型枚举
const (
	ToolTypeString ToolType = "string"
	ToolTypeInt    ToolType = "int"
	ToolTypeFloat  ToolType = "float"
	ToolTypeBoolen ToolType = "boolen"
	ToolTypeArray  ToolType = "array"
	ToolTypeObject ToolType = "object"
)

// ToolParameters 工具参数
type ToolParameters struct {
	Type        ToolType
	Required    bool
	Description string
}

const maxTagLen = 6

// Parser 流式解析器
type Parser struct {
	Tools        []*ToolsDefine
	TokenCache   string
	Mode         int16
	KeyMode      int16
	Stop         bool
	jsonParser   *json.Parser
	toolSolveTmp toolSolveTmp
	ToolResponse map[string]string
	CalledTools  bool
	ToolsSolved  []AIToolsResponse
}

type toolSolveTmp struct {
	toolNum int
}

func (p *Parser) findTool(toolName string) int {
	for idx, tool := range p.Tools {
		if tool.Name == toolName {
			return idx
		}
	}
	return -1
}

func (p *Parser) solveTool() {
	if p.jsonParser.FullCallingObject == nil {
		return
	}
	var pObjects []*any
	var ok bool
	if pObjects, ok = (*p.jsonParser.FullCallingObject).([]*any); !ok {
		// 尝试 ArraySlot
		if arraySlot, isArraySlot := (*p.jsonParser.FullCallingObject).(json.ArraySlot); isArraySlot {
			pObjects = []*any(arraySlot)
		} else {
			p.Stop = true
			return
		}
	}
	if len(pObjects) == 0 {
		return
	}
	for idx, pObject := range pObjects {
		if idx < p.toolSolveTmp.toolNum {
			continue
		}
		if pObject == nil {
			if idx != len(pObjects)-1 { // 不是最后一个元素
				p.Stop = true
				return
			}
			continue
		}
		var pTools map[string]*any
		var toolFinishTag bool = true
		if pTools, ok = (*pObject).(map[string]*any); !ok {
			toolFinishTag = false
			pTools, ok = (*pObject).(json.ObjectSlot)
			if !ok {
				p.Stop = true
				return
			}
		}
		toolNameOrigin, ok := pTools["name"]
		if !ok {
			if idx != len(pObjects)-1 { // 不是最后一个元素
				p.Stop = true
				return
			}
			continue
		}
		toolName, ok := (*toolNameOrigin).(string)
		if !ok {
			if idx != len(pObjects)-1 { // 不是最后一个元素
				p.Stop = true
				return
			}
			continue
		}
		// 在 tools 中寻找工具
		toolID := p.findTool(toolName)
		if toolID == -1 {
			p.Stop = true
			return
		}
		toolCallIDOrigin, ok := pTools["id"]
		if !ok {
			if idx != len(pObjects)-1 { // 不是最后一个元素
				p.Stop = true
				return
			}
			continue
		}
		toolCallID, ok := (*toolCallIDOrigin).(string)
		if !ok {
			if idx != len(pObjects)-1 { // 不是最后一个元素
				p.Stop = true
				return
			}
			continue
		}
		toolParametersOrigin, ok := pTools["parameters"]
		if !ok {
			if idx != len(pObjects)-1 { // 不是最后一个元素
				p.Stop = true
				return
			}
			continue
		}
		toolParameters, ok := (*toolParametersOrigin).(map[string]*any)
		if !ok {
			toolParameters, ok = (*toolParametersOrigin).(json.ObjectSlot)
			if !ok {
				if idx != len(pObjects)-1 { // 不是最后一个元素
					p.Stop = true
					return
				}
				continue
			}
		}
		// 参数类型校验
		for key, value := range toolParameters {
			switch p.Tools[toolID].Parameters[key].Type {
			case ToolTypeString:
				_, okStr := (*value).(string)
				_, okTmpStr := (*value).(json.StringSlot)
				if !okStr && !okTmpStr {
					p.Stop = true
					return
				}
			case ToolTypeInt: // 即使是 IntType，后端获取仍旧是 float64
				val, ok := (*value).(float64)
				if !ok {
					p.Stop = true
					return
				}
				// 校验是否为整数
				if val != float64(int64(val)) {
					p.Stop = true
					return
				}
			case ToolTypeFloat:
				_, ok := (*value).(float64)
				if !ok {
					p.Stop = true
					return
				}
			case ToolTypeBoolen:
				_, ok := (*value).(bool)
				if !ok {
					p.Stop = true
					return
				}
			case ToolTypeArray:
				_, ok := (*value).([]any)
				if !ok {
					p.Stop = true
					return
				}
			case ToolTypeObject:
				_, okMap := (*value).(map[string]*any)
				_, okMapSlot := (*value).(json.ObjectSlot)
				if !okMap && !okMapSlot {
					p.Stop = true
					return
				}
			}
		}
		// 调用工具
		err := p.Tools[toolID].Func(toolCallID, map[string]*any(toolParameters), toolFinishTag)
		if err != nil {
			p.Stop = true
			return
		}
		if toolFinishTag {
			p.ToolsSolved = append(p.ToolsSolved, AIToolsResponse{
				Name:       toolName,
				ID:         toolCallID,
				Parameters: map[string]*any(toolParameters),
			})
			p.toolSolveTmp.toolNum = idx + 1
		}
	}
}

// AddToken 流式传入 token
func (p *Parser) AddToken(token string, tokenThinking string) (string, string, *any, error) {
	if p.Stop {
		return "", "", nil, errors.New("parser stop")
	}
	var response strings.Builder
	var responseThinking strings.Builder
	responseThinking.WriteString(tokenThinking)
	for _, char := range token {
		// 状态机
		solveTag := func(tokens string) error {
			if p.KeyMode == 1 {
				responseThinking.WriteString(tokens)
			} else {
				if p.jsonParser != nil {
					p.jsonParser.AddToken(tokens)
					// TODO: 解析参数
					p.solveTool()
					if p.Stop {
						return errors.New("tool error")
					}
				}
			}
			return nil
		}
		switch p.Mode {
		case 0: // 标签外
			if char == '<' {
				p.Mode = 1
				p.TokenCache = ""
				continue
			}
			response.WriteString(string(char))
		case 1: // 入标签本身
			if char == '>' {
				switch p.TokenCache {
				case "think":
					p.KeyMode = 1
				case "tools":
					p.jsonParser = json.New()
					p.KeyMode = 2
				default:
					response.WriteString("<" + p.TokenCache + ">")
					p.TokenCache = ""
					p.Mode = 0
					continue
				}
				p.TokenCache = ""
				p.Mode = 2
				continue
			}
			p.TokenCache += string(char)
			if len(p.TokenCache) >= maxTagLen {
				p.Mode = 0
				response.WriteString("<" + p.TokenCache)
				p.TokenCache = ""
				continue
			}
		case 2: // 标签内
			if char == '<' {
				p.Mode = 3
				continue
			}
			// 分类处理内容
			err := solveTag(string(char))
			if err != nil {
				return "", "", nil, err
			}
		case 3: // 出标签左尖括号
			if char == '/' {
				p.Mode = 4
				p.TokenCache = ""
				continue
			}
			p.Mode = 2
			// 分类处理内容
			err := solveTag("<" + string(char))
			if err != nil {
				return "", "", nil, err
			}
		case 4: // 出标签本身
			if char == '>' {
				if p.KeyMode == 1 && p.TokenCache == "think" {
					p.KeyMode = 1
				} else if p.KeyMode == 2 && p.TokenCache == "tools" {
					p.KeyMode = 2
					err := p.jsonParser.DoneToken()
					if err != nil {
						return "", "", nil, err
					}
					p.CalledTools = true
				} else {
					err := solveTag("</" + p.TokenCache + ">")
					if err != nil {
						return "", "", nil, err
					}
					p.TokenCache = ""
					p.Mode = 2
					continue
				}
				p.Mode = 0
				continue
			}
			p.TokenCache += string(char)
			if len(p.TokenCache) >= maxTagLen {
				p.Mode = 2
				err := solveTag("</" + p.TokenCache)
				if err != nil {
					return "", "", nil, err
				}
				p.TokenCache = ""
				continue
			}
		}
	}
	return response.String(), responseThinking.String(), nil, nil
}

// DoneToken 传入结束 token
func (p *Parser) DoneToken() (string, string, *[]AIToolsResponse, error) {
	switch p.Mode {
	case 0: // 标签外
		// 无需处理
	case 1: // 入标签本身
		return "<" + p.TokenCache, "", nil, nil
	case 2: // 标签内
		if p.KeyMode == 1 {
			return "", "", nil, nil
		}
		return "", "", nil, nil
	case 3: // 出标签左尖括号
		if p.KeyMode == 1 {
			return "", "<", nil, nil
		}
		return "", "", nil, nil
	case 4: // 出标签本身
		if p.KeyMode == 1 {
			return "", "</" + p.TokenCache, nil, nil
		}
		return "", "", nil, nil
	}
	return "", "", nil, nil
}

// NewParser 创建解析器
func NewParser(tools []*ToolsDefine) *Parser {
	return &Parser{Tools: tools}
}
