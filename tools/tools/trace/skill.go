package trace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cxykevin/alkaid0/internal/skillpath"
	"github.com/cxykevin/alkaid0/skill"
	"github.com/cxykevin/alkaid0/storage/structs"
)

// skill 启用/停用时的读取上下文维护：
//   - 启用 → 把 .alkaid0/skills/<name>/SKILL.md 加入会话 trace list；
//   - 停用 → 删除该 skill 目录下的所有 trace（所有 agent）。
//
// skill 包不能 import trace（trace → skill），因此由这里注册钩子。
func init() {
	skill.SetHooks(skill.Hooks{
		OnEnabled:  traceSkillManifest,
		OnDisabled: untraceSkillDir,
	})
}

// traceSkillManifest 启用 skill 时把 SKILL.md 加入读取上下文。
func traceSkillManifest(session *structs.Chats, s skill.Skill) {
	rel := s.WorkspaceManifest()
	abs := filepath.Join(session.Root, filepath.FromSlash(rel))
	if err := TraceFile(session, rel, abs); err != nil {
		logger.Warn("trace skill %q failed: %v", s.Name, err)
	}
}

// untraceSkillDir 停用 skill 时删除该 skill 目录下的所有 trace。
func untraceSkillDir(session *structs.Chats, name string) {
	if err := UntraceDir(session, skill.WorkspaceDir(name)); err != nil {
		logger.Warn("untrace skill %q failed: %v", name, err)
	}
}

// TraceFile 把 absPath 的文件内容加入会话读取上下文（记录路径为 path）。
//
// 供 skill 启用时自动注入 SKILL.md；与 read 工具共用大小、行数与去重逻辑，
// 但路径已经是确定的绝对路径，不再做工作区相对路径解析。
func TraceFile(session *structs.Chats, path, absPath string) error {
	if session == nil || session.DB == nil {
		return errors.New("session database is not available")
	}
	if path == "" || absPath == "" {
		return errors.New("empty trace path")
	}
	if err := session.GetContext().Err(); err != nil {
		return fmt.Errorf("trace cancelled: %w", err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	if info.Size() > MaxFileSize {
		return fmt.Errorf("%s is too large", path)
	}
	raw, err := os.ReadFile(absPath)
	if err != nil {
		return err
	}
	content := fileContentToString(raw)
	if content == "" {
		return fmt.Errorf("%s is empty or cannot readable (may be binary file)", path)
	}
	if len(strings.Split(content, "\n")) > MaxFileLine {
		return fmt.Errorf("%s is too long", path)
	}
	// 技能入口文件要完整注入（启用即"加载技能"）：不走大文件受限视图。
	if err := writeTraceRow(session, path, content, "", false); err != nil {
		return err
	}
	InvalidateTraceCache(session)
	return nil
}

// UntraceDir 删除会话内所有位于 dir（工作区相对路径）之下的 trace 行，
// 覆盖所有 Agent；同时清掉相关的内容确认缓存。
func UntraceDir(session *structs.Chats, dir string) error {
	if session == nil || session.DB == nil {
		return errors.New("session database is not available")
	}
	dir = filepath.ToSlash(filepath.Clean(dir))
	if dir == "" || dir == "." || dir == "/" {
		return errors.New("invalid trace directory")
	}
	var rows []structs.Traces
	if err := session.DB.Where("chat_id = ?", session.ID).Find(&rows).Error; err != nil {
		return err
	}
	prefix := dir + "/"
	deleted := 0
	for _, row := range rows {
		clean := filepath.ToSlash(filepath.Clean(row.Path))
		if clean != dir && !strings.HasPrefix(clean, prefix) {
			continue
		}
		if err := session.DB.
			Where("chat_id = ? AND path = ? AND agent_id = ?", row.ChatID, row.Path, row.AgentID).
			Delete(&structs.Traces{}).Error; err != nil {
			return err
		}
		ForgetEditContent(session, row.Path)
		deleted++
	}
	if deleted > 0 {
		InvalidateTraceCache(session)
		logger.Info("untraced %d file(s) under %s in ID=%d", deleted, dir, session.ID)
	}
	return nil
}

// isSkillTracePath 判断 trace 路径是否落在工作区 skill 链接目录下。
func isSkillTracePath(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	base := skillpath.WorkspaceDataDir + "/" + skillpath.LinkDirName
	return clean == base || strings.HasPrefix(clean, base+"/")
}

// writeTraceRow 把 path 的内容写入会话读取上下文：
// 已跟踪时只更新读取模式；否则新建行、推进 TraceID 并后台索引。
func writeTraceRow(session *structs.Chats, path, content, readMode string, isHistory bool) error {
	var tracedCount int64
	if err := session.DB.Model(&structs.Traces{}).
		Where("chat_id = ? AND path = ? AND agent_id = ?", session.ID, path, session.NowAgent).
		Count(&tracedCount).Error; err != nil {
		return fmt.Errorf("check trace: %w", err)
	}
	if tracedCount != 0 {
		// 已跟踪：本轮的读取模式（首次受限 / 再次 read 升级全量）写回行，
		// 下一轮构建上下文时据此决定注入大纲、截断还是全文。
		if err := session.DB.Model(&structs.Traces{}).
			Where("chat_id = ? AND path = ? AND agent_id = ?", session.ID, path, session.NowAgent).
			Update("read_mode", readMode).Error; err != nil {
			logger.Warn("update read mode failed: %v", err)
		}
		return nil
	}
	session.TraceID++
	trace := structs.Traces{
		ChatID:      session.ID,
		Path:        path,
		TraceID:     session.TraceID,
		AgentID:     session.NowAgent,
		LastContent: content,
		ReadMode:    readMode,
	}
	if err := session.DB.Save(&trace).Error; err != nil {
		return fmt.Errorf("save trace: %w", err)
	}
	if err := session.DB.Model(&structs.Chats{}).Where("id = ?", session.ID).Update("trace_id", session.TraceID).Error; err != nil {
		return fmt.Errorf("update chat trace id: %w", err)
	}
	// 后台静默索引（打 tempfs 标签）。历史会话内容来自其它会话的消息表，
	// 不是文件产物，不进工作区索引；history 模式的 search 直接查消息表。
	if !isHistory {
		go func() {
			idxPath := path
			if vpath, ok := strings.CutPrefix(idxPath, "@temp/"); ok {
				idxPath = vpath
			}
			if indexTaskFn != nil {
				indexTaskFn(session.Root, idxPath, content, content, []string{"tempfs"})
			}
		}()
	}
	return nil
}
