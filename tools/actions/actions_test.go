package actions

import (
	"testing"

	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/toolobj"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open test database: %v", err)
	}
	
	// 自动迁移
	if err := db.AutoMigrate(&structs.Scopes{}); err != nil {
		t.Fatalf("Failed to migrate: %v", err)
	}
	
	return db
}

func TestAddScope(t *testing.T) {
	// 清空全局状态
	toolobj.Scopes = make(map[string]string)
	
	AddScope("test_scope", "test prompt")
	
	if prompt, ok := toolobj.Scopes["test_scope"]; !ok {
		t.Error("Scope not added")
	} else if prompt != "test prompt" {
		t.Errorf("Expected 'test prompt', got %s", prompt)
	}
}

func TestAddTool(t *testing.T) {
	// 清空全局状态
	toolobj.ToolsList = make(map[string]*toolobj.Tools)
	
	tool := &toolobj.Tools{
		ID:              "test_tool",
		Name:            "Test Tool",
		Scope:           "test",
		UserDescription: "A test tool",
		Parameters:      make(map[string]parser.ToolParameters),
	}
	
	AddTool(tool)
	
	if addedTool, ok := toolobj.ToolsList["test_tool"]; !ok {
		t.Error("Tool not added")
	} else if addedTool.Name != "Test Tool" {
		t.Errorf("Expected 'Test Tool', got %s", addedTool.Name)
	}
}

func TestHookTool(t *testing.T) {
	// 清空全局状态
	toolobj.ToolsList = make(map[string]*toolobj.Tools)
	
	tool := &toolobj.Tools{
		ID:    "test_tool",
		Name:  "Test Tool",
		Hooks: []toolobj.Hook{},
	}
	
	AddTool(tool)
	
	hook := &toolobj.Hook{
		Scope: "test_scope",
	}
	
	HookTool("test_tool", hook)
	
	if len(toolobj.ToolsList["test_tool"].Hooks) != 1 {
		t.Errorf("Expected 1 hook, got %d", len(toolobj.ToolsList["test_tool"].Hooks))
	}
	
	if toolobj.ToolsList["test_tool"].Hooks[0].Scope != "test_scope" {
		t.Errorf("Expected scope 'test_scope', got %s", toolobj.ToolsList["test_tool"].Hooks[0].Scope)
	}
}

func TestEnableScope(t *testing.T) {
	// 清空全局状态
	toolobj.Scopes = make(map[string]string)
	
	db := setupTestDB(t)
	
	AddScope("test_scope", "test prompt")
	
	session := &structs.Chats{
		ID:           1,
		DB:           db,
		EnableScopes: make(map[string]bool),
	}
	
	err := EnableScope(session, "test_scope")
	if err != nil {
		t.Fatalf("Failed to enable scope: %v", err)
	}
	
	if !session.EnableScopes["test_scope"] {
		t.Error("Scope not enabled in session")
	}
	
	// 验证数据库中的状态
	var scope structs.Scopes
	if err := db.Where("name = ? AND chat_id = ?", "test_scope", session.ID).First(&scope).Error; err != nil {
		t.Fatalf("Failed to find scope in database: %v", err)
	}
	
	if !scope.Enabled {
		t.Error("Scope not enabled in database")
	}
}

func TestEnableScopeNotFound(t *testing.T) {
	// 清空全局状态
	toolobj.Scopes = make(map[string]string)
	
	db := setupTestDB(t)
	
	session := &structs.Chats{
		ID:           1,
		DB:           db,
		EnableScopes: make(map[string]bool),
	}
	
	err := EnableScope(session, "non_existent_scope")
	if err == nil {
		t.Error("Expected error for non-existent scope")
	}
}

func TestEnableScopeEmpty(t *testing.T) {
	session := &structs.Chats{
		EnableScopes: make(map[string]bool),
	}
	
	err := EnableScope(session, "")
	if err != nil {
		t.Errorf("Expected no error for empty scope, got %v", err)
	}
}

func TestDisableScope(t *testing.T) {
	// 清空全局状态
	toolobj.Scopes = make(map[string]string)
	
	db := setupTestDB(t)
	
	AddScope("test_scope", "test prompt")
	
	session := &structs.Chats{
		ID:           1,
		DB:           db,
		EnableScopes: make(map[string]bool),
	}
	
	// 先启用
	session.EnableScopes["test_scope"] = true
	
	// 再禁用
	err := DisableScope(session, "test_scope")
	if err != nil {
		t.Fatalf("Failed to disable scope: %v", err)
	}
	
	if session.EnableScopes["test_scope"] {
		t.Error("Scope still enabled in session")
	}
	
	// 验证数据库中的状态
	var scope structs.Scopes
	if err := db.Where("name = ? AND chat_id = ?", "test_scope", session.ID).First(&scope).Error; err != nil {
		t.Fatalf("Failed to find scope in database: %v", err)
	}
	
	if scope.Enabled {
		t.Error("Scope still enabled in database")
	}
}

func TestDisableScopeNotFound(t *testing.T) {
	// 清空全局状态
	toolobj.Scopes = make(map[string]string)
	
	db := setupTestDB(t)
	
	session := &structs.Chats{
		ID:           1,
		DB:           db,
		EnableScopes: make(map[string]bool),
	}
	
	err := DisableScope(session, "non_existent_scope")
	if err == nil {
		t.Error("Expected error for non-existent scope")
	}
}

func TestDisableScopeEmpty(t *testing.T) {
	session := &structs.Chats{
		EnableScopes: make(map[string]bool),
	}
	
	err := DisableScope(session, "")
	if err != nil {
		t.Errorf("Expected no error for empty scope, got %v", err)
	}
}

func TestSetScopeEnabled(t *testing.T) {
	db := setupTestDB(t)
	
	// 测试创建新记录
	err := SetScopeEnabled(db, 1, "test_scope", true)
	if err != nil {
		t.Fatalf("Failed to set scope enabled: %v", err)
	}
	
	var scope structs.Scopes
	if err := db.Where("name = ? AND chat_id = ?", "test_scope", uint32(1)).First(&scope).Error; err != nil {
		t.Fatalf("Failed to find scope: %v", err)
	}
	
	if !scope.Enabled {
		t.Error("Scope not enabled")
	}
	
	// 测试更新现有记录
	err = SetScopeEnabled(db, 1, "test_scope", false)
	if err != nil {
		t.Fatalf("Failed to update scope: %v", err)
	}
	
	if err := db.Where("name = ? AND chat_id = ?", "test_scope", uint32(1)).First(&scope).Error; err != nil {
		t.Fatalf("Failed to find scope: %v", err)
	}
	
	if scope.Enabled {
		t.Error("Scope still enabled after update")
	}
}

func TestSetScopeEnabledNilDB(t *testing.T) {
	// 测试 DB 为 nil 的情况
	err := SetScopeEnabled(nil, 1, "test_scope", true)
	if err != nil {
		t.Errorf("Expected no error for nil DB, got %v", err)
	}
}

func TestLoad(t *testing.T) {
	db := setupTestDB(t)
	
	// 在数据库中创建一些scope记录
	scopes := []structs.Scopes{
		{Name: "scope1", Enabled: true, ChatID: 1},
		{Name: "scope2", Enabled: false, ChatID: 1},
		{Name: "scope3", Enabled: true, ChatID: 2}, // 不同的 chat_id
	}
	
	for _, s := range scopes {
		if err := db.Create(&s).Error; err != nil {
			t.Fatalf("Failed to create scope: %v", err)
		}
	}
	
	session := &structs.Chats{
		ID: 1,
		DB: db,
	}
	
	Load(session)
	
	if session.EnableScopes == nil {
		t.Fatal("EnableScopes is nil")
	}
	
	if !session.EnableScopes["scope1"] {
		t.Error("scope1 should be enabled")
	}
	
	if session.EnableScopes["scope2"] {
		t.Error("scope2 should be disabled")
	}
	
	// scope3 属于不同的 chat_id，不应该被加载
	if _, exists := session.EnableScopes["scope3"]; exists {
		t.Error("scope3 should not be loaded")
	}
}

func TestLoadNilDB(t *testing.T) {
	session := &structs.Chats{
		ID: 1,
		DB: nil,
	}
	
	// 不应该panic
	Load(session)
	
	if session.EnableScopes == nil {
		t.Error("EnableScopes should be initialized")
	}
}

func TestLoadWithExistingScopes(t *testing.T) {
	db := setupTestDB(t)
	
	// 在数据库中创建scope记录
	scope := structs.Scopes{Name: "scope1", Enabled: true, ChatID: 1}
	if err := db.Create(&scope).Error; err != nil {
		t.Fatalf("Failed to create scope: %v", err)
	}
	
	session := &structs.Chats{
		ID: 1,
		DB: db,
		EnableScopes: map[string]bool{
			"existing_scope": true,
		},
	}
	
	Load(session)
	
	// 应该保留现有的scope
	if !session.EnableScopes["existing_scope"] {
		t.Error("existing_scope should still be present")
	}
	
	// 应该加载新的scope
	if !session.EnableScopes["scope1"] {
		t.Error("scope1 should be loaded")
	}
}

func TestGetAllScopes(t *testing.T) {
	db := setupTestDB(t)
	
	// 创建测试数据
	scopes := []structs.Scopes{
		{Name: "scope1", Enabled: true, ChatID: 1},
		{Name: "scope2", Enabled: false, ChatID: 1},
		{Name: "", Enabled: true, ChatID: 1}, // 空名称应该被忽略
		{Name: "scope3", Enabled: true, ChatID: 2}, // 不同的 chat_id
	}
	
	for _, s := range scopes {
		if err := db.Create(&s).Error; err != nil {
			t.Fatalf("Failed to create scope: %v", err)
		}
	}
	
	session := &structs.Chats{
		ID: 1,
		DB: db,
	}
	
	result, err := getAllScopes(session, db)
	if err != nil {
		t.Fatalf("Failed to get all scopes: %v", err)
	}
	
	if len(result) != 2 {
		t.Errorf("Expected 2 scopes, got %d", len(result))
	}
	
	if !result["scope1"] {
		t.Error("scope1 should be enabled")
	}
	
	if result["scope2"] {
		t.Error("scope2 should be disabled")
	}
	
	if _, exists := result["scope3"]; exists {
		t.Error("scope3 should not be included (different chat_id)")
	}
}

func TestGetAllScopesNilDB(t *testing.T) {
	session := &structs.Chats{
		ID: 1,
	}
	
	result, err := getAllScopes(session, nil)
	if err != nil {
		t.Errorf("Expected no error for nil DB, got %v", err)
	}
	
	if len(result) != 0 {
		t.Errorf("Expected empty result, got %d scopes", len(result))
	}
}
