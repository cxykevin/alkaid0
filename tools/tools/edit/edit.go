package edit

import (
	"bytes"
	_ "embed" // embed
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/cxykevin/alkaid0/context/lsp"
	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/index"
	"github.com/cxykevin/alkaid0/tools/toolobj"
	runTool "github.com/cxykevin/alkaid0/tools/tools/run"
	"github.com/cxykevin/alkaid0/tools/tools/trace"
	u "github.com/cxykevin/alkaid0/utils"
)

const toolName = "edit"

//go:embed prompt.md
var prompt string

var logger = log.New("tools:edit")

var paras = map[string]parser.ToolParameters{
	"path": {
		Type:        parser.ToolTypeString,
		Required:    true,
		Description: "The path of the file or virtual object (e.g., '@tree') to be edited. A new file will be created if it does not exist. **must be a RELATIVE path**. '..' is not allowed. Must Be First Parameter",
	},
	"target": {
		Type:        parser.ToolTypeString,
		Required:    false,
		Description: `Must Be Second Parameter for file edits; ignored for @temp/run/<id> terminal input.`,
	},
	"text": {
		Type:        parser.ToolTypeString,
		Required:    true,
		Description: `Replacement or appended text. Must Be Last Parameter`,
	},
}

// PassInfo 传递信息
type PassInfo struct {
	From        string
	Description string
	Parameters  map[string]any
}

// func buildPrompt(session *structs.Chats) (string, error) {
// 	return prompt, nil
// }

// type toolCallFlagTempory struct {
// 	PathOutputed    bool
// 	TargetOutputed  bool
// 	TextOutputedLen int32
// }

// buildRespObj 构造 edit 工具调用的展示内容（文本 + calling_info），
// 供 OnHook 流式预览与 PostHook 追加 ACP v2 Diffs 段时复用。
func buildRespObj(session *structs.Chats, mp map[string]*any) []u.H {
	respString := ""
	var pathVal *string
	var targetVal *string
	var textVal *string
	if pathPtr, ok := mp["path"]; ok && pathPtr != nil {
		if path, ok := (*pathPtr).(string); ok {
			respString += "Path: " + path + "\n"
			pathVal = &path
		}
	}
	if targetPtr, ok := mp["target"]; ok && targetPtr != nil {
		if target, ok := (*targetPtr).(string); ok {
			respString += "Target: " + target + "\n"
			targetVal = &target
		}
	}
	if textPtr, ok := mp["text"]; ok && textPtr != nil {
		if text, ok := (*textPtr).(string); ok {
			respString += "=== Text ===\n" + text + "\n"
			textVal = &text
		}
	}
	return []u.H{{
		"type": "content",
		"content": u.H{
			"type": "text",
			"text": respString,
		},
	}, {
		"type":      "alk.cxykevin.top/calling_info",
		"name":      toolName,
		"messageID": session.CurrentMessageID,
		"args": u.H{
			"path":   pathVal,
			"target": targetVal,
			"text":   textVal,
		},
	}}
}

func updateInfo(session *structs.Chats, mp map[string]*any, cross []*any, toolID string) (bool, []*any, error) {
	toolCallID := fmt.Sprintf("call_%d_%d_%s", session.ID, session.CurrentMessageID, toolID)
	session.SetToolCalling(toolCallID, buildRespObj(session, mp), "edit")
	return true, cross, nil
}

// === ACP v2 Diffs 段生成 ===

// splitLines 按行分割内容，忽略末尾空行（避免 diff 尾部噪音）。
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// diffLine 表示一行的 diff 操作：' ' 上下文、'-' 删除、'+' 新增。
type diffLine struct {
	kind byte
	text string
}

// lineDiff 计算两个行序列的行级 diff（LCS 回溯）。
// 行数乘积过大时退化为整体替换，避免内存爆炸。
func lineDiff(oldLines, newLines []string) []diffLine {
	n, m := len(oldLines), len(newLines)
	if n*m > 4_000_000 {
		ops := make([]diffLine, 0, n+m)
		for _, l := range oldLines {
			ops = append(ops, diffLine{'-', l})
		}
		for _, l := range newLines {
			ops = append(ops, diffLine{'+', l})
		}
		return ops
	}
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if oldLines[i] == newLines[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	ops := make([]diffLine, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		if oldLines[i] == newLines[j] {
			ops = append(ops, diffLine{' ', oldLines[i]})
			i++
			j++
		} else if dp[i+1][j] >= dp[i][j+1] {
			ops = append(ops, diffLine{'-', oldLines[i]})
			i++
		} else {
			ops = append(ops, diffLine{'+', newLines[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffLine{'-', oldLines[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffLine{'+', newLines[j]})
	}
	return ops
}

// buildGitPatch 生成 git 格式的 unified diff 文本（diff --git / --- / +++ / @@ hunks）。
// isNew=true 时旧路径为 /dev/null；无实际变化时返回空字符串。
func buildGitPatch(absPath, oldContent, newContent string, isNew bool) string {
	ops := lineDiff(splitLines(oldContent), splitLines(newContent))
	hasChange := false
	for _, op := range ops {
		if op.kind != ' ' {
			hasChange = true
			break
		}
	}
	if !hasChange {
		return ""
	}
	oldPath := absPath
	if isNew {
		oldPath = "/dev/null"
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "diff --git %s %s\n", oldPath, absPath)
	fmt.Fprintf(&buf, "--- %s\n", oldPath)
	fmt.Fprintf(&buf, "+++ %s\n", absPath)

	const ctx = 3
	type interval struct{ start, end int }
	ivs := []interval{}
	start := -1
	for i, op := range ops {
		if op.kind != ' ' && start < 0 {
			start = i
		}
		if op.kind == ' ' && start >= 0 {
			ivs = append(ivs, interval{start, i})
			start = -1
		}
	}
	if start >= 0 {
		ivs = append(ivs, interval{start, len(ops)})
	}
	merged := []interval{}
	for _, iv := range ivs {
		if len(merged) > 0 && iv.start-merged[len(merged)-1].end <= 2*ctx {
			merged[len(merged)-1].end = iv.end
		} else {
			merged = append(merged, iv)
		}
	}

	oldLine, newLine := 1, 1
	pos := 0
	for _, iv := range merged {
		s := max(iv.start-ctx, 0)
		e := min(iv.end+ctx, len(ops))
		// 推进到 hunk 起点，维护全局行号
		for ; pos < s; pos++ {
			switch ops[pos].kind {
			case ' ', '-':
				oldLine++
			}
			if ops[pos].kind != '-' {
				newLine++
			}
		}
		oldStart, newStart := oldLine, newLine
		oldCount, newCount := 0, 0
		for i := s; i < e; i++ {
			switch ops[i].kind {
			case ' ':
				oldCount++
				newCount++
			case '-':
				oldCount++
			case '+':
				newCount++
			}
		}
		// count=0（纯插入/删除）时起点用前一行号，文件头为 0（git 惯例）
		if oldCount == 0 {
			oldStart = oldLine - 1
		}
		if newCount == 0 {
			newStart = newLine - 1
		}
		fmt.Fprintf(&buf, "@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount)
		for i := s; i < e; i++ {
			buf.WriteByte(ops[i].kind)
			buf.WriteString(ops[i].text)
			buf.WriteByte('\n')
		}
		for i := s; i < e; i++ {
			switch ops[i].kind {
			case ' ', '-':
				oldLine++
			}
			if ops[i].kind != '-' {
				newLine++
			}
		}
		pos = e
	}
	return buf.String()
}

// buildDiffContent 构造 ACP v2 tool_call_update 的 Diffs 段。
// 无实际内容变化时返回 nil（如空文本触发 LSP 诊断）。
func buildDiffContent(absPath string, oldContent, newContent string, isNew bool) u.H {
	patchText := buildGitPatch(absPath, oldContent, newContent, isNew)
	if patchText == "" {
		return nil
	}
	operation := "modify"
	if isNew {
		operation = "add"
	}
	change := u.H{
		"operation": operation,
		"path":      absPath,
		"fileType":  "text",
	}
	if mt := mime.TypeByExtension(filepath.Ext(absPath)); mt != "" {
		change["mimeType"] = mt
	}
	return u.H{
		"type":    "diff",
		"changes": []u.H{change},
		"patch": u.H{
			"format": "git_patch",
			"text":   patchText,
		},
	}
}

// CheckPath 处理路径
func CheckPath(mp map[string]*any) (string, error) {
	// 规范的后台终端路径（@temp/run/<id>）由 edit 直接转发输入，不按文件路径校验。
	// 历史写法 run/<id> 不在这里放行："run/" 同时是普通目录名，放行会让
	// run/../x 绕过 ".." 校验，也让普通路径无法走后续的常规校验。
	if pathPtr, ok := mp["path"]; ok && pathPtr != nil {
		if path, ok := (*pathPtr).(string); ok && strings.HasPrefix(path, "@temp/run/") {
			return path, nil
		}
	}
	// 检查并获取path参数
	pathPtr, ok := mp["path"]
	if !ok || pathPtr == nil {
		return "", errors.New("missing path parameter")
	}
	path, ok := (*pathPtr).(string)
	if !ok || path == "" {
		return "", errors.New("invalid or empty path parameter")
	}
	// 检查path
	if strings.Contains(path, "..") {
		return "", errors.New("path cannot contains '..'")
	}

	if strings.HasPrefix(path, "/") ||
		strings.HasPrefix(path, "\\") ||
		strings.HasPrefix(path, "~") ||
		strings.Contains(path, ":") ||
		strings.Contains(path, "*") ||
		strings.Contains(path, "?") ||
		strings.Contains(path, "\"") ||
		strings.Contains(path, "<") ||
		strings.Contains(path, ">") ||
		strings.Contains(path, "|") ||
		strings.Contains(path, "\n") ||
		strings.Contains(path, "\r") ||
		strings.Contains(path, "\t") ||
		strings.Contains(path, "..") {
		return "", errors.New("path must be a correct and relative path")
	}
	return path, nil
}

// CheckTargetText 处理目标和文本
func CheckTargetText(mp map[string]*any) (string, string, error) {
	// 检查并获取target参数
	targetPtr, ok := mp["target"]
	if !ok || targetPtr == nil {
		return "", "", errors.New("missing target parameter")
	}
	target, ok := (*targetPtr).(string)
	if !ok {
		return "", "", errors.New("invalid target parameter")
	}

	// 检查并获取text参数
	textPtr, ok := mp["text"]
	if !ok || textPtr == nil {
		return "", "", errors.New("missing text parameter")
	}
	text, ok := (*textPtr).(string)
	if !ok {
		return "", "", errors.New("invalid text parameter")
	}

	return target, text, nil
}

// ProcessString 执行字符串编辑
// extractLineFromTarget 从 @lnN 或 @insertN 中提取起始行号
func extractLineFromTarget(target string) (int, bool) {
	var parts string
	switch {
	case strings.HasPrefix(target, "@ln:"):
		parts = strings.TrimPrefix(target, "@ln:")
	case strings.HasPrefix(target, "@insert:"):
		parts = strings.TrimPrefix(target, "@insert:")
	default:
		return 0, false
	}
	// 处理 @ln:N-M 范围语法
	if idx := strings.Index(parts, "-"); idx >= 0 {
		parts = parts[:idx]
	}
	n, err := strconv.Atoi(parts)
	if err != nil {
		return 0, false
	}
	return n, true
}

func ProcessString(content, target, text string, fileExists bool) (string, error) {
	var newContent string
	var err error

	// 文件不存在时处理
	if !fileExists {
		// append / @all / @ln:1 / @insert:0-1 → 新建
		// @regex / 子串替换 / @ln:N(N>1) / @insert:N(N>1) → 报错
		switch {
		case target == "":
			// 追加模式新建
		case target == "@all":
			// 全量替换新建
		case strings.HasPrefix(target, "@ln:") || strings.HasPrefix(target, "@insert:"):
			if line, ok := extractLineFromTarget(target); !ok || line > 1 {
				return "", fmt.Errorf("file does not exist, cannot target line %d", line)
			}
		default:
			return "", errors.New("file does not exist, cannot replace content")
		}
		return text + "\n", nil
	}

	// 根据target执行不同的编辑操作
	switch {
	case target == "":
		// 追加到文件末尾
		if text == "" {
			// 空替空：不修改内容，仅用于触发 LSP 诊断
			newContent = content
		} else if fileExists {
			if content != "" && !strings.HasSuffix(content, "\n") {
				newContent = content + "\n" + text + "\n"
			} else {
				newContent = content + text + "\n"
			}
		} else {
			newContent = text + "\n"
		}

	case target == "@all":
		// 替换整个文件
		newContent = text + "\n"

	case strings.HasPrefix(target, "@ln:"):
		lines := strings.Split(content, "\n")
		newContent, err = handleLineReplace(lines, target, text)
		if err != nil {
			return "", err
		}
	case strings.HasPrefix(target, "@insert:"):
		lines := strings.Split(content, "\n")
		newContent, err = handleLineInsert(lines, target, text)
		if err != nil {
			return "", err
		}

	case strings.HasPrefix(target, "@regex:"):
		newContent, err = handleRegexEdit(content, target, text)
		return newContent, err

	default:
		// 替换第一个匹配的子字符串
		if !fileExists {
			return "", errors.New("file does not exist, cannot replace substring")
		}
		if !strings.Contains(content, target) {
			return "", fmt.Errorf("target string not found: %s", target)
		}
		newContent = strings.Replace(content, target, text, 1)
	}
	return newContent, nil
}

func writeFile(session *structs.Chats, mp map[string]*any, cross []*any) (bool, []*any, map[string]*any, error) {
	path, err := CheckPath(mp)
	if err != nil {
		boolx := false
		success := any(boolx)
		errMsg := any(err.Error())
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}
	if strings.HasPrefix(path, "@docs/") || path == "@docs" {
		boolx := false
		success := any(boolx)
		errMsg := any("@docs paths are read-only; use the read tool instead")
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}
	// 保存原始相对路径，供编辑成功后加入 trace 列表（下面 path 会被改写为绝对路径）
	origRelPath := path

	// A @temp/run path addresses a live terminal rather than a file. Send the
	// text bytes unchanged so callers can provide control keys or a newline.
	// run/<n> 是历史兼容写法：必须先确认该工作区真的有这个活动 run，否则
	// run/main.go 这类普通文件会被劫持成终端输入、永远无法编辑。
	// run 服务以 session.Root 作为工作目录命名空间（见 run.go 的 Workspace）。
	runWorkspace := session.Root
	isRunPath := strings.HasPrefix(path, "@temp/run/")
	if !isRunPath && strings.HasPrefix(path, "run/") {
		isRunPath = runTool.Default.FindInWorkspace(runWorkspace, path) != nil
	}
	if isRunPath {
		textPtr, ok := mp["text"]
		if !ok || textPtr == nil {
			return false, cross, map[string]*any{}, errors.New("missing text parameter")
		}
		text, ok := (*textPtr).(string)
		if !ok {
			return false, cross, map[string]*any{}, errors.New("invalid text parameter")
		}
		if err := runTool.Default.WriteRunStdin(runWorkspace, session.ID, path, []byte(text)); err != nil {
			return false, cross, map[string]*any{}, fmt.Errorf("failed to write terminal input: %w", err)
		}
		success := any(true)
		return false, cross, map[string]*any{"success": &success}, nil
	}

	target, text, err := CheckTargetText(mp)
	if err != nil {
		boolx := false
		success := any(boolx)
		errMsg := any(err.Error())
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	path = filepath.Join(session.Root, filepath.Join(session.CurrentActivatePath, path))

	// CheckPath 只做纯词法校验，挡不住工作区内的符号链接：os.ReadFile/os.WriteFile
	// 会跟随链接，从而读写工作区之外的文件。这里再按"解析符号链接 + 保护 .alkaid0"
	// 校验一次（与 fs RPC 的 validatePath 对齐；此前 edit 完全没有这一层）。
	workspace := filepath.Join(session.Root, session.CurrentActivatePath)
	if err := u.EnsureWorkspacePath(workspace, path, ".alkaid0"); err != nil {
		boolx := false
		success := any(boolx)
		errMsg := any(err.Error())
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	// 读取文件内容：与 read 工具共用同一套编解码（trace.DecodeTextEx）。非 UTF-8
	// 文件（BOM/UTF-16/GBK 等）按解码后的文本比对与编辑，写回时恢复原编码；否则
	// read 存的是解码文本、edit 拿原始字节比对，带 BOM 的文件永远报"内容已被外部修改"。
	// 这里一次读完（不再用 bufio.Scanner）：Scanner 默认 64KiB 行长上限会让压缩后的
	// JS/JSON 直接报错，而且它吃掉行尾 \r 与末尾换行，重写整个文件时把 CRLF 改成 LF。
	var content string
	var encName string
	var hasBOM bool
	fileMode := os.FileMode(0644)
	fileExists := true
	// 实际写入的路径：符号链接要落到链接目标，否则原子 rename 会把链接本身
	// 替换成普通文件（旧的 os.WriteFile 是跟随链接写目标）
	writePath := path

	stat, statErr := os.Lstat(path)
	if statErr != nil {
		if !os.IsNotExist(statErr) {
			boolx := false
			success := any(boolx)
			errMsg := any(fmt.Sprintf("failed to stat file: %v", statErr))
			return false, cross, map[string]*any{
				"success": &success,
				"error":   &errMsg,
			}, nil
		}
		fileExists = false
		encName = trace.EncodingUTF8
	} else {
		info := stat
		if stat.Mode()&os.ModeSymlink != 0 {
			// 链接本身不是常规文件：工作区内指向常规文件的链接是合法用法，
			// 跟随链接后校验目标（越界链接已由 EnsureWorkspacePath 拦住）
			resolvedPath, resolveErr := filepath.EvalSymlinks(path)
			if resolveErr != nil {
				boolx := false
				success := any(boolx)
				errMsg := any(fmt.Sprintf("failed to resolve symlink: %v", resolveErr))
				return false, cross, map[string]*any{
					"success": &success,
					"error":   &errMsg,
				}, nil
			}
			resolved, statLinkErr := os.Stat(resolvedPath)
			if statLinkErr != nil {
				boolx := false
				success := any(boolx)
				errMsg := any(fmt.Sprintf("failed to stat file: %v", statLinkErr))
				return false, cross, map[string]*any{
					"success": &success,
					"error":   &errMsg,
				}, nil
			}
			writePath = resolvedPath
			info = resolved
		}
		// FIFO/设备/目录不是可编辑的文件：读 FIFO 会永久阻塞且不可取消
		if !info.Mode().IsRegular() {
			boolx := false
			success := any(boolx)
			errMsg := any("not a regular file")
			return false, cross, map[string]*any{
				"success": &success,
				"error":   &errMsg,
			}, nil
		}
		// 与 read 工具一致的大小上限，避免把巨大文件整个读进内存
		if info.Size() > trace.MaxFileSize {
			boolx := false
			success := any(boolx)
			errMsg := any("file too large")
			return false, cross, map[string]*any{
				"success": &success,
				"error":   &errMsg,
			}, nil
		}
		fileMode = info.Mode().Perm()
		raw, readErr := os.ReadFile(writePath)
		if readErr != nil {
			boolx := false
			success := any(boolx)
			errMsg := any(fmt.Sprintf("failed to read file: %v", readErr))
			return false, cross, map[string]*any{
				"success": &success,
				"error":   &errMsg,
			}, nil
		}
		content, encName, hasBOM = trace.DecodeTextEx(raw)
		if encName == trace.EncodingBinary {
			// 二进制文件保持旧行为：按原始字节处理（read 工具不会跟踪这类文件）
			content = string(raw)
		}
	}

	// 读取可能阻塞（网络文件系统），写盘前检查取消信号
	if session.GetContext().Err() != nil {
		boolx := false
		success := any(boolx)
		errMsg := any("edit cancelled: " + session.GetContext().Err().Error())
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	if fileExists {
		// 与 read 工具记录的（解码后）内容比对，避免覆盖请求构建后的外部修改
		if err := trace.CheckEditContent(session, origRelPath, content); err != nil {
			boolx := false
			success := any(boolx)
			errMsg := any(err.Error())
			return false, cross, map[string]*any{
				"success": &success,
				"error":   &errMsg,
			}, nil
		}
	} else {
		// 文件已不存在：清掉确认条目，否则残留的旧内容会让该路径永远无法重建
		trace.ForgetEditContent(session, origRelPath)
	}

	logger.Info("edit file \"%s\" mode \"%s\" in ID=%d,agentID=%s", path, target, session.ID, session.CurrentAgentID)

	// 行尾与末尾换行按原文件保留：内部统一按 LF 处理，写回时恢复 CRLF，
	// 否则 Windows 仓库每编辑一次就被整篇改成 LF。
	crlf := fileExists && strings.Contains(content, "\r\n")
	editContent := content
	if crlf {
		editContent = strings.ReplaceAll(content, "\r\n", "\n")
	}
	hadFinalNewline := !fileExists || strings.HasSuffix(editContent, "\n")

	newContent, err := ProcessString(editContent, target, text, fileExists)
	if err == nil {
		if crlf {
			// 模型给的替换文本里可能自带 CRLF：先归一成 LF，再做末尾换行处理
			newContent = strings.ReplaceAll(newContent, "\r\n", "\n")
		}
		newContent = normalizeTrailingNewline(newContent)
		if !hadFinalNewline {
			newContent = strings.TrimSuffix(newContent, "\n")
		}
		if crlf {
			newContent = strings.ReplaceAll(newContent, "\n", "\r\n")
		}
	}
	if err != nil {
		logger.Warn("failed to process string: %v", err)
		boolx := false
		success := any(boolx)
		errMsg := any(err.Error())
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	// 写入文件前检查取消信号（网络文件系统下可能阻塞）
	if session.GetContext().Err() != nil {
		boolx := false
		success := any(boolx)
		errMsg := any("edit cancelled: " + session.GetContext().Err().Error())
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}
	// 写回原编码字节；目标编码表示不了的字符（例如往 GBK 文件里塞 emoji）宁可报错，
	// 也不静默用替换符损坏文件
	outBytes := []byte(newContent)
	if encName != trace.EncodingBinary {
		outBytes, err = trace.EncodeText(newContent, encName, hasBOM)
		if err != nil {
			logger.Warn("failed to encode file: %v", err)
			boolx := false
			success := any(boolx)
			errMsg := any(fmt.Sprintf("failed to encode content back to %s: %v", encName, err))
			return false, cross, map[string]*any{
				"success": &success,
				"error":   &errMsg,
			}, nil
		}
	}
	// 旧内容（LF 归一）用于精确 diff
	oldContentRaw := strings.ReplaceAll(content, "\r\n", "\n")
	// 原子写入：同目录临时文件 + rename，中断时不会留下被截断的半截文件
	if err := writeFileAtomic(writePath, outBytes, fileMode); err != nil {
		logger.Warn("failed to write file: %v", err)
		boolx := false
		success := any(boolx)
		errMsg := any(fmt.Sprintf("failed to write file: %v", err))
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	pathStr := any(origRelPath)
	trace.Trace(session, map[string]*any{
		"path": &pathStr,
	}, []*any{})

	boolx := true
	success := any(boolx)

	// 编辑成功后调用 LSP 格式化和语法检查
	fmtResult := lsp.FormatAndDiagnose(session.Root, path)
	if fmtResult.Error != "" {
		logger.Warn("LSP format+diagnose for %s: %s", path, fmtResult.Error)
	}

	// 生成 ACP v2 Diffs 段：基于磁盘最终内容（LSP 格式化可能已改写文件），
	// 更新广播 content 并持久化，供客户端实时展示与会话还原重放。
	finalContent := newContent
	if fb, readErr := os.ReadFile(writePath); readErr == nil {
		if decoded, name, _ := trace.DecodeTextEx(fb); name != trace.EncodingBinary {
			finalContent = decoded
		} else {
			finalContent = string(fb)
		}
	}
	trace.ConfirmEditContent(session, origRelPath, finalContent)
	respObj := buildRespObj(session, mp)
	if diffObj := buildDiffContent(path, oldContentRaw, strings.ReplaceAll(finalContent, "\r\n", "\n"), !fileExists); diffObj != nil {
		respObj = append(respObj, diffObj)
	}
	if toolIDPtr, ok := mp["_id"]; ok && toolIDPtr != nil {
		if toolID, ok := (*toolIDPtr).(string); ok && toolID != "" {
			toolCallID := fmt.Sprintf("call_%d_%d_%s", session.ID, session.CurrentMessageID, toolID)
			// SetToolCalling 内部会把最终展示内容（含 Diffs 段）按工具调用 ID 落库，
			// 供 session/resume 历史回放重放，无需额外保存。
			session.SetToolCalling(toolCallID, respObj, "edit")
		}
	}

	resultMap := map[string]*any{
		"success": &success,
	}

	if fmtResult.Formatted {
		formatBool := any(fmtResult.Formatted)
		resultMap["format_applied"] = &formatBool
	}

	if len(fmtResult.Diagnostics) > 0 {
		// 将诊断信息作为额外字段返回，AI 将看到并可以修复
		diagAny := any(fmtResult.Diagnostics)
		resultMap["diagnostics"] = &diagAny
	}

	return false, cross, resultMap, nil
}

func normalizeTrailingNewline(s string) string {
	return strings.TrimRight(s, "\n") + "\n"
}

// writeFileAtomic 以"同目录临时文件 + rename"写入：写临时文件期间原文件保持完整，
// 中断/崩溃最多留下一个临时文件，不会出现被截断的半截目标文件。权限沿用原文件。
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".alkaid0-edit-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// 成功 rename 后临时文件已不存在，这里只是失败路径的兜底清理
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func handleLineReplace(lines []string, target, text string) (string, error) {
	parts := strings.TrimPrefix(target, "@ln:")

	if !strings.Contains(parts, "-") {
		lineNum, err := strconv.Atoi(parts)

		if err != nil {
			return "", fmt.Errorf("invalid line number: %s", parts)
		}

		from := lineNum
		to := lineNum

		if lineNum > len(lines) {
			return "", fmt.Errorf("line %d exceeds file length %d", from, len(lines))
		}
		// 构建新内容
		var buf bytes.Buffer
		for i := 0; i < from-1; i++ {
			buf.WriteString(lines[i] + "\n")
		}
		buf.WriteString(text + "\n")
		for i := to; i < len(lines); i++ {
			buf.WriteString(lines[i] + "\n")
		}

		return buf.String(), nil
	}

	// @ln:{from}-{to} 替换行范围
	rangeParts := strings.Split(parts, "-")

	if len(rangeParts) != 2 {
		return "", fmt.Errorf("invalid line range: %s", parts)
	}

	from, err := strconv.Atoi(rangeParts[0])

	if err != nil {
		return "", fmt.Errorf("invalid line number: %s", rangeParts[0])
	}

	to, err := strconv.Atoi(rangeParts[1])

	if err != nil {
		return "", fmt.Errorf("invalid line number: %s", rangeParts[1])
	}

	if from > len(lines) {
		return "", fmt.Errorf("from line %d exceeds file length %d", from, len(lines))
	}
	if to > len(lines) {
		return "", fmt.Errorf("to line %d exceeds file length %d", to, len(lines))
	}
	if from > to {
		return "", fmt.Errorf("from line %d is greater than to line %d", from, to)
	}

	// 构建新内容
	var buf bytes.Buffer
	for i := 0; i < from-1; i++ {
		buf.WriteString(lines[i] + "\n")
	}
	buf.WriteString(text + "\n")
	for i := to; i < len(lines); i++ {
		buf.WriteString(lines[i] + "\n")
	}

	return buf.String(), nil

}
func handleLineInsert(lines []string, target, text string) (string, error) {
	parts := strings.TrimPrefix(target, "@insert:")

	lineNum, err := strconv.Atoi(parts)

	if err != nil {
		return "", fmt.Errorf("invalid line number: %s", parts)
	}

	if lineNum < 1 || lineNum > len(lines)+1 {
		return "", fmt.Errorf("line %d is out of range (file has %d lines)", lineNum, len(lines))
	}

	// 构建新内容
	var buf bytes.Buffer
	for i := range lineNum - 1 {
		buf.WriteString(lines[i] + "\n")
	}
	buf.WriteString(text + "\n")
	for i := lineNum - 1; i < len(lines); i++ {
		buf.WriteString(lines[i] + "\n")
	}

	return buf.String(), nil
}

func handleRegexEdit(content, target, text string) (string, error) {
	// 解析: @regex:/pattern/flags
	patternPart := strings.TrimPrefix(target, "@regex:")

	if len(patternPart) < 3 || patternPart[0] != '/' {
		return "", fmt.Errorf("invalid regex format, expected @regex:/pattern/flags")
	}

	// 去掉开头的'/'
	patternPart = patternPart[1:]

	// 找到最后一个/来分隔pattern和flags
	lastSlash := strings.LastIndex(patternPart, "/")
	if lastSlash < 0 {
		return "", fmt.Errorf("invalid regex format, missing closing /")
	}

	pattern := patternPart[:lastSlash]
	flags := ""
	if lastSlash+1 < len(patternPart) {
		flags = patternPart[lastSlash+1:]
	}

	if pattern == "" {
		return "", fmt.Errorf("empty regex pattern")
	}

	// 检查是否找到匹配
	var re *regexp.Regexp
	var err error

	if strings.Contains(flags, "i") {
		re, err = regexp.Compile("(?i)" + pattern)
	} else {
		re, err = regexp.Compile(pattern)
	}

	if err != nil {
		return "", fmt.Errorf("invalid regex pattern '%s': %v", pattern, err)
	}

	// 检查是否有匹配
	matches := re.FindAllString(content, -1)
	if len(matches) == 0 {
		return "", fmt.Errorf("regex pattern '%s' not found in file", pattern)
	}

	// 执行替换
	global := strings.Contains(flags, "g")

	if global {
		// 必须用字面量替换：ReplaceAllString 会把替换文本里的 $name / ${name}
		// 当作展开模板，模型写 shell/Makefile/正则片段（$VAR、$1）时内容会被静默改写。
		newContent := re.ReplaceAllLiteralString(content, text)
		return newContent, nil
	}
	// éå¨å±æ¨¡å¼åªæ¿æ¢ç¬¬ä¸ä¸ªå¹éé¡¹
	loc := re.FindStringIndex(content)
	if loc == nil {
		return content, nil
	}
	newContent := content[:loc[0]] + text + content[loc[1]:]
	return newContent, nil
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
		PreHook: toolobj.PreHookFunction{
			Priority: 100,
			Func:     nil,
		},
		OnHook: toolobj.OnHookFunction{
			Priority: 100,
			Func:     updateInfo,
		},
		PostHook: toolobj.PostHookFunction{
			Priority: 100,
			Func:     writeFile,
		},
	}); err != nil {
		panic(err)
	}
	return toolName
}

func init() {
	index.AddIndex(load)
}
