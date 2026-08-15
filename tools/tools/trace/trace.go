package trace

import (
	_ "embed" // embed
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/prompts"
	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/index"
	"github.com/cxykevin/alkaid0/tools/toolobj"
	u "github.com/cxykevin/alkaid0/utils"
)

const toolName = "read"

//go:embed prompt.md
var prompt string

//go:embed trace.md
var tracePrompt string

var traceTempate *template.Template

var logger = log.New("tools:trace")

// MaxFileLine 最大文件行数
const MaxFileLine = 5000

// MaxFileSize 最大文件大小
const MaxFileSize = 50 * 1024 // 50KB

func init() {
	traceTempate = prompts.Load("tools:trace:trace", tracePrompt)
}

var paras = map[string]parser.ToolParameters{
	"unread": {
		Type:        parser.ToolTypeBoolean,
		Required:    false,
		Description: "Whether to remove the file from the read context. Default is false.",
	},
	"path": {
		Type:        parser.ToolTypeString,
		Required:    true,
		Description: "The relative path of the file to read or remove from the read context. '..' is not allowed.",
	},
}

// func buildPrompt(session *structs.Chats) (string, error) {
// 	return prompt, nil
// }

type toolCallFlagTempory struct {
	PathOutputed bool
	FlagOutputed bool
}

func updateInfo(session *structs.Chats, mp map[string]*any, cross []*any, toolID string) (bool, []*any, error) {
	toolCallID := fmt.Sprintf("call_%d_%d_%s", session.ID, session.CurrentMessageID, toolID)
	respString := ""
	var pathVal *string
	var unreadVal *bool
	if pathPtr, ok := mp["path"]; ok && pathPtr != nil {
		if path, ok := (*pathPtr).(string); ok {
			respString += "Path: " + path + "\n"
			pathVal = &path
		}
	}
	if unreadPtr, ok := mp["unread"]; ok && unreadPtr != nil {
		if unread, ok := (*unreadPtr).(bool); ok {
			respString += "Unread: " + u.Ternary(unread, "true", "false") + "\n"
			unreadVal = &unread
		}
	}
	respObj := []u.H{{
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
			"name":   pathVal,
			"unread": unreadVal,
		},
	}}
	session.SetToolCalling(toolCallID, respObj, "trace")
	return true, cross, nil
}

// Trace 跟踪文件
func Trace(session *structs.Chats, mp map[string]*any, push []*any) (bool, []*any, map[string]*any, error) {
	// 检查并获取path参数
	pathPtr, ok := mp["path"]
	if !ok || pathPtr == nil {
		boolx := false
		success := any(boolx)
		errMsg := any("missing path parameter")
		return false, push, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}
	nowpath := session.Root
	if nowpath == "" {
		nowpath = "."
	}
	activatePath := session.CurrentActivatePath
	if activatePath == "" {
		activatePath = "."
	}
	nowpath = filepath.Join(nowpath, activatePath)
	nowpath, err := filepath.Abs(nowpath)
	path, ok := (*pathPtr).(string)
	if !ok || path == "" {
		boolx := false
		success := any(boolx)
		errMsg := any("invalid or empty path parameter")
		return false, push, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	// 检查并获取unread参数
	unreadPtr, ok := mp["unread"]
	var unread bool
	if ok && unreadPtr != nil {
		unread, ok = (*unreadPtr).(bool)
		if !ok || path == "" {
			unread = false
		}
	}

	// 检查path
	if strings.Contains(path, "..") {
		boolx := false
		success := any(boolx)
		errMsg := any("path cannot contains '..'")
		return false, push, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
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
		strings.Contains(path, "\t") {
		boolx := false
		success := any(boolx)
		errMsg := any("path must be a correct and relative path")
		return false, push, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	traceStr := "trace"
	if unread {
		traceStr = "unread"
	}
	logger.Info("%s file \"%s\" in ID=%d,agentID=%s", traceStr, path, session.ID, session.NowAgent)

	if unread {
		// 删数据库
		tx := session.DB.Where("chat_id = ? AND path = ? AND agent_id = ?", session.ID, path, session.NowAgent).Delete(&structs.Traces{})
		err := tx.Error
		if err != nil {
			logger.Warn("delete trace failed: %v", err)
			boolx := false
			success := any(boolx)
			errMsg := any(err.Error())
			return false, push, map[string]*any{
				"success": &success,
				"error":   &errMsg,
			}, err
		}
		if tx.RowsAffected == 0 {
			// 没有找到
			boolx := false
			success := any(boolx)
			errMsg := any("no such trace")
			return false, push, map[string]*any{
				"success": &success,
				"error":   &errMsg,
			}, nil
		}
	} else {
		var str string
		var err error
		if vpath, ok := strings.CutPrefix(path, "@temp/"); ok {
			// 查db
			var fileObj structs.ReferFiles
			session.DB.Where("chat_id = ?", session.ID).Where("path = ?", vpath).First(&fileObj)
			str = fileObj.Content
		} else {
			path2 := filepath.Join(nowpath, path)
			path2 = filepath.Clean(path2)
			// 检查文件是否存在
			stat, err := os.Stat(path2)
			if err != nil {
				boolx := false
				success := any(boolx)
				errMsg := any("file not exist")
				return false, push, map[string]*any{
					"success": &success,
					"error":   &errMsg,
				}, nil
			}
			// 文件过大(100K)
			if stat.Size() > MaxFileSize {
				boolx := false
				success := any(boolx)
				errMsg := any("file too large")
				return false, push, map[string]*any{
					"success": &success,
					"error":   &errMsg,
				}, nil
			}
			// 读取文件内容
			if session.GetContext().Err() != nil {
				boolx := false
				success := any(boolx)
				errMsg := any("trace cancelled: " + session.GetContext().Err().Error())
				return false, push, map[string]*any{
					"success": &success,
					"error":   &errMsg,
				}, nil
			}
			content, err := os.ReadFile(path2)
			if err != nil {
				boolx := false
				success := any(boolx)
				errMsg := any("file read error: " + err.Error())
				return false, push, map[string]*any{
					"success": &success,
					"error":   &errMsg,
				}, nil
			}
			str = fileContentToString(content)
			if len(str) == 0 {
				boolx := false
				success := any(boolx)
				errMsg := any("file is empty or cannot readable (may be binary file)")
				return false, push, map[string]*any{
					"success": &success,
					"error":   &errMsg,
				}, nil
			}
		}

		// 读取行数
		lines := strings.Split(str, "\n")
		if len(lines) > MaxFileLine {
			boolx := false
			success := any(boolx)
			errMsg := any("file is too long")
			return false, push, map[string]*any{
				"success": &success,
				"error":   &errMsg,
			}, nil
		}

		// 若文件已在当前会话的跟踪列表中，静默成功（避免复合主键唯一约束冲突）
		var tracedCount int64
		if err := session.DB.Model(&structs.Traces{}).
			Where("chat_id = ? AND path = ? AND agent_id = ?", session.ID, path, session.NowAgent).
			Count(&tracedCount).Error; err != nil {
			logger.Warn("check trace failed: %v", err)
			boolx := false
			success := any(boolx)
			errMsg := any(err.Error())
			return false, push, map[string]*any{
				"success": &success,
				"error":   &errMsg,
			}, err
		}
		if tracedCount == 0 {
			// 更新 TraceID
			session.TraceID++
			// 写数据库
			trace := structs.Traces{
				ChatID:      session.ID,
				Path:        path,
				TraceID:     session.TraceID,
				AgentID:     session.NowAgent,
				LastContent: str,
			}
			err = session.DB.Save(&trace).Error
			if err != nil {
				logger.Warn("trace failed: %v", err)
				boolx := false
				success := any(boolx)
				errMsg := any(err.Error())
				return false, push, map[string]*any{
					"success": &success,
					"error":   &errMsg,
				}, nil
			}
			err = session.DB.Model(&structs.Chats{}).Where("id = ?", session.ID).Update("trace_id", session.TraceID).Error
			if err != nil {
				logger.Warn("update trace failed: %v", err)
				boolx := false
				success := any(boolx)
				errMsg := any(err.Error())
				return false, push, map[string]*any{
					"success": &success,
					"error":   &errMsg,
				}, nil
			}
			// 后台静默索引（打 tempfs 标签）
			go func() {
				idxPath := path
				if vpath, ok := strings.CutPrefix(idxPath, "@temp/"); ok {
					idxPath = vpath
				}
				if indexTaskFn != nil {
					indexTaskFn(session.Root, idxPath, str, str, []string{"tempfs"})
				}
			}()
		}
	}

	// TODO: RAG trace

	// 读 db
	if session.TemporyDataOfSession == nil {
		session.TemporyDataOfSession = make(map[string]any)
	}
	if _, ok := session.TemporyDataOfSession["tools:trace"]; !ok {
		session.TemporyDataOfSession["tools:trace"] = traceCache{}
	}
	if _, ok := session.TemporyDataOfSession["tools:trace"].(traceCache); !ok {
		session.TemporyDataOfSession["tools:trace"] = traceCache{}
	}
	traces := []structs.Traces{}
	err = session.DB.Where("chat_id = ? AND agent_id = ?", session.ID, session.NowAgent).Find(&traces).Error
	if err != nil {
		logger.Warn("read trace failed: %v", err)
		boolx := false
		success := any(boolx)
		errMsg := any(err.Error())
		return false, push, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, err
	}
	session.TemporyDataOfSession["tools:trace"].(traceCache)[session.NowAgent] = traces

	boolx := true
	success := any(boolx)
	msg := "The file has been read and injected into the top of the context."
	msgAny := any(msg)
	pathAny := any(path)
	return false, push, map[string]*any{
		"success": &success,
		"message": &msgAny,
		"path":    &pathAny,
	}, nil
}

type templateStruct struct {
	Name   string
	Size   string
	Length uint32
	Text   string
	Type   string // 空=完整文件内容块；"diff"=增量补丁块（unified diff 文本）
}

// FileBlock 单个被追踪文件渲染后的内容块（trace.md 模板的模板对象），供 build 包类型断言。
type FileBlock = templateStruct

type traceCache map[string]([]structs.Traces)

type traceExpectedContent map[string]string

// confirmTraceContent 记录本轮请求或 Agent 编辑后确认过的文件内容。
// edit 在实际写盘前以此检查请求构建后的外部修改，避免覆盖用户的新内容。
func confirmTraceContent(session *structs.Chats, path, content string) {
	if session.TemporyDataOfSession == nil {
		session.TemporyDataOfSession = make(map[string]any)
	}
	confirmed, _ := session.TemporyDataOfSession[structs.TempKeyTraceConfirmedContent].(traceExpectedContent)
	if confirmed == nil {
		confirmed = make(traceExpectedContent)
		session.TemporyDataOfSession[structs.TempKeyTraceConfirmedContent] = confirmed
	}
	confirmed[path] = content
}

// advanceTraceCache 将实际退化为完整内容块时的内容写回 trace 缓存。
// 方案2只在旧块和 diff 都成功插入时保留 LastContent；请求构建阶段若锚点或成本复核失败，
// 模型收到的是完整当前块，缓存也必须同步到该内容，避免下一轮重复生成同一份 diff。
func AdvanceTraceCache(session *structs.Chats, path string) {
	if session == nil || session.DB == nil || strings.HasPrefix(path, "@temp/") {
		return
	}
	confirmed, _ := session.TemporyDataOfSession[structs.TempKeyTraceConfirmedContent].(traceExpectedContent)
	content, ok := confirmed[path]
	if !ok {
		return
	}
	session.DB.Model(&structs.Traces{}).
		Where("chat_id = ? AND path = ? AND agent_id = ?", session.ID, path, session.NowAgent).
		Update("last_content", content)
	if cache, ok := session.TemporyDataOfSession["tools:trace"].(traceCache); ok {
		if traces, ok := cache[session.NowAgent]; ok {
			for i := range traces {
				if traces[i].ChatID == session.ID && traces[i].Path == path && traces[i].AgentID == session.NowAgent {
					traces[i].LastContent = content
				}
			}
			cache[session.NowAgent] = traces
		}
	}
}

// ConfirmEditContent 记录 Agent 编辑完成后的最终磁盘内容，供同一轮后续 edit 校验。
func ConfirmEditContent(session *structs.Chats, path, content string) {
	confirmTraceContent(session, path, content)
}

// CheckEditContent 确认本轮请求使用的 traced 文件内容在 edit 前没有被外部改写。
// 未进入本轮 trace 上下文的文件保持既有 edit 语义。
func CheckEditContent(session *structs.Chats, path, content string) error {
	if session == nil || strings.HasPrefix(path, "@temp/") || session.TemporyDataOfSession == nil {
		return nil
	}
	confirmed, _ := session.TemporyDataOfSession[structs.TempKeyTraceConfirmedContent].(traceExpectedContent)
	if expected, ok := confirmed[path]; ok && expected != content {
		return fmt.Errorf("file changed outside the agent after the current trace sync; call read for %q before editing", path)
	}
	return nil
}

// InvalidateTraceCache 清理 summary 后不能继续使用的会话级 trace 派生缓存。
// 数据库事务提交后调用，下一次构建会从数据库重新加载剩余 trace 和事件。
func InvalidateTraceCache(session *structs.Chats) {
	if session == nil || session.TemporyDataOfSession == nil {
		return
	}
	delete(session.TemporyDataOfSession, "tools:trace")
	delete(session.TemporyDataOfSession, structs.TempKeyTraceEvents)
	delete(session.TemporyDataOfSession, structs.TempKeyTracePrevEvents)
	delete(session.TemporyDataOfSession, structs.TempKeyTraceFileBlocks)
	delete(session.TemporyDataOfSession, structs.TempKeyTraceDiffPlan)
}

// readTraceFileContent 读取被追踪文件的原始内容（@temp 读 ReferFiles，普通文件读磁盘），
// 返回编码转换后的字符串内容；失败返回 ok=false。
func readTraceFileContent(session *structs.Chats, nowpath string, traceObj structs.Traces) (string, bool) {
	if vpath, ok := strings.CutPrefix(traceObj.Path, "@temp/"); ok {
		var fileObj structs.ReferFiles
		session.DB.Where("chat_id = ?", session.ID).Where("path = ?", vpath).First(&fileObj)
		if fileObj.Content == "" {
			return "", false
		}
		return fileObj.Content, true
	}
	path := filepath.Join(nowpath, traceObj.Path)
	stat, err := os.Stat(path)
	if err != nil {
		logger.Warn("trace warning: \"%s\" get stat error: %v", traceObj.Path, err)
		return "", false
	}
	if stat.Size() > MaxFileSize {
		logger.Warn("trace warning: \"%s\" too large (%d)", traceObj.Path, stat.Size())
		return "", false
	}
	content, err := os.ReadFile(path)
	if err != nil {
		logger.Warn("trace warning: \"%s\" read error: %v", traceObj.Path, err)
		return "", false
	}
	str := fileContentToString(content)
	if len(str) == 0 {
		return "", false
	}
	return str, true
}

// renderContentBlock 用给定内容（已编码转换）渲染 FileBlock（逐行加行号）。
// 输出与历史 renderTraceFile 字节一致，供旧内容块渲染以保持前缀缓存字节稳定。
func renderContentBlock(name, content string) (FileBlock, bool) {
	lines := strings.Split(content, "\n")
	if len(lines) > MaxFileLine {
		logger.Warn("trace warning: \"%s\" too long (%d)", name, len(lines))
		return FileBlock{}, false
	}
	allLenStrLen := len(fmt.Sprintf("%d", len(lines)))
	builder := strings.Builder{}
	for lineno, line := range lines {
		fmt.Fprintf(&builder, "%*d|%s\n", allLenStrLen, lineno+1, line)
	}
	return FileBlock{
		Name:   name,
		Size:   strconv.Itoa(len(content)),
		Length: uint32(len(content)),
		Text:   builder.String(),
	}, true
}

// renderTraceFile 渲染单个被追踪文件的内容块（读盘 + 编码转换 + 逐行行号），失败返回 ok=false。
func renderTraceFile(session *structs.Chats, nowpath string, traceObj structs.Traces) (FileBlock, bool) {
	str, ok := readTraceFileContent(session, nowpath, traceObj)
	if !ok {
		return FileBlock{}, false
	}
	return renderContentBlock(traceObj.Path, str)
}

// isEventFile 判断 path（文件或 @task）在本轮是否有最近 read/edit 事件。
func isEventFile(session *structs.Chats, path string) bool {
	if session.TemporyDataOfSession == nil {
		return false
	}
	m, ok := session.TemporyDataOfSession[structs.TempKeyTraceEvents].(map[string]*structs.TraceEvent)
	if !ok {
		return false
	}
	_, ok = m[path]
	return ok
}

// canKeepEventDiff 只有存在稳定旧块锚点时才允许事件路径保留旧块并插入增量。
// 否则必须退化为最新完整块并推进缓存，避免 DiffPlan 在插入阶段无法消费。
func canKeepEventDiff(session *structs.Chats, path string) bool {
	if !isEventFile(session, path) {
		return true
	}
	prev, ok := session.TemporyDataOfSession[structs.TempKeyTracePrevEvents].(map[string]*structs.TraceEvent)
	if !ok {
		return false
	}
	_, ok = prev[path]
	return ok
}

// RenderTraceBlocks 读取当前 agent 的 Traces 列表，按事件映射分区渲染内容块：
//   - 有最近 read/edit 事件的文件 → 存入 eventBlocks（map[path]FileBlock）并写入
//     session.TemporyDataOfSession[TempKeyTraceFileBlocks]，供 build 包按事件插入；
//   - 无事件的文件 → 渲染进 topBlock（顶部聚合，现状）。
func RenderTraceBlocks(session *structs.Chats) (topBlock string, eventBlocks map[string]FileBlock, err error) {
	nowpath := session.Root
	if nowpath == "" {
		nowpath = "."
	}
	activatePath := session.CurrentActivatePath
	if activatePath == "" {
		activatePath = "."
	}
	nowpath = filepath.Join(nowpath, activatePath)
	nowpath, err = filepath.Abs(nowpath)
	if err != nil {
		return "", nil, errors.New("failed to get absolute path")
	}
	if session.TemporyDataOfSession == nil {
		session.TemporyDataOfSession = make(map[string]any)
	}
	if _, ok := session.TemporyDataOfSession["tools:trace"]; !ok {
		session.TemporyDataOfSession["tools:trace"] = traceCache{}
	}
	if _, ok := session.TemporyDataOfSession["tools:trace"].(traceCache); !ok {
		session.TemporyDataOfSession["tools:trace"] = traceCache{}
	}
	if _, ok := session.TemporyDataOfSession["tools:trace"].(traceCache)[session.NowAgent]; !ok {
		// 读 db
		traces := []structs.Traces{}
		err := session.DB.Where("chat_id = ? AND agent_id = ?", session.ID, session.NowAgent).Find(&traces).Error
		if err != nil {
			return "", nil, err
		}
		session.TemporyDataOfSession["tools:trace"].(traceCache)[session.NowAgent] = traces
	}
	traces, ok := session.TemporyDataOfSession["tools:trace"].(traceCache)[session.NowAgent]
	if !ok {
		return "", nil, errors.New("failed to read traces from database")
	}

	mult, retention := cacheModelConfig(session)
	timeout := cacheTimeout(session, retention)

	eventBlocks = make(map[string]FileBlock)
	diffPlans := make(map[string]DiffPlan)
	topFrags := make([]FileBlock, 0, len(traces))
	for _, traceObj := range traces {
		newContent, ok := readTraceFileContent(session, nowpath, traceObj)
		if !ok {
			continue
		}
		// 缓存决策：方案2（保留旧块+diff）记录到 diffPlans；eventBlocks 仍保留最新块作为退化 fallback
		plan, keep := decideDiffPlan(traceObj.Path, traceObj.LastContent, newContent, timeout, mult)
		if keep && !canKeepEventDiff(session, traceObj.Path) {
			keep = false
		}
		// LastContent 是方案2 diff 的「旧端存档」= 上次以完整块注入上下文的文件内容。
		// 只在方案1（注入完整块）/首次/无变化/@temp 时推进；方案2 候选不推进——
		// 否则现场盘面每轮变化会把 diff 旧端带跑，下次旧块用推进后的内容渲染成
		// 模型从未见过的字节，前缀缓存恰在旧块处断裂（连续编辑缓存率下跌的根因）。
		if keep {
			diffPlans[traceObj.Path] = plan
		} else if traceObj.LastContent != newContent && !strings.HasPrefix(traceObj.Path, "@temp/") {
			session.DB.Model(&structs.Traces{}).
				Where("chat_id = ? AND path = ? AND agent_id = ?", session.ID, traceObj.Path, session.NowAgent).
				Update("last_content", newContent)
		}
		frag, ok := renderContentBlock(traceObj.Path, newContent)
		if !ok {
			continue
		}
		if isEventFile(session, traceObj.Path) {
			eventBlocks[traceObj.Path] = frag
		} else if keep {
			// 无历史事件可锚定时，仍将稳定旧块和增量 diff 放入顶部，不能静默丢失外部修改。
			topFrags = append(topFrags, plan.OldBlock, plan.DiffBlock)
		} else {
			topFrags = append(topFrags, frag)
		}
		confirmTraceContent(session, traceObj.Path, newContent)
	}
	// 始终渲染（含空 slice）：trace.md 有固定 intro 头部，空文件列表也应输出该说明，保持与原 buildTrace 一致
	topBlock, err = prompts.Render(traceTempate, topFrags)
	if err != nil {
		return "", nil, err
	}
	session.TemporyDataOfSession[structs.TempKeyTraceFileBlocks] = eventBlocks
	session.TemporyDataOfSession[structs.TempKeyTraceDiffPlan] = diffPlans
	return topBlock, eventBlocks, nil
}

// RenderTraceBlock 渲染一批 FileBlock 为单个 <tracedFiles> 内容块（单 intro + 多 <file>）。
func RenderTraceBlock(files []FileBlock) (string, error) {
	return prompts.Render(traceTempate, files)
}

func buildTrace(session *structs.Chats) (string, error) {
	topBlock, _, err := RenderTraceBlocks(session)
	return topBlock, err
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
			Func:     Trace,
		},
	}); err != nil {
		panic(err)
	}
	if err := actions.HookTool("", &toolobj.Hook{
		Scope: "",
		PreHook: toolobj.PreHookFunction{
			Priority: 100,
			Func:     buildTrace,
		},
		OnHook: toolobj.OnHookFunction{
			Priority: 100,
			Func:     nil,
		},
		PostHook: toolobj.PostHookFunction{
			Priority: 100,
			Func:     nil,
		},
	}); err != nil {
		panic(err)
	}
	return toolName
}

func init() {
	index.AddIndex(load)
}

// AddTempObject 添加临时文件
func AddTempObject(session *structs.Chats, path string, content string, ro bool) error {
	// 截取内容末尾 MaxFileLine 行，避免临时对象超过 trace 注入上限。
	if ln := len(strings.Split(content, "\n")); ln > MaxFileLine {
		content = "(omitted)\n" + strings.Join(strings.Split(content, "\n")[ln-(MaxFileLine-2):], "\n")
	}
	err := session.DB.Create(structs.ReferFiles{
		ChatID:   session.ID,
		Path:     path,
		Content:  content,
		ReadOnly: ro,
	}).Error
	if err != nil {
		logger.Warn("add temp failed: %v", err)
		return err
	}

	// 若该临时文件已 trace 过，跳过 Traces 写入（静默成功，避免复合主键唯一约束冲突）
	tracePath := "@temp/" + path
	var tracedCount int64
	if err := session.DB.Model(&structs.Traces{}).
		Where("chat_id = ? AND path = ? AND agent_id = ?", session.ID, tracePath, session.NowAgent).
		Count(&tracedCount).Error; err != nil {
		logger.Warn("check trace failed: %v", err)
		return err
	}
	if tracedCount == 0 {
		// 更新 TraceID
		session.TraceID++
		// 写数据库
		trace := structs.Traces{
			ChatID:  session.ID,
			Path:    tracePath,
			TraceID: session.TraceID,
			AgentID: session.NowAgent,
		}
		err = session.DB.Save(&trace).Error
		if err != nil {
			logger.Warn("add trace failed: %v", err)
			return err
		}

		// 后台静默索引（打 tempfs 标签）
		go func() {
			if indexTaskFn != nil {
				indexTaskFn(session.Root, path, content, content, []string{"tempfs"})
			}
		}()
		err = session.DB.Model(&structs.Chats{}).Where("id = ?", session.ID).Update("trace_id", session.TraceID).Error
		if err != nil {
			logger.Warn("add trace failed: %v", err)
			return err
		}
	}

	if session.TemporyDataOfSession == nil {
		session.TemporyDataOfSession = make(map[string]any)
	}
	if _, ok := session.TemporyDataOfSession["tools:trace"]; !ok {
		session.TemporyDataOfSession["tools:trace"] = traceCache{}
	}
	if _, ok := session.TemporyDataOfSession["tools:trace"].(traceCache); !ok {
		session.TemporyDataOfSession["tools:trace"] = traceCache{}
	}
	traces := []structs.Traces{}
	err = session.DB.Where("chat_id = ? AND agent_id = ?", session.ID, session.NowAgent).Find(&traces).Error
	if err != nil {
		logger.Warn("sync trace failed: %v", err)
		return err
	}
	session.TemporyDataOfSession["tools:trace"].(traceCache)[session.NowAgent] = traces

	return err
}

// UpdateTempObject 更新已存在的临时对象内容（按 ChatID+Path 主键覆盖）。
// 用于后台任务定期刷新运行状态/最终结果。
func UpdateTempObject(session *structs.Chats, path string, content string) error {
	// 截取ctn后2000行（与 AddTempObject 保持一致）
	if ln := len(strings.Split(content, "\n")); ln > 2000 {
		content = "(omitted)\n" + strings.Join(strings.Split(content, "\n")[ln-1998:], "\n")
	}
	err := session.DB.Model(&structs.ReferFiles{}).
		Where("chat_id = ? AND path = ?", session.ID, path).
		Update("content", content).Error
	if err != nil {
		logger.Warn("update temp object failed: %v", err)
	}
	return err
}

// StoreTempObject 仅存储到 ReferFiles，不创建 Traces 记录。
// 用于 prompt 分类器，避免 code/log 段被自动 trace。
// ---------------------------------------------------------------------------
// 后台索引（函数指针，由 ui/startup 注入，避免循环导入）
// ---------------------------------------------------------------------------

// IndexTaskFn 后台索引函数类型
// directory: 工作目录, filePath: 文件相对路径, fullContent: 完整内容, embedText: 嵌入文本, tags: 标签列表
type IndexTaskFn func(directory string, filePath string, fullContent string, embedText string, tags []string) error

// indexTaskFn 函数指针，由 SetIndexTaskFn 在启动时注入
var indexTaskFn IndexTaskFn

// SetIndexTaskFn 设置后台索引函数
func SetIndexTaskFn(fn IndexTaskFn) {
	indexTaskFn = fn
}

// StoreTempObject 存储临时对象（不创建 Traces 记录）
func StoreTempObject(session *structs.Chats, path string, content string, ro bool) error {
	err := session.DB.Create(structs.ReferFiles{
		ChatID:   session.ID,
		Path:     path,
		Content:  content,
		ReadOnly: ro,
	}).Error
	if err != nil {
		logger.Warn("store temp object failed: %v", err)
	}
	return err
}
