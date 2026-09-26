package run

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// workflowView 渲染 dynworkflow 事件的只读视图：节点图（mermaid 边）+ 每个节点的
// 状态 / 日志 / 终值。
//
// 终端内容（job.content 与前端推送）保持原始输出；视图只写入临时对象
// （@temp/run/<n>），供 read 工具与 trace 注入查看：模型读到的就是节点图，
// 而不是被协议过滤后的零散 stdout。
//
// 事件由 stdout 解析协程 Apply，渲染由内容刷新协程（以及命令结束时的最终写入）
// 调用，两者并发，因此内部加锁。
type workflowView struct {
	mu sync.Mutex

	// nodeOrder 节点顺序：graph.nodes 的键顺序（dynworkflow 的注册顺序）。
	nodeOrder []string
	// nodeNames 节点 id → 显示名（graph.nodes[].name；旧版本回退到 node_code.name）。
	nodeNames map[string]string
	// edges 去重后的边，保持 graph 中的顺序。
	edges [][2]string
	// states 节点 id → 最近一次 node 事件的 state。
	states map[string]string
	// logs 节点 id → node_log 行（每个节点只保留最近 workflowViewMaxLogsPerNode 行）。
	logs map[string][]string
	// logDropped 标记该节点是否因上限丢弃过日志（渲染时提示）。
	logDropped map[string]bool
	// results 节点 id → 已渲染的 Result 文本（node_result 事件）。
	results map[string]string
	// seen 已加入 nodeOrder 的节点，避免重复。
	seen map[string]bool
}

const (
	// workflowViewMaxLogsPerNode 每个节点保留的 node_log 行数：超出丢弃最早的，
	// 避免高频 print 的工作流在内存里无限增长。
	workflowViewMaxLogsPerNode = 100
	// workflowViewMaxTotalLines 渲染结果的总行数上限：UpdateTempObject 只保留
	// 最后 2000 行，超过会把视图顶部（图与靠前的节点）截掉，因此视图与附加的
	// 原始输出合计控制在该上限内。
	workflowViewMaxTotalLines = 1900
	// workflowViewMaxRawLines 附加到视图之后的原始输出行数上限（视图较小时生效）。
	workflowViewMaxRawLines = 600
	// workflowViewRawSeparator 视图与原始输出之间的分隔线。
	workflowViewRawSeparator = "----- raw output -----"
)

func newWorkflowView() *workflowView {
	return &workflowView{
		nodeNames:  make(map[string]string),
		states:     make(map[string]string),
		logs:       make(map[string][]string),
		logDropped: make(map[string]bool),
		results:    make(map[string]string),
		seen:       make(map[string]bool),
	}
}

// Apply 消费一条 workflow 事件（事件类型见 dynworkflow 上报协议）。
func (v *workflowView) Apply(ev WorkflowEvent) {
	if v == nil {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	switch ev.Type {
	case "graph":
		v.applyGraph(ev.Raw)
	case "node":
		id := workflowEventString(ev.Data["nodeId"])
		if id == "" {
			return
		}
		v.addNode(id)
		v.states[id] = workflowEventString(ev.Data["state"])
	case "node_code":
		// graph 已带显示名；旧的 dynworkflow 里 graph 的 name 等于 id，
		// 这里用节点启动时广播的 node_code.name 兜底。
		id := workflowEventString(ev.Data["nodeId"])
		name := workflowEventString(ev.Data["name"])
		if id == "" || name == "" {
			return
		}
		v.addNode(id)
		if v.nodeNames[id] == "" || v.nodeNames[id] == id {
			v.nodeNames[id] = name
		}
	case "node_log":
		id := workflowEventString(ev.Data["nodeId"])
		if id == "" {
			return
		}
		v.addNode(id)
		lines := append(v.logs[id], workflowEventString(ev.Data["message"]))
		if len(lines) > workflowViewMaxLogsPerNode {
			lines = lines[len(lines)-workflowViewMaxLogsPerNode:]
			v.logDropped[id] = true
		}
		v.logs[id] = lines
	case "node_result":
		id := workflowEventString(ev.Data["nodeId"])
		if id == "" {
			return
		}
		v.addNode(id)
		v.results[id] = renderWorkflowResult(ev.Data["result"])
	}
}

// applyGraph 解析 graph 事件。节点与边的顺序取自 JSON 对象的原始键顺序：
// encoding/json 解到 map 会丢失顺序，而视图按节点注册顺序渲染才稳定。
func (v *workflowView) applyGraph(raw json.RawMessage) {
	var envelope struct {
		Graph struct {
			Nodes json.RawMessage `json:"nodes"`
			Edges json.RawMessage `json:"edges"`
		} `json:"graph"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &envelope) != nil {
		return
	}
	var names map[string]struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(envelope.Graph.Nodes, &names) != nil {
		return
	}
	v.nodeOrder = v.nodeOrder[:0]
	v.seen = make(map[string]bool)
	for _, id := range orderedObjectKeys(envelope.Graph.Nodes) {
		v.seen[id] = true
		v.nodeOrder = append(v.nodeOrder, id)
		if name := names[id].Name; name != "" {
			v.nodeNames[id] = name
		}
	}
	var edges map[string][]string
	if json.Unmarshal(envelope.Graph.Edges, &edges) != nil {
		return
	}
	v.edges = v.edges[:0]
	seenEdge := make(map[string]bool)
	for _, from := range orderedObjectKeys(envelope.Graph.Edges) {
		for _, to := range edges[from] {
			key := from + "\x00" + to
			if seenEdge[key] {
				continue
			}
			seenEdge[key] = true
			v.edges = append(v.edges, [2]string{from, to})
		}
	}
}

// addNode 按需把节点追加到渲染顺序（graph 未覆盖的节点排在末尾）。
func (v *workflowView) addNode(id string) {
	if v.seen == nil {
		v.seen = make(map[string]bool)
	}
	if v.seen[id] {
		return
	}
	v.seen[id] = true
	v.nodeOrder = append(v.nodeOrder, id)
}

// Render 渲染视图，并把原始终端输出附加在视图之后。尚未收到 graph 时直接返回
// 原始输出：workflow 还在启动阶段，与 terminal 的读法保持一致。
func (v *workflowView) Render(raw string) string {
	if v == nil {
		return raw
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.nodeOrder) == 0 {
		return raw
	}
	var b strings.Builder
	for _, edge := range v.edges {
		b.WriteString(edge[0])
		b.WriteString("->")
		b.WriteString(edge[1])
		b.WriteString("\n")
	}
	for _, id := range v.nodeOrder {
		name := v.nodeNames[id]
		if name == "" {
			name = id
		}
		b.WriteString("- ")
		b.WriteString(workflowStateMark(v.states[id]))
		b.WriteString(" ")
		b.WriteString(id)
		b.WriteString(": ")
		b.WriteString(name)
		b.WriteString("\n")
		if v.logDropped[id] {
			b.WriteString("  (earlier log lines omitted)\n")
		}
		for _, line := range v.logs[id] {
			b.WriteString("  ")
			b.WriteString(line)
			b.WriteString("\n")
		}
		if result, ok := v.results[id]; ok {
			b.WriteString("  Result: ")
			b.WriteString(result)
			b.WriteString("\n")
		}
	}
	viewLines := strings.Count(b.String(), "\n")
	if viewLines >= workflowViewMaxTotalLines-1 {
		return b.String()
	}
	raw = strings.TrimRight(raw, "\n")
	if raw == "" {
		return b.String()
	}
	rawLines := strings.Split(raw, "\n")
	// 预留分隔线与空行，并让原始输出不超过单项上限。
	budget := workflowViewMaxTotalLines - viewLines - 1
	if budget > workflowViewMaxRawLines {
		budget = workflowViewMaxRawLines
	}
	if budget <= 0 {
		return b.String()
	}
	omitted := false
	if len(rawLines) > budget {
		rawLines = rawLines[len(rawLines)-budget:]
		omitted = true
	}
	b.WriteString("\n")
	b.WriteString(workflowViewRawSeparator)
	b.WriteString("\n")
	if omitted {
		b.WriteString("(earlier output omitted)\n")
	}
	b.WriteString(strings.Join(rawLines, "\n"))
	b.WriteString("\n")
	return b.String()
}

// workflowUpdateFn 包装临时对象更新：workflow 的临时对象写入渲染后的节点图视图
// （+原始输出），普通任务只做原样透传。终端内容不经过这里。
func workflowUpdateFn(view *workflowView, update func(string)) func(string) {
	if view == nil || update == nil {
		return update
	}
	return func(content string) {
		update(view.Render(content))
	}
}

// workflowStateMark 把 dynworkflow 的节点状态映射为勾选框：未运行为 [ ]，
// 运行中为 [-]，结束（done/error/terminated）为 [X]。
func workflowStateMark(state string) string {
	switch state {
	case "running":
		return "[-]"
	case "done", "error", "terminated":
		return "[X]"
	default:
		return "[ ]"
	}
}

// renderWorkflowResult 渲染 node_result 的值：字符串原样输出（保持多行内容），
// 其余值用紧凑 JSON，序列化失败时退回 fmt 的默认格式。
func renderWorkflowResult(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	if value == nil {
		return "null"
	}
	if encoded, err := json.Marshal(value); err == nil {
		return string(encoded)
	}
	return fmt.Sprint(value)
}

// workflowEventString 从事件字段取字符串（类型不符时返回空串）。
func workflowEventString(value any) string {
	s, _ := value.(string)
	return s
}

// orderedObjectKeys 按出现顺序返回 JSON 对象 raw 的顶层键。
func orderedObjectKeys(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil
	}
	keys := make([]string, 0, 8)
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return keys
		}
		key, ok := keyTok.(string)
		if !ok {
			return keys
		}
		keys = append(keys, key)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return keys
		}
	}
	return keys
}
