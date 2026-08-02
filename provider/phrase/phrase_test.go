package phrase

import (
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
)

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
