package actions

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/internal/configutil"
	"github.com/cxykevin/alkaid0/product"
)

// telemetryInterval 自动 Telemetry 上报间隔（一个月）。
const telemetryInterval = 30 * 24 * time.Hour

// telemetryTimestampName 时间戳文件名（与配置文件同目录，便于随配置一起备份/清理）。
const telemetryTimestampName = "telemetry.timestamp"

// telemetryLastPath 上次 Telemetry 上报时间戳文件路径。var 便于测试注入。
// 放在配置文件同目录（与 usage.json、cron.sqlite 一致），而不是用户缓存目录：
// 服务端以 root/systemd 运行时常拿不到预期的缓存目录，日志排查时也难找。
var telemetryLastPath = func() string {
	return telemetryTimestampPath(config.Path())
}

// telemetryTimestampPath 由配置文件路径推导时间戳路径。
func telemetryTimestampPath(configPath string) string {
	return filepath.Join(filepath.Dir(configutil.ExpandPath(configPath)), telemetryTimestampName)
}

// runAutoTelemetry 自动 Telemetry：仅当距上次上报超过一个周期时，
// 走反馈接口异步上报一条以 [Telemetry] 开头的内容，成功或失败都记录尝试时间戳。
// 首次启动（无时间戳记录）本次不上报，只写入基线时间戳；禁用
// （Config.Feedback.DisableAutoTelemetry）或 debug 模式下跳过。
func runAutoTelemetry() {
	if config.GlobalConfigSafe().Feedback.DisableAutoTelemetry {
		return
	}
	if feedbackDisabledFn() {
		return
	}
	path := telemetryLastPath()
	if path == "" {
		return
	}
	last, err := readTelemetryLast(path)
	if err != nil {
		// 首次运行（或时间戳损坏）：本次不上报，只写入基线时间戳。
		// 必须写，否则每次启动都会走进这个分支、永远不会积累出上报周期。
		logger.Info("auto telemetry: no valid timestamp, recording baseline")
		writeTelemetryTimestamp(path)
		return
	}
	if time.Now().Unix()-last < int64(telemetryInterval.Seconds()) {
		return // 距上次上报不足一个周期
	}

	content := buildTelemetryContent()
	modelLogs := buildTelemetryModelLogs()
	osInfo := buildFeedbackOSInfo()
	ctx, cancel := context.WithTimeout(context.Background(), product.FeedbackSubmitTimeout)
	defer cancel()
	res, err := feedbackSubmit(ctx, content, modelLogs, osInfo)
	if err != nil {
		logger.Warn("auto telemetry submit failed: %v", err)
		// 失败也要记录本次尝试时间：否则反馈服务端长期不可用时，每次启动都会
		// 重试上报（无上限）。宁可失败后推迟一个周期，也不做无限重试。
		writeTelemetryTimestamp(path)
		return
	}
	if res != nil {
		logger.Info("auto telemetry submitted: id=%s", res.FeedbackID)
	}
	writeTelemetryTimestamp(path)
}

// writeTelemetryTimestamp 记录最近一次 Telemetry 尝试时间（成功或失败均写）。
func writeTelemetryTimestamp(path string) {
	if path == "" {
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		logger.Warn("auto telemetry: create dir %s failed: %v", dir, err)
		return
	}
	if err := os.WriteFile(path, []byte(strconv.FormatInt(time.Now().Unix(), 10)), 0600); err != nil {
		logger.Warn("auto telemetry: write timestamp %s failed: %v", path, err)
	}
}

// readTelemetryLast 读取上次 Telemetry 上报时间戳。
func readTelemetryLast(path string) (int64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
}

// buildTelemetryContent 组装 Telemetry 内容（以 [Telemetry] 标识，走 feedback 接口）。
// 不含日志与模型列表；模型列表经 buildTelemetryModelLogs 放入 logs 位置。
func buildTelemetryContent() string {
	return fmt.Sprintf("[Telemetry] Alkaid0/%s commit/%s go/%s %s/%s",
		product.Version, product.CommitID, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

// buildTelemetryModelLogs 组装模型 ID 列表（每行一个），放入 Telemetry 的 logs 位置。
func buildTelemetryModelLogs() string {
	return strings.Join(usedModelIDs(), "\n")
}

// usedModelIDs 收集配置中使用的模型 ID 列表（去重、排序）。
func usedModelIDs() []string {
	cfg := config.GlobalConfigSafe()
	if cfg == nil {
		return nil
	}
	set := map[string]struct{}{}
	for _, m := range cfg.Model.Models {
		if id := strings.TrimSpace(m.ModelID); id != "" {
			set[id] = struct{}{}
		}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}
