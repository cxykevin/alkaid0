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
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"gorm.io/gorm"
)

// feedbackMaxContent SDK 对 content 的字节上限。
const feedbackMaxContent = 1024

// feedbackMaxLogs 随反馈附带的日志尾部字节上限（SDK 上限 64KB，留余量取 63k）。
const feedbackMaxLogs = 63 * 1024

// feedbackLogTail 读取日志尾部；包级变量作为测试缝（默认 log.Tail）。
var feedbackLogTail = func(maxBytes int) string { return log.Tail(maxBytes) }

// feedbackScrubValues 收集提交反馈前必须擦除的敏感值。
//
// 泄露路径：日志包只有静态正则（已知 key 前缀 / URL / Bearer 查询参数），
// 不包含用户通过 /mask add 注册的自定义值；而会话 loop 会以 INFO 级别打印
// 用户输入（ui/loop/loop.go）。用户把已 /mask add 的敏感值粘进 prompt 时，
// 该值会原样写入日志文件，/feedback 的 log.Tail 又把它原样上传到反馈服务器。
// 模型配置里的 ProviderKey 同样格式任意，静态正则未必覆盖。
func feedbackScrubValues(db *gorm.DB) []string {
	values := make([]string, 0, 8)
	if db != nil {
		var rows []storageStructs.CustomMask
		if err := db.Find(&rows).Error; err == nil {
			for _, r := range rows {
				if v := strings.TrimSpace(r.Value); v != "" {
					values = append(values, v)
				}
			}
		}
	}
	if cfg := config.GlobalConfigSafe(); cfg != nil {
		for _, m := range cfg.Model.Models {
			if v := strings.TrimSpace(m.ProviderKey); v != "" {
				values = append(values, v)
			}
		}
	}
	return values
}

// scrubFeedbackSecrets 用 collecting 到的敏感值擦除日志尾部。
func scrubFeedbackSecrets(db *gorm.DB, text string) string {
	if text == "" {
		return text
	}
	for _, v := range feedbackScrubValues(db) {
		text = strings.ReplaceAll(text, v, "***")
	}
	return text
}

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

		// 附带最近日志尾部（I/O 放后台，不阻塞命令处理）。
		// 提交前用同一会话的 /mask 自定义值与模型 ProviderKey 兜底擦除。
		var db *gorm.DB
		if obj != nil && obj.session != nil {
			db = obj.session.DB
		}
		logs := scrubFeedbackSecrets(db, feedbackLogTail(feedbackMaxLogs))
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
