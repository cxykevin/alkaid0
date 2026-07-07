package actions

import "github.com/cxykevin/alkaid0/config"

// updateCfgsToConns 将当前配置广播推送到所有已连接的会话
func updateCfgsToConns() {
	// 收集所有需要更新的会话ID及其模型ID
	sessionConnLock.Lock()
	sessionIDs := make([]string, 0, len(sessionConnMap))
	for sid := range sessionConnMap {
		sessionIDs = append(sessionIDs, sid)
	}
	sessionConnLock.Unlock()

	sessLock.Lock()
	type sessModel struct {
		sid     string
		modelID uint32
	}
	updates := make([]sessModel, 0, len(sessionIDs))
	for _, sid := range sessionIDs {
		if sess, ok := sessions[sid]; ok {
			updates = append(updates, sessModel{sid: sid, modelID: sess.session.LastModelID})
		}
	}
	sessLock.Unlock()

	for _, u := range updates {
		broadcastSessionUpdate(u.sid, SessionUpdate{
			SessionID: u.sid,
			Update: SessionUpdateUpdate{
				SessionUpdate: "config_option_update",
				Content:       buildConfigOptions(uint32(u.modelID)),
			},
		}, 0)
	}
}

// init 注册配置重载钩子，配置变更时自动更新所有会话
func init() {
	config.AddReloadHook(updateCfgsToConns)
}

// reloadFunc 配置重载回调，触发配置广播推送
func reloadFunc(_ any, _ func(string, any, *string) error, _ uint64) (any, error) {
	go updateCfgsToConns()
	return nil, nil
}
