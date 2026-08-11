package build

import (
	"github.com/cxykevin/alkaid0/provider/parser"
	reqStruct "github.com/cxykevin/alkaid0/provider/request/structs"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"gorm.io/gorm"
)

// Build 构造请求体
func Build(db *gorm.DB, session *storageStructs.Chats) (*reqStruct.ChatCompletionRequest, error) {
	// lastChatID := storage.GlobalConfig.CurrentChatID
	// if lastChatID == 0 {
	// 	logger.Error("no last chat id")
	// 	return nil, errors.New("no last chat id")
	// }
	// 构造工具
	var scopes, traces string
	var tools *[]*parser.ToolsDefine = &[]*parser.ToolsDefine{}
	logger.Info("building request body for chatID=%d, agent=%s", session.ID, session.NowAgent)
	chatLine := &storageStructs.Chats{}
	if err := db.Where("id = ?", session.ID).First(chatLine).Error; err != nil {
		// 重读失败时直接返回错误，避免用零值 chatLine 静默降级到错误的模型/agent 上下文
		logger.Error("db error %v", err)
		return nil, err
	}
	if !session.InTestFlag {
		// 检测每个 path 最近一次 read/edit 事件，写入 session.TemporyDataOfSession。
		// trace/task 的全局 PreHook 据此分区（顶部 vs 事件块），RequestBody 据此按事件插入。
		if err := DetectTraceEvents(db, session, chatLine.NowAgent); err != nil {
			logger.Warn("detect trace events error: %v", err) // 非致命：降级为全部顶部聚合
		}
		var err error
		scopes, traces, tools, err = Tools(session)
		if err != nil {
			logger.Error("build tools error %v", err)
			return nil, err
		}
	}
	// 把运行时临时数据（事件映射/内容块）随 chatLine 传给 RequestBody
	chatLine.TemporyDataOfSession = session.TemporyDataOfSession
	body, err := RequestBody(session.ID, int32(chatLine.LastModelID), chatLine.NowAgent, tools, db, scopes, traces, session.CurrentAgentConfig, chatLine)
	if err != nil {
		logger.Error("build request body error %v", err)
		return nil, err
	}
	return body, nil
}
