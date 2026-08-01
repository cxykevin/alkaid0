package actions

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	u "github.com/cxykevin/alkaid0/utils"
)

const (
	fsIOTimeout        = 200 * time.Millisecond
	maxFileContentSize = 1 << 20 // 1 MiB
)

// ---- Timeout helpers ----

type fsOpResult[T any] struct {
	val T
	err error
}

// fsOpWithTimeout 在超时保护下执行返回值的文件系统操作。
// op 接收 ctx：长操作（大文件读取、递归删除）应在可中断处检查 ctx.Done()，
// 使超时后底层操作提前退出，而非继续运行到完成（避免 goroutine/fd 泄漏与"报错但磁盘被改"）。
func fsOpWithTimeout[T any](timeout time.Duration, op func(ctx context.Context) (T, error)) (T, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ch := make(chan fsOpResult[T], 1)
	go func() {
		val, err := op(ctx)
		ch <- fsOpResult[T]{val: val, err: err}
	}()
	select {
	case res := <-ch:
		return res.val, res.err
	case <-ctx.Done():
		var zero T
		return zero, fmt.Errorf("filesystem operation timed out")
	}
}

// fsOpVoidWithTimeout 在超时保护下执行无返回值的文件系统操作
func fsOpVoidWithTimeout(timeout time.Duration, op func(ctx context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ch := make(chan error, 1)
	go func() {
		ch <- op(ctx)
	}()
	select {
	case err := <-ch:
		return err
	case <-ctx.Done():
		return fmt.Errorf("filesystem operation timed out")
	}
}

// ctxReader 在每次 Read 前检查 context，使阻塞读取可随取消提前返回
type ctxReader struct {
	r   io.Reader
	ctx context.Context
}

func (cr *ctxReader) Read(p []byte) (int, error) {
	if err := cr.ctx.Err(); err != nil {
		return 0, err
	}
	return cr.r.Read(p)
}

// removeAllCtx 递归删除目录，在每层检查 ctx，超时/取消时提前退出。
// 相比 os.RemoveAll（无法中途取消），可避免"客户端已收到超时但后台仍在删改磁盘"。
func removeAllCtx(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		// 不是目录（或不可读）：按文件/空目录删除
		return os.Remove(path)
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		child := filepath.Join(path, e.Name())
		if e.IsDir() {
			if err := removeAllCtx(ctx, child); err != nil {
				return err
			}
		} else if err := os.Remove(child); err != nil {
			return err
		}
	}
	return os.Remove(path)
}

// ---- Path validation ----

// isBlockedPath 检查绝对路径是否被屏蔽访问
func isBlockedPath(path string) bool {
	cleaned := filepath.Clean(path)

	if runtime.GOOS == "windows" {
		// Windows 上大小写不敏感
		blockedUpper := strings.ToUpper(filepath.Clean(`C:\ProgramData`))
		cleanedUpper := strings.ToUpper(cleaned)
		if cleanedUpper == blockedUpper || strings.HasPrefix(cleanedUpper, blockedUpper+`\`) {
			return true
		}
		return false
	}

	// Unix/Linux: 屏蔽 /etc
	if cleaned == "/etc" || strings.HasPrefix(cleaned, "/etc/") {
		return true
	}
	return false
}

// validatePath 验证并解析路径
//   - cwd 非空时（有会话）：仅接受相对路径，验证防止路径穿越
//   - cwd 为空时（无会话）：仅接受绝对路径，检查屏蔽路径
func validatePath(cwd, relPath string) (string, error) {
	relPath = strings.ReplaceAll(relPath, "\\", "/")

	// 空路径
	if relPath == "" {
		if cwd == "" {
			return "", fmt.Errorf("path must not be empty")
		}
		return cwd, nil
	}

	if cwd == "" {
		// 无会话模式：仅接受绝对路径
		if !filepath.IsAbs(relPath) {
			return "", fmt.Errorf("absolute path is required when sessionId is not set")
		}
		fullPath := filepath.Clean(relPath)
		if isBlockedPath(fullPath) {
			return "", fmt.Errorf("access to %s is not allowed", fullPath)
		}
		return fullPath, nil
	}

	// 有会话模式：仅接受相对路径
	if relPath[0] == '/' {
		return "", fmt.Errorf("path must be relative")
	}

	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("path must be relative")
	}

	// 在 Clean 之前检查原始路径中的 . 和 .. 分量
	// ACP 协议使用 / 作为路径分隔符
	rawParts := strings.SplitSeq(relPath, "/")
	for part := range rawParts {
		if part == "." || part == ".." {
			return "", fmt.Errorf("path must not contain \".\" or \"..\"")
		}
	}

	cleaned := filepath.Clean(relPath)

	fullPath := filepath.Join(cwd, cleaned)

	// 确保仍在 cwd 内
	rel, err := filepath.Rel(cwd, fullPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path escapes the working directory")
	}

	// 阻止访问 .alkaid0 目录
	if strings.HasPrefix(rel, ".alkaid0") {
		return "", fmt.Errorf("access to .alkaid0 directory is not allowed")
	}

	// 解析符号链接，防止工作区内的 symlink 把读写/删除操作引到工作区外
	if err := validateNoSymlinkEscape(cwd, fullPath); err != nil {
		return "", err
	}

	return fullPath, nil
}

// validateNoSymlinkEscape 对 fullPath 及其已存在的最长前缀解析符号链接，
// 确认解析后的真实路径仍在 cwd 内，防止工作区内的 symlink（如 node_modules、
// venv、用户手动链接的文件）把操作引到 /etc 或用户主目录等工作区外位置。
func validateNoSymlinkEscape(cwd, fullPath string) error {
	realCwd, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		realCwd = filepath.Clean(cwd)
	}
	// 从 fullPath 向上逐级找已存在的前缀做解析检查
	checkPath := fullPath
	for {
		resolved, err := filepath.EvalSymlinks(checkPath)
		if err == nil {
			rel, relErr := filepath.Rel(realCwd, resolved)
			if relErr != nil || strings.HasPrefix(rel, "..") {
				return fmt.Errorf("path escapes the working directory via symlink")
			}
			return nil
		}
		parent := filepath.Dir(checkPath)
		if parent == checkPath {
			return nil
		}
		checkPath = parent
	}
}

// ---- Request/Response types ----

// FsCommonRequest 通用的路径操作请求（stat, mkdir, rm）
type FsCommonRequest struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
}

// FsStatResponse stat 响应
type FsStatResponse struct {
	Size        *int64 `json:"size,omitempty"`
	Permissions string `json:"permissions"`
	Owner       string `json:"owner"`
	Type        string `json:"type"` // "file" | "directory"
}

// FsReadRequest 读取文件/列出目录的请求
type FsReadRequest struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
	Binary    bool   `json:"binary,omitempty"`
	Offset    int64  `json:"offset,omitempty"`
	Length    int64  `json:"length,omitempty"`
}

// FsReadResponse 读取文件/列出目录的响应
type FsReadResponse struct {
	Content any `json:"content"` // string | []FsDirEntry
}

// FsDirEntry 目录条目
type FsDirEntry struct {
	Name string `json:"name"`
	Type string `json:"type"` // "file" | "directory"
	Size *int64 `json:"size,omitempty"`
}

// FsWriteRequest 写文件的请求
type FsWriteRequest struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
	Content   string `json:"content"`
	Binary    bool   `json:"binary,omitempty"`
	Append    bool   `json:"append,omitempty"`
}

// FsWriteResponse 写文件的响应
type FsWriteResponse struct {
	BytesWritten int64 `json:"bytesWritten"`
}

// FsChmodRequest 更改权限的请求
type FsChmodRequest struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
	Mode      string `json:"mode"`
}

// FsChownRequest 更改所有者的请求
type FsChownRequest struct {
	SessionID string `json:"sessionId"`
	Path      string `json:"path"`
	Owner     string `json:"owner"`
}

// ---- Handler functions ----

// FsStat 获取文件/文件夹信息
func FsStat(req FsCommonRequest, _ func(string, any, *string) error, _ uint64) (FsStatResponse, error) {
	if req.SessionID == "" {
		return FsStatResponse{}, fmt.Errorf("sessionId is required")
	}

	cwd, _, err := sessionID2Cwd(req.SessionID)
	if err != nil {
		return FsStatResponse{}, fmt.Errorf("invalid sessionId: %v", err)
	}

	fullPath, err := validatePath(cwd, req.Path)
	if err != nil {
		return FsStatResponse{}, err
	}

	info, err := fsOpWithTimeout(fsIOTimeout, func(ctx context.Context) (fs.FileInfo, error) {
		return os.Stat(fullPath)
	})
	if err != nil {
		return FsStatResponse{}, err
	}

	fileType := "file"
	if info.IsDir() {
		fileType = "directory"
	}

	var size *int64
	if !info.IsDir() {
		s := info.Size()
		size = &s
	}

	owner, err := fsOpWithTimeout(fsIOTimeout, func(ctx context.Context) (string, error) {
		return getOwner(info), nil
	})
	if err != nil {
		return FsStatResponse{}, err
	}

	return FsStatResponse{
		Size:        size,
		Permissions: getPermissions(info),
		Owner:       owner,
		Type:        fileType,
	}, nil
}

// FsRead 读取文件或列出目录。
// sessionId 可选：
//   - 设置 sessionId：仅接受相对路径，在会话工作目录内访问
//   - 不设置 sessionId：仅接受绝对路径，不能访问 /etc 或 C:\ProgramData
func FsRead(req FsReadRequest, _ func(string, any, *string) error, _ uint64) (FsReadResponse, error) {
	var cwd string
	if req.SessionID != "" {
		var err error
		cwd, _, err = sessionID2Cwd(req.SessionID)
		if err != nil {
			return FsReadResponse{}, fmt.Errorf("invalid sessionId: %v", err)
		}
	}

	fullPath, err := validatePath(cwd, req.Path)
	if err != nil {
		return FsReadResponse{}, err
	}

	info, err := fsOpWithTimeout(fsIOTimeout, func(ctx context.Context) (fs.FileInfo, error) {
		return os.Stat(fullPath)
	})
	if err != nil {
		return FsReadResponse{}, err
	}

	// 目录：列出内容
	if info.IsDir() {
		entries, err := fsOpWithTimeout(fsIOTimeout, func(ctx context.Context) ([]os.DirEntry, error) {
			return os.ReadDir(fullPath)
		})
		if err != nil {
			return FsReadResponse{}, err
		}

		dirList := make([]FsDirEntry, 0, len(entries))
		for _, entry := range entries {
			if entry.Name() == ".alkaid0" {
				continue
			}

			de := FsDirEntry{
				Name: entry.Name(),
				Type: "file",
			}
			if entry.IsDir() {
				de.Type = "directory"
			} else {
				finfo, err := entry.Info()
				if err == nil {
					s := finfo.Size()
					de.Size = &s
				}
			}
			dirList = append(dirList, de)
		}

		return FsReadResponse{Content: dirList}, nil
	}

	// 文件：读取内容
	var data []byte
	_, err = fsOpWithTimeout(fsIOTimeout, func(ctx context.Context) (struct{}, error) {
		f, err := os.Open(fullPath)
		if err != nil {
			return struct{}{}, err
		}
		defer f.Close()

		// 获取文件大小用于边界检查
		info, err := f.Stat()
		if err != nil {
			return struct{}{}, err
		}
		fileSize := info.Size()

		// offset 超出文件大小时静默返回空内容
		if req.Offset >= fileSize {
			return struct{}{}, nil
		}

		if req.Offset > 0 {
			_, err = f.Seek(req.Offset, io.SeekStart)
			if err != nil {
				return struct{}{}, err
			}
		}

		if req.Length > 0 {
			// 确保读范围不超出文件结尾，超出时静默截断
			maxRead := fileSize - req.Offset
			if req.Length > maxRead {
				req.Length = maxRead
			}
			data = make([]byte, req.Length)
			// ctx-aware 读取：每次 Read 前检查取消，超时提前退出
			n, err := io.ReadFull(&ctxReader{r: f, ctx: ctx}, data)
			if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
				return struct{}{}, err
			}
			data = data[:n]
		} else {
			// 未指定 length 时从 offset 读到文件结尾。
			// 限制最大读取量（对齐 maxFileContentSize），防止超大文件读入内存导致 OOM。
			limit := int64(maxFileContentSize) + 1
			data, err = io.ReadAll(&ctxReader{r: io.LimitReader(f, limit), ctx: ctx})
			if err != nil {
				return struct{}{}, err
			}
			if len(data) > maxFileContentSize {
				return struct{}{}, fmt.Errorf("file content exceeds %d bytes read limit", maxFileContentSize)
			}
		}
		return struct{}{}, nil
	})
	if err != nil {
		return FsReadResponse{}, err
	}

	var content any
	if req.Binary {
		content = base64.StdEncoding.EncodeToString(data)
	} else {
		content = string(data)
	}

	return FsReadResponse{Content: content}, nil
}

// FsWrite 写文件（不存在则创建，支持追加模式）
func FsWrite(req FsWriteRequest, _ func(string, any, *string) error, _ uint64) (FsWriteResponse, error) {
	if req.SessionID == "" {
		return FsWriteResponse{}, fmt.Errorf("sessionId is required")
	}

	cwd, _, err := sessionID2Cwd(req.SessionID)
	if err != nil {
		return FsWriteResponse{}, fmt.Errorf("invalid sessionId: %v", err)
	}

	fullPath, err := validatePath(cwd, req.Path)
	if err != nil {
		return FsWriteResponse{}, err
	}

	// 解码内容
	var content []byte
	if req.Binary {
		content, err = base64.StdEncoding.DecodeString(req.Content)
		if err != nil {
			return FsWriteResponse{}, fmt.Errorf("invalid base64 content: %v", err)
		}
	} else {
		content = []byte(req.Content)
	}

	// 内容大小限制：不超过 1 MiB（binary 模式按解码后的原始二进制数据计大小）
	if len(content) > maxFileContentSize {
		return FsWriteResponse{}, fmt.Errorf(
			"content exceeds maximum size of 1MB (got %d bytes)", len(content))
	}

	// 确保父目录存在
	parentDir := filepath.Dir(fullPath)
	err = fsOpVoidWithTimeout(fsIOTimeout, func(ctx context.Context) error {
		return os.MkdirAll(parentDir, 0755)
	})
	if err != nil {
		return FsWriteResponse{}, fmt.Errorf("failed to create parent directory: %v", err)
	}

	// 写入文件
	var bytesWritten int64
	_, err = fsOpWithTimeout(fsIOTimeout, func(ctx context.Context) (struct{}, error) {
		// 超时后不再执行写盘，避免"已报超时但磁盘被改"的不一致
		if err := ctx.Err(); err != nil {
			return struct{}{}, err
		}
		flag := os.O_CREATE | os.O_WRONLY
		if req.Append {
			flag |= os.O_APPEND
		} else {
			flag |= os.O_TRUNC
		}

		f, err := os.OpenFile(fullPath, flag, 0644)
		if err != nil {
			return struct{}{}, err
		}
		defer f.Close()

		n, err := f.Write(content)
		if err != nil {
			return struct{}{}, err
		}
		bytesWritten = int64(n)
		return struct{}{}, nil
	})
	if err != nil {
		return FsWriteResponse{}, err
	}

	return FsWriteResponse{BytesWritten: bytesWritten}, nil
}

// FsMkdir 递归创建目录
func FsMkdir(req FsCommonRequest, _ func(string, any, *string) error, _ uint64) (u.H, error) {
	if req.SessionID == "" {
		return u.H{}, fmt.Errorf("sessionId is required")
	}

	cwd, _, err := sessionID2Cwd(req.SessionID)
	if err != nil {
		return u.H{}, fmt.Errorf("invalid sessionId: %v", err)
	}

	fullPath, err := validatePath(cwd, req.Path)
	if err != nil {
		return u.H{}, err
	}

	err = fsOpVoidWithTimeout(fsIOTimeout, func(ctx context.Context) error {
		return os.MkdirAll(fullPath, 0755)
	})
	if err != nil {
		return u.H{}, err
	}

	return u.H{}, nil
}

// FsRm 递归删除文件或目录
func FsRm(req FsCommonRequest, _ func(string, any, *string) error, _ uint64) (u.H, error) {
	if req.SessionID == "" {
		return u.H{}, fmt.Errorf("sessionId is required")
	}

	cwd, _, err := sessionID2Cwd(req.SessionID)
	if err != nil {
		return u.H{}, fmt.Errorf("invalid sessionId: %v", err)
	}

	fullPath, err := validatePath(cwd, req.Path)
	if err != nil {
		return u.H{}, err
	}

	err = fsOpVoidWithTimeout(fsIOTimeout, func(ctx context.Context) error {
		// 可中断的递归删除：超时后提前退出，避免后台继续删改磁盘
		return removeAllCtx(ctx, fullPath)
	})
	if err != nil {
		return u.H{}, err
	}

	return u.H{}, nil
}

// FsChmod 更改文件权限
func FsChmod(req FsChmodRequest, _ func(string, any, *string) error, _ uint64) (u.H, error) {
	if req.SessionID == "" {
		return u.H{}, fmt.Errorf("sessionId is required")
	}
	if req.Mode == "" {
		return u.H{}, fmt.Errorf("mode is required")
	}

	cwd, _, err := sessionID2Cwd(req.SessionID)
	if err != nil {
		return u.H{}, fmt.Errorf("invalid sessionId: %v", err)
	}

	fullPath, err := validatePath(cwd, req.Path)
	if err != nil {
		return u.H{}, err
	}

	modeVal, err := strconv.ParseUint(req.Mode, 8, 32)
	if err != nil {
		return u.H{}, fmt.Errorf("invalid mode: %v", err)
	}

	if runtime.GOOS == "windows" {
		err = fsOpVoidWithTimeout(fsIOTimeout, func(ctx context.Context) error {
			if modeVal&0200 == 0 {
				// 禁止所有者写 → 设为只读
				return os.Chmod(fullPath, 0444)
			}
			return os.Chmod(fullPath, 0666)
		})
	} else {
		err = fsOpVoidWithTimeout(fsIOTimeout, func(ctx context.Context) error {
			return os.Chmod(fullPath, os.FileMode(modeVal))
		})
	}
	if err != nil {
		return u.H{}, err
	}

	return u.H{}, nil
}

// FsChown 更改文件所有者
func FsChown(req FsChownRequest, _ func(string, any, *string) error, _ uint64) (u.H, error) {
	if req.SessionID == "" {
		return u.H{}, fmt.Errorf("sessionId is required")
	}
	if req.Owner == "" {
		return u.H{}, fmt.Errorf("owner is required")
	}

	cwd, _, err := sessionID2Cwd(req.SessionID)
	if err != nil {
		return u.H{}, fmt.Errorf("invalid sessionId: %v", err)
	}

	fullPath, err := validatePath(cwd, req.Path)
	if err != nil {
		return u.H{}, err
	}

	usr, err := user.Lookup(req.Owner)
	if err != nil {
		return u.H{}, fmt.Errorf("failed to look up user: %v", err)
	}

	uid, err := strconv.Atoi(usr.Uid)
	if err != nil {
		return u.H{}, fmt.Errorf("invalid uid: %v", err)
	}

	gid, err := strconv.Atoi(usr.Gid)
	if err != nil {
		return u.H{}, fmt.Errorf("invalid gid: %v", err)
	}

	err = fsOpVoidWithTimeout(fsIOTimeout, func(ctx context.Context) error {
		return os.Chown(fullPath, uid, gid)
	})
	if err != nil {
		return u.H{}, err
	}

	return u.H{}, nil
}
