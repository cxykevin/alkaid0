package structs

import (
	"reflect"
	"strings"
	"testing"

	u "github.com/cxykevin/alkaid0/utils"
)

// TestRenderToolCallingText 渲染规则：键排序、null/空键跳过、非字符串值走 JSON、非 map 输入渲染为空。
func TestRenderToolCallingText(t *testing.T) {
	ptrValue := any("value")
	cases := []struct {
		name   string
		params any
		want   string
	}{
		{"nil params", nil, ""},
		{"empty map", map[string]any{}, ""},
		{"keys sorted", map[string]any{"path": "/a/b", "content": "hello"}, "Content: hello\nPath: /a/b\n"},
		{"number value as json", map[string]any{"count": 3}, "Count: 3\n"},
		{"slice value as json", map[string]any{"list": []any{1, 2}}, "List: [1,2]\n"},
		{"nil value skipped", map[string]any{"a": nil}, ""},
		{"empty key skipped", map[string]any{"": "x"}, ""},
		{"non-ascii key kept whole", map[string]any{"参数": "x"}, "参数: x\n"},
		{"u.H input", u.H{"line": 7}, "Line: 7\n"},
		{"pointer map drops nil entry", map[string]*any{"a": &ptrValue, "b": nil}, "A: value\n"},
		{"unsupported type", 42, ""},
	}
	for _, tc := range cases {
		if got := RenderToolCallingText(tc.params); got != tc.want {
			t.Errorf("%s: RenderToolCallingText(%v) = %q, want %q", tc.name, tc.params, got, tc.want)
		}
	}
}

// TestParamTextFallback 字符串原样、可 JSON 化的值用 JSON、不可 JSON 化的值退回 fmt。
func TestParamTextFallback(t *testing.T) {
	if got := paramText("plain"); got != "plain" {
		t.Errorf("paramText(string) = %q, want %q", got, "plain")
	}
	if got := paramText(nil); got != "null" {
		t.Errorf("paramText(nil) = %q, want %q", got, "null")
	}
	if got := paramText(make(chan int)); !strings.HasPrefix(got, "0x") {
		t.Errorf("paramText(chan) = %q, want a fmt-formatted pointer", got)
	}
}

// TestNormalizeToolCallingParams 归一化：解指针 map、抹平 u.H、非法输入返回 nil。
func TestNormalizeToolCallingParams(t *testing.T) {
	if got := NormalizeToolCallingParams(nil); got != nil {
		t.Errorf("NormalizeToolCallingParams(nil) = %#v, want nil", got)
	}

	ptrValue := any("v")
	got := NormalizeToolCallingParams(map[string]*any{"a": &ptrValue, "b": nil})
	want := map[string]any{"a": "v", "b": nil}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("pointer map = %#v, want %#v", got, want)
	}

	if gotH := NormalizeToolCallingParams(u.H{"n": 1}); !reflect.DeepEqual(gotH, map[string]any{"n": float64(1)}) {
		t.Errorf("u.H input = %#v, want map with float64(1)", gotH)
	}

	if bad := NormalizeToolCallingParams(map[string]any{"bad": make(chan int)}); bad != nil {
		t.Errorf("unmarshalable input = %#v, want nil", bad)
	}

	// 非 map 类型走 default 分支，归一化为空对象（非 nil）。
	gotEmpty := NormalizeToolCallingParams([]string{"a"})
	m, ok := gotEmpty.(map[string]any)
	if !ok || len(m) != 0 {
		t.Errorf("unexpected type input = %#v, want empty map[string]any", gotEmpty)
	}
}

// TestNormalizeToolCallingContent 替换首个文本块、填充 calling_info 的 args，且不就地改写入参。
func TestNormalizeToolCallingContent(t *testing.T) {
	raw := map[string]any{"command": "ls"}
	blocks := []u.H{
		{"type": "content", "content": u.H{"type": "text", "text": "old preview"}},
		{"type": ToolCallingInfoType, "args": "stale"},
	}
	const wantText = "Command: ls\n"

	t.Run("nil raw keeps content", func(t *testing.T) {
		if got := NormalizeToolCallingContent(blocks, nil); !reflect.DeepEqual(got, blocks) {
			t.Errorf("= %#v, want input untouched", got)
		}
	})

	t.Run("nil content", func(t *testing.T) {
		if got := NormalizeToolCallingContent(nil, raw); got != nil {
			t.Errorf("= %#v, want nil", got)
		}
	})

	t.Run("non block content kept", func(t *testing.T) {
		if got := NormalizeToolCallingContent("text", raw); got != "text" {
			t.Errorf("= %#v, want %q", got, "text")
		}
	})

	t.Run("replaces first text block and fills args", func(t *testing.T) {
		got, ok := NormalizeToolCallingContent(blocks, raw).([]u.H)
		if !ok {
			t.Fatalf("result type = %T, want []u.H", got)
		}
		if len(got) != len(blocks) {
			t.Fatalf("len = %d, want %d", len(got), len(blocks))
		}
		if text := got[0]["content"].(u.H)["text"]; text != wantText {
			t.Errorf("text block = %v, want %q", text, wantText)
		}
		if !reflect.DeepEqual(got[1]["args"], raw) {
			t.Errorf("args = %#v, want %#v", got[1]["args"], raw)
		}
		// 入参不能被就地修改（广播与落库共用同一份 blocks）。
		if orig := blocks[0]["content"].(u.H)["text"]; orig != "old preview" {
			t.Errorf("input mutated: %v", orig)
		}
		if blocks[1]["args"] != "stale" {
			t.Errorf("input mutated: %v", blocks[1]["args"])
		}
	})

	t.Run("only first text block replaced", func(t *testing.T) {
		multi := []u.H{
			{"type": "content", "content": u.H{"type": "text", "text": "first"}},
			{"type": "content", "content": u.H{"type": "text", "text": "second"}},
		}
		got, ok := NormalizeToolCallingContent(multi, raw).([]u.H)
		if !ok || len(got) != 2 {
			t.Fatalf("result = %#v, want 2 blocks", got)
		}
		if text := got[0]["content"].(u.H)["text"]; text != wantText {
			t.Errorf("first text block = %v, want %q", text, wantText)
		}
		if text := got[1]["content"].(u.H)["text"]; text != "second" {
			t.Errorf("second text block = %v, want %q", text, "second")
		}
	})

	t.Run("prepends text block when missing", func(t *testing.T) {
		only := []u.H{{"type": "diff", "path": "a.go"}}
		got, ok := NormalizeToolCallingContent(only, raw).([]u.H)
		if !ok || len(got) != 2 {
			t.Fatalf("result = %#v, want 2 blocks", got)
		}
		if got[0]["type"] != "content" {
			t.Errorf("prepended block type = %v, want content", got[0]["type"])
		}
		if text := got[0]["content"].(u.H)["text"]; text != wantText {
			t.Errorf("prepended text = %v, want %q", text, wantText)
		}
		if got[1]["type"] != "diff" {
			t.Errorf("original block lost: %#v", got[1])
		}
	})

	t.Run("empty render keeps block count", func(t *testing.T) {
		emptyParams := map[string]any{}
		only := []u.H{{"type": ToolCallingInfoType}}
		got, ok := NormalizeToolCallingContent(only, emptyParams).([]u.H)
		if !ok || len(got) != 1 {
			t.Fatalf("result = %#v, want single block", got)
		}
		if !reflect.DeepEqual(got[0]["args"], emptyParams) {
			t.Errorf("args = %#v, want %#v", got[0]["args"], emptyParams)
		}
	})
}

// TestBuildToolCallingContent 重建历史回放 content：有参数时文本块 + calling_info，无参数时只有 calling_info。
func TestBuildToolCallingContent(t *testing.T) {
	params := map[string]any{"command": "ls"}
	content := BuildToolCallingContent("run", 42, params)
	if len(content) != 2 {
		t.Fatalf("len = %d, want 2 blocks: %#v", len(content), content)
	}
	if content[0]["type"] != "content" {
		t.Errorf("first block type = %v, want content", content[0]["type"])
	}
	if text := content[0]["content"].(u.H)["text"]; text != "Command: ls\n" {
		t.Errorf("text = %v, want %q", text, "Command: ls\n")
	}
	info := content[1]
	if info["type"] != ToolCallingInfoType || info["name"] != "run" || info["messageID"] != uint64(42) {
		t.Errorf("calling_info block = %#v", info)
	}
	if !reflect.DeepEqual(info["args"], params) {
		t.Errorf("args = %#v, want %#v", info["args"], params)
	}

	// 参数为 nil：只有 calling_info，且 args 是空对象而不是 nil。
	empty := BuildToolCallingContent("tree", 7, nil)
	if len(empty) != 1 {
		t.Fatalf("len = %d, want 1 block: %#v", len(empty), empty)
	}
	args, ok := empty[0]["args"].(map[string]any)
	if !ok || len(args) != 0 {
		t.Errorf("args = %#v, want empty map[string]any", empty[0]["args"])
	}
	if empty[0]["messageID"] != uint64(7) {
		t.Errorf("messageID = %v, want 7", empty[0]["messageID"])
	}
}
