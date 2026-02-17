package agents

import (
	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/storage/structs"
)

var logger *log.LogsObj

func init() {
	logger = log.New("agents")
}

// // CurrentAgentCode 当前代理代号
// var CurrentAgentCode string

// // CurrentAgentID 当前代理ID
// var CurrentAgentID string

// // CurrentAgentConfig 代理配置
// var CurrentAgentConfig cfgStructs.AgentConfig

// Load 加载代理
func Load(session *structs.Chats) error {
	return LoadAgent(session)
}
