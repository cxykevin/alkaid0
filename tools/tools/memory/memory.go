package memory

import (
	_ "embed" // embed
	"errors"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/internal/configutil"
	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/prompts"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/index"
	"github.com/cxykevin/alkaid0/tools/toolobj"
	"github.com/cxykevin/alkaid0/tools/tools/edit"
)

const toolName = "memory"

//go:embed prompt.md
var prompt string

//go:embed memory.md
var memoryPrompt string

//go:embed agents.md
var agentsPrompt string

var memoryTemplate *template.Template
var agentsTemplate *template.Template

var logger = log.New("tools:memory")

// globalMemoryDir 全局 memory 所在目录（配置文件同目录）。
// 用变量而非直接调用 config.Path()，便于测试覆写（config.Path 内部有包级缓存，测试中不可控）。
var globalMemoryDir = func() string {
	return filepath.Dir(configutil.ExpandPath(config.Path()))
}

// userHomeDir 用户主目录，向上查找 AGENTS.md/CLAUDE.md 时的边界；测试可覆写。
var userHomeDir = func() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Clean(h)
}

func init() {
	memoryTemplate = prompts.Load("tools:memory:memory", memoryPrompt)
	agentsTemplate = prompts.Load("tools:memory:agents", agentsPrompt)
}

// resolveMemoryPath 把虚拟路径解析为真实文件路径。
func resolveMemoryPath(session *structs.Chats, path string) (string, error) {
	switch path {
	case "@memory":
		root := session.Root
		if root == "" {
			root = "."
		}
		return filepath.Join(root, ".alkaid0", "MEMORY.md"), nil
	case "@memory/global":
		return filepath.Join(globalMemoryDir(), "MEMORY.md"), nil
	}
	return "", errors.New("unknown memory path: " + path)
}

// readFileIfExists 读文件；文件不存在返回 ("", nil)，其他错误原样返回。
func readFileIfExists(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// readExisting 读文件并返回是否已存在，供 writeMemory 按"新建文件/已有文件"语义处理。
func readExisting(path string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	return string(data), true, nil
}

// buildMemoryPrompt 注入 @memory / @memory/global 内容到 AI 上下文（全局 PreHook，Priority 101）。
// 两者皆空时返回引导语而非空串，避免 ToolPrehookTemplate 渲染出多余空行。
func buildMemoryPrompt(session *structs.Chats) (string, error) {
	project, err := readFileIfExists(mustResolve(session, "@memory"))
	if err != nil {
		logger.Warn("memory read project error: %v", err)
		project = ""
	}
	global, err := readFileIfExists(mustResolve(session, "@memory/global"))
	if err != nil {
		logger.Warn("memory read global error: %v", err)
		global = ""
	}
	project = strings.TrimSpace(project)
	global = strings.TrimSpace(global)
	if project == "" && global == "" {
		return "Virtual object `@memory` is empty. You can store project memory by editing `@memory` with the `edit` tool (saved to `.alkaid0/MEMORY.md`), or `@memory/global` for global memory (saved beside the config file).", nil
	}
	return prompts.Render(memoryTemplate, struct {
		Project string
		Global  string
	}{project, global})
}

// mustResolve 忽略 resolveMemoryPath 的错误（@memory/@memory/global 为常量路径，恒合法）。
func mustResolve(session *structs.Chats, path string) string {
	p, err := resolveMemoryPath(session, path)
	if err != nil {
		return ""
	}
	return p
}

// buildAgentsPrompt 注入工作目录的 AGENTS.md / CLAUDE.md 到 AI 上下文（全局 PreHook，Priority 100）。
// 从 session.Root 向上最多 3 级、第一命中；不跨越文件系统边界和用户主目录边界。
// 两者皆无时返回空串（代价仅是 tool_prehook 渲染出一个多余空行，可接受）。
func buildAgentsPrompt(session *structs.Chats) (string, error) {
	agentsPath := findUpward(session.Root, "AGENTS.md", 3)
	claudePath := findUpward(session.Root, "CLAUDE.md", 3)

	agents, err := readFileIfExists(agentsPath)
	if err != nil {
		logger.Warn("memory read AGENTS.md error: %v", err)
		agents = ""
	}
	claude, err := readFileIfExists(claudePath)
	if err != nil {
		logger.Warn("memory read CLAUDE.md error: %v", err)
		claude = ""
	}
	agents = strings.TrimSpace(agents)
	claude = strings.TrimSpace(claude)
	if agents == "" && claude == "" {
		return "", nil
	}
	return prompts.Render(agentsTemplate, struct {
		Agents string
		Claude string
	}{agents, claude})
}

// findUpward 从 start 目录开始向上最多 maxLevels 级查找首个存在的普通文件 name。
// 每级向上前检查边界：不跨越用户主目录边界、不跨越文件系统边界（sameDevice 兜底为不检测）。
func findUpward(start, name string, maxLevels int) string {
	dir := filepath.Clean(start)
	if dir == "." || dir == "" {
		if wd, err := os.Getwd(); err == nil {
			dir = wd
		}
	}
	home := userHomeDir()
	for level := 0; level <= maxLevels; level++ {
		candidate := filepath.Join(dir, name)
		if fi, err := os.Stat(candidate); err == nil && fi.Mode().IsRegular() {
			return candidate
		}
		if level == maxLevels {
			break
		}
		parent := filepath.Dir(dir)
		if !canAscend(dir, parent, home) {
			break
		}
		dir = parent
	}
	return ""
}

// canAscend 判断能否从 dir 向上到 parent。
// 规则：parent 必须仍处于用户主目录内（含主目录本身）；同一文件系统边界（兜底为不检测）。
func canAscend(dir, parent, home string) bool {
	if home != "" {
		if !withinOrEqual(parent, home) {
			return false
		}
	}
	return sameDevice(dir, parent)
}

// withinOrEqual 判断 path 是否在 ancestor 内部或等于 ancestor（基于 filepath.Rel）。
func withinOrEqual(path, ancestor string) bool {
	rel, err := filepath.Rel(ancestor, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return !strings.HasPrefix(rel, "..")
}

// buildPrompt 注入 @memory 操作规范到 edit 工具 Description（edit PreHook，Priority 90）
func buildPrompt(_ *structs.Chats) (string, error) {
	return prompt, nil
}

// failResult 统一构造失败返回（pass=false 终止 edit 自身 writeFile）
func failResult(cross []*any, errMsg string) (bool, []*any, map[string]*any, error) {
	boolx := false
	success := any(boolx)
	errAny := any(errMsg)
	return false, cross, map[string]*any{
		"success": &success,
		"error":   &errAny,
	}, nil
}

// writeMemory 拦截 edit 对 @memory / @memory/global 的编辑（edit PostHook，Priority 110）。
// 直接读写真实文件，不做任何 ACP 通知。
func writeMemory(session *structs.Chats, mp map[string]*any, cross []*any) (bool, []*any, map[string]*any, error) {
	path, err := edit.CheckPath(mp)
	if err != nil {
		return true, cross, nil, nil // 路径异常 → 放行给 edit.writeFile
	}
	if path != "@memory" && path != "@memory/global" {
		return true, cross, nil, nil // 非 memory 虚拟对象 → 放行
	}

	target, text, err := edit.CheckTargetText(mp)
	if err != nil {
		return failResult(cross, err.Error())
	}

	filePath, err := resolveMemoryPath(session, path)
	if err != nil {
		return failResult(cross, err.Error())
	}

	// 读取现有内容（不存在时按"新建文件"语义处理）
	content, fileExists, err := readExisting(filePath)
	if err != nil {
		logger.Warn("memory read error: %v", err)
		return failResult(cross, "failed to read memory: "+err.Error())
	}

	newContent, err := edit.ProcessString(content, target, text, fileExists)
	if err != nil {
		logger.Warn("memory process error: %v", err)
		return failResult(cross, err.Error())
	}
	// 规范化尾换行（与 edit.normalizeTrailingNewline 一致）
	newContent = strings.TrimRight(newContent, "\n") + "\n"

	// 写入前检查取消信号（网络文件系统下可能阻塞）
	if session.GetContext().Err() != nil {
		return failResult(cross, "memory write cancelled: "+session.GetContext().Err().Error())
	}
	// .alkaid0 / 配置文件同目录可能不存在，先确保目录存在
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		logger.Warn("memory mkdir error: %v", err)
		return failResult(cross, "failed to create memory dir: "+err.Error())
	}
	if err := os.WriteFile(filePath, []byte(newContent), 0644); err != nil {
		logger.Warn("memory write error: %v", err)
		return failResult(cross, "failed to write memory: "+err.Error())
	}

	boolx := true
	success := any(boolx)
	return false, cross, map[string]*any{
		"success": &success,
	}, nil
}

func load() string {
	// 全局 PreHook：注入 memory 内容到 AI 上下文（不注册实际工具）
	if err := actions.HookTool("", &toolobj.Hook{
		Scope: "",
		PreHook: toolobj.PreHookFunction{
			Priority: 101,
			Func:     buildMemoryPrompt,
		},
	}); err != nil {
		panic(err)
	}
	// 全局 PreHook：注入 AGENTS.md / CLAUDE.md 到 AI 上下文
	if err := actions.HookTool("", &toolobj.Hook{
		Scope: "",
		PreHook: toolobj.PreHookFunction{
			Priority: 100,
			Func:     buildAgentsPrompt,
		},
	}); err != nil {
		panic(err)
	}
	// edit 工具的 @memory / @memory/global 拦截
	if err := actions.HookTool("edit", &toolobj.Hook{
		Scope: "",
		PreHook: toolobj.PreHookFunction{
			Priority: 90,
			Func:     buildPrompt,
		},
		PostHook: toolobj.PostHookFunction{
			Priority: 110,
			Func:     writeMemory,
		},
	}); err != nil {
		panic(err)
	}
	return toolName
}

func init() {
	index.AddIndex(load)
}
