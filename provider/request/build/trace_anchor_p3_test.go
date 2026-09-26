package build

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/tools/trace"
	"gorm.io/gorm"
)

// toolResultDelta 生成存储层工具结果 JSON（格式 [{"name","id","return"}]）。
// return 是结果对象的 JSON 字符串——run/fetch/python 的 @temp 路径只出现在这里，
// 因此这里也是事件合成的输入（docs/trace-cache-spec.md §4.1）。
func toolResultDelta(name, id string, result map[string]any) string {
	raw, err := json.Marshal(result)
	if err != nil {
		panic(err)
	}
	out, err := json.Marshal([]map[string]any{{"name": name, "id": id, "return": string(raw)}})
	if err != nil {
		panic(err)
	}
	return string(out)
}

// TestDetectTraceEvents_TempResultEvent 验证 @temp 事件合成：run 产物的路径不在调用参数里，
// 必须能从 role:tool 结果合成事件，且 MsgID 指向产生该结果的 assistant 消息
// （findEventAnchor 才会走到其结果之后，并行工具调用不会被插进 tool 结果序列中间）。
func TestDetectTraceEvents_TempResultEvent(t *testing.T) {
	db := setupTestDB(t)
	msgs := []structs.Messages{
		{ChatID: 40, Type: structs.MessagesRoleUser, Delta: "run it"},
		{ChatID: 40, Type: structs.MessagesRoleAgent, ToolCallingJSONString: `[{"name":"run","id":"call_1","parameters":{"command":"echo hi"}}]`},
		{ChatID: 40, Type: structs.MessagesRoleTool, Delta: toolResultDelta("run", "call_1", map[string]any{"success": true, "path": "@temp/run/1", "output": "hi"})},
	}
	for i := range msgs {
		if err := db.Create(&msgs[i]).Error; err != nil {
			t.Fatalf("create msg: %v", err)
		}
	}

	session := &structs.Chats{ID: 40, DB: db}
	if err := DetectTraceEvents(db, session, ""); err != nil {
		t.Fatalf("DetectTraceEvents: %v", err)
	}
	events, _ := session.TemporyDataOfSession[structs.TempKeyTraceEvents].(map[string]*structs.TraceEvent)
	ev, ok := events["@temp/run/1"]
	if !ok {
		t.Fatalf("run 结果里的 @temp 路径必须合成事件, got %v", events)
	}
	if ev.MsgID != msgs[1].ID {
		t.Errorf("事件 MsgID 必须指向产生结果的 assistant 消息: want %d got %d", msgs[1].ID, ev.MsgID)
	}
	if ev.ToolCallID != "call_1" || ev.IsEdit || ev.IsTask {
		t.Errorf("事件字段不符: %+v", ev)
	}
	prevs, _ := session.TemporyDataOfSession[structs.TempKeyTracePrevEvents].(map[string]*structs.TraceEvent)
	if _, ok := prevs["@temp/run/1"]; ok {
		t.Error("单一事件不应进 prevMap（否则会被误判为可做方案2 差分）")
	}
}

// TestDetectTraceEvents_TempResultFallbackNotEvent 非沙盒降级路径的 path 字段是命令输出本身，
// 不是 @temp 路径，不得合成事件（否则会把命令输出当文件内容反复注入）。
func TestDetectTraceEvents_TempResultFallbackNotEvent(t *testing.T) {
	db := setupTestDB(t)
	msgs := []structs.Messages{
		{ChatID: 42, Type: structs.MessagesRoleAgent, ToolCallingJSONString: `[{"name":"run","id":"call_9","parameters":{}}]`},
		{ChatID: 42, Type: structs.MessagesRoleTool, Delta: toolResultDelta("run", "call_9", map[string]any{"path": "command output text"})},
	}
	for i := range msgs {
		if err := db.Create(&msgs[i]).Error; err != nil {
			t.Fatalf("create msg: %v", err)
		}
	}
	session := &structs.Chats{ID: 42, DB: db}
	if err := DetectTraceEvents(db, session, ""); err != nil {
		t.Fatalf("DetectTraceEvents: %v", err)
	}
	events, _ := session.TemporyDataOfSession[structs.TempKeyTraceEvents].(map[string]*structs.TraceEvent)
	if len(events) != 0 {
		t.Errorf("非 @temp 的 path 不应产生事件: %v", events)
	}
}

// setupAnchorPlanChat 构造 user + 带工具调用的 assistant + role:tool 结果三条消息。
func setupAnchorPlanChat(t *testing.T, db *gorm.DB, chatID uint32) []structs.Messages {
	t.Helper()
	msgs := []structs.Messages{
		{ChatID: chatID, Type: structs.MessagesRoleUser, Delta: "go"},
		{ChatID: chatID, Type: structs.MessagesRoleAgent, ToolCallingJSONString: `[{"name":"run","id":"call_1","parameters":{"command":"bg"}}]`},
		{ChatID: chatID, Type: structs.MessagesRoleTool, Delta: toolResultDelta("run", "call_1", map[string]any{"path": "@temp/run/9"})},
	}
	for i := range msgs {
		if err := db.Create(&msgs[i]).Error; err != nil {
			t.Fatalf("create msg: %v", err)
		}
	}
	return msgs
}

func anchorPlanSession(t *testing.T, db *gorm.DB, chatID uint32, msgs []structs.Messages, plan *trace.AnchorPlan, body string) *structs.Chats {
	t.Helper()
	chatLn := eventTestChatLn(
		map[string]*structs.TraceEvent{"@temp/run/9": {MsgID: msgs[1].ID, ToolCallID: "call_1"}},
		map[string]trace.FileBlock{"@temp/run/9": {Name: "@temp/run/9", Size: "8", Length: 8, Text: "1|" + body + "\n"}},
	)
	chatLn.ID = chatID
	chatLn.DB = db
	chatLn.NowAgent = ""
	chatLn.TemporyDataOfSession[structs.TempKeyTraceAnchorPlan] = map[string]*trace.AnchorPlan{"@temp/run/9": plan}
	trace.ConfirmEditContent(chatLn, "@temp/run/9", body)
	return chatLn
}

func requestFor(t *testing.T, db *gorm.DB, chatID uint32, chatLn *structs.Chats) []string {
	t.Helper()
	toolsList := []*parser.ToolsDefine{}
	req, err := RequestBody(chatID, 1, "", &toolsList, db, "", "", cfgStruct.AgentConfig{}, chatLn)
	if err != nil {
		t.Fatalf("RequestBody: %v", err)
	}
	out := make([]string, 0, len(req.Messages))
	for _, m := range req.Messages {
		out = append(out, m.Content)
	}
	return out
}

// TestRequestBody_TailPlan_AppendsAtEndAndAdvancesAnchor 末尾落位：内容变了但没有新消息承载
// （后台刷新 / 外部改写）时块追加到列表最后，并回写 AnchorMsgID / last_content，
// 下一轮内容未变时块留在原处（前缀命中缓存）。
func TestRequestBody_TailPlan_AppendsAtEndAndAdvancesAnchor(t *testing.T) {
	config.GlobalConfigSwap(*config.GlobalConfig)
	db := setupTestDB(t)
	setupTestConfig()
	if err := db.AutoMigrate(&structs.Traces{}, &structs.ReferFiles{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	chatID := uint32(41)
	msgs := setupAnchorPlanChat(t, db, chatID)
	if err := db.Create(&structs.Traces{ChatID: chatID, Path: "@temp/run/9", AgentID: "", TraceID: 1, LastContent: "old-body", AnchorMsgID: msgs[1].ID}).Error; err != nil {
		t.Fatalf("create trace: %v", err)
	}
	if err := db.Create(&structs.ReferFiles{ChatID: chatID, Path: "run/9", Content: "new-body"}).Error; err != nil {
		t.Fatalf("create refer: %v", err)
	}

	chatLn := anchorPlanSession(t, db, chatID, msgs, &trace.AnchorPlan{Mode: trace.AnchorTail, Full: true}, "new-body")
	contents := requestFor(t, db, chatID, chatLn)
	if len(contents) == 0 || !strings.Contains(contents[len(contents)-1], "new-body") {
		t.Fatalf("末尾落位块必须是最后一条消息, got %v", contents)
	}

	var tr structs.Traces
	if err := db.Where("chat_id = ? AND path = ?", chatID, "@temp/run/9").First(&tr).Error; err != nil {
		t.Fatalf("reload trace: %v", err)
	}
	// role:tool 结果不登记 dbIDToElement，末尾锚点记为最后一条"已登记"消息（assistant），
	// findEventAnchor 从它走到其 role:tool 结果，正好是列表末尾。
	if tr.AnchorMsgID != msgs[1].ID {
		t.Errorf("末尾落位必须回写注入锚点: want %d got %d", msgs[1].ID, tr.AnchorMsgID)
	}
	if tr.LastContent != "new-body" {
		t.Errorf("末尾落位必须推进旧端存档: want %q got %q", "new-body", tr.LastContent)
	}
}

// TestRequestBody_FullPlan_AnchorsAfterGivenMessage 完整块按计划锚点落位：
// 落在指定消息的 role:tool 结果之后，而不是顶部、也不是列表末尾。
func TestRequestBody_FullPlan_AnchorsAfterGivenMessage(t *testing.T) {
	config.GlobalConfigSwap(*config.GlobalConfig)
	db := setupTestDB(t)
	setupTestConfig()
	chatID := uint32(43)
	msgs := setupAnchorPlanChat(t, db, chatID)
	if err := db.Create(&structs.Messages{ChatID: chatID, Type: structs.MessagesRoleUser, Delta: "next question"}).Error; err != nil {
		t.Fatalf("create msg: %v", err)
	}

	chatLn := anchorPlanSession(t, db, chatID, msgs, &trace.AnchorPlan{Mode: trace.AnchorFull, MsgID: msgs[1].ID, Full: true}, "held-body")
	contents := requestFor(t, db, chatID, chatLn)
	blockIdx, nextIdx := -1, -1
	for i, c := range contents {
		if strings.Contains(c, "held-body") {
			blockIdx = i
		}
		if strings.Contains(c, "next question") {
			nextIdx = i
		}
	}
	if blockIdx < 0 {
		t.Fatalf("完整块必须注入, got %v", contents)
	}
	if nextIdx < 0 || blockIdx > nextIdx {
		t.Errorf("完整块必须紧跟锚点消息的结果之后（先于其后消息）: block=%d next=%d", blockIdx, nextIdx)
	}
	if blockIdx == len(contents)-1 {
		t.Error("完整块不应被追加到列表末尾（那是末尾落位的语义）")
	}
}

// TestRequestBody_InactiveTrace_NotInjected 压缩边界之前（事件表里没有它）的 trace
// 本轮不注入，但数据库行保留（审计不丢数据，docs/trace-cache-spec.md §4.5）。
func TestRequestBody_InactiveTrace_NotInjected(t *testing.T) {
	config.GlobalConfigSwap(*config.GlobalConfig)
	db := setupTestDB(t)
	setupTestConfig()
	if err := db.AutoMigrate(&structs.Traces{}, &structs.ReferFiles{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	chatID := uint32(44)
	msgs := setupAnchorPlanChat(t, db, chatID)
	if err := db.Create(&structs.Traces{ChatID: chatID, Path: "@temp/run/9", AgentID: "", TraceID: 1, LastContent: "old-body"}).Error; err != nil {
		t.Fatalf("create trace: %v", err)
	}
	if err := db.Create(&structs.ReferFiles{ChatID: chatID, Path: "run/9", Content: "old-body"}).Error; err != nil {
		t.Fatalf("create refer: %v", err)
	}

	chatLn := anchorPlanSession(t, db, chatID, msgs, nil, "old-body")
	// 事件表里只有别的路径，且 trace 层没有为本条 path 生成落位计划
	// （RenderTraceBlocks 对边界前的 trace 直接跳过，不会产出计划）
	chatLn.TemporyDataOfSession[structs.TempKeyTraceEvents] = map[string]*structs.TraceEvent{
		"other.txt": {MsgID: msgs[1].ID, ToolCallID: "call_1"},
	}
	contents := requestFor(t, db, chatID, chatLn)
	for _, c := range contents {
		if strings.Contains(c, "old-body") {
			t.Fatalf("边界前的 trace 不应注入: %q", c)
		}
	}
	var kept int64
	if err := db.Model(&structs.Traces{}).Where("chat_id = ?", chatID).Count(&kept).Error; err != nil {
		t.Fatalf("count traces: %v", err)
	}
	if kept != 1 {
		t.Errorf("trace 行必须保留在库中, want 1 got %d", kept)
	}
}

// TestRequestBody_SystemNoticesGoToTail 内部运行期通知必须作为**消息列表末尾**的独立块注入，
// 不能进 system 消息：DeepSeek 模板里 tools 在 system 之前，一次通知就会把 tools 之后的
// 整个前缀（含全部历史）打掉（实测命中从 ~95% 掉到 tools 前缀大小）。
func TestRequestBody_SystemNoticesGoToTail(t *testing.T) {
	config.GlobalConfigSwap(*config.GlobalConfig)
	db := setupTestDB(t)
	setupTestConfig()
	chatID := uint32(48)
	setupAnchorPlanChat(t, db, chatID)

	plain := eventTestChatLn(map[string]*structs.TraceEvent{}, map[string]trace.FileBlock{})
	plain.ID = chatID
	plain.DB = db
	plain.NowAgent = ""
	before := requestFor(t, db, chatID, plain)

	notified := eventTestChatLn(map[string]*structs.TraceEvent{}, map[string]trace.FileBlock{})
	notified.ID = chatID
	notified.DB = db
	notified.NowAgent = ""
	notified.TemporyDataOfSession[structs.TempKeySystemNotices] = "[System] queued-notice-48"
	after := requestFor(t, db, chatID, notified)

	if len(before) == 0 || len(after) == 0 {
		t.Fatal("empty request")
	}
	if after[0] != before[0] {
		t.Error("排队通知不得改变 system 消息（否则 tools 之后的整段前缀都会失效）")
	}
	last := after[len(after)-1]
	if !strings.Contains(last, "queued-notice-48") {
		t.Fatalf("通知必须作为末尾独立块注入, last=%q", last)
	}
	if !strings.Contains(last, "Alkaid System Notice") {
		t.Errorf("通知块必须带明确标注，避免被模型当成用户指令: %q", last)
	}
}

// TestRequestBody_DiffTailPlan_KeepsOldBlockAndAppendsDiff 方案2（无事件承载的末尾差分）：
// 旧块字节稳定留在原锚点、增量块追加到列表末尾，且**不**推进锚点/基线
// （否则下一次 diff 的旧端会被带跑，前缀缓存正好在旧块处断裂）。
func TestRequestBody_DiffTailPlan_KeepsOldBlockAndAppendsDiff(t *testing.T) {
	config.GlobalConfigSwap(*config.GlobalConfig)
	db := setupTestDB(t)
	setupTestConfig()
	if err := db.AutoMigrate(&structs.Traces{}, &structs.ReferFiles{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	chatID := uint32(46)
	msgs := setupAnchorPlanChat(t, db, chatID)
	oldBody := strings.Repeat("old line\n", 40)
	if err := db.Create(&structs.Traces{ChatID: chatID, Path: "@temp/run/9", AgentID: "", TraceID: 1,
		LastContent: oldBody, AnchorMsgID: msgs[1].ID}).Error; err != nil {
		t.Fatalf("create trace: %v", err)
	}

	chatLn := eventTestChatLn(
		map[string]*structs.TraceEvent{},
		map[string]trace.FileBlock{"@temp/run/9": {Name: "@temp/run/9", Size: "8", Length: 8, Text: "1|new-body\n"}},
	)
	chatLn.ID = chatID
	chatLn.DB = db
	chatLn.NowAgent = ""
	chatLn.TemporyDataOfSession[structs.TempKeyTraceDiffPlan] = map[string]trace.DiffPlan{
		"@temp/run/9": {
			Keep:      true,
			OldBlock:  trace.FileBlock{Name: "@temp/run/9", Size: "9", Length: 9, Text: "1|old-body\n"},
			DiffBlock: trace.FileBlock{Name: "@temp/run/9", Type: "diff", Size: "10", Length: 10, Text: "diff-line\n"},
		},
	}
	chatLn.TemporyDataOfSession[structs.TempKeyTraceAnchorPlan] = map[string]*trace.AnchorPlan{
		"@temp/run/9": {Mode: trace.AnchorDiffTail, PrevMsgID: msgs[1].ID},
	}

	contents := requestFor(t, db, chatID, chatLn)
	oldIdx, diffIdx := -1, -1
	for i, c := range contents {
		if strings.Contains(c, "old-body") {
			oldIdx = i
		}
		if strings.Contains(c, "diff-line") {
			diffIdx = i
		}
	}
	if oldIdx < 0 {
		t.Fatalf("方案2 必须注入旧块: %v", contents)
	}
	if oldIdx <= 1 {
		t.Errorf("旧块必须留在原锚点（assistant 工具结果之后），不能在历史之前: idx=%d", oldIdx)
	}
	if diffIdx != len(contents)-1 {
		t.Errorf("增量块必须追加到列表末尾: diffIdx=%d total=%d", diffIdx, len(contents))
	}

	var tr structs.Traces
	if err := db.Where("chat_id = ? AND path = '@temp/run/9'", chatID).First(&tr).Error; err != nil {
		t.Fatalf("reload trace: %v", err)
	}
	if tr.AnchorMsgID != msgs[1].ID || tr.LastContent != oldBody {
		t.Errorf("方案2 不得推进锚点/基线: anchor=%d lastLen=%d", tr.AnchorMsgID, len(tr.LastContent))
	}
}

// TestRequestBody_VirtualPlanWithoutEvent_Injected 虚拟对象（@tree）可能始终没有对应事件：
// 只要 trace 层给出了落位计划就必须注入——注入集合不能只遍历事件表（否则计划生成了却不执行）。
func TestRequestBody_VirtualPlanWithoutEvent_Injected(t *testing.T) {
	config.GlobalConfigSwap(*config.GlobalConfig)
	db := setupTestDB(t)
	setupTestConfig()
	chatID := uint32(45)
	msgs := setupAnchorPlanChat(t, db, chatID)

	chatLn := anchorPlanSession(t, db, chatID, msgs, &trace.AnchorPlan{Mode: trace.AnchorTail, Full: true}, "tree-body")
	// 事件表为空：虚拟对象没有事件，只有落位计划
	chatLn.TemporyDataOfSession[structs.TempKeyTraceEvents] = map[string]*structs.TraceEvent{}
	contents := requestFor(t, db, chatID, chatLn)
	if len(contents) == 0 || !strings.Contains(contents[len(contents)-1], "tree-body") {
		t.Fatalf("虚拟对象的落位计划必须被执行（末尾落位）, got %v", contents)
	}
}
