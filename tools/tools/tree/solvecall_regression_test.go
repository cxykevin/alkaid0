package tree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSolveCall_AppendNewFile 回归：模型向 @tree 追加新文件时，
// 未修改的已有文件不应被误判为复制。
//
// 曾经的 bug：diffNode 中所有 keep 节点都被 generateDiffNodes 当作 CreateFile，
// 其 ID 在 origin 的 idMap 中存在 → generateDiff 把它们当成"复制"，
// 且 copy 源是 BuildTree 产生的绝对路径，solveDiffTask 里
// filepath.Join(根目录, 绝对路径) 会拼出错误路径，导致 os.Stat 失败，
// 最终整个 @tree 编辑报 "error in act (stat ...: no such file or directory)"。
func TestSolveCall_AppendNewFile(t *testing.T) {
	testDir := t.TempDir()
	os.Mkdir(filepath.Join(testDir, "dir1"), 0755)
	os.WriteFile(filepath.Join(testDir, "dir1", "file1.txt"), []byte("file1"), 0644)
	os.WriteFile(filepath.Join(testDir, "root_file.txt"), []byte("root"), 0644)

	treeID := int32(0)
	tree, _ := BuildTree(testDir, &treeID, 0)
	tree.Name = "(root)"
	treeStr := BuildString(tree)

	// 追加新文件（ID=3，不与现有 ID 冲突）
	targetStr := treeStr + "\n    - c.txt `3`"

	diff, err := SolveCall(testDir, tree, targetStr)
	if err != nil {
		t.Fatalf("SolveCall: %v", err)
	}

	// diff 只应包含新文件的创建，已有文件不应出现在任何 Copy/Move 操作里
	for _, d := range diff {
		if strings.Contains(d.Origin, "file1.txt") || strings.Contains(d.Origin, "root_file.txt") ||
			strings.Contains(d.Target, "file1.txt") || strings.Contains(d.Target, "root_file.txt") {
			t.Errorf("keep file should not appear in diff: %+v", d)
		}
	}

	// 文件系统状态：新文件已创建、已有文件内容未被破坏
	if _, err := os.Stat(filepath.Join(testDir, "c.txt")); err != nil {
		t.Errorf("c.txt should be created: %v", err)
	}
	if content, _ := os.ReadFile(filepath.Join(testDir, "dir1", "file1.txt")); string(content) != "file1" {
		t.Errorf("file1.txt content changed: %q", content)
	}
	if content, _ := os.ReadFile(filepath.Join(testDir, "root_file.txt")); string(content) != "root" {
		t.Errorf("root_file.txt content changed: %q", content)
	}
}

// TestSolveCall_AppendFileIDReuse 追加的新文件 ID 与已有文件相同也不会被误判为复制。
// 之前的实现里，新文件 ID 命中 origin 中已有文件时会被当作"从该文件复制"，
// 进而对源文件做绝对路径拼接 → stat 失败。
func TestSolveCall_AppendFileIDReuse(t *testing.T) {
	testDir := t.TempDir()
	os.WriteFile(filepath.Join(testDir, "a.txt"), []byte("a"), 0644)

	treeID := int32(0)
	tree, _ := BuildTree(testDir, &treeID, 0)
	tree.Name = "(root)"
	treeStr := BuildString(tree)

	// 新文件 b.txt 复用 ID=1（a.txt 已占用）
	targetStr := treeStr + "\n    - b.txt `1`"

	_, err := SolveCall(testDir, tree, targetStr)
	if err != nil {
		t.Fatalf("SolveCall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(testDir, "b.txt")); err != nil {
		t.Errorf("b.txt should be created: %v", err)
	}
}

// TestSolveCall_MoveFile 移动文件（同一 ID 出现在不同目录）仍应正常工作：
// 生成 Copy+Delete+Move，而不是被 keep 跳过。
func TestSolveCall_MoveFile(t *testing.T) {
	testDir := t.TempDir()
	os.Mkdir(filepath.Join(testDir, "dir1"), 0755)
	os.WriteFile(filepath.Join(testDir, "root_file.txt"), []byte("root"), 0644)

	treeID := int32(0)
	tree, _ := BuildTree(testDir, &treeID, 0)
	tree.Name = "(root)"
	treeStr := BuildString(tree)

	// 把 root_file.txt 从根目录移动到 dir1 下（保持 ID 不变）。
	// dir1 在树字符串里缩进为 1 级（4 空格），root_file 移到其下需缩进 2 级（8 空格）。
	targetStr := indentTreeFile(treeStr, "root_file.txt")
	if targetStr == treeStr {
		t.Fatalf("test setup: target tree unchanged")
	}

	if _, err := SolveCall(testDir, tree, targetStr); err != nil {
		t.Fatalf("SolveCall: %v", err)
	}

	// 根目录不应再有该文件，dir1 下应有
	if _, err := os.Stat(filepath.Join(testDir, "root_file.txt")); !os.IsNotExist(err) {
		t.Errorf("root_file.txt should be moved out of root, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(testDir, "dir1", "root_file.txt")); err != nil {
		t.Errorf("root_file.txt should be moved into dir1: %v", err)
	}
}

// indentTreeFile 在树字符串里把指定文件行的缩进增加一级（4 空格），模拟把该文件移入上层目录下的子目录。
func indentTreeFile(treeStr, name string) string {
	lines := strings.Split(treeStr, "\n")
	for i, ln := range lines {
		if strings.Contains(ln, name) && strings.HasPrefix(strings.TrimSpace(ln), "- ") {
			lines[i] = "    " + ln
			break
		}
	}
	return strings.Join(lines, "\n")
}
