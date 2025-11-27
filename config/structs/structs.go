package structs

// Config 主配置结构
type Config struct {
	Version int32
	Model   ModelsConfig
	Agent   AgentsConfig
	ThemeID int32
}
