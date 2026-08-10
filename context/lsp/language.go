package lsp

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cxykevin/alkaid0/config"
)

// LanguageServerConfig 语言服务器配置（命令+参数）
type LanguageServerConfig struct {
	Command string
	Args    []string
}

// defaultLanguageServers 内置默认语言服务器映射表
var defaultLanguageServers = map[string]LanguageServerConfig{
	".go":   {Command: "gopls"},
	".py":   {Command: "pylsp"},
	".c":    {Command: "clangd"},
	".h":    {Command: "clangd"},
	".cpp":  {Command: "clangd"},
	".hpp":  {Command: "clangd"},
	".cc":   {Command: "clangd"},
	".cxx":  {Command: "clangd"},
	".rs":   {Command: "rust-analyzer"},
	".java": {Command: "jdtls"},
	".kt":   {Command: "kotlin-language-server"},
	".kts":  {Command: "kotlin-language-server"},
	".cs":   {Command: "csharp-ls"},
	".js":   {Command: "typescript-language-server", Args: []string{"--stdio"}},
	".jsx":  {Command: "typescript-language-server", Args: []string{"--stdio"}},
	".ts":   {Command: "typescript-language-server", Args: []string{"--stdio"}},
	".tsx":  {Command: "typescript-language-server", Args: []string{"--stdio"}},
	".vue":  {Command: "vue-language-server", Args: []string{"--stdio"}},
	".css":  {Command: "vscode-css-language-server", Args: []string{"--stdio"}},
	".scss": {Command: "vscode-css-language-server", Args: []string{"--stdio"}},
	".sass": {Command: "vscode-css-language-server", Args: []string{"--stdio"}},
	".less": {Command: "vscode-css-language-server", Args: []string{"--stdio"}},
	".tq":   {Command: "talqor-cli", Args: []string{"--lsp"}},
	// .json/.yaml/.yml/.txt: 不支持 LSP（见 noLSPExtensions），无需启动进程
}

// noLSPExtensions 已知不支持 LSP 但需要被索引的扩展名
// 这些文件不会启动任何 LSP 进程，直接全文件索引
var noLSPExtensions = []string{
	".json",
	".jsonl",
	".yaml",
	".yml",
	".txt",
	".toml",
	".ini",
	".makefile",
	".dockerfile",
	".license",
	".md",
	".mdx",
}

// noLSPFileNames 已知无扩展名、无 LSP、但需要被索引的特定文件名
var noLSPFileNames = map[string]string{
	"makefile":   ".makefile",
	"dockerfile": ".dockerfile",
	"license":    ".license",
}

// GetFileNameExt 返回已知无扩展名文件的映射伪扩展名
func GetFileNameExt(baseName string) (string, bool) {
	ext, ok := noLSPFileNames[strings.ToLower(baseName)]
	return ext, ok
}

// extToLanguageID 文件扩展名到 LSP 语言 ID 的映射
var extToLanguageID = map[string]string{
	".go":    "go",
	".py":    "python",
	".c":     "c",
	".h":     "c",
	".cpp":   "cpp",
	".hpp":   "cpp",
	".cc":    "cpp",
	".cxx":   "cpp",
	".rs":    "rust",
	".java":  "java",
	".kt":    "kotlin",
	".kts":   "kotlin",
	".cs":    "csharp",
	".js":    "javascript",
	".jsx":   "javascript",
	".ts":    "typescript",
	".tsx":   "typescript",
	".vue":   "vue",
	".css":   "css",
	".scss":  "scss",
	".sass":  "sass",
	".less":  "less",
	".json":  "json",
	".jsonl": "jsonl",
	".toml":  "toml",
	".ini":   "ini",
	".md":    "markdown",
	".mdx":   "mdx",
	".yaml":  "yaml",
	".yml":   "yaml",
	".txt":   "text",
}

// extFromPath 从文件路径获取小写扩展名
func extFromPath(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	return ext
}

// resolveLanguageServer 解析文件扩展名对应的语言服务器配置
// 优先使用用户配置，否则使用内置默认值
func resolveLanguageServer(ext string) (LanguageServerConfig, error) {
	// 尝试用户配置覆盖
	cfg := config.GlobalConfigSafe()
	if cfg.Context.LSP.LanguageServers != nil {
		if userCfg, ok := cfg.Context.LSP.LanguageServers[ext]; ok {
			return LanguageServerConfig{
				Command: userCfg.Command,
				Args:    userCfg.Args,
			}, nil
		}
	}

	// 回退到内置默认值
	if def, ok := defaultLanguageServers[ext]; ok {
		return def, nil
	}

	return LanguageServerConfig{}, fmt.Errorf("unsupported file extension: %s", ext)
}

// languageIDFromExt 从文件扩展名获取 LSP 语言 ID
func languageIDFromExt(ext string) string {
	if lang, ok := extToLanguageID[ext]; ok {
		return lang
	}
	// 去掉 . 当作语言 ID
	return strings.TrimPrefix(ext, ".")
}

// languageKey 生成用于管理器缓存的 key
// 格式: "workdir|language"
func languageKey(workdir, language string) string {
	return workdir + "|" + language
}

// SupportedExtensions 返回所有支持的扩展名列表（用户配置 + 内置默认值 + 已知无LSP的扩展名）
func SupportedExtensions() []string {
	cfg := config.GlobalConfigSafe()
	seen := make(map[string]bool)

	// 收集用户配置中自定义的扩展名
	for ext := range cfg.Context.LSP.LanguageServers {
		seen[ext] = true
	}

	// 收集内置默认值（有 LSP 的扩展名）
	for ext := range defaultLanguageServers {
		seen[ext] = true
	}

	// 收集已知无 LSP 但仍需索引的扩展名
	for _, ext := range noLSPExtensions {
		seen[ext] = true
	}

	result := make([]string, 0, len(seen))
	for ext := range seen {
		result = append(result, ext)
	}
	return result
}

// // resolver 接口组合，便于测试替换
// type resolver interface {
// 	resolveLanguageServer(ext string) (LanguageServerConfig, error)
// 	languageIDFromExt(ext string) string
// }
