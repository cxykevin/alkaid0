package request

import (
	"context"
	"strings"
	"time"

	"github.com/cxykevin/alkaid0/config"
	"gorm.io/gorm"

	// "github.com/cxykevin/alkaid0/provider/request"

	"github.com/cxykevin/alkaid0/provider/request/build"
	"github.com/cxykevin/alkaid0/provider/request/structs"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
)

// SummaryTimeout 获取总结超时时间
const SummaryTimeout = 120 * time.Second

// Summary 获取总结
func Summary(ctx context.Context, db *gorm.DB, chatID uint32, agentID string) (string, error) {
	msgID, obj, err := build.Summary(chatID, agentID, db)
	if err != nil {
		return "", err
	}
	if msgID == 0 {
		return "", nil
	}
	ctxn, cancel := context.WithTimeout(ctx, SummaryTimeout)
	defer cancel()

	modelConfig, err := build.GetModelConfig(config.GlobalConfig.Agent.SummaryModel)
	if err != nil {
		return "", err
	}

	// 获取模型信息
	resp := strings.Builder{}
	err = SimpleOpenAIRequest(ctxn, modelConfig.ProviderURL, modelConfig.ProviderKey, modelConfig.ModelID, *obj, func(ret structs.ChatCompletionResponse) error {
		if len(ret.Choices) == 0 {
			return nil // Gemini 喜欢在最后一个消息里返回空内容
		}
		resp.WriteString(ret.Choices[0].Delta.Content)
		return nil
	})
	if err != nil {
		return "", err
	}

	respStr := resp.String()

	// 写db
	err = db.
		Model(&storageStructs.Messages{}).
		Where("id = ?", msgID).
		Select("summary").
		Updates(&storageStructs.Messages{Summary: respStr}).
		Error

	if err != nil {
		return respStr, err
	}

	return respStr, nil

}

// SummarySession 获取总结
func SummarySession(ctx context.Context, session *storageStructs.Chats) (string, error) {
	db := session.DB
	chatID := session.ID
	agentID := session.CurrentAgentID
	return Summary(ctx, db, chatID, agentID)
}
