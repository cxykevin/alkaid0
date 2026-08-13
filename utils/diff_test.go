package u

import (
	"strings"
	"testing"
)

func TestEstimateTokens(t *testing.T) {
	if n := EstimateTokens(""); n != 0 {
		t.Errorf("empty text should be 0, got %d", n)
	}
	if n := EstimateTokens("hello world"); n <= 0 {
		t.Errorf("non-empty ascii should be > 0, got %d", n)
	}
	// 4 个 CJK 字符，按 1 token/字估算为 4
	if n := EstimateTokens("你好世界"); n != 4 {
		t.Errorf("4 CJK chars should be ~4, got %d", n)
	}
}

func TestUnifiedDiff(t *testing.T) {
	if d := UnifiedDiff("a\nb\n", "a\nb\n", "x.txt"); d != "" {
		t.Errorf("identical should be empty, got %q", d)
	}

	// 删除一行
	d := UnifiedDiff("a\nb\nc\n", "a\nc\n", "x.txt")
	if !strings.HasPrefix(d, "--- a/x.txt\n") || !strings.Contains(d, "+++ b/x.txt\n") {
		t.Errorf("missing header: %q", d)
	}
	if !strings.Contains(d, "-b") || !strings.Contains(d, "@@") {
		t.Errorf("missing delete/hunk: %q", d)
	}

	// 增加一行
	d = UnifiedDiff("a\nc\n", "a\nb\nc\n", "x.txt")
	if !strings.Contains(d, "+b") {
		t.Errorf("missing add: %q", d)
	}

	// 修改一行
	d = UnifiedDiff("a\nb\nc\n", "a\nB\nc\n", "x.txt")
	if !strings.Contains(d, "-b") || !strings.Contains(d, "+B") {
		t.Errorf("missing change: %q", d)
	}
}
