package trace

import (
	_ "embed" // embed
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/prompts"
	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/actions"
	docresources "github.com/cxykevin/alkaid0/tools/docs"
	"github.com/cxykevin/alkaid0/tools/index"
	"github.com/cxykevin/alkaid0/tools/toolobj"
	u "github.com/cxykevin/alkaid0/utils"
	"gorm.io/gorm/clause"
)

const toolName = "read"

//go:embed prompt.md
var prompt string

//go:embed trace.md
var tracePrompt string

var traceTempate *template.Template

var logger = log.New("tools:trace")

// MaxFileLine 最大文件行数
const MaxFileLine = 10000

// MaxFileSize 最大文件大小
const MaxFileSize = 256 * 1024 // 256KB

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
	// 类型用模型实际调用的工具名（read）：直播的标题/kind 因此与 session/resume
	// 回放（从落库工具名重建）完全一致。
	session.SetToolCalling(toolCallID, respObj, toolName)
	return true, cross, nil
}

// Trace 跟踪文件
func Trace(session *structs.Chats, mp map[string]*any, push []*any) (bool, []*any, map[string]*any, error) {
	if session == nil || session.DB == nil {
		// 会话未加载数据库（异常调用路径、单元测试等）：返回错误结果即可。
		// 直接访问 session.DB.Model 会命中 gorm 的 nil 接收者并 panic，
		// 而工具在独立 goroutine 中执行，panic 会终止整个进程。
		boolx := false
		success := any(boolx)
		errMsg := any("session database is not available")
		return false, push, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

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
	// 与 edit.go 一致：先拼接 Root 与激活路径，再决定是否回退到当前目录。
	// 旧写法先把空 Root 换成 "."，此后 filepath.Join(".", "/abs/path") 会把
	// 绝对路径清理成相对路径（"tmp/..."），Abs 之后指向一个不存在的位置，
	// 包含性校验必然失败并报 "path escapes the workspace via symlink"。
	nowpath := filepath.Join(session.Root, session.CurrentActivatePath)
	if nowpath == "" {
		nowpath = "."
	}
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

	if strings.HasPrefix(path, "@docs") {
		if !isDocsPath(path) {
			boolx := false
			success := any(boolx)
			errMsg := any("invalid @docs path")
			return false, push, map[string]*any{"success": &success, "error": &errMsg}, nil
		}
		snapshots := getDocsSnapshots(session)
		snapshot, exists := snapshots[path]
		if unread {
			if !exists {
				boolx := false
				success := any(boolx)
				errMsg := any("no such trace")
				return false, push, map[string]*any{"success": &success, "error": &errMsg}, nil
			}
			snapshot.Active = false
			snapshots[path] = snapshot
			clearDocsEventCache(session, path)
		} else {
			if !exists {
				content, ok := docresources.Read(path)
				if !ok {
					boolx := false
					success := any(boolx)
					errMsg := any("file not exist")
					return false, push, map[string]*any{"success": &success, "error": &errMsg}, nil
				}
				if len(content) > MaxFileSize || len(strings.Split(content, "\n")) > MaxFileLine {
					boolx := false
					success := any(boolx)
					errMsg := any("file too large")
					return false, push, map[string]*any{"success": &success, "error": &errMsg}, nil
				}
				snapshot = docsSnapshot{Content: content}
			}
			snapshot.Active = true
			snapshots[path] = snapshot
		}
		boolx := true
		success := any(boolx)
		msg := "The file has been read and injected into the top of the context."
		msgAny := any(msg)
		pathAny := any(path)
		return false, push, map[string]*any{"success": &success, "message": &msgAny, "path": &pathAny}, nil
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
		InvalidateTraceCache(session)
	} else {
		var str string
		var err error
		if vpath, ok := strings.CutPrefix(path, "@temp/"); ok {
			// 查db：记录不存在时必须报错。旧实现忽略 First 的错误，把空内容当成
			// 读取成功，还写脏 Traces 行并递增 TraceID（模型看到"空文件"却无任何提示）。
			var fileObj structs.ReferFiles
			if err := session.DB.Where("chat_id = ?", session.ID).Where("path = ?", vpath).First(&fileObj).Error; err != nil {
				logger.Warn("temp file not found: %s", path)
				boolx := false
				success := any(boolx)
				errMsg := any("file not exist")
				return false, push, map[string]*any{
					"success": &success,
					"error":   &errMsg,
				}, nil
			}
			str = fileObj.Content
		} else {
			path2 := filepath.Join(nowpath, path)
			path2 = filepath.Clean(path2)
			// read 由内置规则自动批准，纯词法校验挡不住工作区内指向外部的符号链接
			// （os.Stat / os.ReadFile 都会跟随链接）。这里补上"解析符号链接 +
			// 保护 .alkaid0"的包含性校验。
			if err := u.EnsureWorkspacePath(nowpath, path2, ".alkaid0"); err != nil {
				boolx := false
				success := any(boolx)
				errMsg := any(err.Error())
				return false, push, map[string]*any{
					"success": &success,
					"error":   &errMsg,
				}, nil
			}
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
			// FIFO/设备/目录不是可读文件：os.ReadFile 会在 FIFO 上永久阻塞且不可取消
			if !stat.Mode().IsRegular() {
				boolx := false
				success := any(boolx)
				errMsg := any("not a regular file")
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

type docsSnapshot struct {
	Content string
	Active  bool
}

// docsSnapshots 按 agent 隔离文档状态，避免子代理互相看到或停用对方的 @docs。
type docsSnapshots map[string]map[string]docsSnapshot

func getDocsSnapshots(session *structs.Chats) map[string]docsSnapshot {
	if session.TemporyDataOfSession == nil {
		session.TemporyDataOfSession = make(map[string]any)
	}
	all, _ := session.TemporyDataOfSession[structs.TempKeyTraceDocsSnapshots].(docsSnapshots)
	if all == nil {
		all = make(docsSnapshots)
		session.TemporyDataOfSession[structs.TempKeyTraceDocsSnapshots] = all
	}
	snapshots := all[session.NowAgent]
	if snapshots == nil {
		snapshots = make(map[string]docsSnapshot)
		all[session.NowAgent] = snapshots
	}
	return snapshots
}

func isDocsPath(path string) bool {
	return docresources.IsDocsPath(path)
}

func clearDocsEventCache(session *structs.Chats, path string) {
	if cache, ok := session.TemporyDataOfSession[structs.TempKeyTraceFileBlocks].(map[string]FileBlock); ok {
		delete(cache, path)
	}
	if events, ok := session.TemporyDataOfSession[structs.TempKeyTraceEvents].(map[string]*structs.TraceEvent); ok {
		delete(events, path)
	}
	if prev, ok := session.TemporyDataOfSession[structs.TempKeyTracePrevEvents].(map[string]*structs.TraceEvent); ok {
		delete(prev, path)
	}
}

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
	confirmed[normalizeTraceContentKey(path)] = content
}

// normalizeTraceContentKey 统一 confirmed 内容的路径键。
// read 记录 "a.cs"、edit 用 "./a.cs" 必须命中同一条记录，否则外部修改保护会被
// 静默绕过；虚拟对象路径（@temp/、@docs/ 等）保持原样。
func normalizeTraceContentKey(path string) string {
	if path == "" || strings.HasPrefix(path, "@") {
		return path
	}
	return filepath.Clean(path)
}

// advanceTraceCache 将实际退化为完整内容块时的内容写回 trace 缓存。
// 方案2只在旧块和 diff 都成功插入时保留 LastContent；请求构建阶段若锚点或成本复核失败，
// 模型收到的是完整当前块，缓存也必须同步到该内容，避免下一轮重复生成同一份 diff。
// @temp/* 同样适用：last_content 是"上次以完整块注入的字节"，也是判定"内容变了但没有新事件"的依据。
func AdvanceTraceCache(session *structs.Chats, path string) {
	if session == nil || session.DB == nil || isDocsPath(path) {
		return
	}
	confirmed, _ := session.TemporyDataOfSession[structs.TempKeyTraceConfirmedContent].(traceExpectedContent)
	content, ok := confirmed[normalizeTraceContentKey(path)]
	if !ok {
		return
	}
	AdvanceTraceLastContent(session, path, content)
}

// AdvanceTraceLastContent 把 path 的「上次以完整块注入的字节」推进为 content（写库 + 同步会话缓存）。
func AdvanceTraceLastContent(session *structs.Chats, path, content string) {
	if session == nil || session.DB == nil || path == "" {
		return
	}
	// upsert：虚拟对象（@tree）首次注入时库里还没有对应行，必须能建行，锚点/基线才不会在重启后丢失。
	if err := session.DB.Omit(clause.Associations).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "path"}, {Name: "chat_id"}, {Name: "agent_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"last_content"}),
	}).Create(&structs.Traces{Path: path, ChatID: session.ID, AgentID: session.NowAgent, LastContent: content}).Error; err != nil {
		logger.Warn("advance last_content failed for %q: %v", path, err)
	}
	updateCachedTrace(session, path, func(t *structs.Traces) { t.LastContent = content })
}

// SetTraceAnchor 记录 path 的注入锚点（上次注入内容块所紧跟的可渲染消息 id）。
// 内容未变化时据此让块留在原位；内容变了但没有新事件承载时由 build 层前移到末尾并回写。
func SetTraceAnchor(session *structs.Chats, path string, msgID uint64) {
	if session == nil || session.DB == nil || path == "" {
		return
	}
	if err := session.DB.Omit(clause.Associations).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "path"}, {Name: "chat_id"}, {Name: "agent_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"anchor_msg_id"}),
	}).Create(&structs.Traces{Path: path, ChatID: session.ID, AgentID: session.NowAgent, AnchorMsgID: msgID}).Error; err != nil {
		logger.Warn("set trace anchor failed for %q: %v", path, err)
	}
	updateCachedTrace(session, path, func(t *structs.Traces) { t.AnchorMsgID = msgID })
}

// updateCachedTrace 就地更新会话内 trace 缓存里的对应行（不写库）。
func updateCachedTrace(session *structs.Chats, path string, apply func(*structs.Traces)) {
	if session == nil || session.TemporyDataOfSession == nil || path == "" {
		return
	}
	cache, ok := session.TemporyDataOfSession["tools:trace"].(traceCache)
	if !ok {
		return
	}
	traces, ok := cache[session.NowAgent]
	if !ok {
		return
	}
	found := false
	for i := range traces {
		if traces[i].ChatID == session.ID && traces[i].Path == path && traces[i].AgentID == session.NowAgent {
			apply(&traces[i])
			found = true
		}
	}
	if found {
		cache[session.NowAgent] = traces
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
	if expected, ok := confirmed[normalizeTraceContentKey(path)]; ok && expected != content {
		return fmt.Errorf("file changed outside the agent after the current trace sync; call read for %q before editing", path)
	}
	return nil
}

// ForgetEditContent 删除某个路径的已确认内容。文件已被删除（想要重建该路径）时调用，
// 否则残留的旧内容会让 CheckEditContent 永远报"内容已被外部修改"。
func ForgetEditContent(session *structs.Chats, path string) {
	if session == nil || session.TemporyDataOfSession == nil {
		return
	}
	confirmed, _ := session.TemporyDataOfSession[structs.TempKeyTraceConfirmedContent].(traceExpectedContent)
	if confirmed == nil {
		return
	}
	delete(confirmed, normalizeTraceContentKey(path))
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

// virtualContentProviders 虚拟对象（@tree 等）的内容来源。
// 由对应工具包在 init/load 阶段注册：trace 包被 edit 包反向依赖（edit → trace），
// 直接 import tree 会成环，因此用注册表解耦。
var virtualContentProviders = map[string]func(session *structs.Chats) (string, bool){}

// RegisterVirtualContent 注册虚拟对象内容提供者（path 形如 "@tree"）。
// 内容块按与普通 traced 文件相同的"事件跟随 / 末尾前移 / 差分"规则注入
// （docs/trace-cache-spec.md §10.1），不再进"历史之前的全局注入块"。
func RegisterVirtualContent(path string, fn func(session *structs.Chats) (string, bool)) {
	if path == "" || fn == nil {
		return
	}
	virtualContentProviders[path] = fn
}

// isVirtualContentPath 判断 path 是否由虚拟对象提供者提供内容。
func isVirtualContentPath(path string) bool {
	_, ok := virtualContentProviders[path]
	return ok
}

// readTraceFileContent 读取被追踪文件的原始内容
// （虚拟对象走提供者、@docs 读快照、@temp 读 ReferFiles、普通文件读磁盘），
// 返回编码转换后的字符串内容；失败返回 ok=false。
func readTraceFileContent(session *structs.Chats, nowpath string, traceObj structs.Traces) (string, bool) {
	if fn, ok := virtualContentProviders[traceObj.Path]; ok {
		content, ok := fn(session)
		if !ok || content == "" {
			return "", false
		}
		return content, true
	}
	if isDocsPath(traceObj.Path) {
		snapshot, ok := getDocsSnapshots(session)[traceObj.Path]
		return snapshot.Content, ok && snapshot.Active
	}
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
	// 已被跟踪的路径也可能在之后被换成 FIFO/设备：构建上下文时同样只读常规文件，
	// 否则会永久阻塞请求构建
	if !stat.Mode().IsRegular() {
		logger.Warn("trace warning: \"%s\" is not a regular file", traceObj.Path)
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

// renderTraceFileFragment 渲染单个 traced 对象的内容块。
// 虚拟对象（@tree）的提供者输出的已是带行号与格式的最终文本，直接作为块文本，
// 不再走 renderContentBlock 的逐行编号（否则双重行号）。
func renderTraceFileFragment(path, content string) (FileBlock, bool) {
	if isVirtualContentPath(path) {
		if content == "" || len(content) > MaxFileSize {
			return FileBlock{}, false
		}
		return FileBlock{
			Name:   path,
			Size:   strconv.Itoa(len(content)),
			Length: uint32(len(content)),
			Text:   content,
		}, true
	}
	return renderContentBlock(path, content)
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
	snapshots := getDocsSnapshots(session)
	if events, ok := session.TemporyDataOfSession[structs.TempKeyTraceEvents].(map[string]*structs.TraceEvent); ok {
		for path := range events {
			if _, exists := snapshots[path]; exists || !isDocsPath(path) {
				continue
			}
			if content, ok := docresources.Read(path); ok {
				snapshots[path] = docsSnapshot{Content: content, Active: true}
			}
		}
	}
	docPaths := make([]string, 0, len(snapshots))
	for path, snapshot := range snapshots {
		if snapshot.Active {
			docPaths = append(docPaths, path)
		}
	}
	sort.Strings(docPaths)
	for _, path := range docPaths {
		snapshot := getDocsSnapshots(session)[path]
		traces = append(traces, structs.Traces{Path: path, ChatID: session.ID, AgentID: session.NowAgent, LastContent: snapshot.Content})
	}
	// 虚拟对象（@tree 等）既不在磁盘也不在 ReferFiles 上，库中没有对应行时补一条合成行；
	// 首次实际注入时由 SetTraceAnchor / AdvanceTraceLastContent upsert 建行。
	// 合成行必须写回会话缓存：否则每轮都会按"首次注入"（LastContent 为空）重新前移。
	cachedTraces := traces
	virtualPaths := make([]string, 0, len(virtualContentProviders))
	for path := range virtualContentProviders {
		virtualPaths = append(virtualPaths, path)
	}
	sort.Strings(virtualPaths)
	virtualAdded := make([]structs.Traces, 0, len(virtualPaths))
	for _, path := range virtualPaths {
		exists := false
		for i := range cachedTraces {
			if cachedTraces[i].Path == path {
				exists = true
				break
			}
		}
		if exists {
			continue
		}
		row := structs.Traces{Path: path, ChatID: session.ID, AgentID: session.NowAgent}
		traces = append(traces, row)
		virtualAdded = append(virtualAdded, row)
	}
	if len(virtualAdded) > 0 {
		merged := make([]structs.Traces, 0, len(cachedTraces)+len(virtualAdded))
		merged = append(merged, cachedTraces...)
		merged = append(merged, virtualAdded...)
		if cache, ok := session.TemporyDataOfSession["tools:trace"].(traceCache); ok {
			cache[session.NowAgent] = merged
		}
	}
	mult, retention := cacheModelConfig(session)
	timeout := cacheTimeout(session, retention)

	events, _ := session.TemporyDataOfSession[structs.TempKeyTraceEvents].(map[string]*structs.TraceEvent)
	prevEvents, _ := session.TemporyDataOfSession[structs.TempKeyTracePrevEvents].(map[string]*structs.TraceEvent)

	eventBlocks = make(map[string]FileBlock)
	diffPlans := make(map[string]DiffPlan)
	anchorPlans := make(map[string]*AnchorPlan)
	topFrags := make([]FileBlock, 0, len(traces))
	for _, traceObj := range traces {
		path := traceObj.Path
		ev, hasEvent := events[path]
		// 拼接期活跃裁剪（docs/trace-cache-spec.md §4.5）：events 已按 summary 边界截断，
		// 不在其中即"边界之前 / 无事件"的 trace——本轮不注入，但数据库行保留（审计不丢数据）。
		// @docs 快照与虚拟对象（@tree）的内容不来自消息事件，不参与该裁剪。
		if events != nil && !hasEvent && !isDocsPath(path) && !isVirtualContentPath(path) {
			continue
		}
		newContent, ok := readTraceFileContent(session, nowpath, traceObj)
		if !ok {
			continue
		}
		frag, ok := renderTraceFileFragment(path, newContent)
		if !ok {
			continue
		}
		confirmTraceContent(session, path, newContent)

		if ev == nil && !isVirtualContentPath(path) {
			// 无事件信息（@docs 快照 / 未走事件检测的调用路径，如直接调用或测试）：维持旧语义。
			// 生产链路由 Build → DetectTraceEvents 保证事件表恒被写入（即使是空表），不会走到这里。
			legacyPlan, keep := decideDiffPlan(path, traceObj.LastContent, newContent, timeout, mult)
			if keep && !canKeepEventDiff(session, path) {
				keep = false
			}
			if keep {
				diffPlans[path] = legacyPlan
				if isEventFile(session, path) {
					eventBlocks[path] = frag
				} else {
					// 无历史事件可锚定时，仍将稳定旧块与增量 diff 放入顶部，不能静默丢失外部修改
					topFrags = append(topFrags, legacyPlan.OldBlock, legacyPlan.DiffBlock)
				}
			} else {
				if !isDocsPath(path) && traceObj.LastContent != newContent {
					AdvanceTraceLastContent(session, path, newContent)
				}
				if isEventFile(session, path) {
					eventBlocks[path] = frag
				} else {
					topFrags = append(topFrags, frag)
				}
			}
			continue
		}

		// 落位决策（docs/trace-cache-spec.md §4.2）：
		//   未变化 → 留在上次注入锚点（字节与位置都不动，前缀命中缓存）
		//   变化且无新事件承载（后台刷新 / 外部改写 / @tree 被其它工具改动）→ 前移到消息列表末尾
		//   首次注入 / 变化来自新事件 → 走「破坏缓存 vs 保留+diff」决策
		// 虚拟对象（@tree）可能始终没有对应事件，此时一律按"末尾落位 + 回填锚点"处理。
		changed := traceObj.LastContent != newContent
		eventMsgID := uint64(0)
		if ev != nil {
			eventMsgID = ev.MsgID
		}
		diffKeep := false
		var ap *AnchorPlan
		switch {
		case !changed && traceObj.AnchorMsgID != 0:
			ap = &AnchorPlan{Mode: AnchorFull, MsgID: traceObj.AnchorMsgID, Full: true}
		case !changed && eventMsgID != 0:
			// 历史库行（锚点未回填）：按最新事件落位并回填，位置与旧行为一致
			ap = &AnchorPlan{Mode: AnchorFull, MsgID: eventMsgID, Full: true}
			SetTraceAnchor(session, path, eventMsgID)
		case !changed:
			// 无事件也无锚点：落到末尾，锚点由 build 层回写
			ap = &AnchorPlan{Mode: AnchorTail, Full: true}
		case eventMsgID != 0 && traceObj.AnchorMsgID != 0 && eventMsgID <= traceObj.AnchorMsgID:
			// 内容变了但没有新事件承载：前移到消息列表末尾
			ap = &AnchorPlan{Mode: AnchorTail, Full: true}
		default:
			// 方案2 的「旧端锚点」：优先最早 read/edit 事件（普通文件连续编辑），
			// 没有事件时回落到上次完整块注入锚点 AnchorMsgID——虚拟对象（@tree）与
			// 后台刷新类内容靠它也能拿到"字节稳定的旧块"，从而走保留+差分而不是整块前移。
			prevMsgID := uint64(0)
			if prev, ok := prevEvents[path]; ok && prev != nil && prev.MsgID != 0 {
				prevMsgID = prev.MsgID
			}
			if prevMsgID == 0 {
				prevMsgID = traceObj.AnchorMsgID
			}
			// newEvent 决定本次变化是否由新消息承载：是 → 锚到最新事件；否 → 锚到列表末尾。
			newEvent := eventMsgID != 0 && (traceObj.AnchorMsgID == 0 || eventMsgID > traceObj.AnchorMsgID)
			if prevMsgID != 0 && canKeepEventDiff(session, path) {
				if plan, keep := decideDiffPlan(path, traceObj.LastContent, newContent, timeout, mult); keep {
					// 方案2：旧块字节稳定地留在旧锚点，增量块锚到最新位置；
					// 旧端存档与注入锚点都不推进，下一次 diff 的旧端仍然稳定（前缀缓存不被打断）。
					diffPlans[path] = plan
					diffKeep = true
					if newEvent {
						ap = &AnchorPlan{Mode: AnchorDiff, MsgID: eventMsgID, PrevMsgID: prevMsgID}
					} else {
						ap = &AnchorPlan{Mode: AnchorDiffTail, PrevMsgID: prevMsgID}
					}
				}
			}
			if !diffKeep {
				if newEvent {
					// 方案1：完整块锚最新事件，并推进旧端存档
					ap = &AnchorPlan{Mode: AnchorFull, MsgID: eventMsgID, Full: true}
					SetTraceAnchor(session, path, eventMsgID)
				} else {
					// 方案1（无新事件承载）：完整块落到列表末尾，锚点由 build 层回写
					ap = &AnchorPlan{Mode: AnchorTail, Full: true}
				}
				AdvanceTraceLastContent(session, path, newContent)
			}
		}
		anchorPlans[path] = ap
		eventBlocks[path] = frag
	}
	// 始终渲染（含空 slice）：trace.md 有固定 intro 头部，空文件列表也应输出该说明，保持与原 buildTrace 一致
	topBlock, err = prompts.Render(traceTempate, topFrags)
	if err != nil {
		return "", nil, err
	}
	session.TemporyDataOfSession[structs.TempKeyTraceFileBlocks] = eventBlocks
	session.TemporyDataOfSession[structs.TempKeyTraceDiffPlan] = diffPlans
	session.TemporyDataOfSession[structs.TempKeyTraceAnchorPlan] = anchorPlans
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
	if isDocsPath(path) || strings.HasPrefix(path, "@docs") {
		return errors.New("@docs paths are read-only and cannot be stored")
	}
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
	if isDocsPath(path) || strings.HasPrefix(path, "@docs") {
		return errors.New("@docs paths are read-only and cannot be stored")
	}
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
	if isDocsPath(path) || strings.HasPrefix(path, "@docs") {
		return errors.New("@docs paths are read-only and cannot be stored")
	}
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
