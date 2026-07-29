package structs

import (
	secconfig "github.com/cxykevin/alkaid0-search-engine/config"
)

// CodebaseConfig 代码库搜索引擎配置
type CodebaseConfig struct {
	// BM25Weight BM25 搜索权重（0~1），向量搜索权重为 1-BM25Weight，默认 0.7
	BM25Weight float64 `default:"0.7"`
	// VectorMinSimilarity 向量最小余弦相似度保留阈值（0~1）
	// 向量搜索结果中余弦相似度低于此值的项被丢弃，默认 0.5
	VectorMinSimilarity float64 `default:"0.5"`
	// BM25RetentionScore BM25 保留阈值
	// BM25 得分高于此值的项被丢弃（BM25 得分越低匹配越好），0 表示不限制
	BM25RetentionScore float64 `default:"0.0"`
}

// ContextConfig 上下文/集成相关配置
type ContextConfig struct {
	LSP                LSPConfig        // LSP 客户端配置
	EmbeddingModelID   int32            `default:"1"` // 嵌入模型
	SearchSummaryModel int32            `default:"0"` // 搜索摘要模型
	OnlineSearch       secconfig.Config // 在线搜索配置
	Codebase           CodebaseConfig   // 代码库搜索引擎调参
}
