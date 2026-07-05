package actions

import (
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

const fsIOTimeout = 200 * time.Millisecond

// ---- Timeout helpers ----

type fsOpResult[T any] struct {
	val T
	err error
}

// fsOpWithTimeout 在超时保护下执行返回值的文件系统操作
func fsOpWithTimeout[T any](timeout time.Duration, op func() (T, error)) (T, error) {
	ch := make(chan fsOpResult[T], 1)
	go func() {
		val, err := op()
		ch <- fsOpResult[T]{val: val, err: err}
	}()
	select {
	case res := <-ch:
		return res.val, res.err
	case <-time.After(timeout):
		var zero T
		return zero, fmt.Errorf("filesystem operation timed out")
	}
}

// fsOpVoidWithTimeout 在超时保护下执行无返回值的文件系统操作
func fsOpVoidWithTimeout(timeout time.Duration, op func() error) error {
	ch := make(chan error, 1)
	go func() {
		ch <- op()
	}()
	select {
	case err := <-ch:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("filesystem operation timed out")
	}
}

// ---- Path validation ----

// validatePath 验证并解析项目内的相对路径
//   - 空路径表示项目根目录，返回 cwd 本身
//   - 拒绝绝对路径
//   - 拒绝包含 . 或 .. 分量的路径
//   - 拒绝 .alkaid0 目录的访问
//   - 使用 filepath.Clean 后确保仍在 cwd 内（防 path traversal）
func validatePath(cwd, relPath string) (string, error) {
	// 空路径表示根目录
	if relPath == "" {
		return cwd, nil
	}
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("path must be relative")
	}

	// 在 Clean 之前检查原始路径中的 . 和 .. 分量
	// ACP 协议使用 / 作为路径分隔符
	rawParts := strings.Split(relPath, "/")
	for _, part := range rawParts {
		if part == "." || part == ".." {
			return "", fmt.Errorf("path must not contain . or ..")
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

	return fullPath, nil
}

// ---- Permissions helper ----

// getPermissions 获取文件权限字符串（八进制格式）
func getPermissions(info fs.FileInfo) string {
	if runtime.GOOS == "windows" {
		if info.Mode().Perm()&0200 == 0 {
			return "0555"
		}
		return "0755"
	}
	return fmt.Sprintf("%o", info.Mode().Perm())
}

// ---- Ownership helper (platform-specific) ----

// getOwner 获取文件所有者的用户名
func getOwner(info fs.FileInfo) string {
	if runtime.GOOS != "windows" {
		// Unix: info.Sys() 返回 *syscall.Stat_t，包含 Uid
		// 在 fs_unix.go 中实现
		return getOwnerUnix(info)
	}
	// Windows：返回当前用户
	return getOwnerCurrentUser()
}

// getOwnerCurrentUser 返回当前用户名
func getOwnerCurrentUser() string {
	usr, err := user.Current()
	if err != nil {
		return ""
	}
	return usr.Username
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

	info, err := fsOpWithTimeout(fsIOTimeout, func() (fs.FileInfo, error) {
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

	owner, err := fsOpWithTimeout(fsIOTimeout, func() (string, error) {
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

// FsRead 读取文件或列出目录
func FsRead(req FsReadRequest, _ func(string, any, *string) error, _ uint64) (FsReadResponse, error) {
	if req.SessionID == "" {
		return FsReadResponse{}, fmt.Errorf("sessionId is required")
	}

	cwd, _, err := sessionID2Cwd(req.SessionID)
	if err != nil {
		return FsReadResponse{}, fmt.Errorf("invalid sessionId: %v", err)
	}

	fullPath, err := validatePath(cwd, req.Path)
	if err != nil {
		return FsReadResponse{}, err
	}

	info, err := fsOpWithTimeout(fsIOTimeout, func() (fs.FileInfo, error) {
		return os.Stat(fullPath)
	})
	if err != nil {
		return FsReadResponse{}, err
	}

	// 目录：列出内容
	if info.IsDir() {
		entries, err := fsOpWithTimeout(fsIOTimeout, func() ([]os.DirEntry, error) {
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
	_, err = fsOpWithTimeout(fsIOTimeout, func() (struct{}, error) {
		f, err := os.Open(fullPath)
		if err != nil {
			return struct{}{}, err
		}
		defer f.Close()

		if req.Offset > 0 {
			_, err = f.Seek(req.Offset, io.SeekStart)
			if err != nil {
				return struct{}{}, err
			}
		}

		if req.Length > 0 {
			data = make([]byte, req.Length)
			n, err := io.ReadFull(f, data)
			if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
				return struct{}{}, err
			}
			data = data[:n]
		} else {
			data, err = io.ReadAll(f)
			if err != nil {
				return struct{}{}, err
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

	// 确保父目录存在
	parentDir := filepath.Dir(fullPath)
	err = fsOpVoidWithTimeout(fsIOTimeout, func() error {
		return os.MkdirAll(parentDir, 0755)
	})
	if err != nil {
		return FsWriteResponse{}, fmt.Errorf("failed to create parent directory: %v", err)
	}

	// 写入文件
	var bytesWritten int64
	_, err = fsOpWithTimeout(fsIOTimeout, func() (struct{}, error) {
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

	err = fsOpVoidWithTimeout(fsIOTimeout, func() error {
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

	err = fsOpVoidWithTimeout(fsIOTimeout, func() error {
		return os.RemoveAll(fullPath)
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
		err = fsOpVoidWithTimeout(fsIOTimeout, func() error {
			if modeVal&0400 == 0 {
				// 禁止所有者读 → 设为只读
				return os.Chmod(fullPath, 0444)
			}
			return os.Chmod(fullPath, 0666)
		})
	} else {
		err = fsOpVoidWithTimeout(fsIOTimeout, func() error {
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

	err = fsOpVoidWithTimeout(fsIOTimeout, func() error {
		return os.Chown(fullPath, uid, gid)
	})
	if err != nil {
		return u.H{}, err
	}

	return u.H{}, nil
}
