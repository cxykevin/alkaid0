package parser_test

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/provider/parser"
)

// callRecord 记录一次 Func 回调
type callRecord struct {
	id       string
	finished bool
}

// recordTool 构造可记录 Func 调用的测试工具
func recordTool(recs *[]callRecord) *parser.ToolsDefine {
	return &parser.ToolsDefine{
		Name: "calculator",
		Parameters: map[string]parser.ToolParameters{
			"expression": {Type: parser.ToolTypeString, Required: true},
			"precision":  {Type: parser.ToolTypeNumber, Required: false},
			"enabled":    {Type: parser.ToolTypeBoolean, Required: false},
			"items":      {Type: parser.ToolTypeArray, Required: false},
		},
		Func: func(id string, _ map[string]*any, finished bool) error {
			*recs = append(*recs, callRecord{id: id, finished: finished})
			return nil
		},
	}
}

// TestNativeAccumulatorSingleCall 单 index arguments 分片：流式预览 + 最终调用 + Origin。
func TestNativeAccumulatorSingleCall(t *testing.T) {
	var recs []callRecord
	acc := parser.NewNativeToolCallAccumulator(nil, []*parser.ToolsDefine{recordTool(&recs)})

	// 首个 chunk：id + name（arguments 为空）
	if err := acc.AddDelta(0, "call_1", "calculator", ""); err != nil {
		t.Fatalf("AddDelta(name chunk) error: %v", err)
	}
	// arguments 分片到达
	for _, c := range []string{`{"expression":"1+1"`, `}`} {
		if err := acc.AddDelta(0, "", "", c); err != nil {
			t.Fatalf("AddDelta(args chunk %q) error: %v", c, err)
		}
	}

	tools := acc.GetTools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].Name != "calculator" || tools[0].ID != "call_1" {
		t.Fatalf("unexpected tool: %+v", tools[0])
	}
	expr, ok := tools[0].Parameters["expression"]
	if !ok || *expr != "1+1" {
		t.Fatalf("unexpected parameters: %+v", tools[0].Parameters)
	}
	// 流式预览（ok=false）至少一次，最后为最终调用（ok=true）
	if len(recs) < 2 {
		t.Fatalf("expected >=2 func calls (preview + final), got %d", len(recs))
	}
	hasPreview := false
	for _, r := range recs {
		if !r.finished {
			hasPreview = true
		}
	}
	if !hasPreview {
		t.Fatal("expected at least one streaming preview call (finished=false)")
	}
	if !recs[len(recs)-1].finished {
		t.Fatal("last call should be finished=true")
	}
	// Origin 序列化内部格式
	origin := acc.Origin()
	var parsed []map[string]any
	if err := json.Unmarshal([]byte(origin), &parsed); err != nil {
		t.Fatalf("origin not valid json: %s (%v)", origin, err)
	}
	if len(parsed) != 1 || parsed[0]["name"] != "calculator" || parsed[0]["id"] != "call_1" {
		t.Fatalf("unexpected origin: %s", origin)
	}
}

// TestNativeAccumulatorMultipleCalls 多 index 交错：各自独立 finalize。
func TestNativeAccumulatorMultipleCalls(t *testing.T) {
	var recs []callRecord
	acc := parser.NewNativeToolCallAccumulator(nil, []*parser.ToolsDefine{recordTool(&recs)})

	// index 0 与 index 1 交错到达
	if err := acc.AddDelta(0, "call_1", "calculator", `{"expression":"a"`); err != nil {
		t.Fatal(err)
	}
	if err := acc.AddDelta(1, "call_2", "calculator", `{"expression":"b"`); err != nil {
		t.Fatal(err)
	}
	if err := acc.AddDelta(0, "", "", `}`); err != nil {
		t.Fatal(err)
	}
	if err := acc.AddDelta(1, "", "", `}`); err != nil {
		t.Fatal(err)
	}

	tools := acc.GetTools()
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	if tools[0].ID != "call_1" || tools[1].ID != "call_2" {
		t.Fatalf("unexpected order: %+v", tools)
	}
	if a, _ := tools[0].Parameters["expression"]; *a != "a" {
		t.Fatalf("call_1 expression = %v", *a)
	}
	if a, _ := tools[1].Parameters["expression"]; *a != "b" {
		t.Fatalf("call_2 expression = %v", *a)
	}
	if !acc.HasTools() {
		t.Fatal("HasTools should be true")
	}
}

// TestNativeAccumulatorNameLate 名称晚于 arguments 到达仍正确派发。
func TestNativeAccumulatorNameLate(t *testing.T) {
	var recs []callRecord
	acc := parser.NewNativeToolCallAccumulator(nil, []*parser.ToolsDefine{recordTool(&recs)})

	// 先喂完整 arguments（无 id/name），再补 id/name
	if err := acc.AddDelta(0, "", "", `{"expression":"x"}`); err != nil {
		t.Fatal(err)
	}
	if err := acc.AddDelta(0, "call_9", "calculator", ""); err != nil {
		t.Fatal(err)
	}

	tools := acc.GetTools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0].ID != "call_9" || tools[0].Name != "calculator" {
		t.Fatalf("unexpected tool: %+v", tools[0])
	}
	if !recs[len(recs)-1].finished {
		t.Fatal("last call should be finished=true")
	}
}

// TestNativeAccumulatorUnknownTool 未知工具名：跳过且不 abort。
func TestNativeAccumulatorUnknownTool(t *testing.T) {
	var recs []callRecord
	acc := parser.NewNativeToolCallAccumulator(nil, []*parser.ToolsDefine{recordTool(&recs)})

	if err := acc.AddDelta(0, "call_1", "no_such_tool", `{"a":1}`); err != nil {
		t.Fatal(err)
	}
	if err := acc.DoneToken(); err != nil {
		t.Fatal(err)
	}
	if acc.HasTools() {
		t.Fatal("unknown tool should not be solved")
	}
	if len(recs) != 0 {
		t.Fatalf("Func should not be called for unknown tool, got %d calls", len(recs))
	}
}

// TestNativeAccumulatorTypeMismatch 参数类型不匹配：宽松校验，仅告警不中止。
func TestNativeAccumulatorTypeMismatch(t *testing.T) {
	var recs []callRecord
	acc := parser.NewNativeToolCallAccumulator(nil, []*parser.ToolsDefine{recordTool(&recs)})

	// expression 期望 string，喂 number：不应中止，参数仍保留
	if err := acc.AddDelta(0, "call_1", "calculator", `{"expression":123}`); err != nil {
		t.Fatal(err)
	}
	tools := acc.GetTools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool despite type mismatch, got %d", len(tools))
	}
	if v, ok := tools[0].Parameters["expression"]; !ok || *v != float64(123) {
		t.Fatalf("expression param = %v", tools[0].Parameters["expression"])
	}
}

// TestNativeAccumulatorTruncatedArgumentsDropsOnlyThatCall 流结束时参数仍未闭合
// （响应被截断/网关断流）：只丢弃这个调用，DoneToken 不得把错误抛给上游——
// 否则整个回合中止，已解析的正文与其他调用一起丢失。
func TestNativeAccumulatorTruncatedArgumentsDropsOnlyThatCall(t *testing.T) {
	var recs []callRecord
	acc := parser.NewNativeToolCallAccumulator(nil, []*parser.ToolsDefine{recordTool(&recs)})

	if err := acc.AddDelta(0, "call_trunc", "calculator", `{"expression":"unclosed`); err != nil {
		t.Fatal(err)
	}
	// 同一个流里其他调用照常完成
	if err := acc.AddDelta(1, "call_ok", "calculator", `{"expression":"fine"}`); err != nil {
		t.Fatal(err)
	}
	if err := acc.DoneToken(); err != nil {
		t.Fatalf("truncated arguments must be dropped, not returned as a stream error: %v", err)
	}
	tools := acc.GetTools()
	if len(tools) != 1 || tools[0].ID != "call_ok" {
		t.Fatalf("truncated call must be dropped, completed call kept, got %+v", tools)
	}
	if !acc.HasTools() {
		t.Fatal("completed call should still be solved")
	}
}

// TestNativeAccumulatorEmptyArguments 空 arguments 不产生工具调用。
func TestNativeAccumulatorEmptyArguments(t *testing.T) {
	var recs []callRecord
	acc := parser.NewNativeToolCallAccumulator(nil, []*parser.ToolsDefine{recordTool(&recs)})

	if err := acc.AddDelta(0, "call_1", "calculator", ""); err != nil {
		t.Fatal(err)
	}
	if err := acc.DoneToken(); err != nil {
		t.Fatal(err)
	}
	if acc.HasTools() {
		t.Fatal("empty arguments should not solve a tool")
	}
}

// TestNativeAccumulatorMalformedArgumentsDropsOnlyThatCall 单个调用的参数 JSON 畸形
// （模型生成错误 / 网关破坏）：只丢弃该调用并告警，不得把错误返回给调用方——
// 否则一次畸形参数会中止整轮流式响应，连已解析的正文与其他调用一起丢弃。
func TestNativeAccumulatorMalformedArgumentsDropsOnlyThatCall(t *testing.T) {
	var recs []callRecord
	acc := parser.NewNativeToolCallAccumulator(nil, []*parser.ToolsDefine{recordTool(&recs)})

	// 对象值缺失（':' 后直接 '}'），json 解析器会报 "unexpected '}'"
	if err := acc.AddDelta(0, "call_bad", "calculator", `{"expression":}`); err != nil {
		t.Fatalf("malformed arguments must be dropped, not returned as a stream error: %v", err)
	}
	// 同一个流里其他调用照常完成
	if err := acc.AddDelta(1, "call_ok", "calculator", `{"expression":"fine"}`); err != nil {
		t.Fatal(err)
	}
	if err := acc.DoneToken(); err != nil {
		t.Fatalf("DoneToken must not abort after a dropped call: %v", err)
	}
	tools := acc.GetTools()
	if len(tools) != 1 || tools[0].ID != "call_ok" {
		t.Fatalf("malformed call must be dropped, completed call kept, got %+v", tools)
	}
}

// TestNativeAccumulatorIndexReuseKeepsAllCalls 同一 index 被复用给多次调用
// （OpenAI 兼容端点偶尔不递增 index）：每个调用都必须保留，不能被
// "已 finalize → 丢弃 arguments" 的旧逻辑吞掉。
func TestNativeAccumulatorIndexReuseKeepsAllCalls(t *testing.T) {
	var recs []callRecord
	acc := parser.NewNativeToolCallAccumulator(nil, []*parser.ToolsDefine{recordTool(&recs)})

	if err := acc.AddDelta(0, "call_1", "calculator", `{"expression":"first"}`); err != nil {
		t.Fatal(err)
	}
	// 复用 index 0 的第二次调用
	if err := acc.AddDelta(0, "call_2", "calculator", `{"expression":"second"}`); err != nil {
		t.Fatal(err)
	}
	if err := acc.DoneToken(); err != nil {
		t.Fatal(err)
	}
	tools := acc.GetTools()
	if len(tools) != 2 {
		t.Fatalf("index reuse must keep both calls, got %d: %+v", len(tools), tools)
	}
	if tools[0].ID != "call_1" || tools[1].ID != "call_2" {
		t.Fatalf("unexpected ids: %+v", tools)
	}
	if v := tools[0].Parameters["expression"]; v == nil || *v != "first" {
		t.Fatalf("call_1 expression = %v", v)
	}
	if v := tools[1].Parameters["expression"]; v == nil || *v != "second" {
		t.Fatalf("call_2 expression = %v", v)
	}
	// 复用后的调用也要进入 Origin 序列化
	var origin []map[string]any
	if err := json.Unmarshal([]byte(acc.Origin()), &origin); err != nil {
		t.Fatalf("invalid origin %q: %v", acc.Origin(), err)
	}
	if len(origin) != 2 || origin[0]["id"] != "call_1" || origin[1]["id"] != "call_2" {
		t.Fatalf("unexpected origin: %s", acc.Origin())
	}
}

// TestNativeAccumulatorIndexReuseWithoutID 复用 index 的新调用先到 arguments、id/name
// 晚到：同样必须新建状态并最终解析出第二个调用。
func TestNativeAccumulatorIndexReuseWithoutID(t *testing.T) {
	var recs []callRecord
	acc := parser.NewNativeToolCallAccumulator(nil, []*parser.ToolsDefine{recordTool(&recs)})

	if err := acc.AddDelta(0, "call_1", "calculator", `{"expression":"first"}`); err != nil {
		t.Fatal(err)
	}
	if err := acc.AddDelta(0, "", "", `{"expression":"second"}`); err != nil {
		t.Fatal(err)
	}
	if err := acc.AddDelta(0, "call_2", "calculator", ""); err != nil {
		t.Fatal(err)
	}
	tools := acc.GetTools()
	if len(tools) != 2 {
		t.Fatalf("index reuse without id must keep both calls, got %d: %+v", len(tools), tools)
	}
	if tools[1].ID != "call_2" {
		t.Fatalf("unexpected second call: %+v", tools[1])
	}
	if v := tools[1].Parameters["expression"]; v == nil || *v != "second" {
		t.Fatalf("call_2 expression = %v", v)
	}
}

// TestNativeAccumulatorDuplicateIDsKeepEachCall 多个调用共用同一 id（网关/历史回放
// 数据重复）时，GetTools/Origin 必须按调用各自返回：旧实现按 id 建索引会把先到的
// 调用覆盖成后到的参数，导致调用被合并/串参数。
func TestNativeAccumulatorDuplicateIDsKeepEachCall(t *testing.T) {
	var recs []callRecord
	acc := parser.NewNativeToolCallAccumulator(nil, []*parser.ToolsDefine{recordTool(&recs)})

	if err := acc.AddDelta(0, "call_dup", "calculator", `{"expression":"first"}`); err != nil {
		t.Fatal(err)
	}
	if err := acc.AddDelta(1, "call_dup", "calculator", `{"expression":"second"}`); err != nil {
		t.Fatal(err)
	}
	tools := acc.GetTools()
	if len(tools) != 2 {
		t.Fatalf("expected 2 calls, got %d: %+v", len(tools), tools)
	}
	if v := tools[0].Parameters["expression"]; v == nil || *v != "first" {
		t.Fatalf("first call parameters overwritten by duplicate id: %v", v)
	}
	if v := tools[1].Parameters["expression"]; v == nil || *v != "second" {
		t.Fatalf("second call expression = %v", v)
	}
	var origin []map[string]any
	if err := json.Unmarshal([]byte(acc.Origin()), &origin); err != nil {
		t.Fatalf("invalid origin %q: %v", acc.Origin(), err)
	}
	if len(origin) != 2 {
		t.Fatalf("unexpected origin length: %s", acc.Origin())
	}
	if p0, ok := origin[0]["parameters"].(map[string]any); !ok || p0["expression"] != "first" {
		t.Fatalf("origin[0] parameters = %v", origin[0]["parameters"])
	}
	if p1, ok := origin[1]["parameters"].(map[string]any); !ok || p1["expression"] != "second" {
		t.Fatalf("origin[1] parameters = %v", origin[1]["parameters"])
	}
}

// TestNativeAccumulatorLongStringArgumentsAllocatedBytes 长字符串参数必须线性累积：
// library/json 逐字符 += 拼接时每次都会复制整个字符串（累计复制 O(n²)，2 万字符约
// 2 亿字节）；改为 strings.Builder 后只做线性追加。这里用 MemStats.TotalAlloc 的
// 增量断言上界：修复前远超上界，修复后仅 KB 量级。
func TestNativeAccumulatorLongStringArgumentsAllocatedBytes(t *testing.T) {
	const n = 20000
	long := strings.Repeat("a", n)
	arguments := `{"expression":"` + long + `"}`

	var throwaway []callRecord
	measure := func() uint64 {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		acc := parser.NewNativeToolCallAccumulator(nil, []*parser.ToolsDefine{recordTool(&throwaway)})
		_ = acc.AddDelta(0, "call_1", "calculator", arguments)
		runtime.ReadMemStats(&after)
		runtime.KeepAlive(acc)
		return after.TotalAlloc - before.TotalAlloc
	}
	measure() // 预热，排除一次性初始化分配
	allocated := measure()
	t.Logf("allocated %d bytes for %d-char string", allocated, n)

	const limit = 20 << 20 // 20MiB：二次方复制约 2 亿字节，线性追加仅 KB 量级
	if allocated > limit {
		t.Fatalf("long string argument allocated %d bytes (quadratic concatenation?), want <= %d", allocated, limit)
	}

	// 内容必须完整保留
	var recs []callRecord
	acc := parser.NewNativeToolCallAccumulator(nil, []*parser.ToolsDefine{recordTool(&recs)})
	if err := acc.AddDelta(0, "call_1", "calculator", arguments); err != nil {
		t.Fatal(err)
	}
	tools := acc.GetTools()
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	v := tools[0].Parameters["expression"]
	if v == nil {
		t.Fatal("expression missing")
	}
	got, ok := (*v).(string)
	if !ok || got != long {
		t.Fatalf("long expression not preserved: len=%d ok=%v", len(got), ok)
	}
}
