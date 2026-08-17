package request

import (
	"strings"
	"testing"

	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
	"gorm.io/gorm"
)

// runLoopJSON 循环测试用的相同 run 调用 JSON（name+参数完全一致）。
const runLoopJSON = `[{"name":"run","id":"call_a","parameters":{"type":"shell","reason":"test","command":"go test ./..."}}]`

// seedLoopHistory 插入 count 条相同工具调用的 assistant 消息。
func seedLoopHistory(t *testing.T, db *gorm.DB, chatID uint32, count int, toolCallingJSON string) {
	t.Helper()
	for range count {
		if err := db.Create(&storageStructs.Messages{
			ChatID:                chatID,
			Type:                  storageStructs.MessagesRoleAgent,
			Delta:                 "ok",
			ToolCallingJSONString: toolCallingJSON,
		}).Error; err != nil {
			t.Fatalf("seed msg: %v", err)
		}
	}
}

// TestDetectToolCallLoop 历史已有 toolCallLoopThreshold-1 条相同调用时，本次相同调用判定循环。
func TestDetectToolCallLoop(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	chat := storageStructs.Chats{ID: 7001}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	seedLoopHistory(t, db, 7001, toolCallLoopThreshold-1, runLoopJSON)

	session := &storageStructs.Chats{ID: 7001, DB: db}
	calls := detectToolCallLoop(session, runLoopJSON)
	if len(calls) == 0 {
		t.Fatalf("期望检测到循环（历史 %d 条相同），实际未触发", toolCallLoopThreshold-1)
	}
	if calls[0].Name != "run" {
		t.Errorf("命中的调用 name 应为 run，实际 %s", calls[0].Name)
	}
}

// TestDetectToolCallLoop_NoLoop 相同调用未达阈值 / 参数不同的调用不判定循环。
func TestDetectToolCallLoop_NoLoop(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	chat := storageStructs.Chats{ID: 7002}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	// 历史仅 1 条相同调用（不足阈值）→ 不触发
	seedLoopHistory(t, db, 7002, toolCallLoopThreshold-2, runLoopJSON)
	session := &storageStructs.Chats{ID: 7002, DB: db}
	if calls := detectToolCallLoop(session, runLoopJSON); len(calls) != 0 {
		t.Errorf("相同调用未达阈值不应触发，实际命中 %v", calls)
	}

	// 历史 2 条相同，本次命令不同 → 不触发
	db.Create(&storageStructs.Chats{ID: 7003})
	seedLoopHistory(t, db, 7003, toolCallLoopThreshold-1, runLoopJSON)
	diff := `[{"name":"run","id":"call_b","parameters":{"type":"shell","reason":"test","command":"go test ./other/..."}}]`
	session2 := &storageStructs.Chats{ID: 7003, DB: db}
	if calls := detectToolCallLoop(session2, diff); len(calls) != 0 {
		t.Errorf("参数不同的调用不应触发循环，实际命中 %v", calls)
	}

	// 历史为空 → 不触发
	db.Create(&storageStructs.Chats{ID: 7004})
	session3 := &storageStructs.Chats{ID: 7004, DB: db}
	if calls := detectToolCallLoop(session3, runLoopJSON); len(calls) != 0 {
		t.Errorf("无历史不应触发，实际命中 %v", calls)
	}
}

// TestDetectToolCallLoop_TailVariation AI 陷入循环时常只改 tail -n 行数与 reason 文案
// 重复同一测试命令（绕过精确匹配）。签名按归一化 command 比较，应识别为循环。
func TestDetectToolCallLoop_TailVariation(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	chat := storageStructs.Chats{ID: 7011}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	// 历史两条：同一测试命令，仅 tail -n 行数不同、reason 文案不同
	hist := []string{
		`[{"name":"run","id":"call_a","parameters":{"type":"shell","reason":"run test v1","command":"go test ./tools/tools/fetch/... ./config/structs/... 2>&1 | tail -n 40"}}]`,
		`[{"name":"run","id":"call_b","parameters":{"type":"shell","reason":"check result","command":"go test ./tools/tools/fetch/... ./config/structs/... 2>&1 | tail -n 30"}}]`,
	}
	for _, j := range hist {
		if err := db.Create(&storageStructs.Messages{
			ChatID:                7011,
			Type:                  storageStructs.MessagesRoleAgent,
			Delta:                 "ok",
			ToolCallingJSONString: j,
		}).Error; err != nil {
			t.Fatalf("seed msg: %v", err)
		}
	}
	// 本次：同样命令，tail -n 60
	cur := `[{"name":"run","id":"call_c","parameters":{"type":"shell","reason":"rerun test","command":"go test ./tools/tools/fetch/... ./config/structs/... 2>&1 | tail -n 60"}}]`
	session := &storageStructs.Chats{ID: 7011, DB: db}
	if calls := detectToolCallLoop(session, cur); len(calls) == 0 {
		t.Fatalf("期望识别为循环（仅 tail -n 行数与 reason 不同），实际未触发")
	}
}

// TestDetectToolCallLoop_SleepExact sleep 的 command 是秒数，用精确值比较——
// 不同等待秒数不应被误判为循环。
func TestDetectToolCallLoop_SleepExact(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	chat := storageStructs.Chats{ID: 7012}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	hist := `[{"name":"run","id":"call_a","parameters":{"type":"sleep","reason":"wait","command":"5"}}]`
	for range toolCallLoopThreshold - 1 {
		if err := db.Create(&storageStructs.Messages{
			ChatID:                7012,
			Type:                  storageStructs.MessagesRoleAgent,
			Delta:                 "ok",
			ToolCallingJSONString: hist,
		}).Error; err != nil {
			t.Fatalf("seed msg: %v", err)
		}
	}
	// 本次 sleep 300：与历史 5 秒不同，不应触发
	cur := `[{"name":"run","id":"call_b","parameters":{"type":"sleep","reason":"wait longer","command":"300"}}]`
	session := &storageStructs.Chats{ID: 7012, DB: db}
	if calls := detectToolCallLoop(session, cur); len(calls) != 0 {
		t.Errorf("sleep 不同秒数不应误判为循环，实际命中 %v", calls)
	}
}

// TestExecuteToolCalls_LoopInjectsWarning 循环命中时注入 role:tool 警告、不执行工具。
func TestExecuteToolCalls_LoopInjectsWarning(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	chat := storageStructs.Chats{ID: 7005}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	seedLoopHistory(t, db, 7005, toolCallLoopThreshold-1, runLoopJSON)

	session := &storageStructs.Chats{ID: 7005, DB: db, CurrentAgentID: "", EnableScopes: make(map[string]bool)}
	if _, err := ExecuteToolCalls(session, runLoopJSON); err != nil {
		t.Fatalf("ExecuteToolCalls: %v", err)
	}
	if session.State != 0 {
		t.Errorf("循环命中后状态应为 Idle，实际 %d", session.State)
	}
	// 验证注入了一条 role:tool 警告消息（而非工具真实执行结果）
	var toolMsgs []storageStructs.Messages
	if err := db.Where("chat_id = ? AND type = ?", 7005, storageStructs.MessagesRoleTool).Find(&toolMsgs).Error; err != nil {
		t.Fatalf("query tool msgs: %v", err)
	}
	if len(toolMsgs) != 1 {
		t.Fatalf("期望 1 条警告 tool 消息，实际 %d", len(toolMsgs))
	}
	if !strings.Contains(toolMsgs[0].Delta, "Tool call loop detected") {
		t.Errorf("警告消息应包含循环提示，实际 %s", toolMsgs[0].Delta)
	}
	if !strings.Contains(toolMsgs[0].Delta, "call_a") {
		t.Errorf("警告消息应带原调用 id 用于配对，实际 %s", toolMsgs[0].Delta)
	}
}
