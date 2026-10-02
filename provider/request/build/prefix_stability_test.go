package build

// 前缀缓存稳定性不变量：请求历史必须"只增不减"。
//
// DeepSeek 等供应商的前缀缓存按请求前缀逐字节匹配，只要某一轮把最旧的历史消息挤出去，
// 分歧点就落在历史第一条消息上——system 之后的整段历史全部按未命中计费。
// 曾在 205 条消息的会话上实测：窗口滑动后首个差异下标=1，可复用前缀只剩 system，
// 命中率上限从 ~95% 跌到 ~20%。因此回放窗口（maxReplayPage）必须是近乎无界的兜底值。

import (
	"testing"

	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
	"gorm.io/gorm"
)

// buildReplay 构建一次请求体并返回「角色|正文」表示，用于逐条比较前缀。
func buildReplay(t *testing.T, chatID uint32, db *gorm.DB, addUserPrompt string) []string {
	t.Helper()
	req, err := RequestBody(chatID, 1, "", noTools(), db, "", addUserPrompt, cfgStruct.AgentConfig{}, &structs.Chats{})
	if err != nil {
		t.Fatalf("RequestBody: %v", err)
	}
	out := make([]string, 0, len(req.Messages))
	for _, m := range req.Messages {
		out = append(out, m.Role+"|"+m.Content)
	}
	return out
}

// TestReplayWindowAppendOnly 超过旧 200 条上限的长会话：追加一条消息后，
// 上一轮的全部消息必须仍是新请求的前缀（不做任何截断/滑动）。
func TestReplayWindowAppendOnly(t *testing.T) {
	setupTestConfig()
	db := setupTestDB(t)

	const chatID = 77
	// 205 > readPageSize*maxPage（旧上限 200），确保旧的滑动窗口行为会在此用例下暴露
	const seeded = 205
	for i := 0; i < seeded; i++ {
		typ := structs.MessagesRoleUser
		delta := "user message number"
		if i%2 == 1 {
			typ = structs.MessagesRoleAgent
			delta = "assistant reply body"
		}
		if err := db.Create(&structs.Messages{ChatID: chatID, Type: typ, Delta: delta}).Error; err != nil {
			t.Fatalf("seed message: %v", err)
		}
	}

	before := buildReplay(t, chatID, db, "")
	if err := db.Create(&structs.Messages{ChatID: chatID, Type: structs.MessagesRoleUser, Delta: "the newest user turn"}).Error; err != nil {
		t.Fatalf("append message: %v", err)
	}
	after := buildReplay(t, chatID, db, "")

	if len(after) != len(before)+1 {
		t.Fatalf("追加一条消息后请求消息数应 +1：before=%d after=%d（回放窗口被截断或滑动）", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("前缀在第 %d 条消息处断裂：before=%q after=%q", i, before[i], after[i])
		}
	}

	reusable, total := 0, 0
	for i, m := range after {
		tk := u.EstimateTokens(m)
		total += tk
		if i < len(before) {
			reusable += tk
		}
	}
	if total > 0 {
		t.Logf("可复用前缀 %d/%d token（%.1f%%），消息数 %d", reusable, total,
			float64(reusable)*100/float64(total), len(after))
	}
}
