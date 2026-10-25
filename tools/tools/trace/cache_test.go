package trace

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/storage/structs"
)

func TestDecideDiffPlan(t *testing.T) {
	// 首次跟踪（oldContent 空）→ 方案1
	if _, keep := decideDiffPlan("a.txt", "", "new", false, 0.2); keep {
		t.Error("first trace should be 方案1 (keep=false)")
	}
	// 无变化 → 方案1
	if _, keep := decideDiffPlan("a.txt", "same\n", "same\n", false, 0.2); keep {
		t.Error("no change should be 方案1")
	}
	// 超时 → 方案1
	if _, keep := decideDiffPlan("a.txt", "old\n", "new\n", true, 0.2); keep {
		t.Error("timeout should be 方案1")
	}
	// 内容太短：统一 diff 的固定开销（文件头 + @@ + 上下文行）就会超过 2× 比例上限 → 方案1
	if _, keep := decideDiffPlan("@temp/x", "old", "new", false, 0.2); keep {
		t.Error("短内容 + 固定 diff 开销应走方案1（比例上限）")
	}
	// 大改动（diff 超过 2× 安全阀）→ 方案1
	var newBig strings.Builder
	for range 20 {
		newBig.WriteString("line\n")
	}
	if _, keep := decideDiffPlan("a.txt", "one\n", newBig.String(), false, 0.2); keep {
		t.Error("diff exceeding the 2x cap should force 方案1")
	}
	// 比例上限的另一侧：内容够大时同一比例的小改动 diff 占比很低 → 保留
	var bigOld strings.Builder
	for i := range 40 {
		fmt.Fprintf(&bigOld, "long content line %02d with plenty of text\n", i)
	}
	if _, keep := decideDiffPlan("a.txt", bigOld.String(), bigOld.String()+"one more line\n", false, 0.2); !keep {
		t.Error("长内容 + 小增量应保留（diff 占比低）")
	}

	// 小改动 → 方案2
	var old, new strings.Builder
	for i := range 30 {
		old.WriteString("line ")
		old.WriteString(string(rune('A' + i%26)))
		old.WriteString("\n")
	}
	lines := strings.Split(strings.TrimSuffix(old.String(), "\n"), "\n")
	lines[15] = "line CHANGED"
	new.WriteString(strings.Join(lines, "\n"))
	new.WriteString("\n")

	plan, keep := decideDiffPlan("a.txt", old.String(), new.String(), false, 0.2)
	if !keep {
		t.Fatal("small change should be 方案2 (keep=true)")
	}
	if !plan.Keep || plan.OldBlock.Text == "" || plan.DiffBlock.Text == "" {
		t.Error("plan should carry old block and diff block")
	}
	if plan.DiffBlock.Type != "diff" {
		t.Errorf("diff block type = %q, want diff", plan.DiffBlock.Type)
	}
	if !strings.Contains(plan.OldBlock.Text, "line") {
		t.Errorf("old block should contain old content: %q", plan.OldBlock.Text)
	}
	if !strings.Contains(plan.DiffBlock.Text, "+++ b/a.txt") {
		t.Errorf("diff block should be unified diff: %q", plan.DiffBlock.Text)
	}
}

func TestKeepDiffPlan(t *testing.T) {
	// 后缀为 0、diff 比整个新块还贵：搬家只付新块，明显更便宜 → 方案1
	plan := DiffPlan{Keep: true, oTok: 100, nTok: 100, dTok: 400, mult: 0.6}
	if KeepDiffPlan(plan, 0) {
		t.Error("diff 比新块还贵时应退化为方案1")
	}
	// 同一份 diff，但旧锚点之后还有很长的历史：搬家要把整段后缀全价重算 → 保留
	if !KeepDiffPlan(plan, 5000) {
		t.Error("长后缀下应保留方案2（搬家的连锁重算成本远大于 diff）")
	}
	// 本项目实际缓存价（CachePriceMultiplier=0.02）：小后缀也倾向保留
	if !KeepDiffPlan(DiffPlan{Keep: true, nTok: 100, dTok: 120, mult: 0.02}, 300) {
		t.Error("缓存价很低时应保留方案2")
	}
	// Keep=false 直接返回 false
	if KeepDiffPlan(DiffPlan{Keep: false}, 1000) {
		t.Error("Keep=false should stay 方案1")
	}
}

func TestCountInjectedTokensFrom(t *testing.T) {
	session := &structs.Chats{TemporyDataOfSession: map[string]any{
		"tools:trace": traceCache{"": {
			{ChatID: 1, Path: "a.txt", AnchorMsgID: 10, LastContent: strings.Repeat("x", 400)},
			{ChatID: 1, Path: "b.txt", AnchorMsgID: 30, LastContent: strings.Repeat("y", 400)},
			{ChatID: 1, Path: "c.txt", AnchorMsgID: 40},
		}},
	}}
	// 两个有内容的块都在 from 之后
	if got := CountInjectedTokensFrom(session, 5); got < 200 {
		t.Errorf("应统计 from 之后的全部内容块: got %d", got)
	}
	// 只算 b.txt
	got := CountInjectedTokensFrom(session, 20)
	if got < 100 || got >= 200 {
		t.Errorf("只应统计锚点晚于 from 的块: got %d", got)
	}
	if got := CountInjectedTokensFrom(nil, 0); got != 0 {
		t.Errorf("nil session 必须返回 0: got %d", got)
	}
}
