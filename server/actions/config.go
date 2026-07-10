package actions

import (
	"encoding/json"
	"fmt"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
)

// ---- Request/Response types ----

// ConfigGetRequest 获取配置的请求（无需参数）
type ConfigGetRequest struct{}

// ConfigGetResponse 获取配置的响应
type ConfigGetResponse struct {
	Config *cfgStructs.Config `json:"config"`
}

// ConfigSetRequest 设置配置的请求
// Config 字段接受部分配置，未指定的字段保持现有值不变
type ConfigSetRequest struct {
	Config json.RawMessage `json:"config"`
}

// ---- Handler functions ----

// ConfigGet 返回完整的当前配置
func ConfigGet(_ ConfigGetRequest, _ func(string, any, *string) error, _ uint64) (ConfigGetResponse, error) {
	return ConfigGetResponse{Config: config.GlobalConfig}, nil
}

// ConfigSet 写入配置并自动重载
// 接受完整的或部分的配置 JSON，通过 json.Unmarshal 合并到现有配置中
// 只有请求中显式指定的字段会被更新，未指定的字段保持现有值
// 保存后自动触发所有注册的重载钩子（配置广播推送等）
func ConfigSet(req ConfigSetRequest, _ func(string, any, *string) error, _ uint64) (any, error) {
	if req.Config == nil {
		return nil, fmt.Errorf("config is required")
	}

	// 校验为合法 JSON
	if !json.Valid(req.Config) {
		return nil, fmt.Errorf("invalid JSON config")
	}

	// 合并到 GlobalConfig（json.Unmarshal 到已存在的对象只覆盖 JSON 中出现的字段）
	if err := json.Unmarshal(req.Config, config.GlobalConfig); err != nil {
		return nil, fmt.Errorf("failed to apply config: %v", err)
	}

	// 保存到文件并触发重载钩子
	config.Save()

	return nil, nil
}
