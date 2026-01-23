package tree

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

// TestSolveCall 测试 SolveCall 函数
func TestSolveCall(t *testing.T) {
	// 创建测试目录结构
	tmpDir, err := os.MkdirTemp("", "tree_solve_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// 创建初始文件结构
	// tmpDir/
	//   file1.txt (ID: 1)
	//   file2.txt (ID: 2)
	//   subdir/
	//     file3.txt (ID: 3)
	file1 := filepath.Join(tmpDir, "file1.txt")
	if err := os.WriteFile(file1, []byte("content1"), 0644); err != nil {
		t.Fatalf("failed to create file1: %v", err)
	}

	file2 := filepath.Join(tmpDir, "file2.txt")
	if err := os.WriteFile(file2, []byte("content2"), 0644); err != nil {
		t.Fatalf("failed to create file2: %v", err)
	}

	subDir := filepath.Join(tmpDir, "subdir")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	file3 := filepath.Join(subDir, "file3.txt")
	if err := os.WriteFile(file3, []byte("content3"), 0644); err != nil {
		t.Fatalf("failed to create file3: %v", err)
	}

	// 构建原始树
	var id int32
	node := BuildTree(tmpDir, &id)
	originalStr := BuildString(node)
	t.Logf("Original tree:\n%s", originalStr)

	t.Run("NoChanges", func(t *testing.T) {
		// AI未做任何更改
		diffObjs, err := SolveCall(node, originalStr)
		if err != nil {
			t.Fatalf("SolveCall failed: %v", err)
		}
		if len(diffObjs) != 0 {
			t.Errorf("expected no changes, got %d diff objects", len(diffObjs))
		}
	})

	t.Run("AddNewFile", func(t *testing.T) {
		// 模拟AI添加新文件
		// 在subsubdir下添加file4.txt，ID为4
		editedStr := fmt.Sprintf(`%s
  subdir
    file3.txt '3'
    file4.txt '4'`, originalStr)

		diffObjs, err := SolveCall(node, editedStr)
		if err != nil {
			t.Fatalf("SolveCall failed: %v", err)
		}

		if len(diffObjs) != 1 {
			t.Fatalf("expected 1 diff object, got %d", len(diffObjs))
		}

		diff := diffObjs[0]
		if diff.Mode != "add" {
			t.Errorf("expected mode 'add', got %s", diff.Mode)
		}
		if diff.Target != "subdir/file4.txt" {
			t.Errorf("expected target 'subdir/file4.txt', got %s", diff.Target)
		}
		if diff.OriginID != 4 {
			t.Errorf("expected OriginID 4, got %d", diff.OriginID)
		}
	})

	t.Run("DeleteFile", func(t *testing.T) {
		// 模拟AI删除file2.txt
		lines := strings.Split(originalStr, "\n")
		var filtered []string
		for _, line := range lines {
			if !strings.Contains(line, "file2.txt") {
				filtered = append(filtered, line)
			}
		}
		editedStr := strings.Join(filtered, "\n")

		diffObjs, err := SolveCall(node, editedStr)
		if err != nil {
			t.Fatalf("SolveCall failed: %v", err)
		}

		if len(diffObjs) != 1 {
			t.Fatalf("expected 1 diff object, got %d", len(diffObjs))
		}

		diff := diffObjs[0]
		if diff.Mode != "delete" {
			t.Errorf("expected mode 'delete', got %s", diff.Mode)
		}
		if diff.Origin != "file2.txt" {
			t.Errorf("expected origin 'file2.txt', got %s", diff.Origin)
		}
		if diff.OriginID != 2 {
			t.Errorf("expected OriginID 2, got %d", diff.OriginID)
		}
	})

	t.Run("ChangeFileID", func(t *testing.T) {
		// 模拟AI更改file1.txt的ID从1到10
		editedStr := strings.Replace(originalStr, "file1.txt '1'", "file1.txt '10'", 1)

		diffObjs, err := SolveCall(node, editedStr)
		if err != nil {
			t.Fatalf("SolveCall failed: %v", err)
		}

		if len(diffObjs) != 2 {
			t.Fatalf("expected 2 diff objects (delete and add), got %d", len(diffObjs))
		}

		// 验证有一个删除和一个添加
		var hasDelete, hasAdd bool
		for _, diff := range diffObjs {
			if diff.Mode == "delete" && diff.Origin == "file1.txt" && diff.OriginID == 1 {
				hasDelete = true
			}
			if diff.Mode == "add" && diff.Target == "file1.txt" && diff.OriginID == 10 {
				hasAdd = true
			}
		}

		if !hasDelete {
			t.Error("expected delete operation for file1.txt with ID 1")
		}
		if !hasAdd {
			t.Error("expected add operation for file1.txt with ID 10")
		}
	})

	t.Run("AddAndDeleteMultiple", func(t *testing.T) {
		// 模拟AI删除file1.txt，添加file4.txt和file5.txt
		lines := strings.Split(originalStr, "\n")
		var filtered []string
		for _, line := range lines {
			if !strings.Contains(line, "file1.txt") {
				filtered = append(filtered, line)
			}
		}
		// 在根目录添加两个新文件
		editedStr := strings.Join(filtered, "\n") + "\nfile4.txt '4'\nfile5.txt '5'"

		diffObjs, err := SolveCall(node, editedStr)
		if err != nil {
			t.Fatalf("SolveCall failed: %v", err)
		}

		if len(diffObjs) != 3 {
			t.Fatalf("expected 3 diff objects (1 delete + 2 add), got %d", len(diffObjs))
		}

		var deleteCount, addCount int
		for _, diff := range diffObjs {
			if diff.Mode == "delete" {
				deleteCount++
			}
			if diff.Mode == "add" {
				addCount++
			}
		}

		if deleteCount != 1 {
			t.Errorf("expected 1 delete operation, got %d", deleteCount)
		}
		if addCount != 2 {
			t.Errorf("expected 2 add operations, got %d", addCount)
		}
	})

	t.Run("EmptyString", func(t *testing.T) {
		// 测试空字符串
		diffObjs, err := SolveCall(node, "")
		if err != nil {
			t.Fatalf("SolveCall failed: %v", err)
		}

		// 所有文件都应该被标记为删除
		if len(diffObjs) != 3 {
			t.Fatalf("expected 3 diff objects (all deleted), got %d", len(diffObjs))
		}

		for _, diff := range diffObjs {
			if diff.Mode != "delete" {
				t.Errorf("expected all operations to be 'delete', got %s", diff.Mode)
			}
		}
	})

	t.Run("ComplexChanges", func(t *testing.T) {
		// 复杂的更改：删除file2.txt，修改file3.txt的ID，添加新文件
		complexStr := fmt.Sprintf(`%s
  subdir
    file3.txt '30'
    newfile.txt '4'`, strings.Replace(originalStr, "file2.txt '2'", "", 1))

		diffObjs, err := SolveCall(node, complexStr)
		if err != nil {
			t.Fatalf("SolveCall failed: %v", err)
		}

		// 应该有：删除file2.txt，删除file3.txt(ID:3)，添加file3.txt(ID:30)，添加newfile.txt
		if len(diffObjs) != 4 {
			t.Fatalf("expected 4 diff objects, got %d", len(diffObjs))
		}

		var hasDeleteFile2, hasDeleteFile3, hasAddFile3, hasAddNewfile bool
		for _, diff := range diffObjs {
			if diff.Mode == "delete" && diff.Origin == "file2.txt" && diff.OriginID == 2 {
				hasDeleteFile2 = true
			}
			if diff.Mode == "delete" && diff.Origin == "subdir/file3.txt" && diff.OriginID == 3 {
				hasDeleteFile3 = true
			}
			if diff.Mode == "add" && diff.Target == "subdir/file3.txt" && diff.OriginID == 30 {
				hasAddFile3 = true
			}
			if diff.Mode == "add" && diff.Target == "subdir/newfile.txt" && diff.OriginID == 4 {
				hasAddNewfile = true
			}
		}

		if !hasDeleteFile2 {
			t.Error("expected delete operation for file2.txt")
		}
		if !hasDeleteFile3 {
			t.Error("expected delete operation for subdir/file3.txt with ID 3")
		}
		if !hasAddFile3 {
			t.Error("expected add operation for subdir/file3.txt with ID 30")
		}
		if !hasAddNewfile {
			t.Error("expected add operation for subdir/newfile.txt")
		}
	})
}

// TestParseDistString 测试 parseDistString 函数
func TestParseDistString(t *testing.T) {
	testStr := `root
  file1.txt '1'
  file2.txt '2'
  subdir
    file3.txt '3'`

	result := parseDistString(testStr)

	// 打印结果用于调试
	t.Logf("Parsed map: %+v", result)

	// 验证解析结果（相对路径，不包含根节点）
	expected := map[string]int32{
		"file1.txt":        1,
		"file2.txt":        2,
		"subdir/file3.txt": 3,
	}

	if len(result) != len(expected) {
		t.Errorf("expected %d entries, got %d", len(expected), len(result))
	}

	for path, id := range expected {
		if result[path] != id {
			t.Errorf("expected %s to have ID %d, got %d", path, id, result[path])
		}
	}
}
