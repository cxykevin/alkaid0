package parser

import (
	"errors"
	"strings"

	"github.com/cxykevin/alkaid0/library/json"
	"github.com/cxykevin/alkaid0/log"
	structs "github.com/cxykevin/alkaid0/storage/structs"
)

// logger 包级日志对象
var logger = log.New("parser")

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
	ToolTypeString  ToolType = "string"
	ToolTypeNumber  ToolType = "number"
	ToolTypeBoolean ToolType = "boolean"
	ToolTypeArray   ToolType = "array"
	ToolTypeObject  ToolType = "object"
)

// ToolParameters 工具参数
type ToolParameters struct {
	Type        ToolType
	Required    bool
	Description string
}

const maxTagLen = 6

// Parser 流式解析器，负责从 AI 响应流中提取 <think> 和 <tools> 标签内容。
// 它使用状态机处理可能被切分的 token，确保在流式传输中准确识别标签边界。
type Parser struct {
	Session          *structs.Chats
	Tools            []*ToolsDefine
	TokenCache       string // 缓存正在解析中的标签名（如 "think" 或 "tools"）
	Mode             int16  // 状态机主模式：0-外部, 1-进入标签, 2-标签内容, 3-可能的结束标签起始, 4-结束标签名解析
	KeyMode          int16  // 当前所处的逻辑区域：0-普通文本, 1-思考(think), 2-工具调用(tools)
	Stop             bool   // 发生错误时停止解析
	jsonParser       *json.Parser
	toolSolveTmp     toolSolveTmp
	ToolResponse     map[string]string
	CalledTools      bool // 标记当前请求是否触发了工具调用
	ToolsSolved      []AIToolsResponse
	ToolOriginString strings.Builder
}

type toolSolveTmp struct {
	toolNum int // 记录已处理的工具数量，用于流式解析 JSON 数组时跳过已处理项
}

func (p *Parser) findTool(toolName string) int {
	for idx, tool := range p.Tools {
		if tool.Name == toolName {
			return idx
		}
	}
	return -1
}

// solveTool 解析并执行工具调用。
// 由于工具调用是以 JSON 数组形式流式传输的，此函数会被多次调用以处理新到达的数组元素。
func (p *Parser) solveTool() {
	// 无已解析数据时跳过（流式传输初期可能尚未接收到有效 JSON）
	if p.jsonParser.FullCallingObject == nil {
		return
	}
	var pObjects []*any
	var ok bool
	// 尝试获取当前已解析出的对象数组。
	// jsonParser 在解析过程中会不断更新 FullCallingObject。
	if pObjects, ok = (*p.jsonParser.FullCallingObject).([]*any); !ok {
		// 若 FullCallingObject 尚未解析为完整数组，尝试以 ArraySlot（json 库定义的未完成数组占位符）读取
		// 流式解析中对象可能在后续 token 到达后才转为完整类型
		if arraySlot, isArraySlot := (*p.jsonParser.FullCallingObject).(json.ArraySlot); isArraySlot {
			pObjects = []*any(arraySlot)
		} else {
			logger.Error("failed to cast FullCallingObject to array")
			p.Stop = true
			return
		}
	}
	if len(pObjects) == 0 {
		return
	}
	// 遍历数组，处理新出现的工具调用对象
	// toolSolveTmp.toolNum 记录上次已处理的元素数量
	// 跳过已处理的元素，只处理新增部分
	for idx, pObject := range pObjects {
		// 跳过此前已处理完成的工具调用，只处理新增元素
		if idx < p.toolSolveTmp.toolNum {
			continue
		}
		if pObject == nil {
			// 非最后一个元素为 nil 通常意味着 JSON 格式异常，停止解析
			if idx != len(pObjects)-1 {
				logger.Warn("nil object at index %d (not last)", idx)
				p.Stop = true
				return
			}
			continue
		}
		var pTools map[string]*any
		var toolFinishTag bool = true
		// 尝试将对象转换为 map，如果转换失败则可能是 ObjectSlot（json 库定义的未完成对象占位符）
		// 流式场景中对象字段可能尚未全部到达，此时为 ObjectSlot 而非完整 map
		if pTools, ok = (*pObject).(map[string]*any); !ok {
			toolFinishTag = false // 标记该工具调用对象尚未完全接收（字段可能还在增加）
			pTools, ok = (*pObject).(json.ObjectSlot)
			if !ok {
				logger.Error("failed to cast object at index %d to map or ObjectSlot", idx)
				p.Stop = true
				return
			}
		}
		// 从 JSON 对象中提取工具调用的三个必需字段：name（工具名）、id（调用 ID）、parameters（参数）
		// 字段缺失仅在非末尾元素时视为致命错误，末尾元素可能因流式传输被截断
		toolNameOrigin, ok := pTools["name"]
		if !ok {
			if idx != len(pObjects)-1 {
				logger.Warn("missing 'name' field at index %d", idx)
				p.Stop = true
				return
			}
			continue
		}
		toolName, ok := (*toolNameOrigin).(string)
		if !ok {
			if idx != len(pObjects)-1 {
				logger.Warn("'name' field is not string at index %d", idx)
				p.Stop = true
				return
			}
			continue
		}
		// 在已注册的工具列表中查找匹配定义，若找不到则停止解析
		toolID := p.findTool(toolName)
		if toolID == -1 {
			logger.Error("tool not found: %s", toolName)
			p.Stop = true
			return
		}
		toolCallIDOrigin, ok := pTools["id"]
		if !ok {
			if idx != len(pObjects)-1 {
				logger.Warn("missing 'id' field at index %d", idx)
				p.Stop = true
				return
			}
			continue
		}
		toolCallID, ok := (*toolCallIDOrigin).(string)
		if !ok {
			if idx != len(pObjects)-1 {
				logger.Warn("'id' field is not string at index %d", idx)
				p.Stop = true
				return
			}
			continue
		}
		toolParametersOrigin, ok := pTools["parameters"]
		if !ok {
			if idx != len(pObjects)-1 {
				logger.Warn("missing 'parameters' field at index %d", idx)
				p.Stop = true
				return
			}
			continue
		}
		toolParameters, ok := (*toolParametersOrigin).(map[string]*any)
		if !ok {
			toolParameters, ok = (*toolParametersOrigin).(json.ObjectSlot)
			if !ok {
				if idx != len(pObjects)-1 {
					logger.Error("'parameters' field is not map or ObjectSlot at index %d", idx)
					p.Stop = true
					return
				}
				continue
			}
		}
		// 实时参数类型校验，确保在工具执行前捕获 AI 的格式错误
		// 校验规则：根据工具定义的参数类型，检查实际 JSON 值的 Go 类型是否匹配
		// 注意：对于 string 和 object 类型还需检查对应的 Slot 占位符类型（流式解析未完成状态）
		for key, value := range toolParameters {
			switch p.Tools[toolID].Parameters[key].Type {
			case ToolTypeString:
				_, okStr := (*value).(string)
				_, okTmpStr := (*value).(json.StringSlot)
				if !okStr && !okTmpStr {
					logger.Warn("parameter '%s' for tool '%s' expected string, got %T", key, toolName, *value)
					p.Stop = true
					return
				}
			case ToolTypeNumber:
				_, ok := (*value).(float64)
				if !ok {
					logger.Warn("parameter '%s' for tool '%s' expected number(float64), got %T", key, toolName, *value)
					p.Stop = true
					return
				}
			case ToolTypeBoolean:
				_, ok := (*value).(bool)
				if !ok {
					logger.Warn("parameter '%s' for tool '%s' expected bool, got %T", key, toolName, *value)
					p.Stop = true
					return
				}
			case ToolTypeArray:
				_, ok := (*value).([]any)
				if !ok {
					logger.Warn("parameter '%s' for tool '%s' expected array, got %T", key, toolName, *value)
					p.Stop = true
					return
				}
			case ToolTypeObject:
				_, okMap := (*value).(map[string]*any)
				_, okMapSlot := (*value).(json.ObjectSlot)
				if !okMap && !okMapSlot {
					logger.Warn("parameter '%s' for tool '%s' expected object, got %T", key, toolName, *value)
					p.Stop = true
					return
				}
			}
		}
		// 调用工具的回调函数（如更新 UI 或执行预检）
		// toolFinishTag 告知回调本次调用是否为完整对象（对实时预览 UI 有意义）
		if p.Tools[toolID].Func != nil {
			err := p.Tools[toolID].Func(toolCallID, map[string]*any(toolParameters), toolFinishTag)
			if err != nil {
				logger.Error("tool function error: %v", err)
				p.Stop = true
				return
			}
		}
		// 如果该工具调用对象已完全接收（toolFinishTag 为 true），则将其加入已解决列表
		// 已解决的工具会触发后续执行流程，未完成的工具等待更多 token 到达后再次解析
		if toolFinishTag {
			logger.Info("tool call solved: %s (id: %s)", toolName, toolCallID)
			p.ToolsSolved = append(p.ToolsSolved, AIToolsResponse{
				Name:       toolName,
				ID:         toolCallID,
				Parameters: map[string]*any(toolParameters),
			})
			p.toolSolveTmp.toolNum = idx + 1
			// 当一个工具调用完全解析完成后，清除 TemporyDataOfRequest。
			// 这是为了确保下一个工具调用的预览状态是干净的。
			if p.Session != nil {
				p.Session.TemporyDataOfRequest = make(map[string]any)
			}
		}
	}
}

// AddToken 流式传入 token 并解析其中的特殊标签。
// 它会返回过滤掉特殊标签后的普通文本响应和思考内容。
// 采用 5 状态状态机处理可能被切分的标签边界：
//
//	0-标签外 → 1-进入标签起始 → 2-标签内容 → 3-可能的结束标签起始 → 4-结束标签名解析
//
// 特殊标签：<think>（模型思考过程）和 <tools>（工具调用 JSON 数组）
func (p *Parser) AddToken(token string, tokenThinking string) (string, string, *any, error) {
	if p.Stop {
		return "", "", nil, errors.New("parser stop")
	}
	var response strings.Builder
	var responseThinking strings.Builder
	// 预先追加来自上游的思考内容（可能由 API 层额外提供的 think token）
	responseThinking.WriteString(tokenThinking)
	// 逐字符解析，确保标签边界即使被 token 切分也能正确处理
	for _, char := range token {
		// solveTag 根据当前 KeyMode 将标签内内容分发到对应的缓冲区或解析器
		solveTag := func(tokens string) error {
			if p.KeyMode == 1 { // 处于 <think> 标签内：累积到响应思考内容
				responseThinking.WriteString(tokens)
			} else { // 处于 <tools> 标签内：交由 jsonParser 增量解析工具调用
				p.ToolOriginString.WriteString(tokens)
				if p.jsonParser != nil {
					p.jsonParser.AddToken(tokens)
					// 每次收到新 token 后尝试解析工具调用（增量处理新增的 JSON 元素）
					p.solveTool()
					if p.Stop {
						return errors.New("tool error")
					}
				}
			}
			return nil
		}
		switch p.Mode {
		case 0: // 状态：标签外。寻找标签起始符 '<'。
			// 绝大多数文本在这里直接输出到普通响应缓冲区
			if char == '<' {
				p.Mode = 1
				p.TokenCache = ""
				continue
			}
			response.WriteString(string(char))
		case 1: // 状态：已收到 '<'，正在解析标签名。
			if char == '>' {
				switch p.TokenCache {
				case "think":
					// 匹配 <think> 标签，后续内容进入思考模式
					logger.Debug("entering think mode")
					logger.Info("Parser: entering think mode")
					p.KeyMode = 1
				case "tools":
					// 匹配 <tools> 标签，创建 JSON 解析器开始解析工具调用数组
					logger.Debug("entering tools mode")
					logger.Info("Parser: entering tools mode")
					p.jsonParser = json.New()
					p.KeyMode = 2
				default:
					// 非预期的标签（如 `<random>`），原样退回给普通响应
					response.WriteString("<" + p.TokenCache + ">")
					p.TokenCache = ""
					p.Mode = 0
					continue
				}
				p.TokenCache = ""
				p.Mode = 2 // 进入标签内容解析模式
				continue
			}
			p.TokenCache += string(char)
			// 防止标签名过长导致内存溢出，若超过 maxTagLen 则视为普通文本
			if len(p.TokenCache) >= maxTagLen {
				p.Mode = 0
				response.WriteString("<" + p.TokenCache)
				p.TokenCache = ""
				continue
			}
		case 2: // 状态：处于标签内容中。寻找可能的结束标签起始符 '<'。
			if char == '<' {
				p.Mode = 3
				continue
			}
			// 将内容分发到对应的处理逻辑（think 或 tools）
			err := solveTag(string(char))
			if err != nil {
				return "", "", nil, err
			}
		case 3: // 状态：在标签内收到了 '<'，判断是否为结束标签（即紧跟 '/'）。
			if char == '/' {
				p.Mode = 4
				p.TokenCache = ""
				continue
			}
			// 不是结束标签，将之前的 '<' 作为内容处理并回退到模式 2
			p.Mode = 2
			err := solveTag("<" + string(char))
			if err != nil {
				return "", "", nil, err
			}
		case 4: // 状态：正在解析结束标签名（如 "/think" 或 "/tools"）。
			if char == '>' {
				if p.KeyMode == 1 && p.TokenCache == "think" {
					logger.Debug("exiting think mode")
					p.KeyMode = 1
				} else if p.KeyMode == 2 && p.TokenCache == "tools" {
					// 工具调用结束，让 JSON 解析器完成剩余解析并标记已调用工具
					logger.Debug("exiting tools mode")
					p.KeyMode = 2
					err := p.jsonParser.DoneToken()
					if err != nil {
						logger.Error("jsonParser DoneToken error: %v", err)
						return "", "", nil, err
					}
					p.CalledTools = true
				} else {
					// 错误的结束标签名，作为普通内容处理
					err := solveTag("</" + p.TokenCache + ">")
					if err != nil {
						return "", "", nil, err
					}
					p.TokenCache = ""
					p.Mode = 2
					continue
				}
				p.Mode = 0 // 成功匹配结束标签，回到标签外状态
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
func NewParser(session *structs.Chats, tools []*ToolsDefine) *Parser {
	if session != nil {
		session.TemporyDataOfRequest = make(map[string]any)
	}
	return &Parser{Session: session, Tools: tools}
}
