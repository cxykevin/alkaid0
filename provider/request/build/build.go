package build

import (
	"errors"

	reqStruct "github.com/cxykevin/alkaid0/provider/request/structs"
	"github.com/cxykevin/alkaid0/storage"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
)

// Build 构造请求体
func Build() (*reqStruct.ChatCompletionRequest, error) {
	lastChatID := storage.GlobalConfig.CurrentChatID
	if lastChatID == 0 {
		logger.Error("no last chat id")
		return nil, errors.New("no last chat id")
	}
	// 构造工具
	scopes, traces, tools := Tools()
	chatLine := storageStructs.Chats{}
	err := storage.DB.Where("id = ?", lastChatID).First(&chatLine).Error
	if err != nil {
		logger.Error("db error %v", err)
	}
	body, err := RequestBody(lastChatID, int32(chatLine.LastModelID), chatLine.NowAgent, tools, storage.DB, scopes, traces)
	if err != nil {
		logger.Error("build request body error %v", err)
		return nil, err
	}
	return body, nil
}
