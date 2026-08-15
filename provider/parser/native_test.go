package parser_test

import (
	"encoding/json"
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

// TestNativeAccumulatorDoneTokenUnclosed 未闭合 arguments：DoneToken 报错，调用不落库
// （对齐提示词模式 </tools> 时 jsonParser.DoneToken 报 incomplete JSON 的语义）。
func TestNativeAccumulatorDoneTokenUnclosed(t *testing.T) {
	var recs []callRecord
	acc := parser.NewNativeToolCallAccumulator(nil, []*parser.ToolsDefine{recordTool(&recs)})

	if err := acc.AddDelta(0, "call_1", "calculator", `{"expression":"unclosed`); err != nil {
		t.Fatal(err)
	}
	if err := acc.DoneToken(); err == nil {
		t.Fatal("DoneToken should error on unclosed arguments (incomplete JSON)")
	}
	if acc.HasTools() {
		t.Fatal("unclosed call should not be solved")
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
