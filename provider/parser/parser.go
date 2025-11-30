package parser

// ToolsDefine 工具接口
type ToolsDefine struct {
	Name        string                    `json:"name"`
	Description string                    `json:"description"`
	Parameters  map[string]ToolParameters `json:"parameters"`
}

// ToolsResponse 工具返回接口
type ToolsResponse struct {
	Name       string         `json:"name"`
	ID         string         `json:"id"`
	Parameters map[string]any `json:"parameters"`
}

// ToolType 工具参数类型枚举
type ToolType string

// 类型枚举
const (
	ToolTypeString ToolType = "string"
	ToolTypeNumber ToolType = "number"
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
	Tools             []ToolsDefine
	TokenCache        string
	Mode              int16
	KeyMode           int16
	ToolCallingObject []ToolsResponse
	Stop              bool
	jsonParser        *JSONParser
}

// AddToken 流式传入 token
func (p *Parser) AddToken(token string) (string, string, *[]ToolsResponse, error) {
	response := ""
	responseThinking := ""
	for _, char := range token {
		// 状态机
		solveTag := func(tokens string) {
			if p.KeyMode == 1 {
				responseThinking += tokens
			} else {
				if p.jsonParser != nil {
					p.jsonParser.AddToken(tokens)
					// TODO: 解析参数
				}
			}
		}
		switch p.Mode {
		case 0: // 标签外
			if char == '<' {
				p.Mode = 1
				p.TokenCache = ""
				continue
			}
			response += string(char)
		case 1: // 入标签本身
			if char == '>' {
				switch p.TokenCache {
				case "think":
					p.KeyMode = 1
				case "tools":
					p.jsonParser = NewJSONParser()
					p.KeyMode = 2
				default:
					response += "<" + p.TokenCache + ">"
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
				response += "<" + p.TokenCache
				p.TokenCache = ""
				continue
			}
		case 2: // 标签内
			if char == '<' {
				p.Mode = 3
				continue
			}
			// 分类处理内容
			solveTag(string(char))
		case 3: // 出标签左尖括号
			if char == '/' {
				p.Mode = 4
				p.TokenCache = ""
				continue
			}
			p.Mode = 2
			// 分类处理内容
			solveTag(string(char))
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
				} else {
					solveTag("</" + p.TokenCache + ">")
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
				solveTag("</" + p.TokenCache)
				p.TokenCache = ""
				continue
			}
		}
	}
	return response, responseThinking, &p.ToolCallingObject, nil
}

// DoneToken 传入结束 token
func (p *Parser) DoneToken() (string, string, *[]ToolsResponse, error) {
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
func NewParser(tools []ToolsDefine) *Parser {
	return &Parser{Tools: tools}
}
