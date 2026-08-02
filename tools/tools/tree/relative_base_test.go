package tree

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestSolveCall_RelativeBase 回归：SolveCall 的 base 是相对路径、
// 而 origin 树由 BuildTree 以绝对路径构建时，不应产生路径拼接错误。
//
// 曾经的 bug：cloneRelNode 里 filepath.Rel(相对base, 绝对path) 返回错误，
// 节点 Path 保留绝对路径 → mapOrigin 里 copy 源为绝对路径 →
// solveDiffTask 的 filepath.Join(根, 绝对路径) 拼出错误路径，
// 报 `stat /root/mnt/.../a.txt: no such file or directory`。
func TestSolveCall_RelativeBase(t *testing.T) {
	parent := t.TempDir()
	testDir := filepath.Join(parent, "work")
	if err := os.Mkdir(testDir, 0755); err != nil {
		t.Fatalf("mkdir work: %v", err)
	}
	os.WriteFile(filepath.Join(testDir, "a.txt"), []byte("a"), 0644)

	// 把进程 cwd 切到 parent，使 base="work" 成为相对路径（模拟相对 cwd）
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(parent); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer os.Chdir(orig)

	// origin 树以绝对路径构建（等价 buildGlobalPrompt 的 filepath.Abs 行为）
	treeID := int32(0)
	tree, _ := BuildTree(testDir, &treeID, 0)
	tree.Name = "(root)"
	treeStr := BuildString(tree)

	// base 为相对路径（与 writeTree 修复前 filepath.Join(Root, activate) 的行为一致）
	base := "work"
	targetStr := treeStr + "\n    - b.txt `2`"

	_, err = SolveCall(base, tree, targetStr)
	if err != nil {
		t.Fatalf("SolveCall with relative base: %v", err)
	}
	// b.txt 应创建在 testDir 下（相对 base 解析到的真实目录）
	if _, err := os.Stat(filepath.Join(testDir, "b.txt")); err != nil {
		t.Errorf("b.txt should be created in %s: %v", testDir, err)
	}
	// 已有文件内容不应被破坏
	if content, _ := os.ReadFile(filepath.Join(testDir, "a.txt")); string(content) != "a" {
		t.Errorf("a.txt content changed: %q", content)
	}
}

// TestSolveCall_RelativeBase_Delete 回归：相对 base 下用 @ln 删除文件
// 同样不应出现路径拼接错误。
func TestSolveCall_RelativeBase_Delete(t *testing.T) {
	parent := t.TempDir()
	testDir := filepath.Join(parent, "work")
	if err := os.Mkdir(testDir, 0755); err != nil {
		t.Fatalf("mkdir work: %v", err)
	}
	os.WriteFile(filepath.Join(testDir, "a.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(testDir, "b.txt"), []byte("b"), 0644)

	orig, _ := os.Getwd()
	os.Chdir(parent)
	defer os.Chdir(orig)

	treeID := int32(0)
	tree, _ := BuildTree(testDir, &treeID, 0)
	tree.Name = "(root)"
	treeStr := BuildString(tree)

	// 用 @ln 删除 b.txt 所在行
	lines := strings.Split(treeStr, "\n")
	target := ""
	lineNo := 0
	for i, ln := range lines {
		if strings.Contains(ln, "b.txt") && strings.HasPrefix(strings.TrimSpace(ln), "- ") {
			lineNo = i
			target = "@ln:" + strconv.Itoa(i+1)
			break
		}
	}
	if target == "" {
		t.Fatalf("test setup: b.txt line not found in tree:\n%s", treeStr)
	}
	// 从树字符串里移除该行
	lines = append(lines[:lineNo], lines[lineNo+1:]...)
	targetStr := strings.Join(lines, "\n")

	_, err := SolveCall("work", tree, targetStr)
	if err != nil {
		t.Fatalf("SolveCall delete with relative base: %v", err)
	}
	if _, err := os.Stat(filepath.Join(testDir, "b.txt")); !os.IsNotExist(err) {
		t.Errorf("b.txt should be deleted, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(testDir, "a.txt")); err != nil {
		t.Errorf("a.txt should remain: %v", err)
	}
}
