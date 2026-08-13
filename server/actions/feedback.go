package actions

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"

	feedbacksdk "github.com/cxykevin/feederback/sdk"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/product"
)

// feedbackMaxContent SDK 对 content 的字节上限。
const feedbackMaxContent = 1024

// feedbackMaxLogs 随反馈附带的日志尾部字节上限（SDK 上限 64KB，留余量取 63k）。
const feedbackMaxLogs = 63 * 1024

// feedbackSubmit 提交缝隙：包级变量，测试可替换为 fake，绕过 PoW/签名。
var feedbackSubmit = func(ctx context.Context, content, logs, osInfo string) (*feedbacksdk.Result, error) {
	client, err := getFeedbackClient()
	if err != nil {
		return nil, err
	}
	return client.Submit(ctx, content, logs, osInfo)
}

// getFeedbackClient 惰性初始化客户端（机器码加载/持久化只做一次，复用 http.Client 连接池）。
var (
	feedbackClientOnce sync.Once
	feedbackClient     *feedbacksdk.Client
	feedbackClientErr  error
)

func getFeedbackClient() (*feedbacksdk.Client, error) {
	feedbackClientOnce.Do(func() {
		feedbackClient, feedbackClientErr = feedbacksdk.New(
			resolveFeedbackServerURL(),
			product.FeedbackProductID,
		)
	})
	return feedbackClient, feedbackClientErr
}

// resolveFeedbackServerURL 解析反馈服务端地址（不含尾部斜杠，SDK New 要求）。
// 优先使用 config.Feedback.URL（非空时），否则回退到内置 product.FeedbackServer。
func resolveFeedbackServerURL() string {
	if cfg := config.GlobalConfigSafe(); cfg != nil && strings.TrimSpace(cfg.Feedback.URL) != "" {
		return strings.TrimRight(strings.TrimSpace(cfg.Feedback.URL), "/")
	}
	return strings.TrimRight(product.FeedbackServer, "/")
}

// feedbackDisabled 是否禁用反馈：debug 模式（ALKAID0_DEBUG=true）或日志 debug 级别时禁用。
func feedbackDisabled() bool {
	if os.Getenv("ALKAID0_DEBUG") == "true" {
		return true
	}
	return log.DebugLevelEnabled()
}

// feedbackCommand 处理 /feedback <内容>：异步提交反馈到反馈服务端。
func feedbackCommand(obj *sessionObj, arg string) (bool, error) {
	if feedbackDisabled() {
		broadcastCmdText(obj, "Feedback is disabled in debug mode.")
		return false, nil
	}

	content := strings.TrimSpace(arg)
	if content == "" {
		return false, fmt.Errorf("Usage: /feedback <content>")
	}
	content = truncateBytes(content, feedbackMaxContent)

	// 同步广播"正在提交"，保证先于 prompt.go 的 idle state_update 到达客户端。
	broadcastCmdText(obj, "Submitting feedback…")

	osInfo := buildFeedbackOSInfo()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), product.FeedbackSubmitTimeout)
		defer cancel()

		// 附带最近日志尾部（I/O 放后台，不阻塞命令处理）
		logs := log.Tail(feedbackMaxLogs)
		result, err := feedbackSubmit(ctx, content, logs, osInfo)
		msg := fmt.Sprintf("Feedback submission failed: %v", err)
		if err == nil {
			msg = fmt.Sprintf("Feedback submitted successfully. Feedback ID: `%s`", result.FeedbackID)
		} else {
			logger.Error("feedback submit failed: %v", err)
		}
		broadcastCmdText(obj, msg)
	}()

	return false, nil
}

// buildFeedbackOSInfo 组装 osInfo（≤1024B）。
func buildFeedbackOSInfo() string {
	return fmt.Sprintf("Alkaid0/%s commit/%s go/%s %s/%s",
		product.Version, product.CommitID, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

// truncateBytes 将 s 截断为不超过 max 字节，且不切断 UTF-8 字符。
func truncateBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := 0
	for i := range s { // range 遍历按 rune 起始字节索引
		if i > max {
			break
		}
		cut = i
	}
	return s[:cut]
}
