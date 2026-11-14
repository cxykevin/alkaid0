package phrase

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
)

// TestAllNeverNil 短语表缺失 / 被写成 null / 未启用时，All 都必须返回空切片，
// 否则调用方（如 alk.cxykevin.top/phrases/list）会把它序列化成 null。
func TestAllNeverNil(t *testing.T) {
	cases := []struct {
		name    string
		enable  bool
		phrases []cfgStructs.Phrase
	}{
		{"disabled", false, nil},
		{"enabled but nil table", true, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			restore := setTestConfig(t, c.enable, c.phrases)
			defer restore()

			got := All()
			if got == nil {
				t.Fatal("All() should return an empty slice, not nil")
			}
			raw, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("marshal All(): %v", err)
			}
			if string(raw) != "[]" {
				t.Errorf("marshalled All() = %s, want []", raw)
			}
		})
	}
}

// setTestConfig 替换全局配置为指定短语配置（挂载在 Context 块下），返回恢复函数。
func setTestConfig(t *testing.T, enable bool, phrases []cfgStructs.Phrase) func() {
	t.Helper()
	return config.GlobalConfigSwap(cfgStructs.Config{
		Context: cfgStructs.ContextConfig{
			Phrase: cfgStructs.PhraseConfig{
				Enable:  enable,
				Phrases: phrases,
			},
		},
	})
}

func TestAllAndLookup(t *testing.T) {
	restore := setTestConfig(t, true, []cfgStructs.Phrase{
		{Short: "hi", Text: "你好，请介绍一下你自己", Desc: "greeting"},
		{Short: "gen", Text: "请生成一段代码"},
	})
	defer restore()

	if got := len(All()); got != 2 {
		t.Fatalf("All() = %d, want 2", got)
	}

	p, ok := Lookup("hi")
	if !ok || p.Text != "你好，请介绍一下你自己" || p.Desc != "greeting" {
		t.Fatalf("Lookup(hi) = %+v, %v", p, ok)
	}
	if _, ok := Lookup("nope"); ok {
		t.Fatal("Lookup(nope) should not exist")
	}
	// 短键两侧空白应被忽略
	if _, ok := Lookup("  gen "); !ok {
		t.Fatal("Lookup(gen with spaces) should exist")
	}
}

func TestDisabled(t *testing.T) {
	restore := setTestConfig(t, false, []cfgStructs.Phrase{
		{Short: "hi", Text: "hello"},
	})
	defer restore()

	if len(All()) != 0 {
		t.Fatal("disabled should return no phrases")
	}
	if _, ok := Lookup("hi"); ok {
		t.Fatal("disabled should not lookup")
	}
}

func TestListText(t *testing.T) {
	restore := setTestConfig(t, true, []cfgStructs.Phrase{
		{Short: "hi", Text: "你好", Desc: "问候"},
	})
	defer restore()

	s := ListText()
	for _, want := range []string{"hi", "问候", "你好"} {
		if !strings.Contains(s, want) {
			t.Fatalf("ListText missing %q: %q", want, s)
		}
	}

	// 无短语时给出引导提示
	restore2 := setTestConfig(t, true, nil)
	defer restore2()
	if !strings.Contains(ListText(), "No phrases configured") {
		t.Fatalf("ListText empty case: %q", ListText())
	}
}
