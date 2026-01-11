package build

import (
	"errors"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/config/structs"
)

// GetModelConfig 获取模型配置
func GetModelConfig(modelID int32) (*structs.ModelConfig, error) {
	modelConfig, ok := config.GlobalConfig.Model.Models[modelID]
	if !ok {
		modelConfig, ok = config.GlobalConfig.Model.Models[config.GlobalConfig.Model.DefaultModelID]
		if !ok {
			return nil, errors.New("Model not found")
		}
	}
	return &modelConfig, nil
}
