package actions

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ---- A15: 悬垂 symlink 也必须被包含性校验拦截 ----

func TestFsWriteRejectsDanglingSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires privileges on Windows")
	}
	outside := t.TempDir()
	tmpDir := t.TempDir()
	sessionID := registerTestSession(t, tmpDir, 1)

	// 悬垂链接（最终目标不存在、但目标父目录存在）指向工作区外：
	// 旧实现只看"已存在的最长前缀"，会放行该链接，O_CREATE 随即在外部建文件。
	link := filepath.Join(tmpDir, "dangling")
	escaped := filepath.Join(outside, "escaped.txt")
	if err := os.Symlink(escaped, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := FsWrite(FsWriteRequest{SessionID: sessionID, Path: "dangling", Content: "escaped"}, nil, 1); err == nil {
		t.Error("悬垂 symlink 指向工作区外时必须拒绝写入")
	}
	if _, statErr := os.Stat(escaped); !os.IsNotExist(statErr) {
		t.Errorf("不得通过悬垂链接在工作区外创建文件: %v", statErr)
	}

	// 中间分量是悬垂链接且指向工作区外：同样拦截
	dirLink := filepath.Join(tmpDir, "dirlink")
	if err := os.Symlink(filepath.Join(outside, "missingdir"), dirLink); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := FsWrite(FsWriteRequest{SessionID: sessionID, Path: "dirlink/sub/f.txt", Content: "x"}, nil, 1); err == nil {
		t.Error("中间悬垂 symlink 指向工作区外时必须拒绝")
	}
	if _, statErr := os.Stat(filepath.Join(outside, "missingdir")); !os.IsNotExist(statErr) {
		t.Errorf("不得通过中间悬垂链接在工作区外创建目录: %v", statErr)
	}

	// 指向工作区内的悬垂链接仍应允许写穿（不能过度收紧）
	inner := filepath.Join(tmpDir, "innerlink")
	if err := os.Symlink(filepath.Join(tmpDir, "created-via-link.txt"), inner); err != nil {
		t.Fatalf("create inner symlink: %v", err)
	}
	if _, err := FsWrite(FsWriteRequest{SessionID: sessionID, Path: "innerlink", Content: "ok"}, nil, 1); err != nil {
		t.Errorf("指向工作区内的悬垂链接不应被拒绝: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(tmpDir, "created-via-link.txt")); err != nil || string(data) != "ok" {
		t.Errorf("应写穿工作区内的悬垂链接: data=%q err=%v", data, err)
	}
}

// ---- A14: fs/write 覆盖写必须是"临时文件 + rename"，不得原地截断 ----

func TestFsWriteOverwriteIsAtomic(t *testing.T) {
	tmpDir := t.TempDir()
	sessionID := registerTestSession(t, tmpDir, 1)

	target := filepath.Join(tmpDir, "data.txt")
	if err := os.WriteFile(target, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	resp, err := FsWrite(FsWriteRequest{SessionID: sessionID, Path: "data.txt", Content: "replaced"}, nil, 1)
	if err != nil {
		t.Fatalf("overwrite failed: %v", err)
	}
	if resp.BytesWritten != int64(len("replaced")) {
		t.Errorf("bytesWritten = %d, want %d", resp.BytesWritten, len("replaced"))
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "replaced" {
		t.Errorf("content = %q, err=%v", data, err)
	}
	// 临时文件不得残留
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".alkaid0-fswrite-") {
			t.Errorf("临时文件未清理: %s", e.Name())
		}
	}
}
