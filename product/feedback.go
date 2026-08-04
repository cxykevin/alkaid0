package product

import "time"

// FeedbackServer 反馈服务端地址（保留用户确认的原始配置值，含尾部斜杠；
// SDK New 要求无尾部斜杠，调用方统一 strings.TrimRight 后传入）。
const FeedbackServer = "https://feed.cxykevin.top/"

// FeedbackProductID 反馈产品 ID（feederback 面板创建的自定义字符 ID）。
const FeedbackProductID = "alkaid0"

// FeedbackSubmitTimeout /feedback 提交整体超时。
// PoW 默认约 30s + 网络往返 + 默认最多 3 次重试（含指数退避），120s 留足余量。
const FeedbackSubmitTimeout = 120 * time.Second
