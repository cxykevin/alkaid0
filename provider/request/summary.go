package request

import (
	"context"
	"strings"
	"time"

	"github.com/cxykevin/alkaid0/config"
	"gorm.io/gorm"

	// "github.com/cxykevin/alkaid0/provider/request"

	"github.com/cxykevin/alkaid0/provider/mask"
	"github.com/cxykevin/alkaid0/provider/request/build"
	"github.com/cxykevin/alkaid0/provider/request/structs"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/tools/trace"
	"github.com/cxykevin/alkaid0/tools/tools/tree"
)

// SummaryTimeout 获取总结超时时间
const SummaryTimeout = 120 * time.Second

// Summary 获取总结
func Summary(ctx context.Context, db *gorm.DB, chatID uint32, agentID string) (string, error) {
	logger.Info("starting summary for chatID=%d, agentID=%s", chatID, agentID)
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
	err = SimpleOpenAIRequest(ctxn, modelConfig.ProviderURL, modelConfig.ProviderKey, modelConfig.ModelID, *obj, mask.NewEngine(db), func(ret structs.ChatCompletionResponse) error {
		if len(ret.Choices) == 0 {
			return nil
		}
		if ret.Choices[0].Delta.Content != "" {
			resp.WriteString(ret.Choices[0].Delta.Content)
		}
		if ret.Choices[0].Delta.ReasoningContent != nil {
			// 如果有推理内容，也可以考虑加入，或者至少确保不漏掉内容
			// 对于总结来说，通常只需要最终内容
		}
		return nil
	})
	if err != nil {
		return "", err
	}

	respStr := resp.String()

	// 只写摘要，**不删 trace**（审计优先，docs/trace-cache-spec.md §4.5）。
	// 压缩边界之前的 trace 不再靠删库收敛，而是在拼接期按"边界后是否有事件"跳过：
	// DetectTraceEvents 本身就在 summary 处截断扫描，其事件表天然就是活跃集合。
	if err := db.Model(&storageStructs.Messages{}).
		Where("id = ?", msgID).
		Select("summary").
		Updates(&storageStructs.Messages{Summary: respStr}).Error; err != nil {
		logger.Error("failed to save summary: %v", err)
		return respStr, err
	}
	logger.Info("summary saved for chatID=%d (traces kept, filtered at assembly)", chatID)

	return respStr, nil

}

// SummarySession 获取总结
func SummarySession(ctx context.Context, session *storageStructs.Chats) (string, error) {
	db := session.DB
	chatID := session.ID
	agentID := session.CurrentAgentID
	resp, err := Summary(ctx, db, chatID, agentID)
	if err == nil {
		trace.InvalidateTraceCache(session)
		tree.InvalidateTreeCache(session)
	}
	return resp, err
}
