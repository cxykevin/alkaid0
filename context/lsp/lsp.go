package lsp

import (
	"github.com/cxykevin/alkaid0/log"
)

var logger *log.LogsObj

func init() {
	logger = log.New("lsp")
}

// topLevelSymbolKinds 判定是否顶层符号的 SymbolKind 集合
var topLevelSymbolKinds = map[SymbolKind]bool{
	SymbolFunction:    true,
	SymbolMethod:      true,
	SymbolClass:       true,
	SymbolInterface:   true,
	SymbolStruct:      true,
	SymbolEnum:        true,
	SymbolConstructor: true,
	SymbolVariable:    true,
	SymbolConstant:    true,
	SymbolModule:      true,
	SymbolNamespace:   true,
	SymbolPackage:     true,
}

// isTopLevel 判断是否为顶层符号
func isTopLevel(kind SymbolKind) bool {
	return topLevelSymbolKinds[kind]
}

// isStructOrClass 判断是否为结构体或类（含有成员的符号）
func isStructOrClass(kind SymbolKind) bool {
	return kind == SymbolClass || kind == SymbolStruct || kind == SymbolInterface
}
