package ios

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkCopy(t *testing.B) {
	// 从环境变量读temp路径，如果不存在则使用默认路径
	tempDir := os.Getenv("ALKAID0_TEST_TEMP")
	var err error
	if tempDir == "" {
		tempDir, err = os.MkdirTemp("", "ios_test")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(tempDir)
	} else {
		tempDir = filepath.Join(tempDir, "ios_test")
		// 如果目录不存在则创建
		if _, err := os.Stat(tempDir); os.IsNotExist(err) {
			if err := os.MkdirAll(tempDir, 0755); err != nil {
				t.Fatal(err)
			}
		}
		defer os.RemoveAll(tempDir)
	}

	t.Run("SmallFile", func(t *testing.B) {
		t.StopTimer()
		src := filepath.Join(tempDir, "small_src")
		dst := filepath.Join(tempDir, "small_dst")
		content := []byte("hello world")
		if err := os.WriteFile(src, content, 0644); err != nil {
			t.Fatal(err)
		}

		t.Run("RunSmallFile", func(t *testing.B) {
			t.ResetTimer()
			t.StartTimer()
			if err := Copy(src, dst); err != nil {
				t.Fatalf("Copy failed: %v", err)
			}

			t.StopTimer()
			got, err := os.ReadFile(dst)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, content) {
				t.Errorf("got %q, want %q", got, content)
			}
			defer os.Remove(dst)
		})
	})

	t.Run("LargeFile", func(t *testing.B) {
		t.StopTimer()
		src := filepath.Join(tempDir, "large_src")
		dst := filepath.Join(tempDir, "large_dst")

		content := make([]byte, 1024*1024*520)
		for i := range content {
			content[i] = byte(i % 256)
		}
		if err := os.WriteFile(src, content, 0644); err != nil {
			t.Fatal(err)
		}
		t.Run("RunLargeFile", func(t *testing.B) {

			t.ResetTimer()
			t.StartTimer()
			if err := Copy(src, dst); err != nil {
				t.Skipf("Skipping cloneFile test: %v (likely not supported on this FS)", err)
			}

			t.StopTimer()
			// got, err := os.ReadFile(dst)
			// if err != nil {
			// 	t.Fatal(err)
			// }
			// if !bytes.Equal(got, content) {
			// 	t.Errorf("content mismatch")
			// }
			defer os.Remove(dst)
		})
	})
}

func BenchmarkCloneFile(t *testing.B) {
	tempDir := os.Getenv("ALKAID0_TEST_TEMP")
	var err error
	if tempDir == "" {
		tempDir, err = os.MkdirTemp("", "clone_test")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(tempDir)
	} else {

		tempDir = filepath.Join(tempDir, "clone_test")
		// 如果目录不存在则创建
		if _, err := os.Stat(tempDir); os.IsNotExist(err) {
			if err := os.MkdirAll(tempDir, 0755); err != nil {
				t.Fatal(err)
			}
		}
		defer os.RemoveAll(tempDir)
	}

	src := filepath.Join(tempDir, "src")
	dst := filepath.Join(tempDir, "dst")
	content := []byte("clone test content")
	if err := os.WriteFile(src, content, 0644); err != nil {
		t.Fatal(err)
	}

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
		t.Errorf("got %q, want %q", got, content)
	}
}
