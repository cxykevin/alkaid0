package tree

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/terminal/sandbox"
)

// TestTreeScanReconcilesStaleMount 验证 tree 扫描会清理失效的挂载记录：
// 有效挂载保留，链接缺失/指向错误/目标不可达时删掉链接与表记录，且不新建。
func TestTreeScanReconcilesStaleMount(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks usually requires developer mode / privileges on Windows")
	}
	dir := t.TempDir()
	target := t.TempDir()
	session := &structs.Chats{
		ID:                   1,
		Root:                 dir,
		CurrentActivatePath:  ".",
		TemporyDataOfSession: map[string]any{},
	}

	if err := sandbox.AddMount(dir, "data", target); err != nil {
		t.Fatalf("AddMount failed: %v", err)
	}
	link := sandbox.MountLinkPath(dir, "data")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	// 有效挂载：扫描后保留。
	if _, err := buildTreeBlock(session); err != nil {
		t.Fatalf("buildTreeBlock failed: %v", err)
	}
	if entries := sandbox.LoadMounts(dir); len(entries) != 1 {
		t.Fatalf("valid mount must be kept, table = %#v", entries)
	}

	// 链接被外部删除：扫描时自愈清理记录，但不会重新创建链接。
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if _, err := buildTreeBlock(session); err != nil {
		t.Fatalf("buildTreeBlock failed: %v", err)
	}
	if entries := sandbox.LoadMounts(dir); len(entries) != 0 {
		t.Fatalf("stale mount must be dropped, table = %#v", entries)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("tree scan must not recreate the mount link: %v", err)
	}
}

// TestTreeScanDropsUnreachableTarget 目标目录消失时同样清理。
func TestTreeScanDropsUnreachableTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks usually requires developer mode / privileges on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(t.TempDir(), "gone")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	session := &structs.Chats{
		ID:                   1,
		Root:                 dir,
		CurrentActivatePath:  ".",
		TemporyDataOfSession: map[string]any{},
	}
	if err := sandbox.AddMount(dir, "data", target); err != nil {
		t.Fatalf("AddMount failed: %v", err)
	}
	if err := os.Symlink(target, sandbox.MountLinkPath(dir, "data")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(target); err != nil {
		t.Fatal(err)
	}

	if _, err := buildTreeBlock(session); err != nil {
		t.Fatalf("buildTreeBlock failed: %v", err)
	}
	if entries := sandbox.LoadMounts(dir); len(entries) != 0 {
		t.Fatalf("unreachable mount must be dropped, table = %#v", entries)
	}
}
