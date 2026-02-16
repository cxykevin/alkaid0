package scope

import (
	"testing"

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
	
	if err := db.AutoMigrate(&structs.Scopes{}); err != nil {
		t.Fatalf("Failed to migrate: %v", err)
	}
	
	return db
}

func TestCheckName(t *testing.T) {
	tests := []struct {
		name      string
		input     map[string]*any
		expectErr bool
		expected  string
	}{
		{
			name: "valid name",
			input: map[string]*any{
				"name": func() *any { s := any("test_scope"); return &s }(),
			},
			expectErr: false,
			expected:  "test_scope",
		},
		{
			name:      "missing name",
			input:     map[string]*any{},
			expectErr: true,
		},
		{
			name: "nil name",
			input: map[string]*any{
				"name": nil,
			},
			expectErr: true,
		},
		{
			name: "empty name",
			input: map[string]*any{
				"name": func() *any { s := any(""); return &s }(),
			},
			expectErr: true,
		},
		{
			name: "invalid type",
			input: map[string]*any{
				"name": func() *any { i := any(123); return &i }(),
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := CheckName(tt.input)
			if tt.expectErr {
				if err == nil {
					t.Error("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if result != tt.expected {
					t.Errorf("Expected %s, got %s", tt.expected, result)
				}
			}
		})
	}
}

func TestUpdateInfo(t *testing.T) {
	session := &structs.Chats{
		TemporyDataOfRequest: make(map[string]any),
	}

	// 测试首次调用
	mp := map[string]*any{
		"name": func() *any { s := any("test_scope"); return &s }(),
	}
	
	pass, cross, err := updateInfo(session, mp, []*any{})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !pass {
		t.Error("Expected pass to be true")
	}
	if cross == nil {
		t.Error("Expected cross to not be nil")
	}

	// 验证临时数据已设置
	if _, ok := session.TemporyDataOfRequest["tools:scope"]; !ok {
		t.Error("Expected temporary data to be set")
	}

	// 测试第二次调用（应该不再输出）
	pass, cross, err = updateInfo(session, mp, []*any{})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !pass {
		t.Error("Expected pass to be true")
	}
}

func TestUpdateInfoWithDisable(t *testing.T) {
	session := &structs.Chats{
		TemporyDataOfRequest: make(map[string]any),
	}

	mp := map[string]*any{
		"name":    func() *any { s := any("test_scope"); return &s }(),
		"disable": func() *any { b := any(true); return &b }(),
	}
	
	pass, _, err := updateInfo(session, mp, []*any{})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if !pass {
		t.Error("Expected pass to be true")
	}

	// 验证临时数据
	tmp, ok := session.TemporyDataOfRequest["tools:scope"]
	if !ok {
		t.Fatal("Expected temporary data to be set")
	}
	
	tmpObj, ok := tmp.(toolCallFlagTempory)
	if !ok {
		t.Fatal("Expected toolCallFlagTempory type")
	}
	
	if !tmpObj.NameOutputed {
		t.Error("Expected NameOutputed to be true")
	}
	if !tmpObj.FlagOutputed {
		t.Error("Expected FlagOutputed to be true")
	}
}

func TestUseScope(t *testing.T) {
	db := setupTestDB(t)
	
	// 初始化全局状态
	toolobj.Scopes = map[string]string{
		"test_scope": "test prompt",
	}
	
	session := &structs.Chats{
		ID:           1,
		DB:           db,
		EnableScopes: make(map[string]bool),
	}

	// 测试启用scope
	mp := map[string]*any{
		"name": func() *any { s := any("test_scope"); return &s }(),
	}
	
	pass, cross, result, err := useScope(session, mp, []*any{})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if pass {
		t.Error("Expected pass to be false")
	}
	if cross == nil {
		t.Error("Expected cross to not be nil")
	}
	
	// 验证返回结果
	if successPtr, ok := result["success"]; !ok || successPtr == nil {
		t.Fatal("Expected success in result")
	} else if success, ok := (*successPtr).(bool); !ok || !success {
		t.Error("Expected success to be true")
	}
	
	// 验证scope已启用
	if !session.EnableScopes["test_scope"] {
		t.Error("Expected scope to be enabled")
	}
}

func TestUseScopeDisable(t *testing.T) {
	db := setupTestDB(t)
	
	// 初始化全局状态
	toolobj.Scopes = map[string]string{
		"test_scope": "test prompt",
	}
	
	session := &structs.Chats{
		ID:           1,
		DB:           db,
		EnableScopes: map[string]bool{"test_scope": true},
	}

	// 测试禁用scope
	mp := map[string]*any{
		"name":    func() *any { s := any("test_scope"); return &s }(),
		"disable": func() *any { b := any(true); return &b }(),
	}
	
	pass, _, result, err := useScope(session, mp, []*any{})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if pass {
		t.Error("Expected pass to be false")
	}
	
	// 验证返回结果
	if successPtr, ok := result["success"]; !ok || successPtr == nil {
		t.Fatal("Expected success in result")
	} else if success, ok := (*successPtr).(bool); !ok || !success {
		t.Error("Expected success to be true")
	}
	
	// 验证scope已禁用
	if session.EnableScopes["test_scope"] {
		t.Error("Expected scope to be disabled")
	}
}

func TestUseScopeMissingName(t *testing.T) {
	session := &structs.Chats{
		EnableScopes: make(map[string]bool),
	}

	mp := map[string]*any{}
	
	pass, _, result, err := useScope(session, mp, []*any{})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if pass {
		t.Error("Expected pass to be false")
	}
	
	// 验证返回错误
	if successPtr, ok := result["success"]; !ok || successPtr == nil {
		t.Fatal("Expected success in result")
	} else if success, ok := (*successPtr).(bool); !ok || success {
		t.Error("Expected success to be false")
	}
	
	if errorPtr, ok := result["error"]; !ok || errorPtr == nil {
		t.Fatal("Expected error in result")
	}
}

func TestUseScopeEmptyName(t *testing.T) {
	session := &structs.Chats{
		EnableScopes: make(map[string]bool),
	}

	mp := map[string]*any{
		"name": func() *any { s := any(""); return &s }(),
	}
	
	pass, _, result, err := useScope(session, mp, []*any{})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if pass {
		t.Error("Expected pass to be false")
	}
	
	// 验证返回错误
	if successPtr, ok := result["success"]; !ok || successPtr == nil {
		t.Fatal("Expected success in result")
	} else if success, ok := (*successPtr).(bool); !ok || success {
		t.Error("Expected success to be false")
	}
}

func TestUseScopeNotFound(t *testing.T) {
	db := setupTestDB(t)
	
	// 清空全局状态
	toolobj.Scopes = make(map[string]string)
	
	session := &structs.Chats{
		ID:           1,
		DB:           db,
		EnableScopes: make(map[string]bool),
	}

	mp := map[string]*any{
		"name": func() *any { s := any("non_existent_scope"); return &s }(),
	}
	
	pass, _, result, err := useScope(session, mp, []*any{})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if pass {
		t.Error("Expected pass to be false")
	}
	
	// 验证返回错误
	if successPtr, ok := result["success"]; !ok || successPtr == nil {
		t.Fatal("Expected success in result")
	} else if success, ok := (*successPtr).(bool); !ok || success {
		t.Error("Expected success to be false")
	}
	
	if errorPtr, ok := result["error"]; !ok || errorPtr == nil {
		t.Fatal("Expected error in result")
	}
}

func TestLoad(t *testing.T) {
	// 清空全局状态
	toolobj.ToolsList = make(map[string]*toolobj.Tools)
	
	result := load()
	
	if result != toolName {
		t.Errorf("Expected %s, got %s", toolName, result)
	}
	
	// 验证工具已添加
	if tool, ok := toolobj.ToolsList[toolName]; !ok {
		t.Error("Tool not added")
	} else {
		if tool.Name != toolName {
			t.Errorf("Expected tool name %s, got %s", toolName, tool.Name)
		}
		if len(tool.Hooks) != 1 {
			t.Errorf("Expected 1 hook, got %d", len(tool.Hooks))
		}
	}
}

func TestUseScopeWithInvalidDisableType(t *testing.T) {
	db := setupTestDB(t)
	
	toolobj.Scopes = map[string]string{
		"test_scope": "test prompt",
	}
	
	session := &structs.Chats{
		ID:           1,
		DB:           db,
		EnableScopes: make(map[string]bool),
	}

	// disable参数类型错误
	mp := map[string]*any{
		"name":    func() *any { s := any("test_scope"); return &s }(),
		"disable": func() *any { i := any(123); return &i }(),
	}
	
	pass, _, result, err := useScope(session, mp, []*any{})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if pass {
		t.Error("Expected pass to be false")
	}
	
	// 应该默认为启用（disable=false）
	if successPtr, ok := result["success"]; !ok || successPtr == nil {
		t.Fatal("Expected success in result")
	} else if success, ok := (*successPtr).(bool); !ok || !success {
		t.Error("Expected success to be true")
	}
	
	if !session.EnableScopes["test_scope"] {
		t.Error("Expected scope to be enabled (default behavior)")
	}
}
