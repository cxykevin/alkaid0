package parser

import (
	stdjson "encoding/json"

	"github.com/cxykevin/alkaid0/library/json"
	structs "github.com/cxykevin/alkaid0/storage/structs"
)

// validateParams 实时参数类型校验，对齐 solveTool 的宽松语义：
// 单个参数类型不匹配仅记录告警并跳过校验，不中止整个流式响应，也不移除参数。
// 支持流式解析未完成状态的 Slot 占位符类型（StringSlot/ArraySlot/ObjectSlot）。
func validateParams(tool *ToolsDefine, params map[string]*any) {
	if tool == nil || len(params) == 0 {
		return
	}
	for key, value := range params {
		if value == nil {
			logger.Warn("parameter '%s' for tool '%s' is null, skip", key, tool.Name)
			continue
		}
		switch tool.Parameters[key].Type {
		case ToolTypeString:
			_, okStr := (*value).(string)
			_, okTmpStr := (*value).(json.StringSlot)
			if !okStr && !okTmpStr {
				logger.Warn("parameter '%s' for tool '%s' expected string, got %T, skip", key, tool.Name, *value)
				continue
			}
		case ToolTypeNumber:
			_, ok := (*value).(float64)
			if !ok {
				logger.Warn("parameter '%s' for tool '%s' expected number(float64), got %T, skip", key, tool.Name, *value)
				continue
			}
		case ToolTypeBoolean:
			_, ok := (*value).(bool)
			if !ok {
				logger.Warn("parameter '%s' for tool '%s' expected bool, got %T, skip", key, tool.Name, *value)
				continue
			}
		case ToolTypeArray:
			_, ok := (*value).([]any)
			_, okArrSlot := (*value).(json.ArraySlot)
			if !ok && !okArrSlot {
				logger.Warn("parameter '%s' for tool '%s' expected array, got %T, skip", key, tool.Name, *value)
				continue
			}
		case ToolTypeObject:
			_, okMap := (*value).(map[string]*any)
			_, okMapSlot := (*value).(json.ObjectSlot)
			if !okMap && !okMapSlot {
				logger.Warn("parameter '%s' for tool '%s' expected object, got %T, skip", key, tool.Name, *value)
				continue
			}
		}
	}
}

// nativeCallState 单个原生 tool_call 的流式累积状态。
// 每个 index 对应一个独立的 libjson.Parser，增量消费 function.arguments。
type nativeCallState struct {
	index      int
	id         string
	name       string
	toolID     int // findTool(name) 结果，-1=未匹配
	jsonParser *json.Parser
	finalized  bool
	invalid    bool
	params     map[string]*any // 最终完成的参数快照（用于 Origin 稳定序列化）
}

// NativeToolCallAccumulator 原生 tool_calls 流式累积器。
//
// 按 delta.tool_calls[i].index 维护独立状态，每个 index 一个 libjson.Parser，
// 把 function.arguments 的增量片段喂给 libjson，复用 ObjectSlot/ArraySlot 增量提取。
// 与 parser.solveTool 语义对齐：未完整 → Func(id, params, false)（流式预览 OnHook）；
// 完整 → Func(id, params, true)，追加 solved 并清 TemporyDataOfRequest。
type NativeToolCallAccumulator struct {
	tools   []*ToolsDefine
	session *structs.Chats
	calls   map[int]*nativeCallState // index → 状态
	order   []int                    // index 出现顺序（保证 Origin 稳定）
	solved  []AIToolsResponse
}

// NewNativeToolCallAccumulator 创建累积器；tools 复用 build.ToolsSolver 产物（含 Func 回调）。
func NewNativeToolCallAccumulator(session *structs.Chats, tools []*ToolsDefine) *NativeToolCallAccumulator {
	return &NativeToolCallAccumulator{
		tools:   tools,
		session: session,
		calls:   make(map[int]*nativeCallState),
	}
}

// findTool 在已注册工具中匹配名称，返回下标或 -1。
func (a *NativeToolCallAccumulator) findTool(name string) int {
	for idx, tool := range a.tools {
		if tool.Name == name {
			return idx
		}
	}
	return -1
}

// AddDelta 喂入一个流式增量。OpenAI 流式中同一 index 的 id/name 通常先于 arguments 到达，
// 且 arguments 会被切成多段增量，每段单独调用本方法。
func (a *NativeToolCallAccumulator) AddDelta(index int, id, name, arguments string) error {
	state := a.calls[index]
	if state == nil {
		state = &nativeCallState{index: index, toolID: -1}
		a.calls[index] = state
		a.order = append(a.order, index)
	}
	if id != "" {
		state.id = id
	}
	if name != "" {
		state.name = name
		if state.toolID == -1 {
			state.toolID = a.findTool(name)
			if state.toolID == -1 {
				state.invalid = true
				logger.Warn("native tool call: tool not found: %s", name)
			}
		}
	}
	if arguments == "" || state.invalid || state.finalized {
		// name/id 可能晚于 arguments 到达：即使本片无新 arguments，也派发已有解析数据
		if state.jsonParser != nil && !state.finalized && !state.invalid {
			return a.dispatch(state)
		}
		return nil
	}
	if state.jsonParser == nil {
		state.jsonParser = json.New()
	}
	if err := state.jsonParser.AddToken(arguments); err != nil {
		return err
	}
	return a.dispatch(state)
}

// dispatch 从 libjson 提取当前参数并派发：未完整 → Func(_, false) 流式预览；
// 完整 → Func(_, true) 并记入 solved（仅首次）。
func (a *NativeToolCallAccumulator) dispatch(state *nativeCallState) error {
	root := state.jsonParser.FullCallingObject
	if root == nil {
		return nil
	}
	var params map[string]*any
	var complete bool
	switch v := (*root).(type) {
	case map[string]*any:
		params = v
		complete = true
	case json.ObjectSlot:
		params = map[string]*any(v)
		complete = false
	default:
		return nil
	}
	if state.id == "" || state.name == "" || state.toolID < 0 {
		return nil
	}
	tool := a.tools[state.toolID]
	if tool == nil {
		return nil
	}
	validateParams(tool, params)
	if !complete {
		if tool.Func != nil {
			if err := tool.Func(state.id, params, false); err != nil {
				return err
			}
		}
		return nil
	}
	// 完整调用：仅首次派发最终状态
	if state.finalized {
		return nil
	}
	if tool.Func != nil {
		if err := tool.Func(state.id, params, true); err != nil {
			return err
		}
	}
	state.finalized = true
	state.params = params
	a.solved = append(a.solved, AIToolsResponse{
		Name:       state.name,
		ID:         state.id,
		Parameters: params,
	})
	// 工具调用完全解析后清除 TemporyDataOfRequest，保证下一个工具预览状态干净
	if a.session != nil {
		a.session.TemporyDataOfRequest = make(map[string]any)
	}
	return nil
}

// DoneToken 流结束收尾：对尚未 finalize 且已有 jsonParser 的调用做 DoneToken，
// 使 ObjectSlot 变为完整对象并补入 solved。
func (a *NativeToolCallAccumulator) DoneToken() error {
	for _, index := range a.order {
		state := a.calls[index]
		if state == nil || state.invalid || state.finalized || state.jsonParser == nil {
			continue
		}
		if err := state.jsonParser.DoneToken(); err != nil {
			return err
		}
		if err := a.dispatch(state); err != nil {
			return err
		}
	}
	return nil
}

// GetTools 返回按 delta index 顺序排列的已解决工具调用列表。
func (a *NativeToolCallAccumulator) GetTools() []AIToolsResponse {
	if len(a.solved) == 0 {
		return nil
	}
	byID := make(map[string]AIToolsResponse, len(a.solved))
	for _, tool := range a.solved {
		byID[tool.ID] = tool
	}
	tools := make([]AIToolsResponse, 0, len(a.solved))
	for _, index := range a.order {
		state := a.calls[index]
		if state == nil || !state.finalized {
			continue
		}
		if tool, ok := byID[state.id]; ok {
			tools = append(tools, tool)
		}
	}
	return tools
}

// HasTools 是否产生过完整调用。
func (a *NativeToolCallAccumulator) HasTools() bool {
	return len(a.solved) > 0
}

// Origin 序列化内部格式 [{"name","id","parameters"}]（供 tool_calling_json_string 持久化）。
// 按 index 出现顺序输出，保证两次序列化稳定。
func (a *NativeToolCallAccumulator) Origin() string {
	if len(a.solved) == 0 {
		return ""
	}
	items := make([]map[string]any, 0, len(a.solved))
	for _, index := range a.order {
		state := a.calls[index]
		if state == nil || !state.finalized {
			continue
		}
		items = append(items, map[string]any{
			"name":       state.name,
			"id":         state.id,
			"parameters": state.params,
		})
	}
	if len(items) == 0 {
		return ""
	}
	buf, err := stdjson.Marshal(items)
	if err != nil {
		logger.Error("native tool call origin marshal error: %v", err)
		return ""
	}
	return string(buf)
}
