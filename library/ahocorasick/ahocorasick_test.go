package ahocorasick

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestBasicReplace(t *testing.T) {
	r := NewReplacer([]Item{
		{Keyword: "sk-xqz789", Replace: "sk-or-realkey"},
		{Keyword: "13912345678", Replace: "13800138000"},
	})
	out := r.Stream([]byte("my key is sk-xqz789 and phone 13912345678"))
	out = append(out, r.Finish()...)
	want := "my key is sk-or-realkey and phone 13800138000"
	if string(out) != want {
		t.Fatalf("got %q, want %q", string(out), want)
	}
}

func TestMultipleChunks(t *testing.T) {
	// 关键字被切分到多个 chunk，仍应完整匹配
	r := NewReplacer([]Item{{Keyword: "sk-or-v1-abcdef", Replace: "REAL"}})
	chunks := [][]byte{
		[]byte("sk-or"),
		[]byte("-v1-"),
		[]byte("abcdef"),
	}
	var out []byte
	for _, ch := range chunks {
		out = append(out, r.Stream(ch)...)
	}
	out = append(out, r.Finish()...)
	if string(out) != "REAL" {
		t.Fatalf("got %q, want %q", string(out), "REAL")
	}
}

func TestLongestMatch(t *testing.T) {
	r := NewReplacer([]Item{
		{Keyword: "abcdef", Replace: "LONG"},
		{Keyword: "def", Replace: "SHORT"},
	})
	out := r.Stream([]byte("abcdef"))
	out = append(out, r.Finish()...)
	if string(out) != "LONG" {
		t.Fatalf("got %q, want %q", string(out), "LONG")
	}
}

func TestLeftmostMatch(t *testing.T) {
	r := NewReplacer([]Item{
		{Keyword: "abc", Replace: "X"},
		{Keyword: "bc", Replace: "Y"},
	})
	out := r.Stream([]byte("abcbc"))
	out = append(out, r.Finish()...)
	if string(out) != "XY" {
		t.Fatalf("got %q, want %q", string(out), "XY")
	}
}

func TestImmediateFlush(t *testing.T) {
	// 无匹配的普通字符应立即刷出（不依赖 chunk 结束）
	r := NewReplacer([]Item{{Keyword: "needle", Replace: "N"}})
	out := r.Stream([]byte("plain text without matches"))
	out = append(out, r.Finish()...)
	if string(out) != "plain text without matches" {
		t.Fatalf("got %q", string(out))
	}
}

func TestFinishFlushesPartialPrefix(t *testing.T) {
	// 流在关键字前缀中途结束：按明文刷出
	r := NewReplacer([]Item{{Keyword: "needle", Replace: "N"}})
	var out []byte
	out = append(out, r.Stream([]byte("need"))...)
	out = append(out, r.Finish()...)
	if string(out) != "need" {
		t.Fatalf("got %q, want %q", string(out), "need")
	}
}

func TestBoundedBufferLongPartial(t *testing.T) {
	// 长串反复逼近但从不完成的关键字前缀：缓冲有界、最终按明文输出
	r := NewReplacer([]Item{{Keyword: "abcdefgh", Replace: "N"}})
	// "abcdefg" 重复 5 次，永不构成 "abcdefgh"
	in := strings.Repeat("abcdefg", 5)
	out := r.Stream([]byte(in))
	out = append(out, r.Finish()...)
	if string(out) != in {
		t.Fatalf("got len=%d want len=%d", len(out), len(in))
	}
	if string(out) != in {
		t.Fatalf("mismatch:\n got %q\nwant %q", string(out), in)
	}
}

func TestUTF8AroundMatches(t *testing.T) {
	// UTF-8 文本与 ASCII 关键字混合，还原后应保持 UTF-8 完整
	r := NewReplacer([]Item{{Keyword: "sk-xqz789", Replace: "sk-real"}})
	in := "你好，密钥是 sk-xqz789，再见 👋"
	out := r.Stream([]byte(in))
	out = append(out, r.Finish()...)
	want := "你好，密钥是 sk-real，再见 👋"
	if string(out) != want {
		t.Fatalf("got %q, want %q", string(out), want)
	}
}

func TestConsecutiveMatches(t *testing.T) {
	r := NewReplacer([]Item{{Keyword: "ab", Replace: "X"}})
	out := r.Stream([]byte("ababab"))
	out = append(out, r.Finish()...)
	if string(out) != "XXX" {
		t.Fatalf("got %q, want %q", string(out), "XXX")
	}
}

func TestEmptyItems(t *testing.T) {
	r := NewReplacer(nil)
	out := r.Stream([]byte("hello world"))
	out = append(out, r.Finish()...)
	if string(out) != "hello world" {
		t.Fatalf("got %q", string(out))
	}
}

func TestCaseSensitive(t *testing.T) {
	r := NewReplacer([]Item{{Keyword: "AbC", Replace: "XYZ"}})
	out := r.Stream([]byte("abc AbC aBc"))
	out = append(out, r.Finish()...)
	if string(out) != "abc XYZ aBc" {
		t.Fatalf("got %q", string(out))
	}
}

func TestClassicOverlaps(t *testing.T) {
	// 经典 AC 用例：后缀同时是其它关键字前缀。
	// 语义是「最左最长、命中后回到根、不重叠」：命中 "she" 时不再回退匹配其中的
	// "he"，命中 "he" 之后 "rs" 也不构成 "hers" —— 期望值即该语义的产物。
	r := NewReplacer([]Item{
		{Keyword: "he", Replace: "1"},
		{Keyword: "she", Replace: "2"},
		{Keyword: "his", Replace: "3"},
		{Keyword: "hers", Replace: "4"},
	})
	out := r.Stream([]byte("ushers his she he hers"))
	out = append(out, r.Finish()...)
	if string(out) != "u2rs 3 2 1 1rs" {
		t.Fatalf("got %q", string(out))
	}
	// 单个 "hers" 的输入同样是「先命中 he 即提交」：命中时刻的最长完成前缀是 "he"，
	// 不会等到 "hers" 完整匹配 —— 这是旧实现就有的语义，必须原样保留。
	out = r.Stream([]byte("hers"))
	out = append(out, r.Finish()...)
	if string(out) != "1rs" {
		t.Fatalf("hers -> %q, want %q", string(out), "1rs")
	}
	// 只在 "hers" 是唯一关键字时才会整段替换
	solo := NewReplacer([]Item{{Keyword: "hers", Replace: "4"}})
	out = solo.Stream([]byte("hers"))
	out = append(out, solo.Finish()...)
	if string(out) != "4" {
		t.Fatalf("solo hers -> %q, want 4", string(out))
	}
}

func TestDuplicateKeywordLastWins(t *testing.T) {
	// 同一关键字重复出现时后者覆盖前者（沿用旧实现语义）
	r := NewReplacer([]Item{
		{Keyword: "key", Replace: "old"},
		{Keyword: "key", Replace: "new"},
	})
	out := r.Stream([]byte("key"))
	out = append(out, r.Finish()...)
	if string(out) != "new" {
		t.Fatalf("got %q, want %q", string(out), "new")
	}
}

func TestSharedTrieIndependentMatchers(t *testing.T) {
	// 一份自动机被多条流共享时，各流状态必须互不影响
	trie := NewTrie([]Item{
		{Keyword: "sk-secret", Replace: "REAL"},
		{Keyword: "13912345678", Replace: "13800138000"},
	})
	a := trie.NewMatcher()
	b := trie.NewMatcher()
	var outA, outB []byte
	// 交错的 chunk：同一份 trie、两条独立的流
	for _, chunk := range []string{"sk-se", "139", "cret", "1234", " and ", "5678"} {
		switch {
		case strings.HasPrefix(chunk, "sk"), chunk == "cret", chunk == " and ":
			outA = append(outA, a.Stream([]byte(chunk))...)
		default:
			outB = append(outB, b.Stream([]byte(chunk))...)
		}
	}
	outA = append(outA, a.Finish()...)
	outB = append(outB, b.Finish()...)
	if string(outA) != "REAL and " {
		t.Fatalf("stream A got %q", string(outA))
	}
	if string(outB) != "13800138000" {
		t.Fatalf("stream B got %q", string(outB))
	}
}

// TestTrieMemoryPerNode 锁定自动机的内存模型：占用必须与节点数线性且常数极小。
//
// 回归背景：旧实现每个节点内联 [256]*node（≈2 KiB/节点），key_mappings 增长到
// 2255 条（28 万节点）时单个自动机就要 620 MiB；而请求侧还会为正文/思考/每条
// 工具参数各建一份，一次请求就能把常驻内存推到数 GiB。改为扁平 CSR 布局后
// 每节点约 30 字节，这里用 96 字节/节点的上限把它钉住。
func TestTrieMemoryPerNode(t *testing.T) {
	items := make([]Item, 0, 2000)
	for i := range 2000 {
		// 40 字节、几乎不共享前缀的关键字（模拟脱敏假值）
		kw := fmt.Sprintf("%08x-%08x-%08x-%08x-%08x", i*2654435761, i*40503+7, i*97+11, i*13+3, i*7+1)
		items = append(items, Item{Keyword: kw, Replace: strings.Repeat("x", 16)})
	}

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	trie := NewTrie(items)

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(trie)

	nodes := trie.NumNodes()
	if nodes < len(items)*8 {
		t.Fatalf("unexpected node count %d for %d keywords", nodes, len(items))
	}
	used := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	perNode := used / int64(nodes)
	if perNode > 96 {
		t.Fatalf("automaton uses %d bytes/node (%d KiB total, %d nodes); want <= 96", perNode, used>>10, nodes)
	}
	t.Logf("nodes=%d retained=%dKiB perNode=%dB", nodes, used>>10, perNode)
}
