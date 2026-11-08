package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cxykevin/alkaid0/config/structs"
)

// TestEnsureCodebaseDefaultsFillsZeros Codebase 三个调参为 0 时补齐初始值。
func TestEnsureCodebaseDefaultsFillsZeros(t *testing.T) {
	cfg := &structs.Config{} // 全零：模拟旧配置被零值落盘
	EnsureCodebaseDefaults(cfg)

	cb := cfg.Context.Codebase
	if cb.BM25Weight != 0.7 {
		t.Errorf("BM25Weight = %v, want 0.7", cb.BM25Weight)
	}
	if cb.VectorMinSimilarity != 0.5 {
		t.Errorf("VectorMinSimilarity = %v, want 0.5", cb.VectorMinSimilarity)
	}
	// BM25RetentionScore 的初始值本身就是 0（表示不限制）
	if cb.BM25RetentionScore != 0 {
		t.Errorf("BM25RetentionScore = %v, want 0", cb.BM25RetentionScore)
	}
}

// TestEnsureCodebaseDefaultsKeepsExplicitValues 非 0 的显式值必须原样保留。
func TestEnsureCodebaseDefaultsKeepsExplicitValues(t *testing.T) {
	cfg := &structs.Config{}
	cfg.Context.Codebase.BM25Weight = 0.3
	cfg.Context.Codebase.VectorMinSimilarity = 0.8
	cfg.Context.Codebase.BM25RetentionScore = 2.5
	EnsureCodebaseDefaults(cfg)

	cb := cfg.Context.Codebase
	if cb.BM25Weight != 0.3 || cb.VectorMinSimilarity != 0.8 || cb.BM25RetentionScore != 2.5 {
		t.Errorf("explicit values changed: %+v", cb)
	}
}

// TestEnsureCodebaseDefaultsPartial 只有为 0 的字段被补齐，其它字段不受影响。
func TestEnsureCodebaseDefaultsPartial(t *testing.T) {
	cfg := &structs.Config{}
	cfg.Context.Codebase.BM25Weight = 0.4
	EnsureCodebaseDefaults(cfg)

	cb := cfg.Context.Codebase
	if cb.BM25Weight != 0.4 {
		t.Errorf("BM25Weight = %v, want 0.4", cb.BM25Weight)
	}
	if cb.VectorMinSimilarity != 0.5 {
		t.Errorf("VectorMinSimilarity = %v, want 0.5", cb.VectorMinSimilarity)
	}
}

// TestEnsureCodebaseDefaultsNilConfig nil 配置不应 panic。
func TestEnsureCodebaseDefaultsNilConfig(t *testing.T) {
	EnsureCodebaseDefaults(nil)
}

// TestLoadFillsCodebaseDefaults 端到端：配置文件里显式写成 0 的 Codebase 调参
// 在 Load 后变为初始值，并随 Save 落盘；显式非 0 值保持不变。
func TestLoadFillsCodebaseDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "codebase_config.json")
	raw := `{
		"Server": {"key": "k"},
		"Context": {"Codebase": {"BM25Weight": 0, "VectorMinSimilarity": 0, "BM25RetentionScore": 0}}
	}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	oldEnv := os.Getenv(envConfigName)
	defer os.Setenv(envConfigName, oldEnv)
	os.Setenv(envConfigName, path)
	oldPath := configPath
	defer func() { configPath = oldPath }()
	configPath = ""

	Load()

	cb := GlobalConfig.Context.Codebase
	if cb.BM25Weight != 0.7 {
		t.Errorf("after Load: BM25Weight = %v, want 0.7", cb.BM25Weight)
	}
	if cb.VectorMinSimilarity != 0.5 {
		t.Errorf("after Load: VectorMinSimilarity = %v, want 0.5", cb.VectorMinSimilarity)
	}

	if err := Save(); err != nil {
		t.Fatalf("save config: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	var saved struct {
		Context struct {
			Codebase structs.CodebaseConfig `json:"Codebase"`
		} `json:"Context"`
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatalf("unmarshal saved config: %v", err)
	}
	if saved.Context.Codebase.BM25Weight != 0.7 || saved.Context.Codebase.VectorMinSimilarity != 0.5 {
		t.Errorf("saved Codebase = %+v, want BM25Weight 0.7 / VectorMinSimilarity 0.5", saved.Context.Codebase)
	}

	// 显式非 0 值必须保留
	if err := os.WriteFile(path, []byte(`{"Server":{"key":"k"},"Context":{"Codebase":{"BM25Weight":0.25,"VectorMinSimilarity":0.9}}}`), 0600); err != nil {
		t.Fatalf("rewrite config: %v", err)
	}
	Reload()

	cb = GlobalConfig.Context.Codebase
	if cb.BM25Weight != 0.25 || cb.VectorMinSimilarity != 0.9 {
		t.Errorf("explicit values lost after Reload: %+v", cb)
	}
}
