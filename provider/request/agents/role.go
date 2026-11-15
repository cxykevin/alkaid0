package agents

import (
	"fmt"
	"slices"
	"strings"

	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	agentconfig "github.com/cxykevin/alkaid0/provider/request/agents/config"
	"github.com/cxykevin/alkaid0/storage/structs"
)

// 会话 role：给单个会话指定一个 agent tag（<agent_tags> 里的名字），
// 让该会话的主 agent 使用对应 AgentConfig 的提示词/模型/审批规则/沙盒配置。
//
// 与子代理的关系：
//   - role 只作用于主 agent（agent_id 为空的普通回合）；
//   - 活跃子代理始终优先，子代理停用后自动回落到 role；
//   - Internal 代理（如 goal-check）仅供服务端内部使用，不能作为会话 role。

// RoleOption 可被客户端选为会话 role 的 agent tag（已过滤 Internal）。
type RoleOption struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Description string `json:"description,omitempty"`
	ModelID     int32  `json:"modelId"`
}

// ResolveRole 解析会话 role tag。
//
// 空 tag 表示"无角色"（返回零值配置与 nil error）；tag 不存在或指向 Internal 代理
// 时返回具体错误，由调用方决定拒绝请求还是仅记录日志。
func ResolveRole(tag string) (cfgStruct.AgentConfig, error) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return cfgStruct.AgentConfig{}, nil
	}
	cfg, ok := agentconfig.GetAgentConfig(tag)
	if !ok {
		return cfgStruct.AgentConfig{}, fmt.Errorf("role %q not found", tag)
	}
	if cfg.Internal {
		return cfgStruct.AgentConfig{}, fmt.Errorf("role %q is internal and cannot be used as a session role", tag)
	}
	return cfg, nil
}

// RoleOptions 返回可用于会话 role 的 tag 列表（过滤 Internal，按名字排序）。
//
// 每个条目都用 GetAgentConfig 重新解析：GetAgentConfigMap 的合并顺序是
// builtin 覆盖用户配置，而实际生效的是用户配置优先，直接遍历 map 会让列表里的
// 展示名/描述与实际使用的配置不一致。
func RoleOptions() []RoleOption {
	cfgMap := agentconfig.GetAgentConfigMap()
	options := make([]RoleOption, 0, len(cfgMap))
	for name := range cfgMap {
		cfg, ok := agentconfig.GetAgentConfig(name)
		if !ok || cfg.Internal {
			continue
		}
		options = append(options, RoleOption{
			Name:        name,
			DisplayName: cfg.AgentName,
			Description: cfg.AgentDescription,
			ModelID:     cfg.AgentModel,
		})
	}
	slices.SortFunc(options, func(a, b RoleOption) int {
		return strings.Compare(a.Name, b.Name)
	})
	return options
}

// IsValidRole 判断 tag 是否可作为会话 role（供配置项校验复用）。
func IsValidRole(tag string) bool {
	_, err := ResolveRole(tag)
	return err == nil
}

// LoadRole 把 session.Role 解析进运行时字段 session.RoleConfig。
//
// 解析失败（配置里删掉了该 tag / 该 tag 变成 Internal）时只记录警告并退化为默认
// 主 agent：保留持久化的 Role 值不清库，用户恢复配置后无需重新设置即可生效。
func LoadRole(session *structs.Chats) {
	if session == nil {
		return
	}
	cfg, err := ResolveRole(session.Role)
	if err != nil {
		logger.Warn("session %d: %v (falling back to default agent)", session.ID, err)
		session.RoleConfig = cfgStruct.AgentConfig{}
		return
	}
	session.RoleConfig = cfg
}
