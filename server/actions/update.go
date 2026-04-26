package actions

import "github.com/cxykevin/alkaid0/config"

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

func init() {
	config.AddReloadHook(updateCfgsToConns)
}

func reloadFunc(_ any, _ func(string, any) error, _ uint64) (any, error) {
	go updateCfgsToConns()
	return nil, nil
}
