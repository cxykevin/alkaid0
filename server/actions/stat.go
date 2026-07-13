package actions

import (
	"github.com/cxykevin/alkaid0/library/chancall"
	u "github.com/cxykevin/alkaid0/utils"
)

// GetStates 获取状态
func GetStates(_ any) (any, error) {
	sessLock.Lock()
	dbLock.Lock()
	defer dbLock.Unlock()
	defer sessLock.Unlock()
	return u.H{
		"sessions": len(sessions),
		"dbs":      len(dbs),
	}, nil
}

func init() {
	chancall.Register("actions/states", GetStates)
}
