package agents

import (
	"errors"
	"path/filepath"
	"strings"

	agentconfig "github.com/cxykevin/alkaid0/provider/request/agents/config"
	"github.com/cxykevin/alkaid0/storage/structs"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
)

// validateBindPath 校验子代理绑定路径必须是合法相对路径（AddAgent/UpdateAgent 共用，
// 避免 UpdateAgent 绕过校验逃逸绑定路径/工作区根目录）。
// 用规范化后的 ".." 组件判断逃逸而非子串匹配，避免误拒 "lib/v2..1" 这类合法名称。
func validateBindPath(path string) error {
	if path == "" {
		return errors.New("path must not be empty")
	}
	// 绝对路径判断必须跨平台：filepath.IsAbs 在 Windows 上不认 "/"、"\" 前缀
	//（只认盘符与 UNC），导致 "/foo" 漏检。这里显式把两种根前缀视为绝对路径。
	if filepath.IsAbs(path) ||
		strings.HasPrefix(path, "/") ||
		strings.HasPrefix(path, `\`) ||
		strings.HasPrefix(path, "~") {
		return errors.New("path must be a correct and relative path")
	}
	cleaned := filepath.Clean(path)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return errors.New("path cannot contains '..'")
	}
	if strings.Contains(path, ":") ||
		strings.Contains(path, "*") ||
		strings.Contains(path, "?") ||
		strings.Contains(path, "\"") ||
		strings.Contains(path, "<") ||
		strings.Contains(path, ">") ||
		strings.Contains(path, "|") ||
		strings.Contains(path, "\n") ||
		strings.Contains(path, "\r") ||
		strings.Contains(path, "\t") {
		return errors.New("path must be a correct and relative path")
	}
	return nil
}

// AddAgent 添加新Agent对象
func AddAgent(session *structs.Chats, agentCode string, agentID string, path string) error {
	// 检查path
	if err := validateBindPath(path); err != nil {
		return err
	}

	_, ok := agentconfig.GetAgentConfig(agentID)
	if !ok {
		return errors.New("agent id not found")
	}

	err := session.DB.Create(storageStructs.SubAgents{
		ID:       agentCode,
		AgentID:  agentID,
		BindPath: path,
	}).Error
	if err != nil {
		return err
	}
	return nil
}

// DeleteAgent 删除Agent对象
func DeleteAgent(session *structs.Chats, agentCode string) error {
	// 禁止删除当前处于激活状态的子代理，否则 Chats.NowAgent 悬空导致会话无法再打开
	if session.NowAgent == agentCode {
		return errors.New("cannot delete the currently active agent, deactivate it first")
	}
	err := session.DB.Where("id = ?", agentCode).Delete(storageStructs.SubAgents{}).Error
	return err
}
