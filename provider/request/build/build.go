package build

import (
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
	scopes, traces, tools := Tools(session)
	chatLine := storageStructs.Chats{}
	err := db.Where("id = ?", session.ID).First(&chatLine).Error
	if err != nil {
		logger.Error("db error %v", err)
	}
	body, err := RequestBody(session.ID, int32(chatLine.LastModelID), chatLine.NowAgent, tools, db, scopes, traces)
	if err != nil {
		logger.Error("build request body error %v", err)
		return nil, err
	}
	return body, nil
}
