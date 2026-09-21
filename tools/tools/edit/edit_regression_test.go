package edit

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/tools/trace"
	"github.com/glebarez/sqlite"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"
	"gorm.io/gorm"
)

// ---- 回归：编码 / 行尾 / 行长 / 常规文件 / 原子写入 ----
//
// 背景（DEEP_REVIEW 确认缺陷）：
//   - read 存的是解码后的文本（去 BOM、UTF-16 转码），edit 却拿原始字节比对，
//     带 BOM/UTF-16/GBK 的文件永远报"内容已被外部修改"，无法编辑；
//   - edit 用 bufio.Scanner 逐行读、再以 "\n" 拼回，CRLF 被整篇改成 LF，末尾
//     换行状态也被改写；
//   - Scanner 默认 64KiB 行长上限让压缩后的 JS/JSON 直接报错；
//   - 没有常规文件/大小校验，FIFO 会永久阻塞、巨大文件会 OOM；
//   - os.WriteFile 原地截断，中断即留下半截文件。

// newEditSession 构造带工作目录与内存数据库的测试会话。
// writeFile 成功后会用 trace.Trace 刷新读取记录（会访问 session.DB），
// 因此测试会话不能没有数据库，否则会在 gorm 里 panic。
func newEditSession(t *testing.T, dir string) *structs.Chats {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	if err := db.AutoMigrate(&structs.Traces{}, &structs.Chats{}, &structs.ReferFiles{}); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	return &structs.Chats{
		ID:                   1,
		DB:                   db,
		CurrentActivatePath:  dir,
		TemporyDataOfRequest: make(map[string]any),
		TemporyDataOfSession: make(map[string]any),
	}
}

// runEdit 走 writeFile 的完整编辑链路（path 为工作区内相对路径）。
func runEdit(t *testing.T, session *structs.Chats, path, target, text string) (map[string]*any, error) {
	t.Helper()
	mp := map[string]*any{
		"path":   ptr(path),
		"target": ptr(target),
		"text":   ptr(text),
	}
	_, _, ret, err := writeFile(session, mp, nil)
	return ret, err
}

// assertEditSucceeded 断言 writeFile 以 success=true 结束。
func assertEditSucceeded(t *testing.T, ret map[string]*any) {
	t.Helper()
	if ret == nil {
		t.Fatal("writeFile 返回了 nil 结果 map")
	}
	v, ok := ret["success"]
	if !ok || v == nil {
		t.Fatal("结果 map 缺少 success")
	}
	b, ok := (*v).(bool)
	if !ok || !b {
		t.Fatalf("期望 success=true, 实际 %v (error=%v)", *v, ret["error"])
	}
}

func TestWriteFile_PreservesCRLFLineEndings(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "win.txt")
	if err := os.WriteFile(file, []byte("a\r\nb\r\nc\r\n"), 0644); err != nil {
		t.Fatal(err)
	}

	ret, err := runEdit(t, newEditSession(t, dir), "win.txt", "@ln:2", "B")
	if err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	assertEditSucceeded(t, ret)

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "a\r\nB\r\nc\r\n" {
		t.Fatalf("CRLF 行尾被破坏: %q", string(data))
	}
}

func TestWriteFile_PreservesMissingFinalNewline(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "noeol.txt")
	if err := os.WriteFile(file, []byte("a\nb"), 0644); err != nil {
		t.Fatal(err)
	}

	ret, err := runEdit(t, newEditSession(t, dir), "noeol.txt", "@ln:1", "A")
	if err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	assertEditSucceeded(t, ret)

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "A\nb" {
		t.Fatalf("原本没有末尾换行的文件被补上了换行: %q", string(data))
	}
}

func TestWriteFile_HandlesLineLongerThanScannerLimit(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "bundle.js")
	longLine := "const x = \"" + strings.Repeat("x", 100*1024) + "\";"
	body := "// HEAD\n" + longLine + "\n// TAIL\n"
	if err := os.WriteFile(file, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	ret, err := runEdit(t, newEditSession(t, dir), "bundle.js", "// HEAD", "// HEADER")
	if err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	assertEditSucceeded(t, ret)

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(longLine)) {
		t.Fatal("超过 64KiB 的长行没有被保留")
	}
	if !bytes.Contains(data, []byte("// HEADER")) {
		t.Fatalf("替换未生效: %q", string(data[:64]))
	}
}

func TestWriteFile_PreservesUTF8BOM(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "bom.txt")
	body := "hello world\n"
	if err := os.WriteFile(file, append([]byte{0xEF, 0xBB, 0xBF}, body...), 0644); err != nil {
		t.Fatal(err)
	}

	session := newEditSession(t, dir)
	// read 工具记录的是解码后（去 BOM）的文本
	trace.ConfirmEditContent(session, "bom.txt", body)

	ret, err := runEdit(t, session, "bom.txt", "world", "there")
	if err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	assertEditSucceeded(t, ret)

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatalf("UTF-8 BOM 丢失: % x", data[:3])
	}
	if got, _ := trace.DecodeText(data); got != "hello there\n" {
		t.Fatalf("内容错误: %q", got)
	}
}

func TestWriteFile_PreservesUTF16Encoding(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "u16.txt")
	body := "hello world\n"
	raw, err := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder().Bytes([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, raw, 0644); err != nil {
		t.Fatal(err)
	}

	session := newEditSession(t, dir)
	trace.ConfirmEditContent(session, "u16.txt", body)

	ret, err := runEdit(t, session, "u16.txt", "world", "there")
	if err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	assertEditSucceeded(t, ret)

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	got, name := trace.DecodeText(data)
	if got != "hello there\n" || name != trace.EncodingUTF16LE {
		t.Fatalf("UTF-16 往返失败: enc=%q content=%q", name, got)
	}
}

func TestWriteFile_RecreatesDeletedFileAfterConfirm(t *testing.T) {
	dir := t.TempDir()
	session := newEditSession(t, dir)
	// 模拟"read 过该文件之后文件被删除"：确认记录仍在
	trace.ConfirmEditContent(session, "gone.txt", "old content\n")

	ret, err := runEdit(t, session, "gone.txt", "@all", "new content")
	if err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	assertEditSucceeded(t, ret)

	data, err := os.ReadFile(filepath.Join(dir, "gone.txt"))
	if err != nil {
		t.Fatalf("被删除的路径必须能重建: %v", err)
	}
	if string(data) != "new content\n" {
		t.Fatalf("内容错误: %q", string(data))
	}
}

func TestWriteFile_PathSpellingCannotBypassExternalChangeGuard(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "e.txt")
	if err := os.WriteFile(file, []byte("external\n"), 0644); err != nil {
		t.Fatal(err)
	}

	session := newEditSession(t, dir)
	// read 工具以 "e.txt" 记录；edit 若用 "./e.txt" 作为 key 就会静默绕过保护
	trace.ConfirmEditContent(session, "e.txt", "agent snapshot\n")

	ret, err := runEdit(t, session, "./e.txt", "@all", "hacked")
	if err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	assertEditFailed(t, ret)

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "external\n" {
		t.Fatalf("外部修改被覆盖: %q", string(data))
	}
}

func TestWriteFile_RunDirectoryFilesNotHijackedAsTerminalInput(t *testing.T) {
	dir := t.TempDir()
	runDir := filepath.Join(dir, "run")
	if err := os.MkdirAll(runDir, 0755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(runDir, "main.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	ret, err := runEdit(t, newEditSession(t, dir), "run/main.go", "@all", "package other")
	if err != nil {
		t.Fatalf("run/ 目录下的普通文件被劫持为终端输入: %v", err)
	}
	assertEditSucceeded(t, ret)

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "package other\n" {
		t.Fatalf("内容错误: %q", string(data))
	}
}

func TestWriteFile_RejectsOversizeFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "huge.txt")
	body := strings.Repeat("0123456789\n", trace.MaxFileSize/11+200)
	if err := os.WriteFile(file, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}

	ret, err := runEdit(t, newEditSession(t, dir), "huge.txt", "@all", "small")
	if err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	assertEditFailed(t, ret)

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != body {
		t.Fatal("超过 read 上限的文件不应被 edit 整体重写")
	}
}

func TestWriteFile_PreservesGBKEncoding(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "gbk.go")
	body := "// 中文注释：你好，世界\npackage main\n"
	raw, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, raw, 0644); err != nil {
		t.Fatal(err)
	}

	session := newEditSession(t, dir)
	// read 工具记录的是 GBK 解码后的 UTF-8 文本
	trace.ConfirmEditContent(session, "gbk.go", body)

	ret, err := runEdit(t, session, "gbk.go", "世界", "世界!")
	if err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	assertEditSucceeded(t, ret)

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	got, name := trace.DecodeText(data)
	if name != trace.EncodingGBK {
		t.Fatalf("GBK 文件被改写成了 %s: %q", name, got)
	}
	if got != "// 中文注释：你好，世界!\npackage main\n" {
		t.Fatalf("内容错误: %q", got)
	}
}

func TestWriteFile_ReplacesFileAtomically(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "atomic.txt")
	link := filepath.Join(dir, "atomic.link")
	if err := os.WriteFile(file, []byte("v1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(file, link); err != nil {
		t.Skipf("硬链接不可用: %v", err)
	}
	before, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	beforeLink, err := os.Stat(link)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, beforeLink) {
		t.Skip("硬链接未生效，无法验证 inode 替换")
	}

	ret, err := runEdit(t, newEditSession(t, dir), "atomic.txt", "@all", "v2")
	if err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	assertEditSucceeded(t, ret)

	after, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	afterLink, err := os.Stat(link)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(after, afterLink) {
		t.Fatal("写入不是同目录临时文件 + rename（原地截断会保留旧 inode，中断即半截文件）")
	}
	data, err := os.ReadFile(link)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "v1\n" {
		t.Fatalf("旧 inode 的内容被原地改写: %q", string(data))
	}
	// 临时文件必须已被 rename 消费或清理，不能残留在工作目录
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".alkaid0-edit-") {
			t.Fatalf("原子写入残留临时文件: %s", entry.Name())
		}
	}
}

// TestWriteFile_PreservesFileMode 回归：原子写入不能重置原文件权限。
func TestWriteFile_PreservesFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 只区分只读位，权限位断言不适用")
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(file, []byte("v1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0600); err != nil {
		t.Fatal(err)
	}

	ret, err := runEdit(t, newEditSession(t, dir), "secret.txt", "@all", "v2")
	if err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	assertEditSucceeded(t, ret)

	fi, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Fatalf("原文件权限 0600 被改成 %v", fi.Mode().Perm())
	}
}

func TestWriteFile_WritesThroughSymlinkWithoutReplacingIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires privileges on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "real.txt")
	link := filepath.Join(dir, "link.txt")
	if err := os.WriteFile(target, []byte("v1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.txt", link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	ret, err := runEdit(t, newEditSession(t, dir), "link.txt", "@all", "v2")
	if err != nil {
		t.Fatalf("writeFile: %v", err)
	}
	assertEditSucceeded(t, ret)

	// 原子 rename 会替换目标路径本身：写符号链接时必须落到链接目标，
	// 不能把工作区里的链接变成普通文件
	if fi, err := os.Lstat(link); err != nil {
		t.Fatal(err)
	} else if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("edit 把符号链接替换成了普通文件")
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "v2\n" {
		t.Fatalf("链接目标内容错误: %q", string(data))
	}
}

func TestCheckPath_RunPrefixStillValidated(t *testing.T) {
	// run/<n> 是终端输入的兼容写法，但 "run/" 同时是普通目录名：
	// 不能因为前缀匹配就跳过路径校验，否则 run/../x 会绕过 ".."
	if p, err := CheckPath(map[string]*any{"path": ptr("run/../secret.txt")}); err == nil {
		t.Fatalf("run/ 前缀不能绕过 '..' 校验, got %q", p)
	}
	if p, err := CheckPath(map[string]*any{"path": ptr("run/main.go")}); err != nil || p != "run/main.go" {
		t.Fatalf("普通 run/ 路径应正常通过: %q %v", p, err)
	}
	if p, err := CheckPath(map[string]*any{"path": ptr("@temp/run/3")}); err != nil || p != "@temp/run/3" {
		t.Fatalf("@temp/run/ 路径应直接放行: %q %v", p, err)
	}
}
