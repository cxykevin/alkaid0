package tools

import (
	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/storage"
	sstructs "github.com/cxykevin/alkaid0/storage/structs"
)

var scopesLogger = log.New("tools:scopes")

// SetScopeEnabled 设置或更新命名空间启用状态
func SetScopeEnabled(name string, enabled bool) error {
	if storage.DB == nil {
		// 如果 DB 未初始化，记录并返回 nil，不阻塞业务
		scopesLogger.Info("DB not initialized, skip persist scope %s", name)
		return nil
	}
	s := sstructs.Scopes{Name: name, Enabled: enabled}
	return storage.DB.Save(&s).Error
}

// GetAllScopes 返回数据库中所有命名空间的启用状态
func GetAllScopes() (map[string]bool, error) {
	result := make(map[string]bool)
	if storage.DB == nil {
		return result, nil
	}
	var rows []sstructs.Scopes
	if err := storage.DB.Find(&rows).Error; err != nil {
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
