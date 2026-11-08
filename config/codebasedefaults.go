package config

import "github.com/cxykevin/alkaid0/config/structs"

// Codebase 搜索调参的期望初始值。与 structs.CodebaseConfig 的 default tag、
// docs/schemas/config.json 以及 context/codebase 的运行时兜底保持一致。
const (
	defaultBM25Weight          float64 = 0.7 // BM25 权重，向量权重 = 1-BM25Weight
	defaultVectorMinSimilarity float64 = 0.5 // 向量最小余弦相似度保留阈值
	defaultBM25RetentionScore  float64 = 0.0 // BM25 保留阈值，0 表示不限制
)

// EnsureCodebaseDefaults 把 Context.Codebase 里为 0 的参数填成初始值。
//
// 为什么需要：0 对这些参数没有可用语义——运行时 searchHybrid 的 clampWeight 会把
// 0（以及越界值）一律当成默认值（BM25Weight → 0.7、VectorMinSimilarity → 0.5），
// BM25RetentionScore 的 0 本身就表示"不限制"（初始值也是 0）。但存量配置文件里
// 常被旧版本按零值落盘成显式 0，显式值会覆盖 default tag，于是 config/get 与
// 落盘 JSON 长期显示 0，与实际生效值不符，客户端也难分辨"未配置"。
//
// 因此这里把 0 直接补成初始配置；非 0 值（含用户刻意调整过的 0.3 等）一律保留。
func EnsureCodebaseDefaults(cfg *structs.Config) {
	if cfg == nil {
		return
	}
	cb := &cfg.Context.Codebase
	if cb.BM25Weight == 0 {
		cb.BM25Weight = defaultBM25Weight
	}
	if cb.VectorMinSimilarity == 0 {
		cb.VectorMinSimilarity = defaultVectorMinSimilarity
	}
	if cb.BM25RetentionScore == 0 {
		cb.BM25RetentionScore = defaultBM25RetentionScore
	}
}
