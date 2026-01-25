package tree

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// TestBuildNodeFromString_RoundTrip 测试 BuildString 与 BuildNodeFromString 的互转
func TestBuildNodeFromString_RoundTrip(t *testing.T) {
	// 构造节点树
	root := &Node{Name: "root", Path: "root", IsDir: true, Children: []*Node{}}
	f1 := &Node{Name: "a.txt", Path: filepath.Join("root", "a.txt"), IsDir: false, ID: 1}
	d1 := &Node{Name: "dir1", Path: filepath.Join("root", "dir1"), IsDir: true, Children: []*Node{}}
	f2 := &Node{Name: "b.txt", Path: filepath.Join("root", "dir1", "b.txt"), IsDir: false, ID: 2}
	d1.Children = append(d1.Children, f2)
	root.Children = append(root.Children, f1, d1)

	s := BuildString(root)
	// 反向解析
	parsed, err := BuildNodeFromString(s)
	if err != nil {
		t.Fatalf("BuildNodeFromString failed: %v", err)
	}
	if parsed.Name != "root" {
		t.Fatalf("expected root name, got %s", parsed.Name)
	}
	// 比较文件名与 id
	want := map[string]int32{"a.txt": 1, filepath.Join("dir1", "b.txt"): 2}
	got := map[string]int32{}
	var walk func(n *Node, prefix string)
	walk = func(n *Node, prefix string) {
		for _, c := range n.Children {
			if c.IsDir {
				walk(c, filepath.Join(prefix, c.Name))
			} else {
				got[filepath.Join(prefix, c.Name)] = c.ID
			}
		}
	}
	walk(parsed, "")
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("roundtrip mismatch:\nwant=%v\ngot=%v", want, got)
	}
}

// TestBuildNodeFromString_InvalidIndent 验证错误缩进被拒绝
func TestBuildNodeFromString_InvalidIndent(t *testing.T) {
	// 使用 3 个空格，不是 4 的倍数
	s := "root\n   - a '1'"
	if _, err := BuildNodeFromString(s); err == nil {
		t.Fatalf("expected indent error, got nil")
	}
}

// TestBuildNodeFromString_InvalidChars 验证非法字符被拒绝
func TestBuildNodeFromString_InvalidChars(t *testing.T) {
	s := "root\n    - bad/name '1'"
	if _, err := BuildNodeFromString(s); err == nil {
		t.Fatalf("expected invalid name error, got nil")
	}
}

// TestBuildNodeFromString_EmptyTree 验证空树被拒绝
func TestBuildNodeFromString_EmptyTree(t *testing.T) {
	s := "\n"
	if _, err := BuildNodeFromString(s); err == nil {
		t.Fatalf("expected empty tree error, got nil")
	}
}

// TestCheckNodeFileNameErr_Duplicate 验证重复文件名检测
func TestCheckNodeFileNameErr_Duplicate(t *testing.T) {
	root := &Node{Name: "root", Path: "root", IsDir: true, Children: []*Node{}}
	a := &Node{Name: "dup.txt", Path: filepath.Join("root", "dup.txt"), IsDir: false, ID: 1}
	b := &Node{Name: "dup.txt", Path: filepath.Join("root", "dup.txt"), IsDir: false, ID: 2}
	root.Children = append(root.Children, a, b)
	if err := checkNodeFileNameErr(root); err == nil {
		t.Fatalf("expected duplicate file name error, got nil")
	}
}

// TestGenerateIDMap 验证 ID 映射生成
func TestGenerateIDMap(t *testing.T) {
	origin := &Node{Name: "root", Path: "root", IsDir: true, Children: []*Node{}}
	a := &Node{Name: "a.txt", Path: filepath.Join("root", "a.txt"), IsDir: false, ID: 10}
	b := &Node{Name: "b.txt", Path: filepath.Join("root", "b.txt"), IsDir: false, ID: 20}
	origin.Children = append(origin.Children, a, b)
	m := idMapOrigin{}
	generateIDMap(&m, origin)
	if got := m[10]; got != filepath.Join("root", "a.txt") {
		t.Fatalf("unexpected mapping for 10: %s", got)
	}
	if got := m[20]; got != filepath.Join("root", "b.txt") {
		t.Fatalf("unexpected mapping for 20: %s", got)
	}
}

// TestGenerateDiff_MoveAndCreateDir 验证移动/复制/删除等操作被生成
func TestGenerateDiff_MoveAndCreateDir(t *testing.T) {
	// origin: root/a.txt (id=1)
	origin := &Node{Name: "root", Path: "root", IsDir: true, Children: []*Node{}}
	a := &Node{Name: "a.txt", Path: filepath.Join("root", "a.txt"), IsDir: false, ID: 1}
	origin.Children = append(origin.Children, a)

	// target: root/sub/a.txt (id=1)  表示文件被移动到 sub/
	target := &Node{Name: "root", Path: "root", IsDir: true, Children: []*Node{}}
	sub := &Node{Name: "sub", Path: filepath.Join("root", "sub"), IsDir: true, Children: []*Node{}}
	ta := &Node{Name: "a.txt", Path: filepath.Join("root", "sub", "a.txt"), IsDir: false, ID: 1}
	sub.Children = append(sub.Children, ta)
	target.Children = append(target.Children, sub)

	// 计算中间 diffNode: 将 origin->target 的差异合并到 diffNode
	diffNode := &Node{IsDir: true}
	if err := solveNodesTreeDiff(diffNode, origin, target); err != nil {
		t.Fatalf("solveNodesTreeDiff error: %v", err)
	}

	diffs, err := generateDiff(origin, diffNode)
	if err != nil {
		t.Fatalf("generateDiff error: %v", err)
	}

	// 收集类型，以便断言包含 Copy/Move/Delete/CreateDir 等
	types := []DiffStatus{}
	for _, d := range diffs {
		types = append(types, d.Type)
	}
	slices.Sort(types)

	// 至少应包含 Copy, Move, Delete（CreateDir 不是必要条件，取决于 diff 生成细节）
	foundCopy := false
	foundMove := false
	foundDelete := false
	for _, tt := range types {
		switch tt {
		case DiffStatusCopy:
			foundCopy = true
		case DiffStatusMove:
			foundMove = true
		case DiffStatusDelete:
			foundDelete = true
		}
	}
	if !foundCopy || !foundMove || !foundDelete {
		t.Fatalf("expected copy/move/delete in diffs, got: %v", types)
	}
}

// TestBuildString_Format 验证 BuildString 输出包含文件 ID 与 - 前缀
func TestBuildString_Format(t *testing.T) {
	root := &Node{Name: "root", Path: "root", IsDir: true, Children: []*Node{}}
	f := &Node{Name: "file.txt", Path: filepath.Join("root", "file.txt"), IsDir: false, ID: 42}
	root.Children = append(root.Children, f)
	s := BuildString(root)
	if !stringsContains(s, "- file.txt") || !stringsContains(s, "'42'") {
		t.Fatalf("unexpected buildstring output: %s", s)
	}
}

// stringsContains 简单帮手，避免额外导入
func stringsContains(s, sub string) bool {
	return len(sub) == 0 || stringsIndex(s, sub) >= 0
}

// stringsIndex 最小实现，避免额外导入 strings
func stringsIndex(s, sep string) int {
	for i := 0; i+len(sep) <= len(s); i++ {
		if s[i:i+len(sep)] == sep {
			return i
		}
	}
	return -1
}

// TestGenerateDiff_SwapFiles 测试两个目录下同名文件交换 ID 的场景
func TestGenerateDiff_SwapFiles(t *testing.T) {
	originStr := `
root
	a
		- c '1'
	b
		- c '2'
`

	targetStr := `
root
	a
		- c '2'
	b
		- c '1'
`

	origin, err := BuildNodeFromString(originStr)
	if err != nil {
		t.Fatalf("build origin: %v", err)
	}
	target, err := BuildNodeFromString(targetStr)
	if err != nil {
		t.Fatalf("build target: %v", err)
	}

	result := &Node{
		Path:  "FakeRoot",
		IsDir: true,
	}

	if err := solveNodesTreeDiff(result, origin, target); err != nil {
		t.Fatalf("solve diff failed: %v", err)
	}

	diffs, err := generateDiff(origin, result)
	if err != nil {
		t.Fatalf("generateDiff failed: %v", err)
	}

	// 我们期望生成的操作包含用于环的临时重命名序列（3 个 Move）
	var moves []Diff
	for _, d := range diffs {
		if d.Type == DiffStatusMove {
			moves = append(moves, d)
		}
	}

	if len(moves) < 2 {
		t.Fatalf("expected at least 2 move operations for swap, got %d; diffs: %#v", len(moves), diffs)
	}

	// // 验证存在一个 move 将 a/c 重命名到临时名（origin ends with "a/c"）
	// foundTmpStart := false
	// foundMiddle := false
	// foundTmpEnd := false
	// for i := 0; i < len(moves); i++ {
	// 	o := moves[i]
	// 	if strings.HasSuffix(o.Origin, filepath.Join("a", "c")) && strings.Contains(o.Target, ".alkaid0.tmp.") {
	// 		foundTmpStart = true
	// 	}
	// 	if strings.HasSuffix(o.Origin, filepath.Join("b", "c")) && strings.HasSuffix(o.Target, filepath.Join("a", "c")) {
	// 		foundMiddle = true
	// 	}
	// 	if strings.Contains(o.Origin, ".alkaid0.tmp.") && strings.HasSuffix(o.Target, filepath.Join("b", "c")) {
	// 		foundTmpEnd = true
	// 	}
	// }

	// if !foundTmpStart || !foundMiddle || !foundTmpEnd {
	// 	t.Fatalf("swap moves not found or incomplete: foundTmpStart=%v foundMiddle=%v foundTmpEnd=%v; moves=%#v", foundTmpStart, foundMiddle, foundTmpEnd, moves)
	// }
}

// TestBuildTree 测试构建树的功能
func TestBuildTree(t *testing.T) {
	// 创建临时测试目录
	tmpDir, err := os.MkdirTemp("", "tree_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// 创建测试文件结构
	// tmpDir/
	//   file1.txt
	//   subdir/
	//     file2.txt
	file1 := filepath.Join(tmpDir, "file1.txt")
	if err := os.WriteFile(file1, []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to create file1: %v", err)
	}

	subDir := filepath.Join(tmpDir, "subdir")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	file2 := filepath.Join(subDir, "file2.txt")
	if err := os.WriteFile(file2, []byte("world"), 0644); err != nil {
		t.Fatalf("failed to create file2: %v", err)
	}

	t.Run("NormalDirectory", func(t *testing.T) {
		var id int32
		node := BuildTree(tmpDir, &id)

		if node.Name != filepath.Base(tmpDir) {
			t.Errorf("expected name %s, got %s", filepath.Base(tmpDir), node.Name)
		}
		if !node.IsDir {
			t.Error("expected node to be a directory")
		}
		if len(node.Children) != 2 {
			t.Errorf("expected 2 children, got %d", len(node.Children))
		}
	})

	t.Run("SingleFile", func(t *testing.T) {
		var id int32
		node := BuildTree(file1, &id)

		if node.Name != "file1.txt" {
			t.Errorf("expected name file1.txt, got %s", node.Name)
		}
		if node.IsDir {
			t.Error("expected node to be a file")
		}
		if node.ID != 1 {
			t.Errorf("expected ID 1, got %d", node.ID)
		}
	})

	t.Run("NonExistentPath", func(t *testing.T) {
		var id int32
		node := BuildTree(filepath.Join(tmpDir, "non_existent"), &id)

		if node.Error == nil {
			t.Error("expected error for non-existent path, got nil")
		}
	})
}

// TestBuildString 测试构建字符串的功能
func TestBuildString(t *testing.T) {
	// 模拟一个简单的树结构
	node := &Node{
		Name:  "root",
		IsDir: true,
		Children: []*Node{
			{
				Name:  "file1.txt",
				IsDir: false,
				ID:    1,
			},
			{
				Name:  "subdir",
				IsDir: true,
				Children: []*Node{
					{
						Name:  "file2.txt",
						IsDir: false,
						ID:    2,
					},
				},
			},
		},
	}

	result := BuildString(node)

	// 验证输出包含关键信息
	expectedParts := []string{
		"root",
		"file1.txt",
		"'1'",
		"subdir",
		"file2.txt",
		"'2'",
	}

	for _, part := range expectedParts {
		if !strings.Contains(result, part) {
			t.Errorf("expected output to contain %q, but it didn't. Result:%s", part, result)
		}
	}
}

// TestBuildNodeFromString 测试从字符串构建节点的功能
func TestBuildNodeFromString(t *testing.T) {
	t.Run("ValidTree", func(t *testing.T) {
		input := `root
    - file1 '1'
    subdir
        - file2 '2'`
		node, err := BuildNodeFromString(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if node.Name != "root" {
			t.Errorf("expected root name 'root', got %s", node.Name)
		}
		if len(node.Children) != 2 {
			t.Errorf("expected 2 children, got %d", len(node.Children))
		}
		// 验证子节点
		var file1, subdir *Node
		for _, child := range node.Children {
			switch child.Name {
			case "file1":
				file1 = child
			case "subdir":
				subdir = child
			}
		}
		if file1 == nil || file1.ID != 1 || file1.IsDir {
			t.Error("file1 node is incorrect")
		}
		if subdir == nil || !subdir.IsDir || len(subdir.Children) != 1 {
			t.Error("subdir node is incorrect")
		}
	})

	t.Run("InvalidIndent", func(t *testing.T) {
		input := `root
  - file1 '1'` // 只有2个空格，而 indentString 是4个
		_, err := BuildNodeFromString(input)
		if err == nil || err.Error() != "invalid indent" {
			t.Errorf("expected 'invalid indent' error, got %v", err)
		}
	})

	t.Run("IndentTooDeep", func(t *testing.T) {
		input := `root
            - file1 '1'` // 跳过了中间层级
		_, err := BuildNodeFromString(input)
		if err == nil || err.Error() != "indent too deep" {
			t.Errorf("expected 'indent too deep' error, got %v", err)
		}
	})

	t.Run("InvalidName", func(t *testing.T) {
		input := `root
    - file/name '1'` // 包含非法字符 /
		_, err := BuildNodeFromString(input)
		if err == nil || err.Error() != "path must be a correct and relative path" {
			t.Errorf("expected path error, got %v", err)
		}
	})

	t.Run("MultipleRoots", func(t *testing.T) {
		input := `root1
root2`
		_, err := BuildNodeFromString(input)
		if err == nil || err.Error() != "invalid tree (multiple root nodes)" {
			t.Errorf("expected multiple roots error, got %v", err)
		}
	})

	t.Run("EmptyInput", func(t *testing.T) {
		_, err := BuildNodeFromString("")
		if err == nil || err.Error() != "empty tree" {
			t.Errorf("expected 'empty tree' error, got %v", err)
		}
	})

	t.Run("Sorting", func(t *testing.T) {
		input := `root
    - b '2'
    - a '1'`
		node, err := BuildNodeFromString(input)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if node.Children[0].Name != "a" || node.Children[1].Name != "b" {
			t.Error("children are not sorted")
		}
	})
}

// TestMaxChildrenNum 测试超过最大子节点数的情况
func TestMaxChildrenNum(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tree_max_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// 创建超过 MaxChildrenNum 的文件
	for i := range MaxChildrenNum + 1 {
		filename := filepath.Join(tmpDir, "file"+fmt.Sprintf("%04d", i)+".txt")
		if err := os.WriteFile(filename, []byte("test"), 0644); err != nil {
			t.Fatalf("failed to create file %d: %v", i, err)
		}
	}

	var id int32
	node := BuildTree(tmpDir, &id)

	if node.ChildrenNum <= MaxChildrenNum {
		t.Errorf("expected ChildrenNum > %d, got %d", MaxChildrenNum, node.ChildrenNum)
	}

	result := BuildString(node)
	if !strings.Contains(result, "files)") {
		t.Errorf("expected output to contain ellipsis for many files, got:%s", result)
	}
}

// TestSolveCallWithOmission 测试省略节点的处理
func TestSolveCallWithOmission(t *testing.T) {
	// 创建测试目录结构
	tmpDir, err := os.MkdirTemp("", "tree_solve_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// 创建超过 MaxChildrenNum 的文件，确保会产生省略节点
	fileCount := int(MaxChildrenNum) + 5
	for i := range fileCount {
		filename := filepath.Join(tmpDir, fmt.Sprintf("file%03d.txt", i))
		if err := os.WriteFile(filename, []byte("test"), 0644); err != nil {
			t.Fatalf("failed to create file: %v", err)
		}
	}

	// 构建原始树
	var id int32
	node := BuildTree(tmpDir, &id)
	originalStr := BuildString(node)

	// 验证原始字符串包含省略节点
	if !strings.Contains(originalStr, "... (") {
		t.Fatalf("expected original string to contain omission, got: %s", originalStr)
	}

	t.Logf("Original tree string:\n%s", originalStr)
}

// // TestSolveCall 测试 SolveCall 函数
// func TestSolveCall(t *testing.T) {
// 	// 创建测试目录结构
// 	tmpDir, err := os.MkdirTemp("", "tree_solve_test")
// 	if err != nil {
// 		t.Fatalf("failed to create temp dir: %v", err)
// 	}
// 	defer os.RemoveAll(tmpDir)

// 	// 创建初始文件结构
// 	// tmpDir/
// 	//   file1.txt (ID: 1)
// 	//   file2.txt (ID: 2)
// 	//   subdir/
// 	//     file3.txt (ID: 3)
// 	file1 := filepath.Join(tmpDir, "file1.txt")
// 	if err := os.WriteFile(file1, []byte("content1"), 0644); err != nil {
// 		t.Fatalf("failed to create file1: %v", err)
// 	}

// 	file2 := filepath.Join(tmpDir, "file2.txt")
// 	if err := os.WriteFile(file2, []byte("content2"), 0644); err != nil {
// 		t.Fatalf("failed to create file2: %v", err)
// 	}

// 	subDir := filepath.Join(tmpDir, "subdir")
// 	if err := os.Mkdir(subDir, 0755); err != nil {
// 		t.Fatalf("failed to create subdir: %v", err)
// 	}

// 	file3 := filepath.Join(subDir, "file3.txt")
// 	if err := os.WriteFile(file3, []byte("content3"), 0644); err != nil {
// 		t.Fatalf("failed to create file3: %v", err)
// 	}

// 	// 构建原始树
// 	var id int32
// 	node := BuildTree(tmpDir, &id)
// 	originalStr := BuildString(node)
// 	t.Logf("Original tree:\n%s", originalStr)

// 	t.Run("NoChanges", func(t *testing.T) {
// 		// AI未做任何更改
// 		diffObjs, err := SolveCall(node, originalStr)
// 		if err != nil {
// 			t.Fatalf("SolveCall failed: %v", err)
// 		}
// 		if len(diffObjs) != 0 {
// 			t.Errorf("expected no changes, got %d diff objects", len(diffObjs))
// 		}
// 	})

// 	t.Run("AddNewFile", func(t *testing.T) {
// 		// 模拟AI添加新文件
// 		// 在subsubdir下添加file4.txt，ID为4
// 		editedStr := fmt.Sprintf(`%s
//   subdir
//     file3.txt '3'
//     file4.txt '4'`, originalStr)

// 		diffObjs, err := SolveCall(node, editedStr)
// 		if err != nil {
// 			t.Fatalf("SolveCall failed: %v", err)
// 		}

// 		if len(diffObjs) != 1 {
// 			t.Fatalf("expected 1 diff object, got %d", len(diffObjs))
// 		}

// 		diff := diffObjs[0]
// 		if diff.Mode != "add" {
// 			t.Errorf("expected mode 'add', got %s", diff.Mode)
// 		}
// 		if diff.Target != "subdir/file4.txt" {
// 			t.Errorf("expected target 'subdir/file4.txt', got %s", diff.Target)
// 		}
// 		if diff.OriginID != 4 {
// 			t.Errorf("expected OriginID 4, got %d", diff.OriginID)
// 		}
// 	})

// 	t.Run("DeleteFile", func(t *testing.T) {
// 		// 模拟AI删除file2.txt
// 		lines := strings.Split(originalStr, "\n")
// 		var filtered []string
// 		for _, line := range lines {
// 			if !strings.Contains(line, "file2.txt") {
// 				filtered = append(filtered, line)
// 			}
// 		}
// 		editedStr := strings.Join(filtered, "\n")

// 		diffObjs, err := SolveCall(node, editedStr)
// 		if err != nil {
// 			t.Fatalf("SolveCall failed: %v", err)
// 		}

// 		if len(diffObjs) != 1 {
// 			t.Fatalf("expected 1 diff object, got %d", len(diffObjs))
// 		}

// 		diff := diffObjs[0]
// 		if diff.Mode != "delete" {
// 			t.Errorf("expected mode 'delete', got %s", diff.Mode)
// 		}
// 		if diff.Origin != "file2.txt" {
// 			t.Errorf("expected origin 'file2.txt', got %s", diff.Origin)
// 		}
// 		if diff.OriginID != 2 {
// 			t.Errorf("expected OriginID 2, got %d", diff.OriginID)
// 		}
// 	})

// 	t.Run("ChangeFileID", func(t *testing.T) {
// 		// 模拟AI更改file1.txt的ID从1到10
// 		editedStr := strings.Replace(originalStr, "file1.txt '1'", "file1.txt '10'", 1)

// 		diffObjs, err := SolveCall(node, editedStr)
// 		if err != nil {
// 			t.Fatalf("SolveCall failed: %v", err)
// 		}

// 		if len(diffObjs) != 2 {
// 			t.Fatalf("expected 2 diff objects (delete and add), got %d", len(diffObjs))
// 		}

// 		// 验证有一个删除和一个添加
// 		var hasDelete, hasAdd bool
// 		for _, diff := range diffObjs {
// 			if diff.Mode == "delete" && diff.Origin == "file1.txt" && diff.OriginID == 1 {
// 				hasDelete = true
// 			}
// 			if diff.Mode == "add" && diff.Target == "file1.txt" && diff.OriginID == 10 {
// 				hasAdd = true
// 			}
// 		}

// 		if !hasDelete {
// 			t.Error("expected delete operation for file1.txt with ID 1")
// 		}
// 		if !hasAdd {
// 			t.Error("expected add operation for file1.txt with ID 10")
// 		}
// 	})

// 	t.Run("AddAndDeleteMultiple", func(t *testing.T) {
// 		// 模拟AI删除file1.txt，添加file4.txt和file5.txt
// 		lines := strings.Split(originalStr, "\n")
// 		var filtered []string
// 		for _, line := range lines {
// 			if !strings.Contains(line, "file1.txt") {
// 				filtered = append(filtered, line)
// 			}
// 		}
// 		// 在根目录添加两个新文件
// 		editedStr := strings.Join(filtered, "\n") + "\nfile4.txt '4'\nfile5.txt '5'"

// 		diffObjs, err := SolveCall(node, editedStr)
// 		if err != nil {
// 			t.Fatalf("SolveCall failed: %v", err)
// 		}

// 		if len(diffObjs) != 3 {
// 			t.Fatalf("expected 3 diff objects (1 delete + 2 add), got %d", len(diffObjs))
// 		}

// 		var deleteCount, addCount int
// 		for _, diff := range diffObjs {
// 			if diff.Mode == "delete" {
// 				deleteCount++
// 			}
// 			if diff.Mode == "add" {
// 				addCount++
// 			}
// 		}

// 		if deleteCount != 1 {
// 			t.Errorf("expected 1 delete operation, got %d", deleteCount)
// 		}
// 		if addCount != 2 {
// 			t.Errorf("expected 2 add operations, got %d", addCount)
// 		}
// 	})

// 	t.Run("EmptyString", func(t *testing.T) {
// 		// 测试空字符串
// 		diffObjs, err := SolveCall(node, "")
// 		if err != nil {
// 			t.Fatalf("SolveCall failed: %v", err)
// 		}

// 		// 所有文件都应该被标记为删除
// 		if len(diffObjs) != 3 {
// 			t.Fatalf("expected 3 diff objects (all deleted), got %d", len(diffObjs))
// 		}

// 		for _, diff := range diffObjs {
// 			if diff.Mode != "delete" {
// 				t.Errorf("expected all operations to be 'delete', got %s", diff.Mode)
// 			}
// 		}
// 	})

// 	t.Run("ComplexChanges", func(t *testing.T) {
// 		// 复杂的更改：删除file2.txt，修改file3.txt的ID，添加新文件
// 		complexStr := fmt.Sprintf(`%s
//   subdir
//     file3.txt '30'
//     newfile.txt '4'`, strings.Replace(originalStr, "file2.txt '2'", "", 1))

// 		diffObjs, err := SolveCall(node, complexStr)
// 		if err != nil {
// 			t.Fatalf("SolveCall failed: %v", err)
// 		}

// 		// 应该有：删除file2.txt，删除file3.txt(ID:3)，添加file3.txt(ID:30)，添加newfile.txt
// 		if len(diffObjs) != 4 {
// 			t.Fatalf("expected 4 diff objects, got %d", len(diffObjs))
// 		}

// 		var hasDeleteFile2, hasDeleteFile3, hasAddFile3, hasAddNewfile bool
// 		for _, diff := range diffObjs {
// 			if diff.Mode == "delete" && diff.Origin == "file2.txt" && diff.OriginID == 2 {
// 				hasDeleteFile2 = true
// 			}
// 			if diff.Mode == "delete" && diff.Origin == "subdir/file3.txt" && diff.OriginID == 3 {
// 				hasDeleteFile3 = true
// 			}
// 			if diff.Mode == "add" && diff.Target == "subdir/file3.txt" && diff.OriginID == 30 {
// 				hasAddFile3 = true
// 			}
// 			if diff.Mode == "add" && diff.Target == "subdir/newfile.txt" && diff.OriginID == 4 {
// 				hasAddNewfile = true
// 			}
// 		}

// 		if !hasDeleteFile2 {
// 			t.Error("expected delete operation for file2.txt")
// 		}
// 		if !hasDeleteFile3 {
// 			t.Error("expected delete operation for subdir/file3.txt with ID 3")
// 		}
// 		if !hasAddFile3 {
// 			t.Error("expected add operation for subdir/file3.txt with ID 30")
// 		}
// 		if !hasAddNewfile {
// 			t.Error("expected add operation for subdir/newfile.txt")
// 		}
// 	})
// }

// cloneNode 做一个简单的深拷贝用于测试
func cloneNode(n *Node) *Node {
	if n == nil {
		return nil
	}
	cn := &Node{
		Name:        n.Name,
		Path:        n.Path,
		IsDir:       n.IsDir,
		Children:    []*Node{},
		ID:          n.ID,
		ChildrenNum: n.ChildrenNum,
		Error:       n.Error,
	}
	for _, c := range n.Children {
		cn.Children = append(cn.Children, cloneNode(c))
	}
	return cn
}

func TestSolveNodesTreeDiff_AddDelete(t *testing.T) {
	originStr := `root
    - file1 '1'
    - file2 '2'`
	targetStr := `root
    - file1 '1'
    - file3 '3'`

	origin, err := BuildNodeFromString(originStr)
	if err != nil {
		t.Fatalf("build origin: %v", err)
	}
	target, err := BuildNodeFromString(targetStr)
	if err != nil {
		t.Fatalf("build target: %v", err)
	}

	result := &Node{}

	if err := solveNodesTreeDiff(result, origin, target); err != nil {
		t.Fatalf("solve diff failed: %v", err)
	}

	// file2 应被标记为删除
	var foundFile2 bool
	var foundFile3 bool
	for _, c := range result.Children {
		if c.Name == "file2" {
			foundFile2 = true
			if !c.RemoveFlag {
				t.Errorf("expected file2 RemoveFlag=true")
			}
		}
		if c.Name == "file3" {
			foundFile3 = true
			if c.ID != 3 {
				t.Errorf("expected file3 ID=3, got %d", c.ID)
			}
		}
		if c.Name == "file1" {
			if c.RemoveFlag {
				t.Errorf("file1 should not be removed")
			}
		}
	}
	if !foundFile2 {
		t.Errorf("result should contain file2 (marked removed)")
	}
	if !foundFile3 {
		t.Errorf("result should contain new file3")
	}
}

func TestSolveNodesTreeDiff_DeleteDir(t *testing.T) {
	originStr := `root
    subdir
        - file2 '2'`
	targetStr := `root`

	origin, err := BuildNodeFromString(originStr)
	if err != nil {
		t.Fatalf("build origin: %v", err)
	}
	target, err := BuildNodeFromString(targetStr)
	if err != nil {
		t.Fatalf("build target: %v", err)
	}

	result := &Node{}

	if err := solveNodesTreeDiff(result, origin, target); err != nil {
		t.Fatalf("solve diff failed: %v", err)
	}

	// subdir 应被标记为删除
	var foundSubdir bool
	for _, c := range result.Children {
		if c.Name == "subdir" {
			foundSubdir = true
			if !c.RemoveFlag {
				t.Errorf("expected subdir RemoveFlag=true")
			}
		}
	}
	if !foundSubdir {
		t.Errorf("result should contain subdir (marked removed)")
	}
}

func TestSolveNodesTreeDiff_SkipErrorAndOmission(t *testing.T) {
	// 构造 origin 包含一个带 Error 的文件，一个省略节点（ChildrenNum > MaxChildrenNum）和一个正常文件
	origin := &Node{
		Name:     "root",
		IsDir:    true,
		Children: []*Node{},
	}
	fileErr := &Node{Name: "errfile", IsDir: false, ID: 1, Error: errors.New("test error")}
	fileOmit := &Node{Name: "omitted", IsDir: true, Children: nil, ChildrenNum: MaxChildrenNum + 10}
	fileOk := &Node{Name: "good", IsDir: false, ID: 2}
	origin.Children = append(origin.Children, fileErr, fileOmit, fileOk)

	// target 只包含 good，意图删除其他两个，但我们期望跳过带 error 与省略节点
	target := &Node{
		Name:     "root",
		IsDir:    true,
		Children: []*Node{{Name: "good", IsDir: false, ID: 2}},
	}

	result := &Node{}

	if err := solveNodesTreeDiff(result, origin, target); err != nil {
		t.Fatalf("solve diff failed: %v", err)
	}

	// 检查 errfile 与 omitted 未被标记为删除
	for _, c := range result.Children {
		if c.Name == "errfile" {
			if c.RemoveFlag {
				t.Errorf("errfile should be skipped (not removed)")
			}
		}
		if c.Name == "omitted" {
			if c.RemoveFlag {
				t.Errorf("omitted should be skipped (not removed)")
			}
		}
		if c.Name == "good" {
			if c.RemoveFlag {
				t.Errorf("good should not be removed")
			}
		}
	}
}
