package lsp

import (
	"testing"
)

func TestExtFromPath(t *testing.T) {
	tests := []struct {
		path string
		ext  string
	}{
		{"/path/to/file.go", ".go"},
		{"file.py", ".py"},
		{"/a/b/c.tsx", ".tsx"},
		{"Makefile", ""},
		{"archive.tar.gz", ".gz"},
	}
	for _, tt := range tests {
		got := extFromPath(tt.path)
		if got != tt.ext {
			t.Errorf("extFromPath(%q) = %q, want %q", tt.path, got, tt.ext)
		}
	}
}

func TestLanguageIDFromExt(t *testing.T) {
	tests := []struct {
		ext  string
		lang string
	}{
		{".go", "go"},
		{".py", "python"},
		{".rs", "rust"},
		{".java", "java"},
		{".js", "javascript"},
		{".ts", "typescript"},
		{".jsx", "javascript"},
		{".tsx", "typescript"},
		{".c", "c"},
		{".cpp", "cpp"},
		{".h", "c"},
		{".hpp", "cpp"},
		{".kt", "kotlin"},
		{".cs", "csharp"},
		{".unknown", "unknown"},
	}
	for _, tt := range tests {
		got := languageIDFromExt(tt.ext)
		if got != tt.lang {
			t.Errorf("languageIDFromExt(%q) = %q, want %q", tt.ext, got, tt.lang)
		}
	}
}

func TestLanguageKey(t *testing.T) {
	key := languageKey("/work", "go")
	if key != "/work|go" {
		t.Errorf("languageKey = %q, want %q", key, "/work|go")
	}
}

func TestDefaultLanguageServers(t *testing.T) {
	expectedExts := []string{".go", ".py", ".c", ".h", ".cpp", ".hpp", ".cc", ".cxx",
		".rs", ".java", ".kt", ".kts", ".cs", ".js", ".jsx", ".ts", ".tsx"}
	for _, ext := range expectedExts {
		if _, ok := defaultLanguageServers[ext]; !ok {
			t.Errorf("missing default language server for %s", ext)
		}
	}
}

func TestDefaultEntriesHaveCommand(t *testing.T) {
	for ext, cfg := range defaultLanguageServers {
		if cfg.Command == "" {
			t.Errorf("default language server for %s has empty command", ext)
		}
	}
}

func TestLanguageIDEntries(t *testing.T) {
	// 构建所有有 LSP 服务器或无 LSP 但受支持的扩展名集合
	supported := make(map[string]bool)
	for ext := range defaultLanguageServers {
		supported[ext] = true
	}
	for _, ext := range noLSPExtensions {
		supported[ext] = true
	}

	for ext, lang := range extToLanguageID {
		if lang == "" {
			t.Errorf("empty language ID for extension %s", ext)
		}
		// 验证每个扩展名要么有默认 LSP 服务器，要么在 noLSPExtensions 中
		if !supported[ext] {
			t.Errorf("extension %s (lang=%s) is not in defaultLanguageServers or noLSPExtensions", ext, lang)
		}
	}
}

func TestNoLSPExtensionsEmptyCommand(t *testing.T) {
	// noLSPExtensions 中的扩展名不应在 defaultLanguageServers 中
	for _, ext := range noLSPExtensions {
		if _, ok := defaultLanguageServers[ext]; ok {
			t.Errorf("extension %s should not have a default language server (use noLSPExtensions instead)", ext)
		}
	}
}
