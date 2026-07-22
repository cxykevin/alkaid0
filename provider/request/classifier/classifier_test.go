package classifier

import (
	"sync"
	"testing"

	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newTestDB 创建内存 SQLite 数据库用于测试。
func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	if err := db.AutoMigrate(&structs.ReferFiles{}, &structs.ClassifySegment{}); err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}
	return db
}

// newTestSession 创建测试用 session。
func newTestSession(t *testing.T) *structs.Chats {
	t.Helper()
	return &structs.Chats{
		DB:   newTestDB(t),
		Root: "/tmp",
	}
}

func TestGenerateRandomID(t *testing.T) {
	id1 := generateRandomID()
	id2 := generateRandomID()

	if len(id1) != 8 {
		t.Errorf("expected 8 hex chars, got %d: %s", len(id1), id1)
	}
	if len(id2) != 8 {
		t.Errorf("expected 8 hex chars, got %d: %s", len(id2), id2)
	}
	if id1 == id2 {
		t.Error("sequential IDs should be different")
	}
}

func TestGetSplitter(t *testing.T) {
	// 重置单例，确保每次测试独立
	once = sync.Once{}
	splitterInstance = nil

	s, err := getSplitter()
	if err != nil {
		t.Fatalf("getSplitter failed: %v", err)
	}
	if s == nil {
		t.Fatal("splitter is nil")
	}

	// 测试单例行为
	s2, err2 := getSplitter()
	if err2 != nil {
		t.Fatalf("getSplitter second call failed: %v", err2)
	}
	if s != s2 {
		t.Error("splitter is not a singleton")
	}
}

func TestClassifyAndTransformEmpty(t *testing.T) {
	once = sync.Once{}
	splitterInstance = nil

	result, infos, err := ClassifyAndTransform(nil, "")
	if err != nil {
		t.Fatalf("unexpected error for empty input: %v", err)
	}
	if result != "" {
		t.Errorf("expected empty result, got %q", result)
	}
	if len(infos) != 0 {
		t.Errorf("expected no segment info for empty input, got %d", len(infos))
	}
}

func TestClassifyAndTransformPassthrough(t *testing.T) {
	once = sync.Once{}
	splitterInstance = nil
	session := newTestSession(t)

	// 纯 prompt 消息应该原样传递
	msg := "请帮我解释一下什么是 RESTful API"
	result, infos, err := ClassifyAndTransform(session, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != msg {
		t.Errorf("expected passthrough, got %q", result)
	}
	if len(infos) != 0 {
		t.Errorf("expected no segment info for pure prompt, got %d", len(infos))
	}
}

func TestClassifyAndTransformWithCode(t *testing.T) {
	once = sync.Once{}
	splitterInstance = nil
	session := newTestSession(t)

	// 包含代码的消息（markdown 代码块）
	msg := "请解释这段代码：\n```go\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n```"
	result, infos, err := ClassifyAndTransform(session, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 应该包含 [path:@temp/prompt/code- 标记
	if len(result) <= len(msg) {
		t.Error("expected result to be longer than input (code should have path marker appended)")
	}
	// 原始文本应保留
	if !contains(result, "func main()") {
		t.Errorf("expected code to remain in result, got: %s", result)
	}
	// 应该有 path 标记
	if !contains(result, "[path:@temp/prompt/code-") {
		t.Errorf("expected path marker in result, got: %s", result)
	}

	// 验证 ReferFiles 中有记录
	var refCount int64
	session.DB.Model(&structs.ReferFiles{}).Where("chat_id = ?", session.ID).Count(&refCount)
	if refCount == 0 {
		t.Error("expected ReferFiles records for code segment")
	}

	// 验证返回了段信息
	if len(infos) == 0 {
		t.Fatal("expected segment info")
	}
	hasCode := false
	hasPrompt := false
	for _, info := range infos {
		if info.Label == "code" {
			hasCode = true
			if info.TempPath == "" {
				t.Error("code segment should have TempPath")
			}
			if !contains(info.Text, "func main()") {
				t.Error("code segment should contain the code text")
			}
		}
		if info.Label == "prompt" {
			hasPrompt = true
		}
	}
	if !hasCode {
		t.Error("expected a code segment in infos")
	}
	if !hasPrompt {
		t.Error("expected a prompt segment in infos")
	}
}

func TestClassifySegmentInfoLogged(t *testing.T) {
	once = sync.Once{}
	splitterInstance = nil
	session := newTestSession(t)

	msg := "查看报错：\nERROR: connection timeout\n请用以下代码修复：\n```bash\ncurl -v http://example.com\n```"
	result, infos, err := ClassifyAndTransform(session, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = result

	// 应包含 prompt、log、code 三种标签
	labels := make(map[string]int)
	for _, info := range infos {
		labels[info.Label]++
	}
	if labels["prompt"] == 0 {
		t.Error("expected prompt segment")
	}
	if labels["log"] == 0 {
		t.Error("expected log segment")
	}
	if labels["code"] == 0 {
		t.Error("expected code segment")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
