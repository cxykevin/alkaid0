package actions

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ---- validatePath 测试 ----

func TestValidatePath_Empty(t *testing.T) {
	// 空路径表示根目录，返回 cwd 本身
	got, err := validatePath("/tmp", "")
	if err != nil {
		t.Fatalf("unexpected error for empty path: %v", err)
	}
	if got != "/tmp" {
		t.Errorf("expected '/tmp', got %q", got)
	}
}

func TestValidatePath_Absolute(t *testing.T) {
	_, err := validatePath("/tmp", "/etc/passwd")
	if err == nil || !strings.Contains(err.Error(), "path must be relative") {
		t.Errorf("expected rejection of absolute path, got %v", err)
	}
}

func TestValidatePath_DotComponent(t *testing.T) {
	tmpDir := t.TempDir()
	tests := []struct {
		name string
		path string
	}{
		{"single dot", "."},
		{"dotdot", ".."},
		{"dot in middle", "foo/./bar"},
		{"dotdot in middle", "foo/../bar"},
		{"dotdot escape", "../../etc"},
		{"deep dotdot escape", "a/b/../../../../etc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validatePath(tmpDir, tt.path)
			if err == nil {
				t.Errorf("expected error for path %q, got nil", tt.path)
			}
		})
	}
}

func TestValidatePath_Alkaid0Blocked(t *testing.T) {
	tmpDir := t.TempDir()
	tests := []string{
		".alkaid0",
		".alkaid0/config.db",
		".alkaid0/sub/file.txt",
	}
	for _, p := range tests {
		t.Run(p, func(t *testing.T) {
			_, err := validatePath(tmpDir, p)
			if err == nil || !strings.Contains(err.Error(), ".alkaid0") {
				t.Errorf("expected .alkaid0 rejection for %q, got %v", p, err)
			}
		})
	}
}

func TestValidatePath_PathTraversal(t *testing.T) {
	// 测试 path traversal 绕过尝试
	tmpDir := t.TempDir()
	tests := []string{
		".." + string(filepath.Separator) + "etc",
		"foo/../../etc",
		"foo/../../.." + string(filepath.Separator) + "etc",
	}
	for _, p := range tests {
		t.Run(p, func(t *testing.T) {
			_, err := validatePath(tmpDir, p)
			if err == nil {
				t.Errorf("expected error for traversal path %q, got nil", p)
			}
		})
	}
}

func TestValidatePath_Valid(t *testing.T) {
	tmpDir := t.TempDir()
	tests := []struct {
		name string
		path string
	}{
		{"simple file", "file.txt"},
		{"nested dir", "a/b/c.txt"},
		{"with leading dots allowed", "file.name.with.dots"},
		{"single char", "a"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			full, err := validatePath(tmpDir, tt.path)
			if err != nil {
				t.Errorf("unexpected error for %q: %v", tt.path, err)
				return
			}
			expected := filepath.Join(tmpDir, tt.path)
			if full != expected {
				t.Errorf("expected path %q, got %q", expected, full)
			}
		})
	}
}

func TestValidatePath_InsideCwd(t *testing.T) {
	// 验证路径解析后仍在 cwd 内
	tmpDir := t.TempDir()
	innerDir := filepath.Join(tmpDir, "inner")
	os.MkdirAll(innerDir, 0755)

	// 正常路径应该工作
	full, err := validatePath(tmpDir, "inner/file.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(tmpDir, "inner/file.txt")
	if full != expected {
		t.Errorf("expected %q, got %q", expected, full)
	}
}

func TestValidatePath_NoCwd_AbsolutePath(t *testing.T) {
	// 无会话模式下，绝对路径应正常工作
	tmpDir := t.TempDir()
	full, err := validatePath("", tmpDir)
	if err != nil {
		t.Fatalf("unexpected error for absolute path without cwd: %v", err)
	}
	if full != tmpDir {
		t.Errorf("expected %q, got %q", tmpDir, full)
	}
}

func TestValidatePath_NoCwd_RelativeRejected(t *testing.T) {
	// 无会话模式下，相对路径应被拒绝
	_, err := validatePath("", "relative/path")
	if err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Errorf("expected rejection of relative path without cwd, got %v", err)
	}
}

func TestValidatePath_NoCwd_EmptyPath(t *testing.T) {
	// 无会话模式下，空路径应被拒绝
	_, err := validatePath("", "")
	if err == nil || !strings.Contains(err.Error(), "path must not be empty") {
		t.Errorf("expected rejection of empty path without cwd, got %v", err)
	}
}

func TestValidatePath_BlockedPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("blocked path test is platform-specific (Linux)")
	}
	// /etc 及其子路径应被屏蔽
	blockedTests := []string{
		"/etc",
		"/etc/passwd",
		"/etc/ssh/sshd_config",
		"/etc/",
	}
	for _, p := range blockedTests {
		t.Run(p, func(t *testing.T) {
			_, err := validatePath("", p)
			if err == nil || !strings.Contains(err.Error(), "not allowed") {
				t.Errorf("expected blocked path error for %q, got %v", p, err)
			}
		})
	}
	// 类似路径不应被屏蔽
	allowedTests := []string{
		"/etc2",
		"/etcabc",
		"/var",
		"/tmp",
	}
	for _, p := range allowedTests {
		t.Run(p, func(t *testing.T) {
			_, err := validatePath("", p)
			if err != nil {
				t.Errorf("expected allowed path %q, got error: %v", p, err)
			}
		})
	}
}

// ---- getPermissions 测试 ----

func TestGetPermissions_Format(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "perm_test.txt")
	os.WriteFile(testFile, []byte("test"), 0644)

	info, err := os.Stat(testFile)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}

	perm := getPermissions(info)
	if perm == "" {
		t.Errorf("expected non-empty permissions")
	}
	if runtime.GOOS != "windows" {
		// 在非 Windows 上应该以 0 开头（八进制格式）
		if len(perm) < 3 {
			t.Errorf("permissions too short: %q", perm)
		}
	} else {
		// Windows 上只映射到 0755 或 0555
		if perm != "0755" && perm != "0555" {
			t.Errorf("unexpected windows permission: %q", perm)
		}
	}
}

// ---- fsOpWithTimeout 测试 ----

func TestFsOpWithTimeout_Success(t *testing.T) {
	val, err := fsOpWithTimeout(1*time.Second, func() (int, error) {
		return 42, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if val != 42 {
		t.Errorf("expected 42, got %d", val)
	}
}

func TestFsOpWithTimeout_Error(t *testing.T) {
	_, err := fsOpWithTimeout(1*time.Second, func() (int, error) {
		return 0, fmt.Errorf("test error")
	})
	if err == nil || err.Error() != "test error" {
		t.Errorf("expected 'test error', got %v", err)
	}
}

func TestFsOpWithTimeout_Timeout(t *testing.T) {
	start := time.Now()
	_, err := fsOpWithTimeout(50*time.Millisecond, func() (int, error) {
		time.Sleep(500 * time.Millisecond) // 远超超时
		return 42, nil
	})
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout error, got %v", err)
	}
	if elapsed > 200*time.Millisecond {
		t.Logf("timeout took %v (expected ~50ms)", elapsed)
	}
}

func TestFsOpVoidWithTimeout_Success(t *testing.T) {
	err := fsOpVoidWithTimeout(1*time.Second, func() error {
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFsOpVoidWithTimeout_Timeout(t *testing.T) {
	err := fsOpVoidWithTimeout(50*time.Millisecond, func() error {
		time.Sleep(500 * time.Millisecond)
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout error, got %v", err)
	}
}

// ---- Handler 参数验证测试 ----

func TestFsValidation_EmptySessionID(t *testing.T) {
	// 所有写操作 handler 在 sessionId 为空时都应该返回错误
	// FsRead 是唯一允许 sessionId 为空的方法（仅读操作，绝对路径）
	handlers := []struct {
		name string
		call func() error
	}{
		{"stat", func() error { _, err := FsStat(FsCommonRequest{SessionID: ""}, nil, 1); return err }},
		{"write", func() error { _, err := FsWrite(FsWriteRequest{SessionID: ""}, nil, 1); return err }},
		{"mkdir", func() error { _, err := FsMkdir(FsCommonRequest{SessionID: ""}, nil, 1); return err }},
		{"rm", func() error { _, err := FsRm(FsCommonRequest{SessionID: ""}, nil, 1); return err }},
		{"chmod", func() error { _, err := FsChmod(FsChmodRequest{SessionID: ""}, nil, 1); return err }},
		{"chown", func() error { _, err := FsChown(FsChownRequest{SessionID: ""}, nil, 1); return err }},
	}
	for _, h := range handlers {
		t.Run(h.name, func(t *testing.T) {
			err := h.call()
			if err == nil || !strings.Contains(err.Error(), "sessionId") {
				t.Errorf("expected sessionId error, got %v", err)
			}
		})
	}
}

func TestFsValidation_InvalidSessionID(t *testing.T) {
	invalidID := "not_a_valid_session_id"
	handlers := []struct {
		name string
		call func() error
	}{
		{"stat", func() error { _, err := FsStat(FsCommonRequest{SessionID: invalidID, Path: "x"}, nil, 1); return err }},
		{"read", func() error { _, err := FsRead(FsReadRequest{SessionID: invalidID, Path: "x"}, nil, 1); return err }},
		{"write", func() error { _, err := FsWrite(FsWriteRequest{SessionID: invalidID, Path: "x"}, nil, 1); return err }},
		{"mkdir", func() error { _, err := FsMkdir(FsCommonRequest{SessionID: invalidID, Path: "x"}, nil, 1); return err }},
		{"rm", func() error { _, err := FsRm(FsCommonRequest{SessionID: invalidID, Path: "x"}, nil, 1); return err }},
		{"chmod", func() error {
			_, err := FsChmod(FsChmodRequest{SessionID: invalidID, Path: "x", Mode: "644"}, nil, 1)
			return err
		}},
		{"chown", func() error {
			_, err := FsChown(FsChownRequest{SessionID: invalidID, Path: "x", Owner: "root"}, nil, 1)
			return err
		}},
	}
	for _, h := range handlers {
		t.Run(h.name, func(t *testing.T) {
			err := h.call()
			if err == nil {
				t.Errorf("expected error for invalid session ID, got nil")
			}
		})
	}
}

func TestFsValidation_ChmodEmptyMode(t *testing.T) {
	tmpDir := t.TempDir()
	sessionID := cwd2SessionID(tmpDir, 1)
	_, err := FsChmod(FsChmodRequest{SessionID: sessionID, Path: "x", Mode: ""}, nil, 1)
	if err == nil || !strings.Contains(err.Error(), "mode") {
		t.Errorf("expected mode error, got %v", err)
	}
}

func TestFsValidation_ChownEmptyOwner(t *testing.T) {
	tmpDir := t.TempDir()
	sessionID := cwd2SessionID(tmpDir, 1)
	_, err := FsChown(FsChownRequest{SessionID: sessionID, Path: "x", Owner: ""}, nil, 1)
	if err == nil || !strings.Contains(err.Error(), "owner") {
		t.Errorf("expected owner error, got %v", err)
	}
}

func TestFsValidation_ChmodInvalidMode(t *testing.T) {
	tmpDir := t.TempDir()
	sessionID := cwd2SessionID(tmpDir, 1)
	_, err := FsChmod(FsChmodRequest{SessionID: sessionID, Path: "x", Mode: "invalid"}, nil, 1)
	if err == nil || !strings.Contains(err.Error(), "invalid mode") {
		t.Errorf("expected invalid mode error, got %v", err)
	}
}

// ---- FsStat 集成测试 ----

func TestFsStat_File(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	content := "hello world"
	os.WriteFile(testFile, []byte(content), 0644)

	sessionID := cwd2SessionID(tmpDir, 1)
	resp, err := FsStat(FsCommonRequest{SessionID: sessionID, Path: "test.txt"}, nil, 1)
	if err != nil {
		t.Fatalf("FsStat failed: %v", err)
	}
	if resp.Type != "file" {
		t.Errorf("expected type 'file', got %q", resp.Type)
	}
	if resp.Size == nil {
		t.Fatal("expected non-nil size for file")
	}
	if *resp.Size != int64(len(content)) {
		t.Errorf("expected size %d, got %d", len(content), *resp.Size)
	}
	if resp.Permissions == "" {
		t.Errorf("expected non-empty permissions")
	}
	if resp.Owner == "" {
		t.Errorf("expected non-empty owner")
	}
}

func TestFsStat_Directory(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "subdir")
	os.MkdirAll(subDir, 0755)

	sessionID := cwd2SessionID(tmpDir, 1)
	resp, err := FsStat(FsCommonRequest{SessionID: sessionID, Path: "subdir"}, nil, 1)
	if err != nil {
		t.Fatalf("FsStat failed: %v", err)
	}
	if resp.Type != "directory" {
		t.Errorf("expected type 'directory', got %q", resp.Type)
	}
	if resp.Size != nil {
		t.Errorf("expected nil size for directory, got %d", *resp.Size)
	}
}

func TestFsStat_NonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	sessionID := cwd2SessionID(tmpDir, 1)
	_, err := FsStat(FsCommonRequest{SessionID: sessionID, Path: "nonexistent.txt"}, nil, 1)
	if err == nil {
		t.Errorf("expected error for non-existent path")
	}
}

func TestFsStat_RootDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	sessionID := cwd2SessionID(tmpDir, 1)
	resp, err := FsStat(FsCommonRequest{SessionID: sessionID, Path: ""}, nil, 1)
	if err != nil {
		t.Fatalf("FsStat(root) failed: %v", err)
	}
	if resp.Type != "directory" {
		t.Errorf("expected type 'directory' for root, got %q", resp.Type)
	}
	if resp.Size != nil {
		t.Errorf("expected nil size for directory, got %d", *resp.Size)
	}
}

// ---- FsRead 集成测试 ----

func TestFsRead_File(t *testing.T) {
	tmpDir := t.TempDir()
	content := "hello world"
	os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte(content), 0644)

	sessionID := cwd2SessionID(tmpDir, 1)
	resp, err := FsRead(FsReadRequest{SessionID: sessionID, Path: "test.txt"}, nil, 1)
	if err != nil {
		t.Fatalf("FsRead failed: %v", err)
	}
	str, ok := resp.Content.(string)
	if !ok {
		t.Fatalf("expected string content, got %T", resp.Content)
	}
	if str != content {
		t.Errorf("expected %q, got %q", content, str)
	}
}

func TestFsRead_WithOffset(t *testing.T) {
	tmpDir := t.TempDir()
	content := "hello world"
	os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte(content), 0644)

	sessionID := cwd2SessionID(tmpDir, 1)
	resp, err := FsRead(FsReadRequest{SessionID: sessionID, Path: "test.txt", Offset: 6}, nil, 1)
	if err != nil {
		t.Fatalf("FsRead failed: %v", err)
	}
	str, ok := resp.Content.(string)
	if !ok {
		t.Fatalf("expected string content, got %T", resp.Content)
	}
	if str != "world" {
		t.Errorf("expected 'world', got %q", str)
	}
}

func TestFsRead_WithLength(t *testing.T) {
	tmpDir := t.TempDir()
	content := "hello world this is a test"
	os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte(content), 0644)

	sessionID := cwd2SessionID(tmpDir, 1)
	resp, err := FsRead(FsReadRequest{SessionID: sessionID, Path: "test.txt", Offset: 6, Length: 5}, nil, 1)
	if err != nil {
		t.Fatalf("FsRead failed: %v", err)
	}
	str, ok := resp.Content.(string)
	if !ok {
		t.Fatalf("expected string content, got %T", resp.Content)
	}
	if str != "world" {
		t.Errorf("expected 'world', got %q", str)
	}
}

func TestFsRead_OffsetPastEnd(t *testing.T) {
	tmpDir := t.TempDir()
	content := "hi"
	os.WriteFile(filepath.Join(tmpDir, "test.txt"), []byte(content), 0644)

	sessionID := cwd2SessionID(tmpDir, 1)
	resp, err := FsRead(FsReadRequest{SessionID: sessionID, Path: "test.txt", Offset: 100}, nil, 1)
	if err != nil {
		t.Fatalf("FsRead failed: %v", err)
	}
	str, ok := resp.Content.(string)
	if !ok {
		t.Fatalf("expected string content, got %T", resp.Content)
	}
	if str != "" {
		t.Errorf("expected empty string, got %q", str)
	}
}

func TestFsRead_Binary(t *testing.T) {
	tmpDir := t.TempDir()
	// 写入二进制数据（包含 null 字节和不可打印字符）
	binaryData := []byte{0x00, 0x01, 0x02, 0xFF, 0xFE, 0x7F}
	os.WriteFile(filepath.Join(tmpDir, "binary.bin"), binaryData, 0644)

	sessionID := cwd2SessionID(tmpDir, 1)
	resp, err := FsRead(FsReadRequest{SessionID: sessionID, Path: "binary.bin", Binary: true}, nil, 1)
	if err != nil {
		t.Fatalf("FsRead failed: %v", err)
	}
	str, ok := resp.Content.(string)
	if !ok {
		t.Fatalf("expected string content, got %T", resp.Content)
	}
	if str == "" {
		t.Errorf("expected non-empty base64 string")
	}
	// base64("AAEC//5/") should be valid
	if len(str) == 0 {
		t.Errorf("expected non-empty base64 content")
	}
}

func TestFsRead_Directory(t *testing.T) {
	tmpDir := t.TempDir()
	// 创建文件结构
	os.WriteFile(filepath.Join(tmpDir, "a.txt"), []byte("aaa"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "b.txt"), []byte("bbb"), 0644)
	os.MkdirAll(filepath.Join(tmpDir, "subdir"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "subdir", "c.txt"), []byte("ccc"), 0644)
	// 创建 .alkaid0 目录（应该被过滤掉）
	os.MkdirAll(filepath.Join(tmpDir, ".alkaid0"), 0755)

	sessionID := cwd2SessionID(tmpDir, 1)
	resp, err := FsRead(FsReadRequest{SessionID: sessionID, Path: ""}, nil, 1)
	if err != nil {
		t.Fatalf("FsRead failed: %v", err)
	}
	entries, ok := resp.Content.([]FsDirEntry)
	if !ok {
		t.Fatalf("expected []FsDirEntry, got %T", resp.Content)
	}

	// 验证 .alkaid0 被过滤
	for _, e := range entries {
		if e.Name == ".alkaid0" {
			t.Errorf(".alkaid0 should be filtered out")
		}
	}

	// 验证 a.txt 和 b.txt 存在
	found := map[string]bool{}
	for _, e := range entries {
		found[e.Name] = true
		if e.Name == "a.txt" {
			if e.Type != "file" {
				t.Errorf("a.txt type should be 'file', got %q", e.Type)
			}
			if e.Size == nil || *e.Size != 3 {
				t.Errorf("a.txt size should be 3, got %v", e.Size)
			}
		}
		if e.Name == "subdir" {
			if e.Type != "directory" {
				t.Errorf("subdir type should be 'directory', got %q", e.Type)
			}
			if e.Size != nil {
				t.Errorf("subdir size should be nil, got %d", *e.Size)
			}
		}
	}
	if !found["a.txt"] {
		t.Errorf("a.txt not found in directory listing")
	}
	if !found["b.txt"] {
		t.Errorf("b.txt not found in directory listing")
	}
	if !found["subdir"] {
		t.Errorf("subdir not found in directory listing")
	}
}

func TestFsRead_NestedPath(t *testing.T) {
	tmpDir := t.TempDir()
	nestedDir := filepath.Join(tmpDir, "a", "b")
	os.MkdirAll(nestedDir, 0755)
	os.WriteFile(filepath.Join(nestedDir, "c.txt"), []byte("nested"), 0644)

	sessionID := cwd2SessionID(tmpDir, 1)
	resp, err := FsRead(FsReadRequest{SessionID: sessionID, Path: "a/b/c.txt"}, nil, 1)
	if err != nil {
		t.Fatalf("FsRead failed: %v", err)
	}
	str, ok := resp.Content.(string)
	if !ok {
		t.Fatalf("expected string content, got %T", resp.Content)
	}
	if str != "nested" {
		t.Errorf("expected 'nested', got %q", str)
	}
}

// ---- FsRead 无会话模式测试 ----

func TestFsRead_NoSession_AbsolutePath(t *testing.T) {
	// 不设置 sessionId，使用绝对路径读取文件
	tmpDir := t.TempDir()
	content := "no-session read test"
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte(content), 0644)

	resp, err := FsRead(FsReadRequest{Path: testFile}, nil, 1)
	if err != nil {
		t.Fatalf("FsRead without sessionId failed: %v", err)
	}
	str, ok := resp.Content.(string)
	if !ok {
		t.Fatalf("expected string content, got %T", resp.Content)
	}
	if str != content {
		t.Errorf("expected %q, got %q", content, str)
	}
}

func TestFsRead_NoSession_AbsolutePath_Binary(t *testing.T) {
	tmpDir := t.TempDir()
	binaryData := []byte{0x00, 0x01, 0x02, 0xFF}
	testFile := filepath.Join(tmpDir, "binary.bin")
	os.WriteFile(testFile, binaryData, 0644)

	resp, err := FsRead(FsReadRequest{Path: testFile, Binary: true}, nil, 1)
	if err != nil {
		t.Fatalf("FsRead without sessionId (binary) failed: %v", err)
	}
	str, ok := resp.Content.(string)
	if !ok {
		t.Fatalf("expected string content, got %T", resp.Content)
	}
	if str == "" {
		t.Errorf("expected non-empty base64 content")
	}
}

func TestFsRead_NoSession_AbsolutePath_WithOffset(t *testing.T) {
	tmpDir := t.TempDir()
	content := "hello world"
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte(content), 0644)

	resp, err := FsRead(FsReadRequest{Path: testFile, Offset: 6}, nil, 1)
	if err != nil {
		t.Fatalf("FsRead without sessionId (offset) failed: %v", err)
	}
	str, ok := resp.Content.(string)
	if !ok {
		t.Fatalf("expected string content, got %T", resp.Content)
	}
	if str != "world" {
		t.Errorf("expected 'world', got %q", str)
	}
}

func TestFsRead_NoSession_RelativePath(t *testing.T) {
	// 不设置 sessionId 时，相对路径应被拒绝
	_, err := FsRead(FsReadRequest{Path: "relative.txt"}, nil, 1)
	if err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Errorf("expected error for relative path without sessionId, got %v", err)
	}
}

func TestFsRead_NoSession_BlockedPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("blocked path test is platform-specific (Linux)")
	}
	// 不允许读取 /etc 下的文件
	_, err := FsRead(FsReadRequest{Path: "/etc/passwd"}, nil, 1)
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("expected blocked path error, got %v", err)
	}
}

func TestFsRead_NoSession_EmptyPath(t *testing.T) {
	// 不设置 sessionId 且 path 为空应报错
	_, err := FsRead(FsReadRequest{Path: ""}, nil, 1)
	if err == nil {
		t.Errorf("expected error for empty path without sessionId")
	}
}

func TestFsRead_NoSession_Directory(t *testing.T) {
	// 不设置 sessionId，使用绝对路径列出目录
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "a.txt"), []byte("aaa"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "b.txt"), []byte("bbb"), 0644)

	resp, err := FsRead(FsReadRequest{Path: tmpDir}, nil, 1)
	if err != nil {
		t.Fatalf("FsRead directory without sessionId failed: %v", err)
	}
	entries, ok := resp.Content.([]FsDirEntry)
	if !ok {
		t.Fatalf("expected []FsDirEntry, got %T", resp.Content)
	}
	found := map[string]bool{}
	for _, e := range entries {
		found[e.Name] = true
	}
	if !found["a.txt"] || !found["b.txt"] {
		t.Errorf("expected a.txt and b.txt in directory listing, got %+v", entries)
	}
}

func TestFsRead_WithSession_AbsolutePath(t *testing.T) {
	// 有 sessionId 时绝对路径应被拒绝（即使是读操作）
	tmpDir := t.TempDir()
	sessionID := cwd2SessionID(tmpDir, 1)
	_, err := FsRead(FsReadRequest{SessionID: sessionID, Path: "/tmp"}, nil, 1)
	if err == nil || !strings.Contains(err.Error(), "path must be relative") {
		t.Errorf("expected rejection of absolute path with sessionId, got %v", err)
	}
}

// ---- FsWrite 集成测试 ----

func TestFsWrite_NewFile(t *testing.T) {
	tmpDir := t.TempDir()
	sessionID := cwd2SessionID(tmpDir, 1)

	resp, err := FsWrite(FsWriteRequest{
		SessionID: sessionID,
		Path:      "new.txt",
		Content:   "hello",
	}, nil, 1)
	if err != nil {
		t.Fatalf("FsWrite failed: %v", err)
	}
	if resp.BytesWritten != 5 {
		t.Errorf("expected 5 bytes written, got %d", resp.BytesWritten)
	}

	// 验证文件实际已写入
	data, _ := os.ReadFile(filepath.Join(tmpDir, "new.txt"))
	if string(data) != "hello" {
		t.Errorf("expected 'hello', got %q", string(data))
	}
}

func TestFsWrite_Overwrite(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "data.txt"), []byte("old"), 0644)

	sessionID := cwd2SessionID(tmpDir, 1)
	_, err := FsWrite(FsWriteRequest{
		SessionID: sessionID,
		Path:      "data.txt",
		Content:   "new",
	}, nil, 1)
	if err != nil {
		t.Fatalf("FsWrite failed: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(tmpDir, "data.txt"))
	if string(data) != "new" {
		t.Errorf("expected 'new', got %q", string(data))
	}
}

func TestFsWrite_Append(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "log.txt"), []byte("first\n"), 0644)

	sessionID := cwd2SessionID(tmpDir, 1)
	resp, err := FsWrite(FsWriteRequest{
		SessionID: sessionID,
		Path:      "log.txt",
		Content:   "second\n",
		Append:    true,
	}, nil, 1)
	if err != nil {
		t.Fatalf("FsWrite failed: %v", err)
	}
	if resp.BytesWritten != 7 {
		t.Errorf("expected 7 bytes written, got %d", resp.BytesWritten)
	}

	data, _ := os.ReadFile(filepath.Join(tmpDir, "log.txt"))
	if string(data) != "first\nsecond\n" {
		t.Errorf("expected 'first\\nsecond\\n', got %q", string(data))
	}
}

func TestFsWrite_AutoCreateParentDir(t *testing.T) {
	tmpDir := t.TempDir()
	sessionID := cwd2SessionID(tmpDir, 1)

	_, err := FsWrite(FsWriteRequest{
		SessionID: sessionID,
		Path:      "a/b/c/d.txt",
		Content:   "deep",
	}, nil, 1)
	if err != nil {
		t.Fatalf("FsWrite failed: %v", err)
	}

	// 验证目录已自动创建
	data, _ := os.ReadFile(filepath.Join(tmpDir, "a/b/c/d.txt"))
	if string(data) != "deep" {
		t.Errorf("expected 'deep', got %q", string(data))
	}
}

func TestFsWrite_Binary(t *testing.T) {
	tmpDir := t.TempDir()
	sessionID := cwd2SessionID(tmpDir, 1)

	// base64("hello\x00world") = "aGVsbG8Ad29ybGQ="
	resp, err := FsWrite(FsWriteRequest{
		SessionID: sessionID,
		Path:      "binary.bin",
		Content:   "aGVsbG8Ad29ybGQ=",
		Binary:    true,
	}, nil, 1)
	if err != nil {
		t.Fatalf("FsWrite failed: %v", err)
	}
	if resp.BytesWritten != 11 {
		t.Errorf("expected 11 bytes written, got %d", resp.BytesWritten)
	}

	data, _ := os.ReadFile(filepath.Join(tmpDir, "binary.bin"))
	expected := []byte("hello\x00world")
	if len(data) != len(expected) {
		t.Fatalf("expected %d bytes, got %d", len(expected), len(data))
	}
	for i := range expected {
		if data[i] != expected[i] {
			t.Errorf("byte %d: expected %02x, got %02x", i, expected[i], data[i])
		}
	}
}

func TestFsWrite_InvalidBase64(t *testing.T) {
	tmpDir := t.TempDir()
	sessionID := cwd2SessionID(tmpDir, 1)

	_, err := FsWrite(FsWriteRequest{
		SessionID: sessionID,
		Path:      "bad.bin",
		Content:   "not-valid-base64!!!",
		Binary:    true,
	}, nil, 1)
	if err == nil || !strings.Contains(err.Error(), "base64") {
		t.Errorf("expected base64 error, got %v", err)
	}
}

// ---- FsMkdir 集成测试 ----

func TestFsMkdir(t *testing.T) {
	tmpDir := t.TempDir()
	sessionID := cwd2SessionID(tmpDir, 1)

	_, err := FsMkdir(FsCommonRequest{SessionID: sessionID, Path: "newdir"}, nil, 1)
	if err != nil {
		t.Fatalf("FsMkdir failed: %v", err)
	}

	info, err := os.Stat(filepath.Join(tmpDir, "newdir"))
	if err != nil {
		t.Fatalf("stat newdir failed: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("newdir should be a directory")
	}
}

func TestFsMkdir_Recursive(t *testing.T) {
	tmpDir := t.TempDir()
	sessionID := cwd2SessionID(tmpDir, 1)

	_, err := FsMkdir(FsCommonRequest{SessionID: sessionID, Path: "a/b/c/d/e"}, nil, 1)
	if err != nil {
		t.Fatalf("FsMkdir recursive failed: %v", err)
	}

	info, err := os.Stat(filepath.Join(tmpDir, "a/b/c/d/e"))
	if err != nil {
		t.Fatalf("stat nested dir failed: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("should be a directory")
	}
}

// ---- FsRm 集成测试 ----

func TestFsRm_File(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "delete_me.txt")
	os.WriteFile(testFile, []byte("bye"), 0644)

	sessionID := cwd2SessionID(tmpDir, 1)
	_, err := FsRm(FsCommonRequest{SessionID: sessionID, Path: "delete_me.txt"}, nil, 1)
	if err != nil {
		t.Fatalf("FsRm failed: %v", err)
	}

	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Errorf("file should be deleted")
	}
}

func TestFsRm_Directory(t *testing.T) {
	tmpDir := t.TempDir()
	os.MkdirAll(filepath.Join(tmpDir, "mydir", "sub"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "mydir", "sub", "f.txt"), []byte("x"), 0644)

	sessionID := cwd2SessionID(tmpDir, 1)
	_, err := FsRm(FsCommonRequest{SessionID: sessionID, Path: "mydir"}, nil, 1)
	if err != nil {
		t.Fatalf("FsRm failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(tmpDir, "mydir")); !os.IsNotExist(err) {
		t.Errorf("directory should be deleted")
	}
}

// ---- FsChmod 集成测试 ----

func TestFsChmod(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod behavior differs on Windows")
	}

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "script.sh")
	os.WriteFile(testFile, []byte("#!/bin/sh"), 0644)

	sessionID := cwd2SessionID(tmpDir, 1)
	_, err := FsChmod(FsChmodRequest{
		SessionID: sessionID,
		Path:      "script.sh",
		Mode:      "0755",
	}, nil, 1)
	if err != nil {
		t.Fatalf("FsChmod failed: %v", err)
	}

	info, _ := os.Stat(testFile)
	perm := info.Mode().Perm()
	// 验证可执行位已被设置
	if perm&0100 == 0 {
		t.Errorf("expected executable bit to be set, got %o", perm)
	}
}

// ---- FsChown 集成测试 ----

func TestFsChown_NonExistentUser(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("test"), 0644)

	sessionID := cwd2SessionID(tmpDir, 1)
	_, err := FsChown(FsChownRequest{
		SessionID: sessionID,
		Path:      "test.txt",
		Owner:     "this_user_does_not_exist_12345",
	}, nil, 1)
	if err == nil {
		t.Errorf("expected error for non-existent user")
	}
}

// ---- Session ID 与 handler 的集成 ----

func TestFsRoundTrip(t *testing.T) {
	// 完整测试：mkdir → write → stat → read → chmod → rm
	tmpDir := t.TempDir()
	sessionID := cwd2SessionID(tmpDir, 42)

	// 1. mkdir
	_, err := FsMkdir(FsCommonRequest{SessionID: sessionID, Path: "testdir"}, nil, 1)
	if err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	// 2. write
	_, err = FsWrite(FsWriteRequest{
		SessionID: sessionID,
		Path:      "testdir/hello.txt",
		Content:   "Hello, Filesystem ACP!",
	}, nil, 1)
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}

	// 3. stat
	statResp, err := FsStat(FsCommonRequest{SessionID: sessionID, Path: "testdir/hello.txt"}, nil, 1)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if *statResp.Size != 22 {
		t.Errorf("expected size 22, got %d", *statResp.Size)
	}

	// 4. read
	readResp, err := FsRead(FsReadRequest{SessionID: sessionID, Path: "testdir/hello.txt"}, nil, 1)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if readResp.Content.(string) != "Hello, Filesystem ACP!" {
		t.Errorf("unexpected content: %q", readResp.Content.(string))
	}

	// 5. read directory listing
	dirResp, err := FsRead(FsReadRequest{SessionID: sessionID, Path: "testdir"}, nil, 1)
	if err != nil {
		t.Fatalf("read dir failed: %v", err)
	}
	entries := dirResp.Content.([]FsDirEntry)
	if len(entries) != 1 || entries[0].Name != "hello.txt" {
		t.Errorf("unexpected directory listing: %+v", entries)
	}

	// 6. rm
	_, err = FsRm(FsCommonRequest{SessionID: sessionID, Path: "testdir"}, nil, 1)
	if err != nil {
		t.Fatalf("rm failed: %v", err)
	}

	// 7. verify deleted
	_, err = os.Stat(filepath.Join(tmpDir, "testdir"))
	if !os.IsNotExist(err) {
		t.Errorf("testdir should be deleted")
	}
}

func TestFs_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "empty.txt")
	os.WriteFile(testFile, []byte{}, 0644)

	sessionID := cwd2SessionID(tmpDir, 1)

	// stat should have size=0 (non-nil pointer)
	resp, err := FsStat(FsCommonRequest{SessionID: sessionID, Path: "empty.txt"}, nil, 1)
	if err != nil {
		t.Fatalf("FsStat failed: %v", err)
	}
	if resp.Size == nil {
		t.Errorf("expected non-nil size for empty file, got nil")
	} else if *resp.Size != 0 {
		t.Errorf("expected size 0, got %d", *resp.Size)
	}

	// read should return empty string
	readResp, err := FsRead(FsReadRequest{SessionID: sessionID, Path: "empty.txt"}, nil, 1)
	if err != nil {
		t.Fatalf("FsRead failed: %v", err)
	}
	if readResp.Content.(string) != "" {
		t.Errorf("expected empty content, got %q", readResp.Content.(string))
	}
}

func TestFs_PathSecurity(t *testing.T) {
	// 确保无法通过任何方式访问 cwd 外的文件
	tmpDir := t.TempDir()
	sessionID := cwd2SessionID(tmpDir, 1)

	// 创建外部文件
	outsideFile := filepath.Join(os.TempDir(), "alkaid0_fs_test_outside.txt")
	os.WriteFile(outsideFile, []byte("should not be accessible"), 0644)
	defer os.Remove(outsideFile)

	// 尝试通过 path traversal 访问
	_, err := FsStat(FsCommonRequest{SessionID: sessionID, Path: fmt.Sprintf("..%s..%s..%stmp%salkaid0_fs_test_outside.txt",
		string(filepath.Separator), string(filepath.Separator), string(filepath.Separator), string(filepath.Separator))}, nil, 1)
	if err == nil {
		t.Errorf("path traversal should be blocked")
	}
}
