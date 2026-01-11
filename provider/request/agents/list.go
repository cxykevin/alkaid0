package agents

import (
	"github.com/cxykevin/alkaid0/storage"
	"github.com/cxykevin/alkaid0/storage/structs"
)

// ListAgents 列出所有代理
func ListAgents() ([]structs.SubAgents, error) {
	var agents []structs.SubAgents
	err := storage.DB.Find(&agents).Error
	if err != nil {
		logger.Error("agents list error: %v", err)
	}
	return agents, err
}
