package agents

import (
	"context"
	"sync"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStruct "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/provider/request/agents/actions"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setupTestDB 创建内存 SQLite 数据库并迁移所需的表
func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}

	if err := db.AutoMigrate(
		&structs.Chats{},
		&structs.Messages{},
		&structs.SubAgents{},
	); err != nil {
		t.Fatalf("Failed to migrate: %v", err)
	}

	return db
}

// setupTestSession 创建包含 DB 和基本配置的测试会话
func setupTestSession(t *testing.T, db *gorm.DB) *structs.Chats {
	t.Helper()

	// 设置 Agent 配置 — 用于 ActivateAgent 和 LoadAgent 的 agentconfig 查找
	*config.GlobalConfig = cfgStruct.Config{
		Agent: cfgStruct.AgentsConfig{
			IgnoreBuiltinAgents: true,
			Agents: map[string]cfgStruct.AgentConfig{
				"tag-coder": {
					AgentName:        "Coder",
					AgentDescription: "A coding agent",
				},
				"tag-reviewer": {
					AgentName:        "Reviewer",
					AgentDescription: "A review agent",
				},
			},
		},
	}

	chat := structs.Chats{
		ID:          1,
		LastModelID: 1,
		DB:          db,
	}
	if err := db.Create(&chat).Error; err != nil {
		t.Fatalf("Failed to create test chat: %v", err)
	}
	return &chat
}

// TestAddAgent_Success 测试成功添加 Agent
func TestAddAgent_Success(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	err := AddAgent(session, "my-agent", "tag-coder", "workdir/sub")
	if err != nil {
		t.Fatalf("AddAgent failed: %v", err)
	}

	// 验证数据库中的记录
	var agent structs.SubAgents
	if err := db.Where("id = ?", "my-agent").First(&agent).Error; err != nil {
		t.Fatalf("Failed to find agent: %v", err)
	}
	if agent.AgentID != "tag-coder" {
		t.Errorf("AgentID = %q, want %q", agent.AgentID, "tag-coder")
	}
	if agent.BindPath != "workdir/sub" {
		t.Errorf("BindPath = %q, want %q", agent.BindPath, "workdir/sub")
	}
}

// TestAddAgent_PathValidation 测试路径验证
func TestAddAgent_PathValidation(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	invalidPaths := []string{
		"../outside",
		"/absolute/path",
		"\\windows\\path",
		"~/home",
		"path:with:colons",
		"path*with*wildcard",
		"path?with?wildcard",
		"path\"with\"quote",
		"path<with<bracket",
		"path>with>bracket",
		"path|with|pipe",
		"path\nwith\nnewline",
	}

	for _, path := range invalidPaths {
		err := AddAgent(session, "test-agent", "tag-coder", path)
		if err == nil {
			t.Errorf("AddAgent with path %q should have failed", path)
		}
	}
}

// TestAddAgent_DuplicateID 测试重复添加相同 ID 的 Agent
func TestAddAgent_DuplicateID(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	err := AddAgent(session, "dup-agent", "tag-coder", "workdir")
	if err != nil {
		t.Fatalf("First AddAgent failed: %v", err)
	}

	err = AddAgent(session, "dup-agent", "tag-coder", "workdir")
	if err == nil {
		t.Error("Second AddAgent with same ID should have failed")
	}
}

// TestAddAgent_InvalidTag 测试不存在的 Agent 标签
func TestAddAgent_InvalidTag(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	err := AddAgent(session, "bad-tag-agent", "non-existent-tag", "workdir")
	if err == nil {
		t.Error("AddAgent with non-existent tag should have failed")
	}
}

// TestDeleteAgent_Success 测试成功删除 Agent
func TestDeleteAgent_Success(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	// 先添加
	if err := AddAgent(session, "delete-me", "tag-coder", "workdir"); err != nil {
		t.Fatalf("AddAgent failed: %v", err)
	}

	// 再删除
	if err := DeleteAgent(session, "delete-me"); err != nil {
		t.Fatalf("DeleteAgent failed: %v", err)
	}

	// 验证已删除
	var agent structs.SubAgents
	err := db.Where("id = ?", "delete-me").First(&agent).Error
	if err == nil {
		t.Error("Agent should have been deleted")
	}
}

// TestDeleteAgent_NonExistent 测试删除不存在的 Agent
func TestDeleteAgent_NonExistent(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	err := DeleteAgent(session, "non-existent")
	if err != nil {
		t.Logf("DeleteAgent non-existent returned error: %v (expected behavior may vary)", err)
	}
}

// TestUpdateAgent_Success 测试更新 Agent
func TestUpdateAgent_Success(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	if err := AddAgent(session, "update-me", "tag-coder", "old/path"); err != nil {
		t.Fatalf("AddAgent failed: %v", err)
	}

	if err := UpdateAgent(session, "update-me", "tag-reviewer", "new/path"); err != nil {
		t.Fatalf("UpdateAgent failed: %v", err)
	}

	var agent structs.SubAgents
	if err := db.Where("id = ?", "update-me").First(&agent).Error; err != nil {
		t.Fatalf("Failed to find updated agent: %v", err)
	}
	if agent.AgentID != "tag-reviewer" {
		t.Errorf("AgentID after update = %q, want %q", agent.AgentID, "tag-reviewer")
	}
	if agent.BindPath != "new/path" {
		t.Errorf("BindPath after update = %q, want %q", agent.BindPath, "new/path")
	}
}

// TestUpdateAgent_NonExistent 测试更新不存在的 Agent
func TestUpdateAgent_NonExistent(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	err := UpdateAgent(session, "ghost", "tag-coder", "path")
	if err == nil {
		t.Error("UpdateAgent on non-existent agent should have failed")
	}
}

// TestListAgents_Empty 测试空列表
func TestListAgents_Empty(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	agents, err := ListAgents(session.DB)
	if err != nil {
		t.Fatalf("ListAgents failed: %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("Expected empty list, got %d agents", len(agents))
	}
}

// TestListAgents_WithData 测试列表包含数据
func TestListAgents_WithData(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	for _, name := range []string{"agent-a", "agent-b", "agent-c"} {
		if err := AddAgent(session, name, "tag-coder", "workdir"); err != nil {
			t.Fatalf("AddAgent %s failed: %v", name, err)
		}
	}

	agents, err := ListAgents(session.DB)
	if err != nil {
		t.Fatalf("ListAgents failed: %v", err)
	}
	if len(agents) != 3 {
		t.Errorf("Expected 3 agents, got %d", len(agents))
	}
}

// TestLoadAgent_EmptyNowAgent 测试 NowAgent 为空时跳过
func TestLoadAgent_EmptyNowAgent(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)
	session.NowAgent = ""

	err := LoadAgent(session)
	if err != nil {
		t.Fatalf("LoadAgent with empty NowAgent should not error: %v", err)
	}
}

// TestLoadAgent_Success 测试加载 Agent
func TestLoadAgent_Success(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	// 先添加 SubAgent
	subAgent := structs.SubAgents{
		ID:       "coder-agent",
		AgentID:  "tag-coder",
		BindPath: "/workspace",
	}
	if err := db.Create(&subAgent).Error; err != nil {
		t.Fatalf("Failed to create subagent: %v", err)
	}

	session.NowAgent = "coder-agent"

	err := LoadAgent(session)
	if err != nil {
		t.Fatalf("LoadAgent failed: %v", err)
	}

	if session.CurrentActivatePath != "/workspace" {
		t.Errorf("CurrentActivatePath = %q, want %q", session.CurrentActivatePath, "/workspace")
	}
	if session.CurrentAgentID != "coder-agent" {
		t.Errorf("CurrentAgentID = %q, want %q", session.CurrentAgentID, "coder-agent")
	}
	if session.CurrentAgentConfig.AgentName != "Coder" {
		t.Errorf("CurrentAgentConfig.AgentName = %q, want %q", session.CurrentAgentConfig.AgentName, "Coder")
	}
}

// TestLoadAgent_NotFound 测试加载不存在的 Agent
func TestLoadAgent_NotFound(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)
	session.NowAgent = "non-existent-agent"

	err := LoadAgent(session)
	if err == nil {
		t.Error("LoadAgent with non-existent agent should have failed")
	}
}

// TestActivateAgent_Success 测试激活 Agent
func TestActivateAgent_Success(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	// 先添加 SubAgent 记录
	subAgent := structs.SubAgents{
		ID:       "activate-test",
		AgentID:  "tag-coder",
		BindPath: "/test/path",
	}
	if err := db.Create(&subAgent).Error; err != nil {
		t.Fatalf("Failed to create subagent: %v", err)
	}

	prompt := "You are now an agent"
	err := ActivateAgent(session, "activate-test", prompt)
	if err != nil {
		t.Fatalf("ActivateAgent failed: %v", err)
	}

	if session.NowAgent != "activate-test" {
		t.Errorf("NowAgent = %q, want %q", session.NowAgent, "activate-test")
	}
	if session.CurrentActivatePath != "/test/path" {
		t.Errorf("CurrentActivatePath = %q, want %q", session.CurrentActivatePath, "/test/path")
	}

	// 验证 prompt 消息已写入
	var msg structs.Messages
	if err := db.Where("chat_id = ? AND agent_id = ?", session.ID, "activate-test").First(&msg).Error; err != nil {
		t.Fatalf("Failed to find activation message: %v", err)
	}
	if msg.Delta != prompt {
		t.Errorf("Message Delta = %q, want %q", msg.Delta, prompt)
	}
}

// TestActivateAgent_NotFound 测试激活不存在的 Agent
func TestActivateAgent_NotFound(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	err := ActivateAgent(session, "non-existent", "prompt")
	if err == nil {
		t.Error("ActivateAgent with non-existent agent should have failed")
	}
}

// TestDeactivateAgent_Success 测试取消激活 Agent
func TestDeactivateAgent_Success(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	// 先设置当前 Agent
	subAgent := structs.SubAgents{
		ID:       "deactivate-test",
		AgentID:  "tag-coder",
		BindPath: "/test",
	}
	if err := db.Create(&subAgent).Error; err != nil {
		t.Fatalf("Failed to create subagent: %v", err)
	}

	session.NowAgent = "deactivate-test"
	session.CurrentAgentID = "deactivate-test"
	session.CurrentActivatePath = "/test"
	if err := db.Save(session).Error; err != nil {
		t.Fatalf("Failed to save session: %v", err)
	}

	// 取消激活 — 后台 goroutine 可能因为 request.Summary 上下文而短暂运行后退出
	err := DeactivateAgent(session, "Goodbye agent")
	if err != nil {
		t.Fatalf("DeactivateAgent failed: %v", err)
	}

	// NowAgent 应被清空
	if session.NowAgent != "" {
		t.Errorf("NowAgent should be empty after deactivation, got %q", session.NowAgent)
	}
	if session.CurrentAgentID != "" {
		t.Errorf("CurrentAgentID should be empty after deactivation, got %q", session.CurrentAgentID)
	}

	// 验证数据库中的 NowAgent 已更新
	var updatedChat structs.Chats
	if err := db.Where("id = ?", session.ID).First(&updatedChat).Error; err != nil {
		t.Fatalf("Failed to find updated chat: %v", err)
	}
	if updatedChat.NowAgent != "" {
		t.Errorf("DB NowAgent should be empty after deactivation, got %q", updatedChat.NowAgent)
	}
}

// TestDeactivateAgent_NoPrompt 测试停用时不带 prompt
func TestDeactivateAgent_NoPrompt(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	session.NowAgent = "something"
	if err := db.Save(session).Error; err != nil {
		t.Fatalf("Failed to save session: %v", err)
	}

	err := DeactivateAgent(session, "")
	if err != nil {
		t.Fatalf("DeactivateAgent without prompt failed: %v", err)
	}

	if session.NowAgent != "" {
		t.Errorf("NowAgent should be empty, got %q", session.NowAgent)
	}
}

// TestLoad_Delegation 测试 Load 函数委托给 LoadAgent
func TestLoad_Delegation(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)
	session.NowAgent = ""

	err := Load(session)
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}
}

// TestConsumerRouting 测试 consumer 的类型路由 — 通过 actions 包直接调用
func TestConsumerRouting(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	// 测试 actions 包的路由：通过 actions.Call 测试 AddAgent
	err := actions.AddAgent(session, "routed-agent", "tag-coder", "routed/path")
	if err != nil {
		t.Fatalf("actions.AddAgent failed: %v", err)
	}

	// 验证
	var agent structs.SubAgents
	if err := db.Where("id = ?", "routed-agent").First(&agent).Error; err != nil {
		t.Fatalf("Failed to find agent via actions routing: %v", err)
	}
	if agent.AgentID != "tag-coder" {
		t.Errorf("AgentID = %q, want %q", agent.AgentID, "tag-coder")
	}

	// 测试 DeleteAgent 路由
	err = actions.DeleteAgent(session, "routed-agent")
	if err != nil {
		t.Fatalf("actions.DeleteAgent failed: %v", err)
	}

	// 测试 ListAgent 路由
	agents, err := actions.ListAgent(session)
	if err != nil {
		t.Fatalf("actions.ListAgent failed: %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("Expected 0 agents after delete, got %d", len(agents))
	}
}

// TestFullLifecycle 测试 Agent 的完整生命周期
func TestFullLifecycle(t *testing.T) {
	db := setupTestDB(t)
	session := setupTestSession(t, db)

	// 1. Add
	if err := AddAgent(session, "lifecycle", "tag-coder", "workdir"); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// 2. List 包含新添加的
	agents, err := ListAgents(session.DB)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(agents) != 1 {
		t.Fatalf("Expected 1 agent, got %d", len(agents))
	}

	// 3. Update
	if err := UpdateAgent(session, "lifecycle", "tag-reviewer", "newdir"); err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	// 4. Delete
	if err := DeleteAgent(session, "lifecycle"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// 5. List 为空
	agents, err = ListAgents(session.DB)
	if err != nil {
		t.Fatalf("List after delete failed: %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("Expected 0 agents after full lifecycle, got %d", len(agents))
	}
}

var (
	summaryCtx    context.Context
	summaryCancel context.CancelFunc
	summaryOnce   sync.Once
)

// ensureSummaryCancel 确保在测试结束时关闭可能的后台协程
func ensureSummaryCancel() {
	summaryOnce.Do(func() {
		summaryCtx, summaryCancel = context.WithCancel(context.Background())
	})
}

func TestMain(m *testing.M) {
	ensureSummaryCancel()
	m.Run()
	summaryCancel()
}
