package codebase

import (
	"github.com/cxykevin/alkaid0/context/lsp"
	"github.com/cxykevin/alkaid0/context/outline"
)

// toOutlineSymbols 把 LSP 的符号结果转换为大纲渲染需要的字段。
// 索引阶段只保留名称/类型/签名/起始行——大纲不携带代码正文。
func toOutlineSymbols(symbols []lsp.SymbolResult) []outline.Symbol {
	syms := make([]outline.Symbol, 0, len(symbols))
	for _, sym := range symbols {
		syms = append(syms, outline.Symbol{
			Name:      sym.Name,
			Kind:      sym.KindName,
			Signature: sym.Signature,
			Line:      sym.Line + 1, // LSP 行号 0-based，大纲展示 1-based
		})
	}
	return syms
}
