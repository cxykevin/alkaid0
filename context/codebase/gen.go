// Package codebase 代码向量数据库的生成与查询系统
package codebase

import (
	"fmt"
	"sync"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/log"

	_ "github.com/glebarez/go-sqlite" // 注册 "sqlite" database/sql 驱动
	_ "modernc.org/sqlite/vec"        // 初始化 sqlite-vec 扩展
)

var logger *log.LogsObj

func init() {
	logger = log.New("codebase")
}

// VecDBs 向量数据库集合，按目录路径索引
var VecDBs = make(map[string]*CodebaseDB)

// VecDBsLock 保护 VecDBs map
var VecDBsLock sync.Mutex

// DefaultDim 默认向量维度（配置中未指定时使用）
const DefaultDim = 768

// 包内部缓存的嵌入模型配置，由 Initialize 填充
var (
	embedModelCfg *structs.ModelConfig
	embedDim      int
)

// GetDimFromCfg 从全局配置读取当前嵌入模型的维度
func GetDimFromCfg() int {
	mc, err := resolveModelFromCfg()
	if err != nil {
		logger.Warn("GetDimFromCfg: %v; using default %d", err, DefaultDim)
		return DefaultDim
	}
	dim := mc.ProviderSpecificConfig.Dimension
	if dim <= 0 {
		logger.Warn("GetDimFromCfg: dimension %d <= 0; using default %d", dim, DefaultDim)
		return DefaultDim
	}
	return dim
}

// IsEmbeddingConfigured 检查是否配置了 embedding 模型
func IsEmbeddingConfigured() bool {
	_, err := resolveModelFromCfg()
	return err == nil
}

// resolveModelFromCfg 从配置中找 embedding 模型的 ModelConfig
func resolveModelFromCfg() (*structs.ModelConfig, error) {
	cfg := config.GlobalConfigSafe()

	// 先按 EmbeddingModelID 找
	if cfg.Context.EmbeddingModelID != 0 {
		if mc, ok := cfg.Model.Models[cfg.Context.EmbeddingModelID]; ok {
			if mc.Type == structs.ModelTypeEmbedding {
				return &mc, nil
			}
			logger.Warn("EmbeddingModelID %d type=%s, not embedding, searching...", cfg.Context.EmbeddingModelID, mc.Type)
		}
	}

	// 遍历所有模型，找 Type=embedding 的
	for _, mc := range cfg.Model.Models {
		if mc.Type == structs.ModelTypeEmbedding {
			return &mc, nil
		}
	}

	return nil, fmt.Errorf("no embedding model found in configuration")
}
