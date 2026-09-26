package trace

import (
	"fmt"
	"strings"
	"testing"
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
	// betweenTok=0、mult=0.6、dTok 接近 oTok：cost2=0.6*100+95=155 > 1.5*100=150 → 方案1
	plan := DiffPlan{Keep: true, oTok: 100, nTok: 100, dTok: 95, mult: 0.6}
	if KeepDiffPlan(plan, 0) {
		t.Error("betweenTok=0 with high mult should degrade to 方案1")
	}
	// betweenTok 很大：方案1 连锁重算成本暴涨，方案2 更划算
	if !KeepDiffPlan(plan, 1000) {
		t.Error("large betweenTok should favor 方案2")
	}
	// Keep=false 直接返回 false
	if KeepDiffPlan(DiffPlan{Keep: false}, 1000) {
		t.Error("Keep=false should stay 方案1")
	}
}
