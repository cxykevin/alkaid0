package actions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/server/client/jsonrpc"
	u "github.com/cxykevin/alkaid0/utils"
)

// ---- helpers ----

// configSetup 为测试创建临时配置环境。
// 设置 ALKAID0_CONFIG_PATH 到临时路径，保存 GlobalConfig 快照用于恢复。
func configSetup(t *testing.T) (restore func()) {
	t.Helper()

	// 保存内存中的配置快照
	origConfig := *config.GlobalConfig

	// 创建临时配置文件路径
	tmpDir := t.TempDir()
	tmpCfgPath := filepath.Join(tmpDir, "config.json")
	t.Setenv("ALKAID0_CONFIG_PATH", tmpCfgPath)

	// 重置 configPath 缓存指向临时路径（Load 会从 env 重新解析），
	// 避免 config.Save() 误用其它测试缓存下来的旧路径。文件不存在时
	// Load 内部会创建默认配置并保存，后续 config.Save() 即可正常写入。
	config.Load()

	return func() {
		config.GlobalConfigSwap(origConfig)
	}
}

// ---- ConfigGet 测试 ----

// TestConfigGetReturnsNonNil config/get 应返回非 nil 的配置
func TestConfigGetReturnsNonNil(t *testing.T) {
	resp, err := ConfigGet(ConfigGetRequest{}, nil, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Config == nil {
		t.Fatal("ConfigGetResponse.Config should not be nil")
	}
}

// TestConfigGetReturnsSameGlobalConfig config/get 应返回与 GlobalConfig 相同指针
func TestConfigGetReturnsSameGlobalConfig(t *testing.T) {
	resp, err := ConfigGet(ConfigGetRequest{}, nil, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Config != config.GlobalConfig {
		t.Error("ConfigGet should return the same GlobalConfig pointer")
	}
}

// TestConfigGetJSONSerializable config/get 的响应应能正常 JSON 序列化/反序列化
func TestConfigGetJSONSerializable(t *testing.T) {
	resp, err := ConfigGet(ConfigGetRequest{}, nil, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("ConfigGetResponse should be JSON serializable: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("serialized config should not be empty")
	}

	var decoded ConfigGetResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("ConfigGetResponse should be JSON deserializable: %v", err)
	}
	if decoded.Config == nil {
		t.Fatal("decoded Config should not be nil")
	}
}

// ---- ConfigSet 测试 ----

// TestConfigSetNilConfig config/set 传入 nil config 应报错
func TestConfigSetNilConfig(t *testing.T) {
	_, err := ConfigSet(ConfigSetRequest{Config: nil}, nil, 0)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
}

// TestConfigSetInvalidJSON config/set 传入非法 JSON 应报错
func TestConfigSetInvalidJSON(t *testing.T) {
	_, err := ConfigSet(ConfigSetRequest{
		Config: json.RawMessage(`{invalid}`),
	}, nil, 0)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// TestConfigSetPartialUpdate 部分更新只影响指定字段，未指定字段保持不变
func TestConfigSetPartialUpdate(t *testing.T) {
	defer configSetup(t)()

	origHost := config.GlobalConfig.Server.Host

	newPort := uint16(19999)
	reqData, _ := json.Marshal(map[string]any{
		"Server": map[string]any{
			"port": newPort,
		},
	})

	_, err := ConfigSet(ConfigSetRequest{Config: reqData}, nil, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if config.GlobalConfig.Server.Port != newPort {
		t.Errorf("Server.Port = %d, want %d", config.GlobalConfig.Server.Port, newPort)
	}
	if config.GlobalConfig.Server.Host != origHost {
		t.Errorf("Server.Host should remain unchanged: got %q, want %q",
			config.GlobalConfig.Server.Host, origHost)
	}

	// 验证文件已持久化
	tmpCfgPath := os.Getenv("ALKAID0_CONFIG_PATH")
	savedData, err := os.ReadFile(tmpCfgPath)
	if err != nil {
		t.Fatalf("failed to read saved config: %v", err)
	}
	var savedCfg struct {
		Server struct {
			Port uint16 `json:"port"`
		} `json:"Server"`
	}
	if err := json.Unmarshal(savedData, &savedCfg); err != nil {
		t.Fatalf("failed to unmarshal saved config: %v", err)
	}
	if savedCfg.Server.Port != newPort {
		t.Errorf("saved config Server.Port = %d, want %d", savedCfg.Server.Port, newPort)
	}
}

// TestConfigSetFullUpdate 完整配置替换
func TestConfigSetFullUpdate(t *testing.T) {
	defer configSetup(t)()

	newHost := "0.0.0.0"
	newPort := uint16(19998)
	reqData, _ := json.Marshal(map[string]any{
		"Server": map[string]any{
			"host": newHost,
			"port": newPort,
		},
	})

	_, err := ConfigSet(ConfigSetRequest{Config: reqData}, nil, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if config.GlobalConfig.Server.Host != newHost {
		t.Errorf("Server.Host = %q, want %q", config.GlobalConfig.Server.Host, newHost)
	}
	if config.GlobalConfig.Server.Port != newPort {
		t.Errorf("Server.Port = %d, want %d", config.GlobalConfig.Server.Port, newPort)
	}
}

// TestConfigSetPreservesUnspecifiedField 未指定的嵌套字段应保持不变
func TestConfigSetPreservesUnspecifiedField(t *testing.T) {
	defer configSetup(t)()

	origPath := config.GlobalConfig.Server.Path

	reqData, _ := json.Marshal(map[string]any{
		"Server": map[string]any{
			"host": "config-set-test-host",
		},
	})

	_, err := ConfigSet(ConfigSetRequest{Config: reqData}, nil, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if config.GlobalConfig.Server.Path != origPath {
		t.Errorf("Server.Path should remain unchanged: got %q, want %q",
			config.GlobalConfig.Server.Path, origPath)
	}
}

// TestConfigSetEmptyJSONObject 空对象不应报错也不应修改任何字段
func TestConfigSetEmptyJSONObject(t *testing.T) {
	defer configSetup(t)()

	origHost := config.GlobalConfig.Server.Host

	reqData, _ := json.Marshal(map[string]any{})

	_, err := ConfigSet(ConfigSetRequest{Config: reqData}, nil, 0)
	if err != nil {
		t.Fatalf("unexpected error for empty object: %v", err)
	}

	if config.GlobalConfig.Server.Host != origHost {
		t.Error("empty JSON object should not modify any field")
	}
}

// TestConfigGetAfterSet config/set 后 config/get 应返回最新值
func TestConfigGetAfterSet(t *testing.T) {
	defer configSetup(t)()

	newPort := uint16(19997)
	reqData, _ := json.Marshal(map[string]any{
		"Server": map[string]any{
			"port": newPort,
		},
	})

	_, err := ConfigSet(ConfigSetRequest{Config: reqData}, nil, 0)
	if err != nil {
		t.Fatalf("ConfigSet failed: %v", err)
	}

	resp, err := ConfigGet(ConfigGetRequest{}, nil, 0)
	if err != nil {
		t.Fatalf("ConfigGet failed: %v", err)
	}
	if resp.Config.Server.Port != newPort {
		t.Errorf("after ConfigSet, Server.Port = %d, want %d",
			resp.Config.Server.Port, newPort)
	}
}

// TestConfigSetInvalidModelIDPrefix config/set 处理深层嵌套字段
func TestConfigSetUpdateModelConfig(t *testing.T) {
	defer configSetup(t)()

	// 更新 Model 中的 DefaultModelID
	newDefaultID := int32(42)
	reqData, _ := json.Marshal(map[string]any{
		"Model": map[string]any{
			"defaultModelID": newDefaultID,
		},
	})

	_, err := ConfigSet(ConfigSetRequest{Config: reqData}, nil, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if config.GlobalConfig.Model.DefaultModelID != newDefaultID {
		t.Errorf("Model.DefaultModelID = %d, want %d",
			config.GlobalConfig.Model.DefaultModelID, newDefaultID)
	}
}

// TestConfigSetDeleteModelItem config/set 传 {"Models":{"1":null}} 应真正删除模型键，
// 而不是像纯 json.Unmarshal 那样只把值置零（修复删除不同步到 server 的问题）。
func TestConfigSetDeleteModelItem(t *testing.T) {
	defer configSetup(t)()

	// 预置两个模型。
	config.GlobalConfig.Model.Models = map[int32]cfgStructs.ModelConfig{
		1: {ModelName: "one", ModelID: "m1"},
		2: {ModelName: "two", ModelID: "m2"},
	}
	reqData, _ := json.Marshal(map[string]any{
		"Model": map[string]any{
			"Models": map[string]any{"1": nil},
		},
	})

	_, err := ConfigSet(ConfigSetRequest{Config: reqData}, nil, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if m, ok := config.GlobalConfig.Model.Models[1]; ok {
		t.Errorf("model 1 should be deleted from GlobalConfig, got %+v", m)
	}
	if _, ok := config.GlobalConfig.Model.Models[2]; !ok {
		t.Error("model 2 should remain unchanged")
	}

	// 验证已持久化到文件（键 1 不应出现在保存的配置中）。
	tmpCfgPath := os.Getenv("ALKAID0_CONFIG_PATH")
	savedData, err := os.ReadFile(tmpCfgPath)
	if err != nil {
		t.Fatalf("failed to read saved config: %v", err)
	}
	var savedCfg struct {
		Model struct {
			Models map[string]json.RawMessage `json:"Models"`
		} `json:"Model"`
	}
	if err := json.Unmarshal(savedData, &savedCfg); err != nil {
		t.Fatalf("failed to unmarshal saved config: %v", err)
	}
	if _, ok := savedCfg.Model.Models["1"]; ok {
		t.Error("model 1 should not appear in the persisted config file")
	}
	if _, ok := savedCfg.Model.Models["2"]; !ok {
		t.Error("model 2 should appear in the persisted config file")
	}
}

// TestConfigSetModelFieldPartialUpdate 复现：对 Model.Models.*（map 值的结构体
// 字段）做局部更新时，未在 patch 中出现的兄弟字段必须保持原值。
// 历史 bug：json.Unmarshal 合并对 map 类型字段（map[int32]ModelConfig）遇到
// patch 中出现的键会用全新解码的值整体替换，丢失该键下未 patch 的字段——
// 表现为"改第二个字段后第一个字段又回去了"。
func TestConfigSetModelFieldPartialUpdate(t *testing.T) {
	defer configSetup(t)()

	config.GlobalConfig.Model.Models = map[int32]cfgStructs.ModelConfig{
		1: {ModelName: "Kimi", ModelID: "kimi-k2", TokenLimit: 8192, CompressSize: 128000},
	}

	// 第一次更新 TokenLimit。
	req1, _ := json.Marshal(map[string]any{
		"Model": map[string]any{
			"Models": map[string]any{"1": map[string]any{"TokenLimit": 99999}},
		},
	})
	if _, err := ConfigSet(ConfigSetRequest{Config: req1}, nil, 0); err != nil {
		t.Fatalf("set TokenLimit: %v", err)
	}

	// 第二次更新 CompressSize——TokenLimit 与 ModelName 必须保持第一次的结果。
	req2, _ := json.Marshal(map[string]any{
		"Model": map[string]any{
			"Models": map[string]any{"1": map[string]any{"CompressSize": 22222}},
		},
	})
	if _, err := ConfigSet(ConfigSetRequest{Config: req2}, nil, 0); err != nil {
		t.Fatalf("set CompressSize: %v", err)
	}

	m := config.GlobalConfig.Model.Models[1]
	if m.TokenLimit != 99999 {
		t.Errorf("TokenLimit = %d, want 99999 (must survive second partial update)", m.TokenLimit)
	}
	if m.CompressSize != 22222 {
		t.Errorf("CompressSize = %d, want 22222", m.CompressSize)
	}
	if m.ModelName != "Kimi" {
		t.Errorf("ModelName = %q, want Kimi", m.ModelName)
	}
}

// TestConfigSetLanguageServerPartialUpdate 验证 Context.LSP.LanguageServers
// （map[string]LanguageServerConfig）与 Agent.Agents/Model.Models 同构，局部更新
// 与 null 键删除同样生效：先改一个字段再改另一个，前值（含 Args 数组）保持；
// 传 null 真删除键。
func TestConfigSetLanguageServerPartialUpdate(t *testing.T) {
	defer configSetup(t)()

	config.GlobalConfig.Context.LSP.LanguageServers = map[string]cfgStructs.LanguageServerConfig{
		".go": {Command: "gopls", Args: []string{"-mode", "stdio"}},
		".py": {Command: "pyright"},
	}

	// 局部更新 .go 的 Command——Args 必须保持（map 值深度合并，未 patch 字段不清零）。
	req1, _ := json.Marshal(map[string]any{
		"Context": map[string]any{
			"LSP": map[string]any{
				"LanguageServers": map[string]any{".go": map[string]any{"Command": "gopls-fixed"}},
			},
		},
	})
	if _, err := ConfigSet(ConfigSetRequest{Config: req1}, nil, 0); err != nil {
		t.Fatalf("set .go Command: %v", err)
	}
	ls := config.GlobalConfig.Context.LSP.LanguageServers[".go"]
	if ls.Command != "gopls-fixed" {
		t.Errorf(".go Command = %q, want gopls-fixed", ls.Command)
	}
	if len(ls.Args) != 2 || ls.Args[0] != "-mode" || ls.Args[1] != "stdio" {
		t.Errorf(".go Args should survive partial update, got %v", ls.Args)
	}

	// 删除 .py 键（null 键真删除）。
	req2, _ := json.Marshal(map[string]any{
		"Context": map[string]any{
			"LSP": map[string]any{
				"LanguageServers": map[string]any{".py": nil},
			},
		},
	})
	if _, err := ConfigSet(ConfigSetRequest{Config: req2}, nil, 0); err != nil {
		t.Fatalf("delete .py: %v", err)
	}
	if _, ok := config.GlobalConfig.Context.LSP.LanguageServers[".py"]; ok {
		t.Error(".py should be deleted")
	}
	if _, ok := config.GlobalConfig.Context.LSP.LanguageServers[".go"]; !ok {
		t.Error(".go should remain")
	}
}

// TestConfigSetPhrasesArray config/set 整体替换 Context.Phrase.Phrases 数组
// （客户端编辑器对数组字段整体替换写回，元素编辑/增删均发送完整数组）。
func TestConfigSetPhrasesArray(t *testing.T) {
	defer configSetup(t)()

	reqData, _ := json.Marshal(map[string]any{
		"Context": map[string]any{
			"Phrase": map[string]any{
				"Phrases": []any{
					map[string]any{"Short": "intro", "Text": "你好，请介绍一下你自己。", "Desc": "开场介绍"},
					map[string]any{"Short": "plan", "Text": "请制定详细方案。", "Desc": "制定方案"},
				},
			},
		},
	})
	if _, err := ConfigSet(ConfigSetRequest{Config: reqData}, nil, 0); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ps := config.GlobalConfig.Context.Phrase.Phrases
	if len(ps) != 2 {
		t.Fatalf("Phrases len = %d, want 2", len(ps))
	}
	if ps[0].Short != "intro" || ps[0].Text == "" || ps[0].Desc != "开场介绍" {
		t.Errorf("Phrases[0] = %+v, want intro with text and desc", ps[0])
	}
	if ps[1].Short != "plan" {
		t.Errorf("Phrases[1].Short = %q, want plan", ps[1].Short)
	}
}

// TestConfigSetDeleteAgentItem config/set 传 {"Agents":{"main":null}} 应真正删除子代理键。
func TestConfigSetDeleteAgentItem(t *testing.T) {
	defer configSetup(t)()

	config.GlobalConfig.Agent.Agents = map[string]cfgStructs.AgentConfig{
		"main":     {AgentName: "Main"},
		"frontend": {AgentName: "Front"},
	}
	reqData, _ := json.Marshal(map[string]any{
		"Agent": map[string]any{
			"Agents": map[string]any{"main": nil},
		},
	})

	_, err := ConfigSet(ConfigSetRequest{Config: reqData}, nil, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if a, ok := config.GlobalConfig.Agent.Agents["main"]; ok {
		t.Errorf("agent main should be deleted from GlobalConfig, got %+v", a)
	}
	if _, ok := config.GlobalConfig.Agent.Agents["frontend"]; !ok {
		t.Error("agent frontend should remain unchanged")
	}
}

// TestConfigSetNullStructField struct 字段传 null 保持 unmarshal 行为：
// encoding/json 对非 map 的 struct 字段传 null 不设置，保持原值不变。
func TestConfigSetNullStructField(t *testing.T) {
	defer configSetup(t)()

	config.GlobalConfig.Server.Host = "10.0.0.1"
	reqData, _ := json.Marshal(map[string]any{"Server": nil})

	_, err := ConfigSet(ConfigSetRequest{Config: reqData}, nil, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if config.GlobalConfig.Server.Host != "10.0.0.1" {
		t.Errorf("Server.Host should remain unchanged for struct null, got %q", config.GlobalConfig.Server.Host)
	}
}

// TestConfigSetJSONRPCResponds 回归测试：config/set 经 jsonrpc.Set 包装后
// 返回非 nil 结果，保证带 ID 的请求能收到 `{"result":{}}` 响应。
// 历史 bug：ConfigSet 返回 nil,nil，jsonrpc 框架不发送响应，前端永久等待挂起
// （框架层已兜底，见 server/client/jsonrpc/server.go 的 "if obj == nil" 分支）。
func TestConfigSetJSONRPCResponds(t *testing.T) {
	defer configSetup(t)()

	srv := jsonrpc.New()
	jsonrpc.Set(srv, "alk.cxykevin.top/config/set", ConfigSet)
	fn := srv.Methods["alk.cxykevin.top/config/set"]
	if fn == nil {
		t.Fatal("config/set 未注册到 jsonrpc 服务器")
	}

	obj, err := fn(u.H{
		"config": map[string]any{
			"Server": map[string]any{"port": 23456},
		},
	}, func(_ string, _ any, _ *string) error { return nil }, 1)
	if err != nil {
		t.Fatalf("config/set 不应报错: %v", err)
	}
	if obj == nil {
		t.Fatal("config/set 返回值不应为 nil：jsonrpc 对 nil 结果不发送响应，前端将永久等待")
	}

	// 配置应已生效
	if config.GlobalConfig.Server.Port != 23456 {
		t.Errorf("config/set 未生效: Server.Port = %d, want 23456", config.GlobalConfig.Server.Port)
	}
}

// TestConfigSetJSONRPCErrorResponse 回归测试：config/set 参数错误时，
// 包装后的方法返回错误（err 非 nil），jsonrpc 会回错误响应而非挂起。
func TestConfigSetJSONRPCErrorResponse(t *testing.T) {
	defer configSetup(t)()

	srv := jsonrpc.New()
	jsonrpc.Set(srv, "alk.cxykevin.top/config/set", ConfigSet)
	fn := srv.Methods["alk.cxykevin.top/config/set"]

	// 缺 config 字段
	_, err := fn(u.H{}, func(_ string, _ any, _ *string) error { return nil }, 1)
	if err == nil {
		t.Fatal("缺少 config 字段应返回错误")
	}
}
