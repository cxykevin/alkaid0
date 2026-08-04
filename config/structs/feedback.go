package structs

// FeedbackConfig 反馈上报配置。
type FeedbackConfig struct {
	// URL 反馈服务端地址。为空时回退到内置 product.FeedbackServer。
	URL string
	// DisableAutoTelemetry 是否禁用自动 Telemetry 上报（每月一次）。
	DisableAutoTelemetry bool
}
