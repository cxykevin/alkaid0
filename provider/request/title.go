package request

import (
	"context"
	"strings"
	"time"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/provider/request/build"
	"github.com/cxykevin/alkaid0/provider/request/structs"
	"gorm.io/gorm"
)

// TitleTimeout 标题生成超时时间
const TitleTimeout = 30 * time.Second

// TitleSummary 生成会话标题（首次生成：第一条用户请求 + 第一条 AI 响应）。
// 不写库，由调用方写入 Chats.AITitle；消息不足时返回 ("", nil)。
func TitleSummary(ctx context.Context, db *gorm.DB, chatID uint32) (string, error) {
	return titleRequest(ctx, db, chatID, false)
}

// TitleSummaryFull 重生成会话标题（compress 后：完整对话输入）。
// 不写库，由调用方写入 Chats.AITitle；无有效消息时返回 ("", nil)。
func TitleSummaryFull(ctx context.Context, db *gorm.DB, chatID uint32) (string, error) {
	return titleRequest(ctx, db, chatID, true)
}

// titleRequest 发送标题生成请求并累加流式响应
func titleRequest(ctx context.Context, db *gorm.DB, chatID uint32, full bool) (string, error) {
	logger.Info("starting title generation for chatID=%d, full=%t", chatID, full)

	var obj *structs.ChatCompletionRequest
	var err error
	if full {
		obj, err = build.TitleFull(chatID, db)
	} else {
		obj, err = build.Title(chatID, db)
	}
	if err != nil {
		return "", err
	}
	if obj == nil {
		return "", nil
	}

	ctxn, cancel := context.WithTimeout(ctx, TitleTimeout)
	defer cancel()

	modelConfig, err := build.GetModelConfig(config.GlobalConfig.Agent.TitleModel)
	if err != nil {
		return "", err
	}

	// 获取模型信息
	resp := strings.Builder{}
	err = SimpleOpenAIRequest(ctxn, modelConfig.ProviderURL, modelConfig.ProviderKey, modelConfig.ModelID, *obj, func(ret structs.ChatCompletionResponse) error {
		if len(ret.Choices) == 0 {
			return nil
		}
		if ret.Choices[0].Delta.Content != "" {
			resp.WriteString(ret.Choices[0].Delta.Content)
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	title := strings.TrimSpace(resp.String())
	if title == "" {
		return "", nil
	}
	logger.Info("title generated for chatID=%d: %s", chatID, title)
	return title, nil
}
