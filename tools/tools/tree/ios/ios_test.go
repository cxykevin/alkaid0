package ios

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkCopy(b *testing.B) {
	// 从环境变量读temp路径，如果不存在则使用默认路径
	tempDir := os.Getenv("ALKAID0_TEST_TEMP")
	var err error
	if tempDir == "" {
		tempDir, err = os.MkdirTemp("", "ios_test")
		if err != nil {
			b.Fatal(err)
		}
		defer os.RemoveAll(tempDir)
	} else {
		tempDir = filepath.Join(tempDir, "ios_test")
		// 如果目录不存在则创建
		if _, err := os.Stat(tempDir); os.IsNotExist(err) {
			if err := os.MkdirAll(tempDir, 0755); err != nil {
				b.Fatal(err)
			}
		}
		defer os.RemoveAll(tempDir)
	}

	b.Run("SmallFile", func(sb *testing.B) {
		sb.StopTimer()
		src := filepath.Join(tempDir, "small_src")
		dst := filepath.Join(tempDir, "small_dst")
		content := []byte("hello world")
		if err := os.WriteFile(src, content, 0644); err != nil {
			sb.Fatal(err)
		}

		sb.Run("RunSmallFile", func(sb2 *testing.B) {
			sb2.ResetTimer()
			sb2.StartTimer()
			if err := Copy(src, dst); err != nil {
				sb2.Fatalf("Copy failed: %v", err)
			}

			sb2.StopTimer()
			got, err := os.ReadFile(dst)
			if err != nil {
				sb2.Fatal(err)
			}
			if !bytes.Equal(got, content) {
				sb2.Errorf("got %q, want %q", got, content)
			}
			defer os.Remove(dst)
		})
	})

	b.Run("LargeFile", func(sb *testing.B) {
		sb.StopTimer()
		src := filepath.Join(tempDir, "large_src")
		dst := filepath.Join(tempDir, "large_dst")

		content := make([]byte, 1024*1024*520)
		for i := range content {
			content[i] = byte(i % 256)
		}
		if err := os.WriteFile(src, content, 0644); err != nil {
			sb.Fatal(err)
		}
		sb.Run("RunLargeFile", func(sb2 *testing.B) {
			sb2.ResetTimer()
			sb2.StartTimer()
			if err := Copy(src, dst); err != nil {
				sb2.Skipf("Skipping cloneFile test: %v (likely not supported on this FS)", err)
			}
			sb2.StopTimer()
			defer os.Remove(dst)
		})
	})
}

func BenchmarkCloneFile(b *testing.B) {
	tempDir, content := setupCloneTest(b)
	if tempDir == "" {
		return
	}
	defer os.RemoveAll(tempDir)

	src := filepath.Join(tempDir, "src")
	dst := filepath.Join(tempDir, "dst")

	s, err := os.Open(src)
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()

	d, err := os.Create(dst)
	if err != nil {
		b.Fatal(err)
	}
	defer d.Close()

	err = cloneFile(int(s.Fd()), int(d.Fd()))
	if err != nil {
		b.Skipf("Skipping cloneFile test: %v (likely not supported on this FS)", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		b.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		b.Errorf("got %q, want %q", got, content)
	}
}

// TestCopy 测试 Copy 函数的基本功能
func TestCopy(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "ios_copy_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	src := filepath.Join(tempDir, "src.txt")
	dst := filepath.Join(tempDir, "dst.txt")
	content := []byte("hello world test")

	if err := os.WriteFile(src, content, 0644); err != nil {
		t.Fatal(err)
	}

	// 复制文件
	if err := Copy(src, dst); err != nil {
		t.Fatalf("Copy failed: %v", err)
	}

	// 验证目标文件内容
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Copy content mismatch: got %q, want %q", got, content)
	}
}

// TestCopyEmptyFile 测试复制空文件
func TestCopyEmptyFile(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "ios_empty_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	src := filepath.Join(tempDir, "empty.txt")
	dst := filepath.Join(tempDir, "empty_dst.txt")

	if err := os.WriteFile(src, []byte{}, 0644); err != nil {
		t.Fatal(err)
	}

	if err := Copy(src, dst); err != nil {
		t.Fatalf("Copy empty file failed: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("Expected empty file, got %d bytes", len(got))
	}
}

// TestCopyCloneFallbackKeepsDestination 回归测试：FICLONE 失败后回退复制成功时，
// 目标文件必须保留且内容正确，函数必须返回 nil。
//
// 背景：旧实现把 cloneFile 的错误写在同一个 err 上，回退 io.Copy 成功后 err 仍非 nil，
// defer 里的清理逻辑便删掉了刚复制好的目标文件，同时向上返回成功——表现为
// @tree 复制/移动大文件时目标凭空消失（在不支持 reflink 的文件系统上必现）。
func TestCopyCloneFallbackKeepsDestination(t *testing.T) {
	dir := t.TempDir()
	origin := filepath.Join(dir, "origin.bin")
	dist := filepath.Join(dir, "dist.bin")
	content := []byte("hello alkaid0 copy fallback")

	if err := os.WriteFile(origin, content, 0644); err != nil {
		t.Fatalf("prepare origin failed: %v", err)
	}

	// 强制走"大文件"分支，并让 FICLONE 恒失败，从而确定性地覆盖回退路径
	restoreLimit, restoreClone := maxCopyLimit, cloneFileFn
	maxCopyLimit = -1
	cloneFileFn = func(_, _ int) error { return errors.New("FICLONE unsupported") }
	t.Cleanup(func() { maxCopyLimit, cloneFileFn = restoreLimit, restoreClone })

	if err := Copy(origin, dist); err != nil {
		t.Fatalf("Copy should succeed via io.Copy fallback, got %v", err)
	}

	got, err := os.ReadFile(dist)
	if err != nil {
		t.Fatalf("destination must survive a successful fallback copy: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("destination content mismatch: got %q, want %q", got, content)
	}
}

// TestCopyRemovesDestinationOnRealFailure 回归测试：复制确实失败时必须清理半成品目标文件。
func TestCopyRemovesDestinationOnRealFailure(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "srcdir")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("prepare source dir failed: %v", err)
	}
	// 目录不能作为 io.Copy 的源（读取必然失败），用来构造真实的复制失败
	if err := os.WriteFile(filepath.Join(srcDir, "child.txt"), []byte("x"), 0644); err != nil {
		t.Fatalf("prepare child failed: %v", err)
	}
	dist := filepath.Join(dir, "dist.bin")

	restoreLimit, restoreClone := maxCopyLimit, cloneFileFn
	maxCopyLimit = -1
	cloneFileFn = func(_, _ int) error { return errors.New("FICLONE unsupported") }
	t.Cleanup(func() { maxCopyLimit, cloneFileFn = restoreLimit, restoreClone })

	if err := Copy(srcDir, dist); err == nil {
		t.Fatalf("Copy from a directory should fail")
	}
	if _, err := os.Stat(dist); !os.IsNotExist(err) {
		t.Errorf("failed copy must not leave a partial destination behind (stat err=%v)", err)
	}
}

// TestCopyClosesDestinationBeforeRemoving 回归测试：失败清理必须发生在关闭目标句柄之后。
//
// 背景：旧实现用 defer 先 os.Remove(dist) 再 d.Close()。Windows 的 CreateFileW 默认不带
// FILE_SHARE_DELETE，删除仍被打开的文件会因共享冲突失败，而该错误被丢弃，于是半成品目标
// 文件残留（CI windows-latest 实测 stat err=<nil>）。这个顺序约束在 Linux 上观察不到
// （unlink 对已打开的文件照样成功），因此这里通过 removeFileFn 直接断言句柄已经关闭。
func TestCopyClosesDestinationBeforeRemoving(t *testing.T) {
	dir := t.TempDir()
	srcDir := filepath.Join(dir, "srcdir")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("prepare source dir failed: %v", err)
	}
	// 目录不能作为 io.Copy 的源（读取必然失败），用来构造真实的复制失败
	if err := os.WriteFile(filepath.Join(srcDir, "child.txt"), []byte("x"), 0644); err != nil {
		t.Fatalf("prepare child failed: %v", err)
	}
	dist := filepath.Join(dir, "dist.bin")

	restoreLimit, restoreClone, restoreRemove := maxCopyLimit, cloneFileFn, removeFileFn
	maxCopyLimit = -1
	cloneFileFn = func(_, _ int) error { return errors.New("FICLONE unsupported") }
	removed := false
	removeFileFn = func(d *os.File, path string) error {
		removed = true
		// 句柄关闭后 Stat 必然报错；仍能 Stat 成功说明删除先于关闭执行
		if _, err := d.Stat(); err == nil {
			t.Errorf("destination must be closed before it is removed, otherwise Windows cannot delete it")
		}
		return os.Remove(path)
	}
	t.Cleanup(func() { maxCopyLimit, cloneFileFn, removeFileFn = restoreLimit, restoreClone, restoreRemove })

	if err := Copy(srcDir, dist); err == nil {
		t.Fatalf("Copy from a directory should fail")
	}
	if !removed {
		t.Errorf("failed copy must trigger the cleanup")
	}
	if _, err := os.Stat(dist); !os.IsNotExist(err) {
		t.Errorf("failed copy must not leave a partial destination behind (stat err=%v)", err)
	}
}

// TestCopyNonexistentSrc 测试复制不存在的源文件
func TestCopyNonexistentSrc(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "ios_noent_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	src := filepath.Join(tempDir, "nonexistent.txt")
	dst := filepath.Join(tempDir, "dst.txt")

	err = Copy(src, dst)
	if err == nil {
		t.Error("Expected error for nonexistent source, got nil")
	}
}

// TestMkdirAllThenCopy 测试先创建目录再复制
func TestMkdirAllThenCopy(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "ios_mkdir_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	// 创建深层目录
	deepDir := filepath.Join(tempDir, "a", "b", "c")
	if err := os.MkdirAll(deepDir, 0755); err != nil {
		t.Fatal(err)
	}

	src := filepath.Join(tempDir, "src.txt")
	content := []byte("deep test")
	if err := os.WriteFile(src, content, 0644); err != nil {
		t.Fatal(err)
	}

	// 复制到深层目录
	dst := filepath.Join(deepDir, "copied.txt")
	if err := Copy(src, dst); err != nil {
		t.Fatalf("Copy to deep dir failed: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Copy content mismatch: got %q, want %q", got, content)
	}
}

// setupCloneTest 辅助函数，创建用于 cloneFile 测试的临时文件
func setupCloneTest(t interface {
	Fatal(...any)
}) (string, []byte) {
	tempDir, err := os.MkdirTemp("", "clone_test")
	if err != nil {
		t.Fatal(err)
	}

	src := filepath.Join(tempDir, "src")
	content := []byte("clone test content")
	if err := os.WriteFile(src, content, 0644); err != nil {
		os.RemoveAll(tempDir)
		t.Fatal(err)
	}

	return tempDir, content
}

// TestCloneFile 测试 cloneFile 函数
func TestCloneFile(t *testing.T) {
	tempDir, content := setupCloneTest(t)
	defer os.RemoveAll(tempDir)

	src := filepath.Join(tempDir, "src")
	dst := filepath.Join(tempDir, "dst")

	s, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	d, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	err = cloneFile(int(s.Fd()), int(d.Fd()))
	if err != nil {
		t.Skipf("Skipping cloneFile test: %v (likely not supported on this FS)", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("cloneFile content mismatch: got %q, want %q", got, content)
	}
}
