// Package file 提供 move / copy / link 三种文件操作。
//
// 语义要点：
//   - src 可以是虚拟对象（@task/@memory/@tree 等）：其文本内容写入 dst；
//   - dst=@trash（仅 move）删除 src；
//   - dst=@temp/run/<id> 把 src 的整个文件内容喂给运行中的终端（不发送 EOF）；
//   - link 只接受真实路径，src/dst 都不能是虚拟对象、@trash 或终端；
//   - copy 大文件优先 CoW（FICLONE 等），回退到分块复制并通过
//     session.PushToolProgress 汇报进度（客户端 tool_call_update in_progress）。
package file

import (
	_ "embed" // embed
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/terminal/sandbox"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/index"
	"github.com/cxykevin/alkaid0/tools/toolobj"
	"github.com/cxykevin/alkaid0/tools/tools/edit"
	runTool "github.com/cxykevin/alkaid0/tools/tools/run"
	"github.com/cxykevin/alkaid0/tools/tools/trace"
	"github.com/cxykevin/alkaid0/tools/tools/tree/ios"
	u "github.com/cxykevin/alkaid0/utils"
)

const (
	toolName      = "file"
	trashPath     = "@trash"
	runPathPrefix = "@temp/run/"

	// terminalChunkSize 写入终端的块大小，避免大文件一次性读进内存。
	terminalChunkSize = 1024 * 1024
	// progressMinBytes 低于该大小的复制不推送进度（避免小文件刷屏）。
	progressMinBytes = 8 * 1024 * 1024
)

//go:embed prompt.md
var prompt string

var logger = log.New("tools:file")

var paras = map[string]parser.ToolParameters{
	"type": {
		Type:        parser.ToolTypeString,
		Required:    true,
		Description: `Operation: "move", "copy" or "link"`,
	},
	"src": {
		Type:        parser.ToolTypeString,
		Required:    true,
		Description: "Source path relative to the workspace, or a virtual object (e.g. @task, @memory, @tree, @cron). Virtual sources are written to dst as text",
	},
	"dst": {
		Type:        parser.ToolTypeString,
		Required:    true,
		Description: `Destination path relative to the workspace. For move only, "@trash" deletes src. "@temp/run/<id>" sends the whole file to that running terminal without EOF. dst cannot be a virtual object`,
	},
}

func load() string {
	actions.AddTool(&toolobj.Tools{
		Scope:           "", // Global Tools
		Name:            toolName,
		UserDescription: prompt,
		Parameters:      paras,
		ID:              toolName,
	})
	if err := actions.HookTool(toolName, &toolobj.Hook{
		Scope: "",
		// OnHook 为 nil：ExecToolOnHook 的兜底逻辑按统一格式补 calling_info。
		PostHook: toolobj.PostHookFunction{
			Priority: 100,
			Func:     runFile,
		},
	}); err != nil {
		panic(err)
	}
	return toolName
}

func init() {
	index.AddIndex(load)
}

// runFile 执行 move / copy / link。
func runFile(session *structs.Chats, args map[string]*any, cross []*any) (bool, []*any, map[string]*any, error) {
	if session == nil {
		return fail(cross, "session is not available")
	}
	if err := session.GetContext().Err(); err != nil {
		return fail(cross, "file operation cancelled: "+err.Error())
	}

	typ, err := requiredString(args, "type")
	if err != nil {
		return fail(cross, err.Error())
	}
	op := strings.ToLower(strings.TrimSpace(typ))
	if op != "move" && op != "copy" && op != "link" {
		return fail(cross, fmt.Sprintf(`unknown type %q: expected "move", "copy" or "link"`, typ))
	}
	src, err := requiredString(args, "src")
	if err != nil {
		return fail(cross, err.Error())
	}
	dst, err := requiredString(args, "dst")
	if err != nil {
		return fail(cross, err.Error())
	}
	if src == trashPath {
		return fail(cross, "src cannot be @trash")
	}

	workspace, err := filepath.Abs(filepath.Join(session.Root, session.CurrentActivatePath))
	if err != nil {
		return fail(cross, "failed to resolve workspace: "+err.Error())
	}

	srcVirtual := trace.IsProviderPath(src)
	dstIsTrash := dst == trashPath
	dstIsTerminal := isTerminalDst(workspace, dst)

	// 逐项校验 dst 与 type 的组合，错误信息在运行时返回（提示词不展开 @trash+虚拟源的限制）。
	if op == "link" {
		if srcVirtual {
			return fail(cross, "link does not support a virtual source path")
		}
		if dstIsTrash || dstIsTerminal || trace.IsProviderPath(dst) {
			return fail(cross, "link destination must be a real file path (not @trash, a terminal or a virtual object)")
		}
	} else {
		if dstIsTrash {
			if op != "move" {
				return fail(cross, `@trash is only supported when type="move"`)
			}
			if srcVirtual {
				return fail(cross, "@trash cannot be used with a virtual source path")
			}
		}
		if !dstIsTrash && !dstIsTerminal && trace.IsProviderPath(dst) {
			return fail(cross, "dst cannot be a virtual object; only @trash (move only) and @temp/run/<id> are accepted")
		}
	}

	// 虚拟源：把它当前的文本内容写到 dst（或终端）。虚拟对象本身没有实体可删除，
	// 因此 move 与 copy 行为一致。
	if srcVirtual {
		content, ok := trace.ProviderContent(session, src)
		if !ok || content == "" {
			return fail(cross, fmt.Sprintf("virtual source %q is empty or unavailable", src))
		}
		if dstIsTerminal {
			if err := runTool.Default.WriteRunStdin(workspace, session.ID, dst, []byte(content)); err != nil {
				return fail(cross, "failed to write to terminal: "+err.Error())
			}
			return successResult(cross, op, src, dst, "virtual content written to terminal", int64(len(content)))
		}
		dstAbs, err := resolveWorkspacePath(workspace, dst, false)
		if err != nil {
			return fail(cross, err.Error())
		}
		if err := writeTextFile(dstAbs, content); err != nil {
			return fail(cross, "failed to write destination: "+err.Error())
		}
		return successResult(cross, op, src, dst, "virtual content written to file", int64(len(content)))
	}

	srcAbs, err := resolveWorkspacePath(workspace, src, true)
	if err != nil {
		return fail(cross, err.Error())
	}
	linkInfo, err := os.Lstat(srcAbs)
	if err != nil {
		return fail(cross, "source not found: "+err.Error())
	}
	// 跟随符号链接后的实际类型（copy 用；move/link 操作链接本身）。
	srcInfo, statErr := os.Stat(srcAbs)
	if statErr != nil {
		// 断链：copy 不能跟随，move/link 仍可处理链接本身。
		srcInfo = linkInfo
	}

	switch op {
	case "move":
		if dstIsTrash {
			if err := os.RemoveAll(srcAbs); err != nil {
				return fail(cross, "failed to delete source: "+err.Error())
			}
			return successResult(cross, op, src, dst, "deleted", fileBytes(srcInfo))
		}
		if dstIsTerminal {
			if !srcInfo.Mode().IsRegular() {
				return fail(cross, "only a regular file can be sent to a terminal")
			}
			if err := streamFileToTerminal(session, workspace, dst, srcAbs); err != nil {
				return fail(cross, "failed to write to terminal: "+err.Error())
			}
			if err := os.RemoveAll(srcAbs); err != nil {
				return fail(cross, "content sent, but failed to remove source: "+err.Error())
			}
			return successResult(cross, op, src, dst, "content sent to terminal and source removed", fileBytes(srcInfo))
		}
		dstAbs, err := resolveWorkspacePath(workspace, dst, false)
		if err != nil {
			return fail(cross, err.Error())
		}
		if samePath(srcAbs, dstAbs) {
			return fail(cross, "src and dst are the same path")
		}
		if srcInfo.IsDir() {
			if isSubPath(srcAbs, dstAbs) {
				return fail(cross, "destination cannot be inside the source directory")
			}
			if _, err := os.Lstat(dstAbs); err == nil {
				return fail(cross, "destination directory already exists: "+dst)
			}
		} else if dstInfo, err := os.Stat(dstAbs); err == nil && dstInfo.IsDir() {
			return fail(cross, "destination is a directory: "+dst)
		}
		if err := ensureParentDir(dstAbs); err != nil {
			return fail(cross, err.Error())
		}
		prog := newProgress(session, args, op, dirSizeIfDir(srcAbs, srcInfo))
		if err := movePath(srcAbs, dstAbs, srcInfo, prog); err != nil {
			return fail(cross, err.Error())
		}
		logger.Info("file move %s -> %s in ID=%d", src, dst, session.ID)
		return successResult(cross, op, src, dst, "moved", fileBytes(srcInfo))

	case "copy":
		if dstIsTerminal {
			if !srcInfo.Mode().IsRegular() {
				return fail(cross, "only a regular file can be sent to a terminal")
			}
			if err := streamFileToTerminal(session, workspace, dst, srcAbs); err != nil {
				return fail(cross, "failed to write to terminal: "+err.Error())
			}
			return successResult(cross, op, src, dst, "content sent to terminal", fileBytes(srcInfo))
		}
		dstAbs, err := resolveWorkspacePath(workspace, dst, false)
		if err != nil {
			return fail(cross, err.Error())
		}
		if samePath(srcAbs, dstAbs) {
			return fail(cross, "src and dst are the same path")
		}
		if dstInfo, err := os.Stat(dstAbs); err == nil && dstInfo.IsDir() {
			return fail(cross, "destination is a directory: "+dst)
		}
		if err := ensureParentDir(dstAbs); err != nil {
			return fail(cross, err.Error())
		}
		if srcInfo.IsDir() {
			if isSubPath(srcAbs, dstAbs) {
				return fail(cross, "destination cannot be inside the source directory")
			}
			total, _ := dirSize(srcAbs)
			prog := newProgress(session, args, op, total)
			if err := copyDir(srcAbs, dstAbs, prog); err != nil {
				return fail(cross, "failed to copy directory: "+err.Error())
			}
			logger.Info("file copy dir %s -> %s in ID=%d", src, dst, session.ID)
			return successResult(cross, op, src, dst, "directory copied", total)
		}
		if !srcInfo.Mode().IsRegular() {
			return fail(cross, "copy only supports regular files and directories")
		}
		prog := newProgress(session, args, op, srcInfo.Size())
		if err := ios.CopyWithProgress(srcAbs, dstAbs, prog.report); err != nil {
			return fail(cross, "failed to copy file: "+err.Error())
		}
		sandbox.ApplyConfiguredFileOwner(dstAbs)
		logger.Info("file copy %s -> %s (%d bytes) in ID=%d", src, dst, srcInfo.Size(), session.ID)
		return successResult(cross, op, src, dst, "copied", srcInfo.Size())

	case "link":
		dstAbs, err := resolveWorkspacePath(workspace, dst, false)
		if err != nil {
			return fail(cross, err.Error())
		}
		if samePath(srcAbs, dstAbs) {
			return fail(cross, "src and dst are the same path")
		}
		if _, err := os.Lstat(dstAbs); err == nil {
			return fail(cross, "destination already exists: "+dst)
		}
		if err := ensureParentDir(dstAbs); err != nil {
			return fail(cross, err.Error())
		}
		target := srcAbs
		// 同目录树内优先用相对链接，移动整个工作区后链接仍然有效。
		if rel, relErr := filepath.Rel(filepath.Dir(dstAbs), srcAbs); relErr == nil && !strings.HasPrefix(rel, "..") {
			target = rel
		}
		if err := os.Symlink(target, dstAbs); err != nil {
			return fail(cross, "failed to create symlink: "+err.Error())
		}
		logger.Info("file link %s -> %s in ID=%d", src, dst, session.ID)
		return successResult(cross, op, src, dst, "symlink created", 0)
	}

	return fail(cross, "unreachable")
}

// === 路径与校验 ===

// resolveWorkspacePath 校验相对路径并解析为绝对路径（含符号链接越界检查）。
func resolveWorkspacePath(workspace, p string, mustExist bool) (string, error) {
	rel, err := checkRelativePath(p)
	if err != nil {
		return "", err
	}
	abs := filepath.Join(workspace, rel)
	if err := u.EnsureWorkspacePath(workspace, abs, ".alkaid0"); err != nil {
		return "", err
	}
	if mustExist {
		if _, err := os.Lstat(abs); err != nil {
			return "", fmt.Errorf("source not found: %w", err)
		}
	}
	return abs, nil
}

// checkRelativePath 复用 edit 工具的词法校验（拒绝 ..、绝对路径、通配符等）。
func checkRelativePath(p string) (string, error) {
	v := any(p)
	return edit.CheckPath(map[string]*any{"path": &v})
}

func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

// isSubPath 判断 child 是否位于 parent 目录内部（不含两者相等）。
func isSubPath(parent, child string) bool {
	rel, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func isTerminalDst(workspace, dst string) bool {
	if strings.HasPrefix(dst, runPathPrefix) {
		return true
	}
	// 兼容历史写法 run/<id>：只有确实存在同工作目录的活动 run 时才视为终端，
	// 否则 run/main.go 这类普通路径会被误判。
	if strings.HasPrefix(dst, "run/") {
		return runTool.Default.FindInWorkspace(workspace, dst) != nil
	}
	return false
}

func ensureParentDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "" || dir == "." {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}
	return nil
}

// writeTextFile 写入虚拟对象文本（覆盖已有文件），并同步配置的运行用户属主。
func writeTextFile(path, content string) error {
	if err := ensureParentDir(path); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}
	sandbox.ApplyConfiguredFileOwner(path)
	return nil
}

// === 文件操作 ===

// movePath 优先 rename；失败（跨文件系统、Windows 覆盖失败等）回退 copy + remove。
func movePath(srcAbs, dstAbs string, info os.FileInfo, prog *progressReporter) error {
	if err := os.Rename(srcAbs, dstAbs); err == nil {
		return nil
	} else if info.IsDir() {
		if copyErr := copyDir(srcAbs, dstAbs, prog); copyErr != nil {
			return fmt.Errorf("rename failed (%v), copy fallback failed: %w", err, copyErr)
		}
		if err := os.RemoveAll(srcAbs); err != nil {
			return fmt.Errorf("copied to destination but failed to remove source: %w", err)
		}
		return nil
	} else {
		// 目标已存在时先删掉，保证回退复制可以覆盖（Windows 上 rename 不覆盖）。
		if _, statErr := os.Lstat(dstAbs); statErr == nil {
			if rmErr := os.Remove(dstAbs); rmErr != nil {
				return fmt.Errorf("rename failed (%v), failed to replace destination: %w", err, rmErr)
			}
		}
		if copyErr := ios.CopyWithProgress(srcAbs, dstAbs, prog.report); copyErr != nil {
			return fmt.Errorf("rename failed (%v), copy fallback failed: %v", err, copyErr)
		}
		sandbox.ApplyConfiguredFileOwner(dstAbs)
		if rmErr := os.RemoveAll(srcAbs); rmErr != nil {
			return fmt.Errorf("copied to destination but failed to remove source: %w", rmErr)
		}
		return nil
	}
}

// copyDir 递归复制目录（常规文件走 ios.CopyWithProgress，符号链接复制为链接本身）。
func copyDir(srcAbs, dstAbs string, prog *progressReporter) error {
	var copied int64
	return filepath.WalkDir(srcAbs, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcAbs, path)
		if err != nil {
			return err
		}
		target := dstAbs
		if rel != "." {
			target = filepath.Join(dstAbs, rel)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if !filepath.IsAbs(link) {
				link = filepath.Join(filepath.Dir(path), link)
			}
			return os.Symlink(link, target)
		case info.Mode().IsRegular():
			base := copied
			if err := ios.CopyWithProgress(path, target, func(n, _ int64) {
				prog.report(base+n, 0)
			}); err != nil {
				return err
			}
			copied += info.Size()
			return nil
		default:
			// FIFO/设备等特殊文件不复制。
			return nil
		}
	})
}

// streamFileToTerminal 分块把文件内容写进运行中的终端（不发送 EOF）。
func streamFileToTerminal(session *structs.Chats, workspace, dst, srcAbs string) error {
	f, err := os.Open(srcAbs)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, terminalChunkSize)
	for {
		n, readErr := f.Read(buf)
		if n > 0 {
			// WriteRunStdin 在内部同步写入 PTY，返回后缓冲区可安全复用。
			if writeErr := runTool.Default.WriteRunStdin(workspace, session.ID, dst, buf[:n]); writeErr != nil {
				return writeErr
			}
		}
		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func dirSize(path string) (int64, error) {
	var total int64
	err := filepath.WalkDir(path, func(_ string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.Type().IsRegular() {
			if info, infoErr := d.Info(); infoErr == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total, err
}

func dirSizeIfDir(path string, info os.FileInfo) int64 {
	if info == nil || !info.IsDir() {
		return fileBytes(info)
	}
	total, _ := dirSize(path)
	return total
}

func fileBytes(info os.FileInfo) int64 {
	if info == nil || !info.Mode().IsRegular() {
		return 0
	}
	return info.Size()
}

// === 进度上报 ===

// progressReporter 把复制进度通过 session.PushToolProgress 推给客户端
// （tool_call_update status=in_progress）。小于 progressMinBytes 的任务不推送。
type progressReporter struct {
	session    *structs.Chats
	toolCallID string
	tool       string
	verb       string
	total      int64
	last       time.Time
	lastPct    int
}

func newProgress(session *structs.Chats, args map[string]*any, verb string, total int64) *progressReporter {
	p := &progressReporter{session: session, tool: toolName, verb: verb, total: total}
	if total < progressMinBytes {
		return p
	}
	if id := optionalString(args, "_id"); id != "" {
		p.toolCallID = fmt.Sprintf("call_%d_%d_%s", session.ID, session.GetCurrentMessageID(), id)
	}
	return p
}

func (p *progressReporter) report(copied, total int64) {
	if p == nil || p.toolCallID == "" {
		return
	}
	if total > 0 {
		p.total = total
	}
	pct := 100
	if p.total > 0 {
		pct = int(copied * 100 / p.total)
		if pct > 100 {
			pct = 100
		}
		if pct < 0 {
			pct = 0
		}
	}
	now := time.Now()
	// 百分比未变时节流到 300ms；百分比变化时最多 120ms 一次，避免高频广播。
	if pct == p.lastPct && now.Sub(p.last) < 300*time.Millisecond {
		return
	}
	if pct != 100 && now.Sub(p.last) < 120*time.Millisecond {
		return
	}
	p.last = now
	p.lastPct = pct
	p.session.PushToolProgress(p.toolCallID, p.tool, fmt.Sprintf("%s %d%% (%s / %s)", p.verb, pct, humanBytes(copied), humanBytes(p.total)))
}

func humanBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	value := float64(n)
	idx := -1
	for value >= 1024 && idx < len(units)-1 {
		value /= 1024
		idx++
	}
	return fmt.Sprintf("%.1f %s", value, units[idx])
}

// === 参数与结果 ===

func requiredString(args map[string]*any, key string) (string, error) {
	v, ok := args[key]
	if !ok || v == nil {
		return "", fmt.Errorf("%s is required", key)
	}
	s, ok := (*v).(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("%s must not be empty", key)
	}
	return s, nil
}

func optionalString(args map[string]*any, key string) string {
	v, ok := args[key]
	if !ok || v == nil {
		return ""
	}
	s, _ := (*v).(string)
	return strings.TrimSpace(s)
}

func fail(cross []*any, message string) (bool, []*any, map[string]*any, error) {
	success := any(false)
	errMsg := any(message)
	return false, cross, map[string]*any{
		"success": &success,
		"error":   &errMsg,
	}, nil
}

func successResult(cross []*any, op, src, dst, message string, bytes int64) (bool, []*any, map[string]*any, error) {
	success := any(true)
	typ := any(op)
	srcVal := any(src)
	dstVal := any(dst)
	msg := any(message)
	bytesVal := any(bytes)
	return false, cross, map[string]*any{
		"success": &success,
		"type":    &typ,
		"src":     &srcVal,
		"dst":     &dstVal,
		"message": &msg,
		"bytes":   &bytesVal,
	}, nil
}
