package agents

import (
	"github.com/cxykevin/alkaid0/storage/structs"
	"gorm.io/gorm"
)

// ListAgents 列出所有代理
func ListAgents(db *gorm.DB) ([]structs.SubAgents, error) {
	var agents []structs.SubAgents
	err := db.Find(&agents).Error
	if err != nil {
		logger.Error("agents list error: %v", err)
	}
	return agents, err
}
