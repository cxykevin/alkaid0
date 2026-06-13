package actions

import "github.com/cxykevin/alkaid0/config"

// updateCfgsToConns 将当前配置广播推送到所有已连接的会话
func updateCfgsToConns() {
	sessionConnLock.Lock()
	defer sessionConnLock.Unlock()
	for i := range sessionConnMap {
		sess, ok := sessions[i]
		if !ok {
			continue
		}
		modelID := sess.session.LastModelID
		sessionConnLock.Unlock()
		broadcastSessionUpdate(i, SessionUpdate{
			SessionID: i,
			Update: SessionUpdateUpdate{
				SessionUpdate: "config_option_update",
				Content:       buildConfigOptions(uint32(modelID)),
			},
		}, 0)
		sessionConnLock.Lock()
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
