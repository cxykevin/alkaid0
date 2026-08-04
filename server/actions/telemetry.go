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
	"github.com/cxykevin/alkaid0/product"
)

// telemetryInterval 自动 Telemetry 上报间隔（一个月）。
const telemetryInterval = 30 * 24 * time.Hour

// telemetryLastPath 上次 Telemetry 上报时间戳文件路径。var 便于测试注入。
var telemetryLastPath = func() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "alkaid0", "telemetry_last_unix")
}

// runAutoTelemetry 自动 Telemetry：仅当距上次上报超过一个周期时，
// 走反馈接口异步上报一条以 [Telemetry] 开头的内容，成功后记录时间戳。
// 首次启动（无时间戳记录）不上报；禁用（Config.Feedback.DisableAutoTelemetry）或 debug 模式下跳过。
func runAutoTelemetry() {
	if config.GlobalConfigSafe().Feedback.DisableAutoTelemetry {
		return
	}
	if feedbackDisabled() {
		return
	}
	path := telemetryLastPath()
	if path == "" {
		return
	}
	last, err := readTelemetryLast(path)
	if err != nil {
		return // 从未上报过：首次启动不上报
	}
	if time.Now().Unix()-last < int64(telemetryInterval.Seconds()) {
		return // 距上次上报不足一个周期
	}

	content := buildTelemetryContent()
	modelLogs := buildTelemetryModelLogs()
	osInfo := buildFeedbackOSInfo()
	ctx, cancel := context.WithTimeout(context.Background(), product.FeedbackSubmitTimeout)
	defer cancel()
	if _, err := feedbackSubmit(ctx, content, modelLogs, osInfo); err != nil {
		logger.Warn("auto telemetry submit failed: %v", err)
		return
	}
	// 成功后才写时间戳，失败留待下次重试。
	if dir := filepath.Dir(path); os.MkdirAll(dir, 0700) == nil {
		_ = os.WriteFile(path, []byte(strconv.FormatInt(time.Now().Unix(), 10)), 0600)
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
