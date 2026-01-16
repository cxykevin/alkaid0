package actions

import (
	"maps"

	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/storage/structs"
	"gorm.io/gorm"
)

var logger *log.LogsObj

func init() {
	logger = log.New("tools:actions")
}

// Load 从数据库加载命名空间启用状态
func Load(session *structs.Chats) {
	if session.EnableScopes == nil {
		session.EnableScopes = make(map[string]bool)
	}
	// 尝试从数据库加载命名空间启用状态（若 DB 未初始化则忽略）
	if scs, err := getAllScopes(session, session.DB); err == nil {
		maps.Copy(session.EnableScopes, scs)
	} else {
		logger.Error("failed to load scopes from storage: %v", err)
	}
}

// SetScopeEnabled 设置或更新命名空间启用状态
func SetScopeEnabled(db *gorm.DB, chatID uint32, name string, enabled bool) error {
	if db == nil {
		// 如果 DB 未初始化，记录并返回 nil，不阻塞业务
		logger.Info("DB not initialized, skip persist scope %s", name)
		return nil
	}
	s := structs.Scopes{Name: name, Enabled: enabled, ChatID: chatID}
	return db.Save(&s).Error
}

// getAllScopes 返回数据库中所有命名空间的启用状态
func getAllScopes(session *structs.Chats, db *gorm.DB) (map[string]bool, error) {
	result := make(map[string]bool)
	if db == nil {
		return result, nil
	}
	var rows []structs.Scopes
	if err := db.Where("chat_id = ?", session.ID).Find(&rows).Error; err != nil {
		return result, err
	}
	for _, r := range rows {
		if r.Name == "" {
			continue
		}
		result[r.Name] = r.Enabled
	}
	return result, nil
}
