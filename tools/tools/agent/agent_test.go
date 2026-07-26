package agent

import (
	"sync"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/library/chancall"
	agents "github.com/cxykevin/alkaid0/provider/request/agents/actions"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

var (
	testConsumerOnce sync.Once
)

// anyPtr 创建 *any 指针
func anyPtr(v any) *any {
	x := v
	return &x
}

// initTestAgentsConsumer 注册测试用的 agents chancall 消费者
func initTestAgentsConsumer() {
	testConsumerOnce.Do(func() {
		agents.Call = chancall.Register(agents.ConsumerName, func(obj any) (any, error) {
			switch o := obj.(type) {
			case agents.Add:
				return nil, o.Session.DB.Create(&storageStructs.SubAgents{
					ID:       o.AgentCode,
					AgentID:  o.AgentID,
					BindPath: o.Path,
				}).Error
			case agents.Update:
				var existing storageStructs.SubAgents
				if err := o.Session.DB.Where("id = ?", o.AgentCode).First(&existing).Error; err != nil {
					return nil, err
				}
				existing.AgentID = o.AgentID
				existing.BindPath = o.Path
				return nil, o.Session.DB.Save(&existing).Error
			case agents.Del:
				return nil, o.Session.DB.Where("id = ?", o.AgentCode).Delete(&storageStructs.SubAgents{}).Error
			case agents.List:
				var list []storageStructs.SubAgents
				err := o.Session.DB.Find(&list).Error
				return list, err
			case agents.Activate:
				var agent storageStructs.SubAgents
				if err := o.Session.DB.Where("id = ?", o.AgentCode).First(&agent).Error; err != nil {
					return nil, err
				}
				o.Session.CurrentActivatePath = agent.BindPath
				o.Session.NowAgent = o.AgentCode
				o.Session.CurrentAgentID = agent.ID
				o.Session.CurrentAgentConfig = cfgStruct.AgentConfig{}
				return nil, o.Session.DB.Save(o.Session).Error
			case agents.Deactivate:
				o.Session.NowAgent = ""
				o.Session.CurrentAgentID = ""
				o.Session.CurrentActivatePath = ""
				o.Session.CurrentAgentConfig = cfgStruct.AgentConfig{}
				return nil, o.Session.DB.Model(&storageStructs.Chats{}).Where("id = ?", o.Session.ID).Update("now_agent", "").Error
			}
			return nil, nil
		})
	})
}

// setupTestDB 创建内存 SQLite 并迁移表
func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	initTestAgentsConsumer()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}
	if err := db.AutoMigrate(
		&storageStructs.Chats{},
		&storageStructs.Messages{},
		&storageStructs.SubAgents{},
	); err != nil {
		t.Fatalf("Failed to migrate: %v", err)
	}
	return db
}

// setupTestSession 创建测试会话并配置 agent config
func setupTestSession(t *testing.T, db *gorm.DB) *storageStructs.Chats {
	t.Helper()
	*config.GlobalConfig = cfgStruct.Config{
		Agent: cfgStruct.AgentsConfig{
			IgnoreBuiltinAgents: true,
			Agents: map[string]cfgStruct.AgentConfig{
				"tag-coder": {
					AgentName:        "Coder",
					AgentDescription: "A coding agent",
				},
			},
		},
	}

	chat := storageStructs.Chats{
		ID:          1,
		LastModelID: 1,
		DB:          db,
		InTestFlag:  true,
	}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("Failed to create test chat: %v", err)
	}

	// 初始化工具调用上下文
	chat.ToolCallingContext = make(map[string]any)
	chat.ToolCallingType = make(map[string]string)

	return &chat
}

// --- CheckName 测试 ---

func TestCheckName_Valid(t *testing.T) {
	mp := map[string]*any{
		"name": anyPtr("my-agent"),
	}
	name, err := CheckName(mp)
	if err != nil {
		t.Fatalf("CheckName failed: %v", err)
	}
	if name != "my-agent" {
		t.Errorf("CheckName = %q, want %q", name, "my-agent")
	}
}

func TestCheckName_MissingKey(t *testing.T) {
	_, err := CheckName(map[string]*any{})
	if err == nil {
		t.Error("CheckName with empty map should fail")
	}
}

func TestCheckName_NilValue(t *testing.T) {
	mp := map[string]*any{
		"name": nil,
	}
	_, err := CheckName(mp)
	if err == nil {
		t.Error("CheckName with nil value should fail")
	}
}

func TestCheckName_EmptyString(t *testing.T) {
	mp := map[string]*any{
		"name": anyPtr(""),
	}
	_, err := CheckName(mp)
	if err == nil {
		t.Error("CheckName with empty string should fail")
	}
}

func TestCheckName_NonStringType(t *testing.T) {
	mp := map[string]*any{
		"name": anyPtr(123),
	}
	_, err := CheckName(mp)
	if err == nil {
		t.Error("CheckName with non-string type should fail")
	}
}

func TestCheckName_NilMap(t *testing.T) {
	_, err := CheckName(nil)
	if err == nil {
		t.Error("CheckName with nil map should fail")
	}
}

// --- CheckPrompt 测试 ---

func TestCheckPrompt_Valid(t *testing.T) {
	mp := map[string]*any{
		"prompt": anyPtr("You are a helpful agent"),
	}
	prompt, err := CheckPrompt(mp)
	if err != nil {
		t.Fatalf("CheckPrompt failed: %v", err)
	}
	if prompt != "You are a helpful agent" {
		t.Errorf("CheckPrompt = %q, want %q", prompt, "You are a helpful agent")
	}
}

func TestCheckPrompt_MissingKey(t *testing.T) {
	_, err := CheckPrompt(map[string]*any{})
	if err == nil {
		t.Error("CheckPrompt with empty map should fail")
	}
}

func TestCheckPrompt_Empty(t *testing.T) {
	mp := map[string]*any{
		"prompt": anyPtr(""),
	}
	_, err := CheckPrompt(mp)
	if err == nil {
		t.Error("CheckPrompt with empty string should fail")
	}
}

func TestCheckPrompt_NilMap(t *testing.T) {
	_, err := CheckPrompt(nil)
	if err == nil {
		t.Error("CheckPrompt with nil map should fail")
	}
}

// --- enableActivate / enableDeactivate 测试 ---

func TestEnableActivate(t *testing.T) {
	tests := []struct {
		name           string
		currentAgentID string
		wantActivate   bool
		wantDeactivate bool
	}{
		{name: "无活跃 Agent", currentAgentID: "", wantActivate: true, wantDeactivate: false},
		{name: "有活跃 Agent", currentAgentID: "active", wantActivate: false, wantDeactivate: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := &storageStructs.Chats{CurrentAgentID: tt.currentAgentID}
			if got := enableActivate(session); got != tt.wantActivate {
				t.Errorf("enableActivate = %v, want %v", got, tt.wantActivate)
			}
			if got := enableDeactivate(session); got != tt.wantDeactivate {
				t.Errorf("enableDeactivate = %v, want %v", got, tt.wantDeactivate)
			}
		})
	}
}

// --- updateAgentInfo 测试 ---

func TestUpdateAgentInfo_Basic(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	mp := map[string]*any{
		"name":   anyPtr("test-agent"),
		"tag":    anyPtr("tag-coder"),
		"delete": anyPtr(false),
	}
	var cross []*any

	ok, resultCross, err := updateAgentInfo(session, mp, cross, "tool_1")
	if err != nil {
		t.Fatalf("updateAgentInfo failed: %v", err)
	}
	if !ok {
		t.Error("updateAgentInfo should return true")
	}
	if resultCross != nil {
		t.Log("updateAgentInfo passed cross through (values are preserved)")
	}

	toolCallID := "call_1_0_tool_1"
	if _, ok := session.ToolCallingContext[toolCallID]; !ok {
		t.Error("ToolCallingContext should contain toolCallID")
	}
	if session.ToolCallingType[toolCallID] != "agent" {
		t.Errorf("ToolCallingType = %q, want %q", session.ToolCallingType[toolCallID], "agent")
	}
}

func TestUpdateAgentInfo_DeleteOnly(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	mp := map[string]*any{
		"name":   anyPtr("test-agent"),
		"delete": anyPtr(true),
	}
	updateAgentInfo(session, mp, nil, "tool_2")

	if _, ok := session.ToolCallingContext["call_1_0_tool_2"]; !ok {
		t.Error("ToolCallingContext should contain toolCallID")
	}
}

// --- updateInfo 测试 ---

func TestUpdateInfo_ActivateMode(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	mp := map[string]*any{
		"name":   anyPtr("coder-agent"),
		"prompt": anyPtr("activate prompt"),
	}
	updateInfo(session, mp, nil, "tool_3")

	toolCallID := "call_1_0_tool_3"
	if _, ok := session.ToolCallingContext[toolCallID]; !ok {
		t.Error("ToolCallingContext should contain toolCallID for activation")
	}
	if session.ToolCallingType[toolCallID] != "activate_agent" {
		t.Errorf("ToolCallingType = %q, want %q", session.ToolCallingType[toolCallID], "activate_agent")
	}
}

func TestUpdateInfo_DeactivateMode(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)
	session.CurrentAgentID = "active-agent"

	mp := map[string]*any{
		"name":   anyPtr("active-agent"),
		"prompt": anyPtr("deactivate prompt"),
	}
	updateInfo(session, mp, nil, "tool_4")

	toolCallID := "call_1_0_tool_4"
	if session.ToolCallingType[toolCallID] != "deactivate_agent" {
		t.Errorf("ToolCallingType = %q, want %q", session.ToolCallingType[toolCallID], "deactivate_agent")
	}
}

// --- editAgent 测试 ---

func TestEditAgent_Create(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	mp := map[string]*any{
		"name": anyPtr("new-agent"),
		"tag":  anyPtr("tag-coder"),
		"path": anyPtr("workdir"),
	}

	_, _, result, err := editAgent(session, mp, nil)
	if err != nil {
		t.Fatalf("editAgent failed: %v", err)
	}
	if result == nil {
		t.Fatal("editAgent should return non-nil result")
	}
	if result["success"] == nil {
		t.Fatal("editAgent result should have success field")
	}
	if success, ok := (*result["success"]).(bool); !ok || !success {
		t.Errorf("editAgent success = %v, want true", result["success"])
	}

	// 验证 agent 已创建
	var agent storageStructs.SubAgents
	if err := db.Where("id = ?", "new-agent").First(&agent).Error; err != nil {
		t.Fatalf("Agent should have been created: %v", err)
	}
	if agent.AgentID != "tag-coder" {
		t.Errorf("AgentID = %q, want %q", agent.AgentID, "tag-coder")
	}
}

func TestEditAgent_Update(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	// 先创建
	_, _, _, _ = editAgent(session, map[string]*any{
		"name": anyPtr("update-agent"),
		"tag":  anyPtr("tag-coder"),
		"path": anyPtr("old/path"),
	}, nil)

	// 再更新（通过 editAgent 的内部逻辑：先查询，存在则 UpdateAgent）
	_, _, result, err := editAgent(session, map[string]*any{
		"name": anyPtr("update-agent"),
		"tag":  anyPtr("tag-coder"),
		"path": anyPtr("new/path"),
	}, nil)
	if err != nil {
		t.Fatalf("editAgent update failed: %v", err)
	}
	if success, ok := (*result["success"]).(bool); !ok || !success {
		t.Errorf("editAgent update success = %v, want true", result["success"])
	}

	// 验证更新
	var agent storageStructs.SubAgents
	if err := db.Where("id = ?", "update-agent").First(&agent).Error; err != nil {
		t.Fatalf("Agent should exist: %v", err)
	}
	if agent.BindPath != "new/path" {
		t.Errorf("BindPath after update = %q, want %q", agent.BindPath, "new/path")
	}
}

func TestEditAgent_Delete(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	// 先创建
	editAgent(session, map[string]*any{
		"name": anyPtr("delete-agent"),
		"tag":  anyPtr("tag-coder"),
		"path": anyPtr("workdir"),
	}, nil)

	// 删除
	_, _, result, err := editAgent(session, map[string]*any{
		"name":   anyPtr("delete-agent"),
		"delete": anyPtr(true),
	}, nil)
	if err != nil {
		t.Fatalf("editAgent delete failed: %v", err)
	}
	if success, ok := (*result["success"]).(bool); !ok || !success {
		t.Errorf("editAgent delete success = %v, want true", result["success"])
	}
}

func TestEditAgent_MissingName(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	mp := map[string]*any{
		"tag":  anyPtr("coder"),
		"path": anyPtr("workdir"),
	}
	_, _, result, err := editAgent(session, mp, nil)
	if err != nil {
		t.Fatalf("editAgent with missing name should handle error: %v", err)
	}
	if result == nil {
		t.Fatal("editAgent should return error result when name is missing")
	}
	if result["success"] == nil || *result["success"] == any(true) {
		t.Error("editAgent should fail when name is missing")
	}
}

func TestEditAgent_MissingTag(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	mp := map[string]*any{
		"name": anyPtr("no-tag-agent"),
		"path": anyPtr("workdir"),
	}
	_, _, result, err := editAgent(session, mp, nil)
	if err != nil {
		t.Fatalf("editAgent with missing tag: %v", err)
	}
	if result["error"] == nil {
		t.Error("Expected error for missing tag")
	}
}

func TestEditAgent_MissingPath(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	mp := map[string]*any{
		"name": anyPtr("no-path-agent"),
		"tag":  anyPtr("tag-coder"),
	}
	_, _, result, err := editAgent(session, mp, nil)
	if err != nil {
		t.Fatalf("editAgent with missing path: %v", err)
	}
	if result["error"] == nil {
		t.Error("Expected error for missing path")
	}
}

// --- useAgent 测试 ---

func TestUseAgent_Success(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	// 先创建 agent 记录
	agent := storageStructs.SubAgents{
		ID:       "agent-to-activate",
		AgentID:  "tag-coder",
		BindPath: "/workspace",
	}
	if err := db.Create(&agent).Error; err != nil {
		t.Fatalf("Failed to create subagent: %v", err)
	}

	mp := map[string]*any{
		"name":   anyPtr("agent-to-activate"),
		"prompt": anyPtr("You are active"),
	}
	_, _, result, err := useAgent(session, mp, nil)
	if err != nil {
		t.Fatalf("useAgent failed: %v", err)
	}
	if result == nil {
		t.Fatal("useAgent should return result")
	}
	if success, ok := (*result["success"]).(bool); !ok || !success {
		t.Errorf("useAgent success = %v, want true", result["success"])
	}
	if session.NowAgent != "agent-to-activate" {
		t.Errorf("NowAgent = %q, want %q", session.NowAgent, "agent-to-activate")
	}
}

func TestUseAgent_MissingName(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	mp := map[string]*any{
		"prompt": anyPtr("prompt"),
	}
	_, _, result, err := useAgent(session, mp, nil)
	if err != nil {
		t.Fatalf("useAgent with missing name: %v", err)
	}
	if result["error"] == nil {
		t.Error("Expected error for missing name")
	}
}

func TestUseAgent_MissingPrompt(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	mp := map[string]*any{
		"name": anyPtr("agent"),
	}
	_, _, result, err := useAgent(session, mp, nil)
	if err != nil {
		t.Fatalf("useAgent with missing prompt: %v", err)
	}
	if result["error"] == nil {
		t.Error("Expected error for missing prompt")
	}
}

// --- unuseAgent 测试 ---

func TestUnuseAgent_Success(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)
	session.CurrentAgentID = "active-agent"

	mp := map[string]*any{
		"prompt": anyPtr("Goodbye"),
	}
	_, _, result, err := unuseAgent(session, mp, nil)
	if err != nil {
		t.Fatalf("unuseAgent failed: %v", err)
	}
	if result == nil {
		t.Fatal("unuseAgent should return result")
	}
	if success, ok := (*result["success"]).(bool); !ok || !success {
		t.Errorf("unuseAgent success = %v, want true", result["success"])
	}
}

func TestUnuseAgent_MissingPrompt(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)
	session.CurrentAgentID = "active-agent"

	mp := map[string]*any{}
	_, _, result, err := unuseAgent(session, mp, nil)
	if err != nil {
		t.Fatalf("unuseAgent with missing prompt: %v", err)
	}
	if result["error"] == nil {
		t.Error("Expected error for missing prompt")
	}
}
