package structs

// Config 主配置结构
type Config struct {
	JSONSchema string `json:"$schema" default:"https://raw.githubusercontent.com/cxykevin/alkaid0/refs/heads/main/docs/schemas/config.json"`
	Version    int32
	Model      ModelsConfig
	Agent      AgentsConfig
	ThemeID    int32
	Server     RPCConfig
}
