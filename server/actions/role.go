package actions

import (
	"fmt"
	"strings"

	"github.com/cxykevin/alkaid0/provider/request/agents"
	"github.com/cxykevin/alkaid0/storage/structs"
)

// 私有 ACP 方法：alk.cxykevin.top/session/role/get | .../role/set。
//
// 会话角色（session role）是 chats.role 列，值是 config.Agent.Agents 中的
// agent tag（也就是 <agent_tags> 里的名字）。非空时该会话的主 agent 使用对应
// AgentConfig 的提示词/模型/审批规则/沙盒配置；Internal 代理不可作为角色。
//
// 与 configOptions 的关系：role 同时作为 configId="role" 暴露给标准
// session/set_config_option；两条入口共用 applySessionRole，校验/落库/广播一致。
// 写入后广播 config_option_update（所有连接的 role/模型下拉同步刷新）。

// RoleGetRequest 读取会话角色。
type RoleGetRequest struct {
	SessionID string `json:"sessionId"`
}

// RoleGetResponse 当前角色与可选项。
type RoleGetResponse struct {
	Role  string              `json:"role"`
	Roles []agents.RoleOption `json:"roles"`
}

// RoleSetRequest 设置/清除会话角色。空字符串 = 清除（回到默认主 agent）。
type RoleSetRequest struct {
	SessionID string `json:"sessionId"`
	Role      string `json:"role"`
}

// RoleSetResponse 写入后的角色（空串表示已清除）。
type RoleSetResponse struct {
	Role string `json:"role"`
}

// applySessionRole 校验并写入会话角色：校验 tag → 单列落库 → 同步运行时字段。
// 空 tag 表示清除（角色配置回落默认主 agent）。
func applySessionRole(session *structs.Chats, tag string) error {
	if session == nil {
		return fmt.Errorf("session is nil")
	}
	tag = strings.TrimSpace(tag)
	cfg, err := agents.ResolveRole(tag)
	if err != nil {
		return err
	}
	if session.DB == nil {
		return fmt.Errorf("session db is not initialized")
	}
	if err := session.DB.Model(&structs.Chats{}).Where("id = ?", session.ID).
		Update("role", tag).Error; err != nil {
		return fmt.Errorf("failed to set role: %v", err)
	}
	if tag == "" {
		session.ClearRoleRuntime()
	} else {
		session.SetRoleRuntime(tag, cfg)
	}
	return nil
}

// broadcastRoleConfigOptions 把写入后的 role/模型/推理强度选项推给会话所有连接。
// 广播失败只记录日志：role 已经落库，下一次建连或 config 重载会重新同步。
func broadcastRoleConfigOptions(sessionID string, sess *structs.Chats) {
	if sess == nil {
		return
	}
	configOptions := buildConfigOptions(sess.EffectiveModelID(), sess.Role, sess.ReasoningEffort)
	if err := broadcastSessionUpdate(sessionID, SessionUpdate{
		SessionID: sessionID,
		Update: SessionUpdateUpdate{
			SessionUpdate: "config_option_update",
			ConfigOptions: configOptions,
		},
	}, 0); err != nil {
		logger.Warn("failed to broadcast config options after role change: %v", err)
	}
}

// RoleGet 读取会话当前角色与可选 tag 列表。
// 会话允许冷状态（服务端重启后内存注册表为空），但必须是本连接绑定过的会话。
func RoleGet(req RoleGetRequest, _ func(string, any, *string) error, connID uint64) (RoleGetResponse, error) {
	cwd, chatID, canonical, err := goalTargetForConn(req.SessionID, connID)
	if err != nil {
		return RoleGetResponse{}, err
	}
	if obj, ok := lookupSession(canonical); ok && obj.session != nil {
		return RoleGetResponse{Role: obj.session.Role, Roles: agents.RoleOptions()}, nil
	}

	db, err := loadDB(cwd)
	if err != nil {
		return RoleGetResponse{}, err
	}
	defer closeDB(cwd)
	var sess structs.Chats
	if err := db.Select("id", "role").Where("id = ?", chatID).Limit(1).Find(&sess).Error; err != nil {
		return RoleGetResponse{}, err
	}
	if sess.ID == 0 {
		return RoleGetResponse{}, fmt.Errorf("session not found")
	}
	return RoleGetResponse{Role: sess.Role, Roles: agents.RoleOptions()}, nil
}

// RoleSet 设置/清除会话角色。空 role = 清除。
// 会话在内存中时同步运行时配置并广播 config_option_update；冷会话只落库，
// 下次 loadSession 会解析生效（与 goal/set 的行为一致）。
func RoleSet(req RoleSetRequest, _ func(string, any, *string) error, connID uint64) (RoleSetResponse, error) {
	cwd, chatID, canonical, err := goalTargetForConn(req.SessionID, connID)
	if err != nil {
		return RoleSetResponse{}, err
	}
	tag := strings.TrimSpace(req.Role)
	// 先校验 tag（未知 / Internal 直接报错），冷热路径共用同一套规则。
	if _, err := agents.ResolveRole(tag); err != nil {
		return RoleSetResponse{}, err
	}

	if obj, ok := lookupSession(canonical); ok && obj.session != nil {
		if err := applySessionRole(obj.session, tag); err != nil {
			return RoleSetResponse{}, err
		}
		broadcastRoleConfigOptions(canonical, obj.session)
		return RoleSetResponse{Role: obj.session.Role}, nil
	}

	db, err := loadDB(cwd)
	if err != nil {
		return RoleSetResponse{}, err
	}
	defer closeDB(cwd)
	res := db.Model(&structs.Chats{}).Where("id = ?", chatID).Update("role", tag)
	if res.Error != nil {
		return RoleSetResponse{}, fmt.Errorf("failed to set role: %v", res.Error)
	}
	if res.RowsAffected == 0 {
		return RoleSetResponse{}, fmt.Errorf("session not found")
	}
	return RoleSetResponse{Role: tag}, nil
}
