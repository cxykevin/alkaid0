package ahocorasick

import (
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
