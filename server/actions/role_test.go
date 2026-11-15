package actions

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/context/codebase"
	"github.com/cxykevin/alkaid0/storage/structs"
)

// setupRoleTestConfig 在测试配置上注册会话 role 用到的 agent tag。
func setupRoleTestConfig(t *testing.T) {
	t.Helper()
	restore := configSetup(t)
	t.Cleanup(restore)
	config.GlobalConfig.Agent.Agents = map[string]cfgStructs.AgentConfig{
		"reviewer": {
			AgentName:   "Reviewer",
			AgentPrompt: "review the code",
			AgentModel:  1,
		},
		"internal-x": {
			AgentName: "Internal X",
			Internal:  true,
		},
	}
}

func TestSessionNewRoleArgParsing(t *testing.T) {
	tests := []struct {
		name string
		root json.RawMessage
		args map[string]json.RawMessage
		want string
	}{
		{
			name: "根级字符串",
			root: json.RawMessage(`"reviewer"`),
			want: "reviewer",
		},
		{
			name: "args 内字符串",
			args: map[string]json.RawMessage{sessionNewRoleArg: json.RawMessage(`"plan"`)},
			want: "plan",
		},
		{
			name: "根级优先于 args",
			root: json.RawMessage(`"reviewer"`),
			args: map[string]json.RawMessage{sessionNewRoleArg: json.RawMessage(`"plan"`)},
			want: "reviewer",
		},
		{
			name: "非字符串按未设置",
			root: json.RawMessage(`123`),
			want: "",
		},
		{
			name: "空白字符串视为未设置",
			root: json.RawMessage(`"   "`),
			want: "",
		},
		{
			name: "缺省",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sessionNewRole(tt.root, tt.args); got != tt.want {
				t.Errorf("sessionNewRole() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildConfigOptionsRole(t *testing.T) {
	setupRoleTestConfig(t)

	options := buildConfigOptions(0, "reviewer", "unset")
	var roleOpt *ConfigOption
	for i := range options {
		if options[i].ConfigID == "role" {
			roleOpt = &options[i]
			break
		}
	}
	if roleOpt == nil {
		t.Fatal("configOptions 缺少 role 选项")
	}
	if roleOpt.CurrentValue != "reviewer" {
		t.Errorf("role currentValue = %q, want reviewer", roleOpt.CurrentValue)
	}
	if roleOpt.Type != "select" || roleOpt.Category != "role" {
		t.Errorf("role option = %+v, want select/role", *roleOpt)
	}
	if len(roleOpt.Options) == 0 || roleOpt.Options[0].Value != "" || roleOpt.Options[0].Name != "Default" {
		t.Errorf("role options 首项应为清空用的 Default: %+v", roleOpt.Options)
	}
	values := map[string]bool{}
	for _, o := range roleOpt.Options {
		values[o.Value] = true
	}
	if !values["reviewer"] {
		t.Error("role options 缺少 reviewer")
	}
	if values["internal-x"] {
		t.Error("role options 不应包含 Internal 代理")
	}
	if values["goal-check"] {
		t.Error("role options 不应包含内置 Internal 代理 goal-check")
	}
}

// newRoleTestSession 创建一个已加载到内存的会话，并返回其规范 sessionID。
func newRoleTestSession(t *testing.T) (string, *structs.Chats) {
	t.Helper()
	dir, db, ids := newSessionListDB(t, 1)
	sessionID := cwd2SessionID(dir, ids[0])
	sess := &structs.Chats{ID: ids[0], DB: db}
	sessLock.Lock()
	sessions[sessionID] = &sessionObj{cwd: dir, id: ids[0], session: sess}
	sessLock.Unlock()
	t.Cleanup(func() {
		sessLock.Lock()
		delete(sessions, sessionID)
		sessLock.Unlock()
	})
	return sessionID, sess
}

func TestSessionSetConfigOptionRole(t *testing.T) {
	setupRoleTestConfig(t)
	sessionID, sess := newRoleTestSession(t)

	// 设置角色：落库 + 运行时配置同步
	if _, err := SessionSetConfigOption(SessionSetConfigOptionRequest{
		SessionID: sessionID, ConfigID: "role", Value: "reviewer",
	}, nil, 1); err != nil {
		t.Fatalf("set role error = %v", err)
	}
	if sess.Role != "reviewer" || sess.RoleConfig.AgentName != "Reviewer" {
		t.Errorf("运行时 role = %q / %q", sess.Role, sess.RoleConfig.AgentName)
	}
	var row structs.Chats
	if err := sess.DB.Select("id", "role").Where("id = ?", sess.ID).First(&row).Error; err != nil {
		t.Fatalf("query role: %v", err)
	}
	if row.Role != "reviewer" {
		t.Errorf("持久化 role = %q, want reviewer", row.Role)
	}

	// role 覆盖模型时，改模型必须报错而不是静默无效
	if _, err := SessionSetConfigOption(SessionSetConfigOptionRequest{
		SessionID: sessionID, ConfigID: "model", Value: "0/whatever",
	}, nil, 1); err == nil || !strings.Contains(err.Error(), "overridden by session role") {
		t.Errorf("model option with role override: err = %v, want overridden error", err)
	}

	// 未知 / Internal tag 拒绝
	if _, err := SessionSetConfigOption(SessionSetConfigOptionRequest{
		SessionID: sessionID, ConfigID: "role", Value: "no-such-role",
	}, nil, 1); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("unknown role: err = %v, want not found", err)
	}
	if _, err := SessionSetConfigOption(SessionSetConfigOptionRequest{
		SessionID: sessionID, ConfigID: "role", Value: "internal-x",
	}, nil, 1); err == nil || !strings.Contains(err.Error(), "internal") {
		t.Errorf("internal role: err = %v, want internal error", err)
	}

	// 空 value 清除角色
	if _, err := SessionSetConfigOption(SessionSetConfigOptionRequest{
		SessionID: sessionID, ConfigID: "role", Value: "",
	}, nil, 1); err != nil {
		t.Fatalf("clear role error = %v", err)
	}
	if sess.Role != "" || sess.RoleConfig.AgentName != "" {
		t.Errorf("清除后运行时 role = %q / %q", sess.Role, sess.RoleConfig.AgentName)
	}
	if err := sess.DB.Select("id", "role").Where("id = ?", sess.ID).First(&row).Error; err != nil {
		t.Fatalf("query role after clear: %v", err)
	}
	if row.Role != "" {
		t.Errorf("清除后持久化 role = %q, want empty", row.Role)
	}
}

func TestSessionNewWithRole(t *testing.T) {
	setupRoleTestConfig(t)
	dir := t.TempDir()
	t.Cleanup(func() { _ = codebase.CloseDirectory(dir) })

	noop := func(string, any, *string) error { return nil }
	const connID = 9101
	resp, err := SessionNew(SessionNewRequest{
		Cwd:  dir,
		Role: json.RawMessage(`"reviewer"`),
	}, noop, connID)
	if err != nil {
		t.Fatalf("SessionNew with role error = %v", err)
	}
	t.Cleanup(func() {
		unregisterConnCall(connID, resp.SessionID)
		bindedSessionOnConnMu.Lock()
		delete(bindedSessionOnConn, connID)
		bindedSessionOnConnMu.Unlock()
		closeSession(resp.SessionID)
	})

	var roleValue string
	for _, opt := range resp.ConfigOptions {
		if opt.ConfigID == "role" {
			roleValue = opt.CurrentValue
		}
	}
	if roleValue != "reviewer" {
		t.Errorf("session/new configOptions role = %q, want reviewer", roleValue)
	}

	obj, ok := lookupSession(resp.SessionID)
	if !ok || obj.session == nil {
		t.Fatal("session not loaded in memory")
	}
	// Windows 上 codebase 索引 goroutine 会持有 .alkaid0/codebase.sqlite；
	// 必须等它结束，否则 t.TempDir 清理会因文件被占用而失败。
	if obj.indexDone != nil {
		select {
		case <-obj.indexDone:
		case <-time.After(15 * time.Second):
			t.Error("timeout waiting for async index goroutine")
		}
	}
	if obj.session.Role != "reviewer" || obj.session.RoleConfig.AgentName != "Reviewer" {
		t.Errorf("session/new 运行时 role = %q / %q", obj.session.Role, obj.session.RoleConfig.AgentName)
	}
}

func TestSessionNewRejectsInvalidRoleBeforeCreate(t *testing.T) {
	setupRoleTestConfig(t)
	dir := t.TempDir()

	_, err := SessionNew(SessionNewRequest{
		Cwd:  dir,
		Role: json.RawMessage(`"no-such-role"`),
	}, nil, 9102)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("SessionNew invalid role error = %v, want not found", err)
	}
	// 校验在 loadSession 之前：不应创建 .alkaid0 或会话行。
	if _, statErr := os.Stat(dir + "/.alkaid0"); statErr == nil {
		t.Error("非法 role 不应创建 .alkaid0")
	}
}

func TestSessionListReturnsRole(t *testing.T) {
	setupRoleTestConfig(t)
	dir, db, ids := newVisibleSessionListDB(t, 2)
	if err := db.Model(&structs.Chats{}).Where("id = ?", ids[1]).
		Update("role", "reviewer").Error; err != nil {
		t.Fatalf("set role: %v", err)
	}

	resp, err := SessionList(SessionListRequest{Cwd: dir}, nil, 9301)
	if err != nil {
		t.Fatalf("SessionList error = %v", err)
	}
	roles := map[uint32]string{}
	for _, info := range resp.Sessions {
		_, id, perr := parseSessionID(info.SessionID)
		if perr != nil {
			t.Fatalf("parse session id %q: %v", info.SessionID, perr)
		}
		roles[id] = info.Role
	}
	if roles[ids[0]] != "" {
		t.Errorf("无 role 的会话应返回空 role, got %q", roles[ids[0]])
	}
	if roles[ids[1]] != "reviewer" {
		t.Errorf("session/list role = %q, want reviewer", roles[ids[1]])
	}
}

func TestRoleRPCColdAndWarm(t *testing.T) {
	setupRoleTestConfig(t)
	dir, db, ids := newSessionListDB(t, 1)
	sessionID := cwd2SessionID(dir, ids[0])

	const connID = 9201
	bindedSessionOnConnMu.Lock()
	bindedSessionOnConn[connID] = []string{sessionID}
	bindedSessionOnConnMu.Unlock()
	t.Cleanup(func() {
		bindedSessionOnConnMu.Lock()
		delete(bindedSessionOnConn, connID)
		bindedSessionOnConnMu.Unlock()
	})

	// 未绑定连接拒绝
	if _, err := RoleGet(RoleGetRequest{SessionID: sessionID}, nil, connID+1); err == nil ||
		!strings.Contains(err.Error(), "not bound") {
		t.Errorf("RoleGet unbound: err = %v, want not bound", err)
	}

	// 冷会话读取：空角色 + 可选列表
	got, err := RoleGet(RoleGetRequest{SessionID: sessionID}, nil, connID)
	if err != nil {
		t.Fatalf("RoleGet cold error = %v", err)
	}
	if got.Role != "" {
		t.Errorf("RoleGet cold role = %q, want empty", got.Role)
	}
	if len(got.Roles) == 0 {
		t.Error("RoleGet cold roles is empty")
	}

	// 冷会话写入
	if _, err := RoleSet(RoleSetRequest{SessionID: sessionID, Role: "reviewer"}, nil, connID); err != nil {
		t.Fatalf("RoleSet cold error = %v", err)
	}
	var row structs.Chats
	if err := db.Select("id", "role").Where("id = ?", ids[0]).First(&row).Error; err != nil {
		t.Fatalf("query role: %v", err)
	}
	if row.Role != "reviewer" {
		t.Errorf("RoleSet cold persisted role = %q, want reviewer", row.Role)
	}

	// 未知 / Internal 拒绝（不落库）
	if _, err := RoleSet(RoleSetRequest{SessionID: sessionID, Role: "no-such-role"}, nil, connID); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Errorf("RoleSet unknown: err = %v, want not found", err)
	}
	if _, err := RoleSet(RoleSetRequest{SessionID: sessionID, Role: "internal-x"}, nil, connID); err == nil ||
		!strings.Contains(err.Error(), "internal") {
		t.Errorf("RoleSet internal: err = %v, want internal error", err)
	}

	// 热会话：同步运行时字段
	sess := &structs.Chats{ID: ids[0], DB: db, Role: "reviewer"}
	sessLock.Lock()
	sessions[sessionID] = &sessionObj{cwd: dir, id: ids[0], session: sess}
	sessLock.Unlock()
	t.Cleanup(func() {
		sessLock.Lock()
		delete(sessions, sessionID)
		sessLock.Unlock()
	})

	resp, err := RoleSet(RoleSetRequest{SessionID: sessionID, Role: "plan"}, nil, connID)
	if err != nil {
		t.Fatalf("RoleSet warm error = %v", err)
	}
	if resp.Role != "plan" || sess.Role != "plan" || sess.RoleConfig.AgentName != "Planner" {
		t.Errorf("RoleSet warm: resp=%q role=%q config=%q", resp.Role, sess.Role, sess.RoleConfig.AgentName)
	}

	// 热会话清除
	if _, err := RoleSet(RoleSetRequest{SessionID: sessionID, Role: ""}, nil, connID); err != nil {
		t.Fatalf("RoleSet clear error = %v", err)
	}
	if sess.Role != "" || sess.RoleConfig.AgentName != "" {
		t.Errorf("RoleSet clear: role=%q config=%q", sess.Role, sess.RoleConfig.AgentName)
	}
}
