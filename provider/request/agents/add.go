package agents

import (
	"errors"
	"strings"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/storage"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
)

// AddAgent 添加新Agent对象
func AddAgent(agentCode string, agentID string, path string) error {
	// 检查path
	if strings.Contains(path, "..") {
		return errors.New("path cannot contains '..'")
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
		return errors.New("path must be a correct and relative path")
	}

	_, ok := config.GlobalConfig.Agent.Agents[agentID]
	if !ok {
		return errors.New("agent id not found")
	}

	err := storage.DB.Create(storageStructs.SubAgents{
		AgentID:  agentID,
		BindPath: path,
	}).Error
	if err != nil {
		return err
	}
	return nil
}

// DeleteAgent 删除Agent对象
func DeleteAgent(agentCode string) error {
	err := storage.DB.Where("id = ?", agentCode).Delete(storageStructs.SubAgents{}).Error
	return err
}
