package trace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
)

func TestCacheTimeout(t *testing.T) {
	if cacheTimeout(time.Time{}, 180) {
		t.Error("零值时间（新会话/查询失败）不应判超时")
	}
	if cacheTimeout(time.Now().Add(-3*time.Hour), 0) {
		t.Error("保留时间 <= 0 不应判超时")
	}
	if cacheTimeout(time.Now().Add(-time.Minute), 180) {
		t.Error("一分钟前不应判超时")
	}
	// 用 4 小时：与 180 分钟保留期保持 1 小时间隔，避开"恰好等于边界"的时钟粒度问题
	// （Windows 的单调时钟粒度约 15ms，边界值可能被算成刚好 3 小时而判不超时）
	if !cacheTimeout(time.Now().Add(-4*time.Hour), 180) {
		t.Error("四小时前应判超时")
	}
}

// TestLastRequestTime 上一次请求时间取最后一条消息的 time 列：
// 活跃回合持续刷新、跨会话互不影响、无消息时为零值。
func TestLastRequestTime(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	if got := lastRequestTime(&structs.Chats{ID: 1, DB: db}); !got.IsZero() {
		t.Errorf("无消息时应返回零值: %v", got)
	}

	now := time.Now().Truncate(time.Second)
	if err := db.Create(&structs.Messages{ChatID: 1, Type: structs.MessagesRoleUser, Delta: "hi",
		Time: uint64(now.Add(-2 * time.Hour).Unix())}).Error; err != nil {
		t.Fatalf("create old message: %v", err)
	}
	if err := db.Create(&structs.Messages{ChatID: 1, Type: structs.MessagesRoleAgent, Delta: "yo",
		Time: uint64(now.Unix())}).Error; err != nil {
		t.Fatalf("create new message: %v", err)
	}
	if got := lastRequestTime(&structs.Chats{ID: 1, DB: db}); got.Unix() != now.Unix() {
		t.Errorf("应取最后一条消息的时间: want %d got %d", now.Unix(), got.Unix())
	}

	// 其它会话（含子代理）的消息不能参与本会话的判断
	if err := db.Create(&structs.Messages{ChatID: 2, Type: structs.MessagesRoleUser, Delta: "x",
		Time: uint64(now.Add(time.Hour).Unix())}).Error; err != nil {
		t.Fatalf("create other chat message: %v", err)
	}
	if got := lastRequestTime(&structs.Chats{ID: 1, DB: db}); got.Unix() != now.Unix() {
		t.Errorf("只应看本会话消息: got %d", got.Unix())
	}

	if got := lastRequestTime(nil); !got.IsZero() {
		t.Error("nil session 应返回零值")
	}
	if got := lastRequestTime(&structs.Chats{ID: 1}); !got.IsZero() {
		t.Error("无 DB 时应返回零值")
	}
}

// TestRenderTraceBlocks_ExpiredCacheForcesFull 跨过缓存保留期后必须放弃「保留旧块 + diff」：
// 旧块换不到任何缓存折扣，只剩多传一份内容的成本，应直接让完整块落到最新事件（方案1）。
// 未过期时仍走方案2（旧块留在原锚点 + diff 锚到最新事件）。
func TestRenderTraceBlocks_ExpiredCacheForcesFull(t *testing.T) {
	for _, tc := range []struct {
		name     string
		lastAge  time.Duration
		wantMode AnchorMode
	}{
		{"缓存仍有效", time.Minute, AnchorDiff},
		{"缓存已过期", 4 * time.Hour, AnchorFull},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setupTestDB(t)
			defer u.Unwrap(db.DB()).Close()

			var oldContent strings.Builder
			for i := range 40 {
				fmt.Fprintf(&oldContent, "long content line %02d with plenty of text\n", i)
			}
			newContent := strings.Replace(oldContent.String(), "line 20", "line XX", 1)
			if newContent == oldContent.String() {
				t.Fatal("测试内容构造失败：没有改动到任何一行")
			}

			tmpDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(tmpDir, "a.txt"), []byte(newContent), 0644); err != nil {
				t.Fatalf("write a.txt: %v", err)
			}
			if err := db.Create(&structs.Traces{ChatID: 1, Path: "a.txt", TraceID: 1, AgentID: "test_agent",
				LastContent: oldContent.String(), AnchorMsgID: 10}).Error; err != nil {
				t.Fatalf("create trace: %v", err)
			}
			// 最后一条消息的时间 = 上一轮请求时间
			if err := db.Create(&structs.Messages{ChatID: 1, Type: structs.MessagesRoleAgent, Delta: "edit",
				Time: uint64(time.Now().Add(-tc.lastAge).Unix())}).Error; err != nil {
				t.Fatalf("create message: %v", err)
			}

			session := &structs.Chats{
				ID:       1,
				DB:       db,
				NowAgent: "test_agent",
				Root:     tmpDir,
				TemporyDataOfSession: map[string]any{
					structs.TempKeyTraceEvents: map[string]*structs.TraceEvent{
						"a.txt": {MsgID: 20, ToolCallID: "call_1", IsEdit: true},
					},
				},
			}
			if _, _, err := RenderTraceBlocks(session); err != nil {
				t.Fatalf("RenderTraceBlocks: %v", err)
			}
			plans, _ := session.TemporyDataOfSession[structs.TempKeyTraceAnchorPlan].(map[string]*AnchorPlan)
			p := plans["a.txt"]
			if p == nil {
				t.Fatal("缺少落位计划")
			}
			if p.Mode != tc.wantMode {
				t.Errorf("落位模式 = %v, want %v（lastAge=%v）", p.Mode, tc.wantMode, tc.lastAge)
			}
		})
	}
}
