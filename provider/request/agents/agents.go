package agents

import (
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/storage"
	"github.com/cxykevin/alkaid0/storage/structs"
)

var logger *log.LogsObj

func init() {
	logger = log.New("agents")
}

// CurrentAgentCode 当前代理代号
var CurrentAgentCode string

// CurrentAgentID 当前代理ID
var CurrentAgentID string

// CurrentAgentConfig 代理配置
var CurrentAgentConfig cfgStructs.AgentConfig

// Load 加载代理
func Load(chatID uint32) error {
	queryObj := structs.Chats{}
	err := storage.DB.Where("id = ?", chatID).First(&queryObj).Error
	if err != nil {
		return err
	}
	CurrentAgentCode = queryObj.NowAgent
	return LoadAgent(CurrentAgentCode)
}
