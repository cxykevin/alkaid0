package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

// nullContainersRaw 覆盖配置里所有 map / slice 字段显式写成 null 的情形。
const nullContainersRaw = `{
	"Server": {"key": "k"},
	"Model": {"Models": null},
	"Agent": {
		"Agents": null,
		"TerminalEnvs": null,
		"Fetch": {"RewriteHeaders": null}
	},
	"Context": {
		"LSP": {"LanguageServers": null},
		"Phrase": {"Phrases": null}
	},
	"DataMask": {"mask_ip_whitelist": null},
	"Hooks": {"OnCronjobWakeup": null, "OnUserMessage": null, "OnTaskFinish": null}
}`

// collectNullPaths 收集 JSON 树中值为 null 的节点路径。
func collectNullPaths(prefix string, v any, out *[]string) {
	switch x := v.(type) {
	case map[string]any:
		for k, vv := range x {
			collectNullPaths(prefix+"."+k, vv, out)
		}
	case []any:
		for i, vv := range x {
			collectNullPaths(prefix+"["+strconv.Itoa(i)+"]", vv, out)
		}
	case nil:
		*out = append(*out, prefix)
	}
}

// nullPathsInJSON 返回 JSON 字节流里所有 null 值的路径。
func nullPathsInJSON(t *testing.T, data []byte) []string {
	t.Helper()
	var tree any
	if err := json.Unmarshal(data, &tree); err != nil {
		t.Fatalf("unmarshal JSON: %v", err)
	}
	var nulls []string
	collectNullPaths("", tree, &nulls)
	return nulls
}

// TestEnsureContainerDefaultsNullValues 配置里显式写出的 null 必须归一化为 {} / []。
func TestEnsureContainerDefaultsNullValues(t *testing.T) {
	cfg := applyLoadPipeline(t, nullContainersRaw)

	if cfg.Model.Models == nil {
		t.Error("Model.Models should not be nil")
	}
	if cfg.Agent.Agents == nil {
		t.Error("Agent.Agents should not be nil")
	}
	if cfg.Agent.TerminalEnvs == nil {
		t.Error("Agent.TerminalEnvs should not be nil")
	}
	if cfg.Agent.Fetch.RewriteHeaders == nil {
		t.Error("Agent.Fetch.RewriteHeaders should not be nil")
	}
	if cfg.Context.LSP.LanguageServers == nil {
		t.Error("Context.LSP.LanguageServers should not be nil")
	}
	if cfg.Context.Phrase.Phrases == nil {
		t.Error("Context.Phrase.Phrases should not be nil")
	}
	if cfg.DataMask.MaskIPWhitelist == nil {
		t.Error("DataMask.MaskIPWhitelist should not be nil")
	}
	if cfg.Hooks.OnCronjobWakeup == nil || cfg.Hooks.OnUserMessage == nil || cfg.Hooks.OnTaskFinish == nil {
		t.Errorf("Hooks slices should not be nil: %+v", cfg.Hooks)
	}

	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if nulls := nullPathsInJSON(t, b); len(nulls) != 0 {
		t.Errorf("config JSON still contains null at %v", nulls)
	}
}

// TestEnsureContainerDefaultsMapAndSliceElements map / slice 元素内部的 nil 容器
// 同样要归一化（map 元素不可寻址，需要复制后写回）。
func TestEnsureContainerDefaultsMapAndSliceElements(t *testing.T) {
	cfg := applyLoadPipeline(t, `{"Agent":{"Fetch":{"RewriteHeaders":{"https://x/.*":null}}},
		"Context":{
			"LSP":{"LanguageServers":{".go":{"Command":"gopls","Args":null}}},
			"Phrase":{"Phrases":[{"Short":"s","Text":"t"}]}}}`)

	if inner, ok := cfg.Agent.Fetch.RewriteHeaders["https://x/.*"]; !ok {
		t.Fatal("RewriteHeaders entry missing")
	} else if inner == nil {
		t.Error("RewriteHeaders element should not be nil")
	}

	srv, ok := cfg.Context.LSP.LanguageServers[".go"]
	if !ok {
		t.Fatal(`LanguageServers[".go"] missing`)
	}
	if srv.Command != "gopls" {
		t.Errorf("LanguageServers[.go].Command = %q, want gopls", srv.Command)
	}
	if srv.Args == nil {
		t.Error("LanguageServers[.go].Args should not be nil")
	}
	if len(srv.Args) != 0 {
		t.Errorf("LanguageServers[.go].Args = %v, want empty", srv.Args)
	}

	if cfg.Context.Phrase.Phrases[0].Short != "s" || cfg.Context.Phrase.Phrases[0].Text != "t" {
		t.Errorf("Phrases content lost: %+v", cfg.Context.Phrase.Phrases)
	}
}

// TestEnsureContainerDefaultsKeepsValues 已有内容必须原样保留，不能被归一化过程改写。
func TestEnsureContainerDefaultsKeepsValues(t *testing.T) {
	cfg := applyLoadPipeline(t, `{
		"Agent":{"TerminalEnvs":{"LANG":"zh_CN.UTF-8"},"Fetch":{"RewriteHeaders":{"https://x/.*":{"Authorization":"Bearer k"}}}},
		"Context":{"LSP":{"LanguageServers":{".go":{"Command":"gopls","Args":["-v"]}}},"Phrase":{"Phrases":[{"Short":"s","Text":"t"}]}},
		"DataMask":{"mask_ip_whitelist":["1.1.1.1"]},
		"Hooks":{"OnUserMessage":["echo hi"]}}`)

	if got := cfg.Agent.TerminalEnvs["LANG"]; got != "zh_CN.UTF-8" {
		t.Errorf("TerminalEnvs lost: %q", got)
	}
	if got := cfg.Agent.Fetch.RewriteHeaders["https://x/.*"]["Authorization"]; got != "Bearer k" {
		t.Errorf("RewriteHeaders lost: %q", got)
	}
	if srv := cfg.Context.LSP.LanguageServers[".go"]; !reflect.DeepEqual(srv.Args, []string{"-v"}) {
		t.Errorf("LanguageServers[.go].Args = %v, want [-v]", srv.Args)
	}
	if p := cfg.Context.Phrase.Phrases; len(p) != 1 || p[0].Short != "s" || p[0].Text != "t" {
		t.Errorf("Phrases lost: %+v", p)
	}
	if !reflect.DeepEqual(cfg.DataMask.MaskIPWhitelist, []string{"1.1.1.1"}) {
		t.Errorf("MaskIPWhitelist lost: %v", cfg.DataMask.MaskIPWhitelist)
	}
	if !reflect.DeepEqual(cfg.Hooks.OnUserMessage, []string{"echo hi"}) {
		t.Errorf("Hooks.OnUserMessage lost: %v", cfg.Hooks.OnUserMessage)
	}
}

// TestEnsureContainerDefaultsIdempotent 重复执行结果不变。
func TestEnsureContainerDefaultsIdempotent(t *testing.T) {
	cfg := applyLoadPipeline(t, nullContainersRaw)
	first, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	EnsureContainerDefaults(cfg)
	second, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(first) != string(second) {
		t.Error("EnsureContainerDefaults should be idempotent")
	}
}

// TestEnsureContainerDefaultsNilConfig nil 配置不应 panic。
func TestEnsureContainerDefaultsNilConfig(t *testing.T) {
	EnsureContainerDefaults(nil)
}

// TestLoadNormalizesNullContainers 端到端：含 null 的配置文件经 Load 后容器非 nil，
// 再次 Save 落盘不再出现 null。
func TestLoadNormalizesNullContainers(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "null_config.json")
	if err := os.WriteFile(path, []byte(nullContainersRaw), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	oldEnv := os.Getenv(envConfigName)
	defer os.Setenv(envConfigName, oldEnv)
	os.Setenv(envConfigName, path)
	oldPath := configPath
	defer func() { configPath = oldPath }()
	configPath = ""

	Load()

	if !Loaded() {
		t.Fatal("Loaded() should be true after a successful load")
	}
	if GlobalConfig.Agent.Fetch.RewriteHeaders == nil {
		t.Error("after Load: Agent.Fetch.RewriteHeaders should not be nil")
	}
	if GlobalConfig.Context.LSP.LanguageServers == nil {
		t.Error("after Load: Context.LSP.LanguageServers should not be nil")
	}
	if GlobalConfig.Context.Phrase.Phrases == nil {
		t.Error("after Load: Context.Phrase.Phrases should not be nil")
	}
	if GlobalConfig.Hooks.OnUserMessage == nil {
		t.Error("after Load: Hooks.OnUserMessage should not be nil")
	}

	if err := Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if nulls := nullPathsInJSON(t, data); len(nulls) != 0 {
		t.Errorf("saved config still contains null at %v", nulls)
	}
}
