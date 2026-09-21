//go:build linux

package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeShim 写入一个可执行 shell 脚本垫片。
func writeShim(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o755); err != nil {
		t.Fatalf("写入垫片 %s 失败: %v", name, err)
	}
}

// extractInnerMountScript 从生产代码生成的 mount.sh 中取出 chroot 内部执行的脚本。
// 内层脚本按设计不含单引号，因此可以用"chroot "$T" sh -uc '"与结尾的"' -- "$@""
// 定位。格式变化时这里会直接失败，避免测试静默地什么都不检查。
func extractInnerMountScript(t *testing.T, outer string) string {
	t.Helper()
	const marker = `chroot "$T" sh -uc '`
	start := strings.LastIndex(outer, marker)
	if start < 0 {
		t.Fatalf("未找到内层脚本起始标记 %q", marker)
	}
	rest := outer[start+len(marker):]
	end := strings.Index(rest, `' -- "$@"`)
	if end < 0 {
		t.Fatalf("未找到内层脚本结束标记")
	}
	return rest[:end]
}

// TestMountScriptRemovesFakeEtcFiles 回归测试：mount.sh 在 chroot 内用 mktemp 生成
// 伪造的 passwd/group 并 bind 到 /etc/passwd、/etc/group。mktemp 路径是 chroot 里的
// /tmp——它属于可写目录白名单、实际就是宿主的可写临时目录，因此 bind 之后必须立即
// 删除，否则每次沙盒命令都会在宿主 /tmp 残留 .alk-sandbox-etc-* 文件。
//
// 真实沙盒需要 root + mount 权限，这里用垫片执行生产代码生成并 embed 的内层脚本：
// mktemp 重定向到测试临时目录、mount 为空操作，于是"命令结束后没有
// .alk-sandbox-etc-* 文件"等价于脚本执行了清理。
func TestMountScriptRemovesFakeEtcFiles(t *testing.T) {
	shimDir := t.TempDir()
	scratch := t.TempDir()

	realMktemp, err := exec.LookPath("mktemp")
	if err != nil {
		t.Skipf("系统缺少 mktemp: %v", err)
	}

	writeShim(t, shimDir, "mount", "#!/bin/sh\nexit 0\n")
	writeShim(t, shimDir, "mktemp", fmt.Sprintf(`#!/bin/sh
if [ "$1" = "-d" ]; then
	exec %s -d "%s/XXXXXX"
fi
case "$1" in
	*/.alk-sandbox-etc-*)
		exec %s "%s/.alk-sandbox-etc-XXXXXX"
		;;
	*)
		exec %s "%s/XXXXXX"
		;;
esac
`, realMktemp, scratch, realMktemp, scratch, realMktemp, scratch))

	// PATH 同时用于 exec.LookPath 与内层脚本里的 mount/mktemp。
	t.Setenv("PATH", shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	workDir := t.TempDir()
	sb, err := New(Config{
		WorkDir:       workDir,
		IsolationMode: IsolationOS,
		Timeout:       30 * time.Second,
	})
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	// 走真实生产路径生成 mount.sh（与沙盒执行时完全一致的外层脚本）。
	isolated, err := sb.createLinuxIsolatedCommand(context.Background(), "true")
	if err != nil {
		t.Fatalf("createLinuxIsolatedCommand failed: %v", err)
	}
	outer := ""
	for i, arg := range isolated.Args {
		if arg == "-c" && i+1 < len(isolated.Args) {
			outer = isolated.Args[i+1]
			break
		}
	}
	if outer == "" {
		t.Fatal("未在 unshare 参数中找到 mount.sh")
	}
	inner := extractInnerMountScript(t, outer)

	// 内层脚本依赖外层 chroot 前导出的环境变量（含可写目录 ALK_WD_n）。
	child := exec.Command("sh", "-uc", inner, "--", "true")
	child.Env = append(os.Environ(),
		"REAL_USER=tester",
		"ALK_WORKDIR="+workDir,
		"ALK_RUN_UID=0",
		"ALK_RUN_GID=0",
		"ALK_RUN_USER=tester",
	)
	for i, dir := range sb.GetWritableDirs() {
		child.Env = append(child.Env, fmt.Sprintf("ALK_WD_%d=%s", i, dir))
	}
	var stderr bytes.Buffer
	child.Stderr = &stderr
	if err := child.Run(); err != nil {
		t.Fatalf("执行内层脚本失败: %v (stderr: %s)", err, stderr.String())
	}

	files, err := filepath.Glob(filepath.Join(scratch, ".alk-sandbox-etc-*"))
	if err != nil {
		t.Fatalf("Glob failed: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("mount.sh 结束后伪造的 passwd/group 文件残留在宿主可写目录: %v", files)
	}
}
