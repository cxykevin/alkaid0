package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/cxykevin/alkaid0/config/structs"
)

// applyLoadPipeline 复现 Load() 对已解析配置的补齐流程（默认基底 + raw 叠加 + 三个 Ensure）。
func applyLoadPipeline(t *testing.T, raw string) *structs.Config {
	t.Helper()
	cfg := structs.BuildDefault(&structs.Config{})
	if err := json.Unmarshal([]byte(raw), cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	EnsureOnlineSearchDefaults(cfg, json.RawMessage(raw))
	EnsureCacheDefaults(cfg, json.RawMessage(raw))
	EnsureElementDefaults(cfg, json.RawMessage(raw))
	return cfg
}

// TestEnsureElementDefaultsModelMap Models 元素里缺失的字段应回落到 default tag 默认值。
func TestEnsureElementDefaultsModelMap(t *testing.T) {
	cfg := applyLoadPipeline(t, `{"Model":{"Models":{"0":{"ModelName":"m","ModelID":"id"}}}}`)
	m, ok := cfg.Model.Models[0]
	if !ok {
		t.Fatal("Models[0] missing")
	}
	if m.ModelName != "m" || m.ModelID != "id" {
		t.Errorf("explicit fields should be kept, got %q/%q", m.ModelName, m.ModelID)
	}
	if m.TokenLimit != 8192 {
		t.Errorf("TokenLimit = %d, want 8192", m.TokenLimit)
	}
	if m.MaxCompletionTokens != 16384 {
		t.Errorf("MaxCompletionTokens = %d, want 16384", m.MaxCompletionTokens)
	}
	if m.CompressSize != 128000 {
		t.Errorf("CompressSize = %d, want 128000", m.CompressSize)
	}
	if m.ModelTopP != -1 || m.ModelTopK != -1 || m.ModelTemperature != -1 {
		t.Errorf("TopP/TopK/Temperature = %v/%v/%v, want -1/-1/-1", m.ModelTopP, m.ModelTopK, m.ModelTemperature)
	}
	if m.ProviderURL == "" || m.ProviderKey == "" {
		t.Errorf("ProviderURL/ProviderKey should fall back to defaults, got %q/%q", m.ProviderURL, m.ProviderKey)
	}
	if !m.ProviderSpecificConfig.EnableReasoningEffort || !m.ProviderSpecificConfig.EnableUsage {
		t.Errorf("ProviderSpecificConfig defaults lost: %+v", m.ProviderSpecificConfig)
	}
}

// TestEnsureElementDefaultsKeepsExplicitValues 用户显式写出的值（含 0 / false / 非默认值）必须保留。
func TestEnsureElementDefaultsKeepsExplicitValues(t *testing.T) {
	cfg := applyLoadPipeline(t, `{"Model":{"Models":{"0":{
		"ModelName":"m","TokenLimit":0,"ModelTopP":0.5,"EnableThinking":true,
		"ProviderSpecificConfig":{"EnableUsage":false}}}}}`)
	m := cfg.Model.Models[0]
	if m.TokenLimit != 0 {
		t.Errorf("explicit TokenLimit 0 should be kept, got %d", m.TokenLimit)
	}
	if m.ModelTopP != 0.5 {
		t.Errorf("explicit ModelTopP 0.5 should be kept, got %v", m.ModelTopP)
	}
	if !m.EnableThinking {
		t.Error("explicit EnableThinking true should be kept")
	}
	if m.ProviderSpecificConfig.EnableUsage {
		t.Error("explicit EnableUsage false should be kept")
	}
	// 未写出的字段仍应补默认值
	if m.MaxCompletionTokens != 16384 {
		t.Errorf("MaxCompletionTokens = %d, want 16384", m.MaxCompletionTokens)
	}
}

// TestEnsureElementDefaultsAgentMap Agents 元素（含嵌套 Color 结构体）同样要补默认值。
func TestEnsureElementDefaultsAgentMap(t *testing.T) {
	cfg := applyLoadPipeline(t, `{"Agent":{"Agents":{"sub":{"AgentName":"n"}}}}`)
	a, ok := cfg.Agent.Agents["sub"]
	if !ok {
		t.Fatal("Agents[sub] missing")
	}
	if a.AgentName != "n" {
		t.Errorf("explicit AgentName should be kept, got %q", a.AgentName)
	}
	if a.AgentShortDescription != "A default subagent" {
		t.Errorf("AgentShortDescription = %q, want default", a.AgentShortDescription)
	}
	if a.AgentDescription != "Default Agent" || a.AgentPrompt != "You are a helpful assistant" {
		t.Errorf("AgentDescription/AgentPrompt should fall back to defaults, got %q/%q", a.AgentDescription, a.AgentPrompt)
	}
	if a.Color.Red != 128 || a.Color.Green != 128 || a.Color.Blue != 128 {
		t.Errorf("Color defaults lost: %+v", a.Color)
	}
}

// TestEnsureElementDefaultsNestedMap map[string]string 与 map[string]map[string]string 元素内容必须保留。
func TestEnsureElementDefaultsNestedMap(t *testing.T) {
	cfg := applyLoadPipeline(t, `{"Agent":{"TerminalEnvs":{"LANG":"zh_CN.UTF-8"},"Fetch":{"RewriteHeaders":{"https://x/.*":{"Authorization":"Bearer k"}}}}}`)
	if got := cfg.Agent.TerminalEnvs["LANG"]; got != "zh_CN.UTF-8" {
		t.Errorf("TerminalEnvs[LANG] = %q, want zh_CN.UTF-8", got)
	}
	if got := cfg.Agent.Fetch.RewriteHeaders["https://x/.*"]["Authorization"]; got != "Bearer k" {
		t.Errorf("RewriteHeaders lost: %q", got)
	}
}

// TestEnsureElementDefaultsNilRaw 没有既有配置 JSON 时不应改动配置。
func TestEnsureElementDefaultsNilRaw(t *testing.T) {
	cfg := structs.BuildDefault(&structs.Config{})
	before, _ := json.Marshal(cfg)
	EnsureElementDefaults(cfg, nil)
	after, _ := json.Marshal(cfg)
	if string(before) != string(after) {
		t.Error("EnsureElementDefaults with nil raw should not change config")
	}
}

// TestLoadFillsDefaultsFromPartialFile 端到端：配置文件只写一部分字段时，
// struct 字段与 map 元素都应回落到默认值。
func TestLoadFillsDefaultsFromPartialFile(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "partial_config.json")
	if err := os.WriteFile(path, []byte(`{"Version":5,"Server":{"key":"k"},"Model":{"Models":{"0":{"ModelName":"m","ModelID":"id"}}}}`), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	oldEnv := os.Getenv(envConfigName)
	defer os.Setenv(envConfigName, oldEnv)
	os.Setenv(envConfigName, path)
	oldPath := configPath
	defer func() { configPath = oldPath }()
	configPath = ""

	Load()

	if GlobalConfig.Version != 5 {
		t.Errorf("explicit Version should be kept, got %d", GlobalConfig.Version)
	}
	if GlobalConfig.Server.Key != "k" {
		t.Errorf("explicit Server.Key should be kept, got %q", GlobalConfig.Server.Key)
	}
	// struct 字段缺失 → 回落 default tag
	if GlobalConfig.Server.Port != 7433 {
		t.Errorf("Server.Port = %d, want 7433", GlobalConfig.Server.Port)
	}
	if GlobalConfig.Server.Path != "/acp" {
		t.Errorf("Server.Path = %q, want /acp", GlobalConfig.Server.Path)
	}
	if GlobalConfig.Agent.MaxCallCount != 50 {
		t.Errorf("Agent.MaxCallCount = %d, want 50", GlobalConfig.Agent.MaxCallCount)
	}
	// map 元素缺失 → 回落 default tag
	if GlobalConfig.Model.Models[0].TokenLimit != 8192 {
		t.Errorf("Models[0].TokenLimit = %d, want 8192", GlobalConfig.Model.Models[0].TokenLimit)
	}
	if GlobalConfig.Model.Models[0].ModelName != "m" {
		t.Errorf("Models[0].ModelName = %q, want m", GlobalConfig.Model.Models[0].ModelName)
	}
}

// sliceElemFixture / sliceHolderFixture 用于直接验证 slice 元素重建：
// 结构体元素带 default tag，模拟"配置里写成 []struct 且元素字段只写一半"的场景。
type sliceElemFixture struct {
	Name string `default:"fallback"`
	Num  int    `default:"7"`
	Flag bool   `default:"true"`
}

type sliceHolderFixture struct {
	Items []sliceElemFixture
}

// TestRebuildSliceElementsAppliesElementDefault slice 与 map 同理：encoding/json 对
// slice 是整体替换元素，元素内未写出的字段会降为 Go 零值，需要单独补 default tag 默认值；
// 而元素里显式写出的值（含 0 / false）必须保留。
func TestRebuildSliceElementsAppliesElementDefault(t *testing.T) {
	raw := `{"Items":[{"Name":"a"},{"Num":3,"Flag":false},{}]}`
	var holder sliceHolderFixture
	if err := json.Unmarshal([]byte(raw), &holder); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	// 前置条件：未经补齐时缺失字段是 Go 零值（default tag 不参与）
	if holder.Items[0].Num != 0 || holder.Items[1].Name != "" || holder.Items[2].Flag {
		t.Fatalf("precondition failed (json must not apply tag defaults): %+v", holder.Items)
	}

	var tree map[string]any
	if err := json.Unmarshal([]byte(raw), &tree); err != nil {
		t.Fatalf("unmarshal raw tree: %v", err)
	}
	rebuildSliceElements(reflect.ValueOf(&holder).Elem().Field(0), tree["Items"])

	want := []sliceElemFixture{
		{Name: "a", Num: 7, Flag: true},
		{Name: "fallback", Num: 3, Flag: false},
		{Name: "fallback", Num: 7, Flag: true},
	}
	if !reflect.DeepEqual(holder.Items, want) {
		t.Errorf("slice elements = %+v, want %+v", holder.Items, want)
	}
}

// TestEnsureElementDefaultsKeepsSliceFields 端到端：Phrase.Phrases（[]struct）与
// LSP.LanguageServers 元素里的 Args（[]string）都必须原样保留，重建元素不得吞掉 slice。
func TestEnsureElementDefaultsKeepsSliceFields(t *testing.T) {
	cfg := applyLoadPipeline(t, `{"Context":{
		"LSP":{"Enabled":true,"LanguageServers":{".go":{"Command":"gopls","Args":["-v","-rpc.trace"]}}},
		"Phrase":{"Enable":true,"Phrases":[{"Short":"s1","Text":"t1","Desc":"d1"},{"Short":"s2","Text":"t2"}]}}}`)

	lsp := cfg.Context.LSP
	if !lsp.Enabled {
		t.Error("explicit LSP.Enabled true should be kept")
	}
	if lsp.IdleTimeout != 600 {
		t.Errorf("LSP.IdleTimeout = %d, want 600 (default tag)", lsp.IdleTimeout)
	}
	srv, ok := lsp.LanguageServers[".go"]
	if !ok {
		t.Fatal(`LanguageServers[".go"] missing`)
	}
	if srv.Command != "gopls" {
		t.Errorf("LanguageServers[.go].Command = %q, want gopls", srv.Command)
	}
	if len(srv.Args) != 2 || srv.Args[0] != "-v" || srv.Args[1] != "-rpc.trace" {
		t.Errorf("LanguageServers[.go].Args = %v, want [-v -rpc.trace]", srv.Args)
	}

	if !cfg.Context.Phrase.Enable {
		t.Error("explicit Phrase.Enable true should be kept")
	}
	if len(cfg.Context.Phrase.Phrases) != 2 {
		t.Fatalf("Phrases has %d elements, want 2: %+v", len(cfg.Context.Phrase.Phrases), cfg.Context.Phrase.Phrases)
	}
	if p := cfg.Context.Phrase.Phrases[0]; p.Short != "s1" || p.Text != "t1" || p.Desc != "d1" {
		t.Errorf("Phrases[0] = %+v, want s1/t1/d1", p)
	}
	if p := cfg.Context.Phrase.Phrases[1]; p.Short != "s2" || p.Text != "t2" || p.Desc != "" {
		t.Errorf("Phrases[1] = %+v, want s2/t2/empty", p)
	}
}

// TestReloadRefillsElementDefaults 热重载（Reload → Load）与首次加载走同一条补齐流程：
// 改完配置文件后，显式写出的值要生效，未写出的字段仍要有 default tag 默认值。
func TestReloadRefillsElementDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "reload_config.json")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatalf("write config: %v", err)
		}
	}

	oldEnv := os.Getenv(envConfigName)
	defer os.Setenv(envConfigName, oldEnv)
	os.Setenv(envConfigName, path)
	oldPath := configPath
	defer func() { configPath = oldPath }()
	configPath = ""

	write(`{"Version":1,"Server":{"key":"k"},"Model":{"Models":{"0":{"ModelName":"first","ModelID":"id1"}}}}`)
	Load()

	if got := GlobalConfig.Model.Models[0].TokenLimit; got != 8192 {
		t.Fatalf("after Load: Models[0].TokenLimit = %d, want 8192", got)
	}

	// 改动配置文件后热重载：新值生效、新增元素也要补齐、slice 字段照常解析
	write(`{"Version":2,"Server":{"key":"k"},
		"Model":{"Models":{"0":{"ModelName":"second","ModelID":"id2"},"1":{"ModelName":"extra","ModelID":"id3"}}},
		"Context":{"Phrase":{"Enable":true,"Phrases":[{"Short":"s","Text":"t"}]}}}`)
	Reload()

	if GlobalConfig.Version != 2 {
		t.Errorf("after Reload: Version = %d, want 2", GlobalConfig.Version)
	}
	m0 := GlobalConfig.Model.Models[0]
	if m0.ModelName != "second" || m0.ModelID != "id2" {
		t.Errorf("after Reload: explicit fields not applied: %+v", m0)
	}
	if m0.TokenLimit != 8192 || m0.MaxCompletionTokens != 16384 {
		t.Errorf("after Reload: Models[0] defaults lost: %+v", m0)
	}
	m1, ok := GlobalConfig.Model.Models[1]
	if !ok {
		t.Fatal("after Reload: Models[1] missing")
	}
	if m1.ModelName != "extra" || m1.TokenLimit != 8192 {
		t.Errorf("after Reload: Models[1] = %+v, want explicit name + default TokenLimit", m1)
	}
	if !GlobalConfig.Context.Phrase.Enable || len(GlobalConfig.Context.Phrase.Phrases) != 1 {
		t.Errorf("after Reload: Phrase config lost: %+v", GlobalConfig.Context.Phrase)
	}
	if p := GlobalConfig.Context.Phrase.Phrases[0]; p.Short != "s" || p.Text != "t" {
		t.Errorf("after Reload: Phrases[0] = %+v, want s/t", p)
	}
}
