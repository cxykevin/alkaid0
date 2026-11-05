package actions

import (
	"github.com/cxykevin/alkaid0/ui/loop"
	u "github.com/cxykevin/alkaid0/utils"
)

// retryUpdateName 请求重试事件的 sessionUpdate 名称。
// 纯通知：客户端可展示“第 N 次重试 / 等待时间 / 错误摘要”，不参与消息时间线。
const retryUpdateName = "alk.cxykevin.top/session/retry"

// broadcastRetryEvent 广播一次即将进行的请求重试。
func broadcastRetryEvent(sessionID string, info *loop.RetryInfo) {
	if info == nil {
		return
	}
	content := u.H{
		"sessionUpdate": retryUpdateName,
		"attempt":       info.Attempt,
		"backoffMs":     info.Backoff.Milliseconds(),
		"error":         info.ErrorText,
	}
	if err := broadcastSessionUpdate(sessionID, SessionUpdate{
		SessionID: sessionID,
		Update:    content,
	}, 0); err != nil {
		logger.Warn("failed to broadcast retry update: %v", err)
	}
}
