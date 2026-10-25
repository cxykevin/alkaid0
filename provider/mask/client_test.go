package mask

import (
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	configStructs "github.com/cxykevin/alkaid0/config/structs"
)

// configSwapForTest 临时替换全局脱敏配置，返回恢复函数。
func configSwapForTest(t *testing.T, enable bool) func() {
	t.Helper()
	restore := config.GlobalConfigSwap(configStructs.Config{
		DataMask: configStructs.DataMaskConfig{
			Enable:             enable,
			MaskAPIKey:         enable,
			MaskJWT:            enable,
			MaskClientMessages: enable,
		},
	})
	return restore
}

// TestClientMaskerTextConsistentWithEngine 客户端展示脱敏与出站脱敏使用同一映射：
// 同一原值在两侧得到同一个假值，且假值不会被再次脱敏。
func TestClientMaskerTextConsistentWithEngine(t *testing.T) {
	enableMask(t)
	db := newTestDB(t)

	cm := NewClientMasker(db)
	if cm == nil {
		t.Fatal("client masker should be enabled")
	}
	orig := "sk-or-v1-abc123def456ghi789"
	text := "key " + orig
	got := cm.Text(text)
	if strings.Contains(got, orig) {
		t.Fatalf("client text still contains original key: %q", got)
	}

	eng := NewEngine(db)
	want := eng.MaskText(text)
	if got != want {
		t.Fatalf("client masker and engine disagree: %q vs %q", got, want)
	}

	// 假值已经是脱敏结果，不能被再脱敏一次。
	if again := cm.Text(got); again != got {
		t.Fatalf("fake value was masked twice: %q -> %q", got, again)
	}
}

// TestClientMaskerStreamAcrossChunks 敏感值被切分到多个 chunk 时仍能完整替换。
func TestClientMaskerStreamAcrossChunks(t *testing.T) {
	enableMask(t)
	db := newTestDB(t)

	cm := NewClientMasker(db)
	orig := "sk-or-v1-abc123def456ghi789"
	full := "before " + orig + " after"
	want := cm.Text(full)

	key := StreamKey{Choice: 1, Kind: StreamKindContent}
	chunks := []string{"before sk-or", "-v1-abc123", "def456ghi789 after"}
	var got strings.Builder
	for _, chunk := range chunks {
		got.WriteString(cm.Stream(key, chunk))
	}
	got.WriteString(cm.FinishStream(key))
	if got.String() != want {
		t.Fatalf("streamed mask mismatch:\n got %q\nwant %q", got.String(), want)
	}
}

// TestClientMaskerStreamFinishKeepsTail 流结束时未完成的关键字前缀按原文输出，
// 不能因为等待匹配而丢字。
func TestClientMaskerStreamFinishKeepsTail(t *testing.T) {
	enableMask(t)
	db := newTestDB(t)

	cm := NewClientMasker(db)
	key := StreamKey{Choice: 2, Kind: StreamKindContent}
	head := cm.Stream(key, "tail sk-or")
	tail := cm.FinishStream(key)
	if head+tail != "tail sk-or" {
		t.Fatalf("tail lost: head=%q tail=%q", head, tail)
	}
	if again := cm.FinishStream(key); again != "" {
		t.Fatalf("finished stream should be empty, got %q", again)
	}
}

// TestClientMaskerRefreshesMappings 请求侧引擎新建映射后，长期持有的客户端
// 脱敏器必须感知到，否则会把新生成的假值当成新的敏感值再脱敏。
func TestClientMaskerRefreshesMappings(t *testing.T) {
	enableMask(t)
	db := newTestDB(t)

	cm := NewClientMasker(db) // 此时映射表为空
	eng := NewEngine(db)
	fake := eng.MaskText("sk-or-v1-abc123def456ghi789")

	if got := cm.Text(fake); got != fake {
		t.Fatalf("client masker did not refresh mappings: %q -> %q", fake, got)
	}
}

// TestClientMaskerCustomValue 自定义脱敏值（/mask add）同样作用于客户端展示。
func TestClientMaskerCustomValue(t *testing.T) {
	enableMask(t)
	db := newTestDB(t)
	if err := AddCustom(db, "internal-host"); err != nil {
		t.Fatalf("add custom: %v", err)
	}

	cm := NewClientMasker(db)
	got := cm.Text("connect to internal-host now")
	if strings.Contains(got, "internal-host") {
		t.Fatalf("custom value not masked: %q", got)
	}
}

// TestClientMaskerNilAndDisabled db 为空或总开关关闭时返回 nil。
func TestClientMaskerNilAndDisabled(t *testing.T) {
	if NewClientMasker(nil) != nil {
		t.Fatal("nil db should return nil client masker")
	}
	restore := configSwapForTest(t, false)
	defer restore()
	if NewClientMasker(newTestDB(t)) != nil {
		t.Fatal("disabled mask should return nil client masker")
	}
}
