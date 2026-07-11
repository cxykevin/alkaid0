package funcs

import (
	"os"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/storage"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/toolobj"
	u "github.com/cxykevin/alkaid0/utils"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *gorm.DB {
	os.Setenv("ALKAID_DEBUG_SQLITEFILE", ":memory:")
	db, err := storage.InitStorage("", "")
	if err != nil {
		t.Fatalf("Failed to init storage: %v", err)
	}
	return db
}

func TestGetChats(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	chats, err := GetChats(db)
	if err != nil {
		t.Fatalf("GetChats failed: %v", err)
	}
	oldchats := len(chats)

	// Create some chats
	chat1 := &structs.Chats{Title: "Chat 1"}
	chat2 := &structs.Chats{Title: "Chat 2"}
	db.Create(chat1)
	db.Create(chat2)

	chats, err = GetChats(db)
	if err != nil {
		t.Fatalf("GetChats failed: %v", err)
	}
	if len(chats)-oldchats != 2 {
		t.Errorf("Expected 2 chats, got %d", len(chats)-oldchats)
	}
}

func TestQueryChat(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	chat := &structs.Chats{Title: "Test Chat"}
	db.Create(chat)

	found, err := QueryChat(db, chat.ID)
	if err != nil {
		t.Fatalf("QueryChat failed: %v", err)
	}
	if found.Title != "Test Chat" {
		t.Errorf("Expected title 'Test Chat', got '%s'", found.Title)
	}
}

func TestCreateChat(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	id, err := CreateChat(db)
	if err != nil {
		t.Fatalf("CreateChat failed: %v", err)
	}
	if id == 0 {
		t.Error("Expected non-zero ID")
	}
}

func TestDeleteChat(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	chat := &structs.Chats{Title: "To Delete"}
	db.Create(chat)

	err := DeleteChat(db, chat)
	if err != nil {
		t.Fatalf("DeleteChat failed: %v", err)
	}

	// Verify deleted
	_, err = QueryChat(db, chat.ID)
	if err == nil {
		t.Error("Expected error after delete")
	}
}

// --- 纯函数测试：读取全局配置 ---

// configSetup 保存并恢复全局配置
func configSetup() func() {
	oldCfg := *config.GlobalConfig
	return func() { *config.GlobalConfig = oldCfg }
}

func TestGetModelName(t *testing.T) {
	defer configSetup()()

	*config.GlobalConfig = cfgStructs.Config{
		Model: cfgStructs.ModelsConfig{
			DefaultModelID: 1,
			Models: map[int32]cfgStructs.ModelConfig{
				1: {ModelName: "gpt-4", ModelID: "gpt-4"},
				2: {ModelName: "claude-3", ModelID: "claude-3"},
			},
		},
	}

	tests := []struct {
		name        string
		modelID     uint32
		defaultName string
		want        string
	}{
		{name: "modelID=0 返回默认", modelID: 0, defaultName: "default", want: "default"},
		{name: "存在的模型", modelID: 1, defaultName: "default", want: "gpt-4"},
		{name: "不存在的模型返回默认", modelID: 999, defaultName: "fallback", want: "fallback"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetModelName(tt.modelID, tt.defaultName)
			if got != tt.want {
				t.Errorf("GetModelName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetModels(t *testing.T) {
	defer configSetup()()

	*config.GlobalConfig = cfgStructs.Config{
		Model: cfgStructs.ModelsConfig{
			Models: map[int32]cfgStructs.ModelConfig{
				2: {ModelName: "model-2"},
				1: {ModelName: "model-1"},
				3: {ModelName: "model-3"},
			},
		},
	}

	models := GetModels()
	if len(models) != 3 {
		t.Fatalf("Expected 3 models, got %d", len(models))
	}
	// 验证按 ID 排序
	if models[0].ID != 1 || models[1].ID != 2 || models[2].ID != 3 {
		t.Errorf("Models not sorted by ID: got %+v", models)
	}
	if models[0].Config.ModelName != "model-1" {
		t.Errorf("First model name = %q, want %q", models[0].Config.ModelName, "model-1")
	}
}

func TestGetModels_Empty(t *testing.T) {
	defer configSetup()()

	*config.GlobalConfig = cfgStructs.Config{
		Model: cfgStructs.ModelsConfig{
			Models: map[int32]cfgStructs.ModelConfig{},
		},
	}

	models := GetModels()
	if len(models) != 0 {
		t.Errorf("Expected empty models, got %d", len(models))
	}
}

func TestGetModelInfo(t *testing.T) {
	defer configSetup()()

	*config.GlobalConfig = cfgStructs.Config{
		Model: cfgStructs.ModelsConfig{
			Models: map[int32]cfgStructs.ModelConfig{
				1: {ModelName: "gpt-4", ModelTemperature: 0.7},
			},
		},
	}

	// 存在的模型
	info, err := GetModelInfo(1)
	if err != nil {
		t.Fatalf("GetModelInfo(1) failed: %v", err)
	}
	if info.ModelName != "gpt-4" {
		t.Errorf("ModelName = %q, want %q", info.ModelName, "gpt-4")
	}

	// 不存在的模型
	_, err = GetModelInfo(999)
	if err == nil {
		t.Error("GetModelInfo(999) should error")
	}
}

func TestGetAgentTags(t *testing.T) {
	defer configSetup()()

	*config.GlobalConfig = cfgStructs.Config{
		Agent: cfgStructs.AgentsConfig{
			IgnoreBuiltinAgents: true,
			Agents: map[string]cfgStructs.AgentConfig{
				"reviewer": {AgentName: "Reviewer"},
				"coder":    {AgentName: "Coder"},
			},
		},
	}

	tags := GetAgentTags()
	if len(tags) != 2 {
		t.Fatalf("Expected 2 agent tags, got %d", len(tags))
	}
}

func TestGetAgentTags_Empty(t *testing.T) {
	defer configSetup()()

	*config.GlobalConfig = cfgStructs.Config{
		Agent: cfgStructs.AgentsConfig{
			IgnoreBuiltinAgents: true,
			Agents:              map[string]cfgStructs.AgentConfig{},
		},
	}

	tags := GetAgentTags()
	if len(tags) != 0 {
		t.Errorf("Expected empty tags, got %d", len(tags))
	}
}

func TestGetScopes(t *testing.T) {
	oldScopes := toolobj.Scopes
	toolobj.Scopes = map[string]string{
		"global": "global scope",
		"local":  "local scope",
	}
	defer func() { toolobj.Scopes = oldScopes }()

	scopes := GetScopes()
	if len(scopes) != 2 {
		t.Fatalf("Expected 2 scopes, got %d", len(scopes))
	}
}

func TestGetScopes_Empty(t *testing.T) {
	oldScopes := toolobj.Scopes
	toolobj.Scopes = map[string]string{}
	defer func() { toolobj.Scopes = oldScopes }()

	scopes := GetScopes()
	if len(scopes) != 0 {
		t.Errorf("Expected empty scopes, got %d", len(scopes))
	}
}
