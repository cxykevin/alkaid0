package structs

import (
	"sync"
	"testing"

	"github.com/cxykevin/alkaid0/ui/state"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// TestChatsStateAccessorsConcurrent 验证 State/ToolState/CurrentMessageID 的访问器
// 并发安全。需在 -race 下运行：修复前这些字段是裸读写，loop goroutine 与广播
// goroutine 并发访问时 race detector 会报告 data race。
func TestChatsStateAccessorsConcurrent(t *testing.T) {
	c := &Chats{}
	var wg sync.WaitGroup
	const workers = 8
	const perWorker = 500

	// 写入方：模拟 loop goroutine 的状态迁移
	for w := range workers {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for j := range perWorker {
				c.SetState(state.State(j % 7))
				c.SetToolState(uint64(j % 3))
				c.SetCurrentMessageID(uint64(j))
				_ = w
			}
		}(w)
	}
	// 读取方：模拟 server 广播 goroutine 的状态/工具状态读取
	for range workers {
		wg.Go(func() {
			for range perWorker {
				_ = c.GetState()
				_ = c.GetToolState()
				_ = c.GetCurrentMessageID()
			}
		})
	}
	wg.Wait()
}

// TestChatsStateAccessorsShareMemoryWithFields 访问器必须读写同一个字段内存：
// server/actions 等处仍有 sState == state.StateIdle 的直接比较，
// 若访问器使用独立镜像字段，两边的值会分叉（这是本方案选择就地原子读写的原因）。
func TestChatsStateAccessorsShareMemoryWithFields(t *testing.T) {
	c := &Chats{}
	c.State = state.StateWaitApprove // 历史调用方/测试的直接赋值
	if got := c.GetState(); got != state.StateWaitApprove {
		t.Fatalf("GetState()=%v, want WaitApprove（访问器与字段内存不一致）", got)
	}
	c.SetState(state.StateIdle)
	if c.State != state.StateIdle {
		t.Fatalf("direct field=%v, want Idle（SetState 未写回字段）", c.State)
	}
	c.ToolState = 2
	if got := c.GetToolState(); got != 2 {
		t.Fatalf("GetToolState()=%d, want 2", got)
	}
	c.SetToolState(1)
	if c.ToolState != 1 {
		t.Fatalf("ToolState=%d, want 1", c.ToolState)
	}
	c.CurrentMessageID = 99
	if got := c.GetCurrentMessageID(); got != 99 {
		t.Fatalf("GetCurrentMessageID()=%d, want 99", got)
	}
	c.SetCurrentMessageID(100)
	if c.CurrentMessageID != 100 {
		t.Fatalf("CurrentMessageID=%d, want 100", c.CurrentMessageID)
	}
}

// TestChatsSaveStatePersistsWithoutClobberingTitle SaveState 只更新 state 列：
// 必须落库状态，同时不得覆盖其它列（标题 goroutine 写入的 ai_title）。
func TestChatsSaveStatePersistsWithoutClobberingTitle(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&Chats{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	chat := &Chats{}
	if err := db.Create(chat).Error; err != nil {
		t.Fatalf("create chat: %v", err)
	}
	// 标题 goroutine 异步写入 DB（内存快照仍是空）
	if err := db.Model(&Chats{}).Where("id = ?", chat.ID).Update("ai_title", "AI 生成的标题").Error; err != nil {
		t.Fatalf("update ai_title: %v", err)
	}
	chat.DB = db
	chat.SetState(state.StateWaitApprove)
	if err := chat.SaveState(state.StateIdle); err != nil {
		t.Fatalf("SaveState: %v", err)
	}
	var got Chats
	if err := db.First(&got, chat.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.State != state.StateIdle {
		t.Fatalf("persisted state=%v, want Idle", got.State)
	}
	if got.AITitle != "AI 生成的标题" {
		t.Fatalf("SaveState clobbered ai_title: got %q", got.AITitle)
	}
}
