package u

import (
	"fmt"
	"strings"
)

// UnifiedDiff 对 old/new 文本按行生成 unified diff（Git 风格 ---/+++ 头 + @@ hunk @@）。
// path 为相对路径，用于 diff 头。old 与 new 相同时返回空字符串。
//
// 实现为 LCS 行级 diff：先裁剪公共前缀/后缀，再对中间差异段做 LCS 回溯；
// 差异段过大（old×new 超过阈值）时降级为「全删+全插」，避免 O(n*m) 内存/耗时失控。
func UnifiedDiff(oldText, newText, path string) string {
	if oldText == newText {
		return ""
	}
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)

	prefix, suffix := commonTrim(oldLines, newLines)
	oldMid := oldLines[prefix : len(oldLines)-suffix]
	newMid := newLines[prefix : len(newLines)-suffix]

	var ops []diffOp
	if len(oldMid)*len(newMid) <= 1_000_000 {
		ops = lcsDiffOps(oldMid, newMid)
	} else {
		for _, line := range oldMid {
			ops = append(ops, diffOp{kind: opDel, line: line})
		}
		for _, line := range newMid {
			ops = append(ops, diffOp{kind: opIns, line: line})
		}
	}

	var b strings.Builder
	b.WriteString("--- a/" + path + "\n")
	b.WriteString("+++ b/" + path + "\n")
	b.WriteString(renderHunk(oldLines, newLines, prefix, suffix, ops))
	return b.String()
}

// splitLines 按 \n 分割文本为行（去末尾换行），空文本返回 0 行。
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// commonTrim 返回 old/new 行的公共前缀行数与公共后缀行数（用于缩小 LCS 规模）。
func commonTrim(a, b []string) (prefix, suffix int) {
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	for suffix < len(a)-prefix && suffix < len(b)-prefix &&
		a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}
	return prefix, suffix
}

type diffOpKind int

const (
	opEqual diffOpKind = iota
	opDel
	opIns
)

type diffOp struct {
	kind diffOpKind
	line string
}

// lcsDiffOps 对 a、b 做 LCS 回溯，返回从 a 变换到 b 的操作序列（equal/delete/insert）。
func lcsDiffOps(a, b []string) []diffOp {
	n, m := len(a), len(b)
	// l 存 LCS 长度，dir 存回溯方向：0=diag(equal)、1=up(删除 a[i-1])、2=left(插入 b[j-1])。
	l := make([][]int32, n+1)
	dir := make([][]int16, n+1)
	for i := range l {
		l[i] = make([]int32, m+1)
		dir[i] = make([]int16, m+1)
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if a[i-1] == b[j-1] {
				l[i][j] = l[i-1][j-1] + 1
				dir[i][j] = 0
			} else if l[i-1][j] >= l[i][j-1] {
				l[i][j] = l[i-1][j]
				dir[i][j] = 1
			} else {
				l[i][j] = l[i][j-1]
				dir[i][j] = 2
			}
		}
	}

	var ops []diffOp
	i, j := n, m
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && dir[i][j] == 0:
			ops = append(ops, diffOp{kind: opEqual, line: a[i-1]})
			i--
			j--
		case i > 0 && (j == 0 || dir[i][j] == 1):
			ops = append(ops, diffOp{kind: opDel, line: a[i-1]})
			i--
		default:
			ops = append(ops, diffOp{kind: opIns, line: b[j-1]})
			j--
		}
	}
	for x, y := 0, len(ops)-1; x < y; x, y = x+1, y-1 {
		ops[x], ops[y] = ops[y], ops[x]
	}
	return ops
}

// renderHunk 生成单个 unified diff hunk（3 行上下文）。上下文行取 oldLines（公共区域两版本一致）。
func renderHunk(oldLines, newLines []string, prefix, suffix int, ops []diffOp) string {
	const ctx = 3

	type signedLine struct {
		sign byte
		text string
	}
	var lines []signedLine

	start := max(prefix-ctx, 0)
	// 前导上下文
	for k := start; k < prefix; k++ {
		lines = append(lines, signedLine{' ', oldLines[k]})
	}
	// 中间差异
	for _, op := range ops {
		switch op.kind {
		case opEqual:
			lines = append(lines, signedLine{' ', op.line})
		case opDel:
			lines = append(lines, signedLine{'-', op.line})
		case opIns:
			lines = append(lines, signedLine{'+', op.line})
		}
	}
	// 后缀上下文
	end := min(len(oldLines)-suffix+ctx, len(oldLines))
	for k := len(oldLines) - suffix; k < end; k++ {
		lines = append(lines, signedLine{' ', oldLines[k]})
	}

	oldCount, newCount := 0, 0
	for _, l := range lines {
		if l.sign != '+' {
			oldCount++
		}
		if l.sign != '-' {
			newCount++
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", start+1, oldCount, start+1, newCount)
	for _, l := range lines {
		b.WriteByte(l.sign)
		b.WriteString(l.text)
		b.WriteByte('\n')
	}
	return b.String()
}
