package lsp

import (
	"testing"
)

func TestIsTopLevel(t *testing.T) {
	tests := []struct {
		kind SymbolKind
		want bool
	}{
		{SymbolFunction, true},
		{SymbolClass, true},
		{SymbolStruct, true},
		{SymbolInterface, true},
		{SymbolEnum, true},
		{SymbolMethod, true},
		{SymbolConstructor, true},
		{SymbolVariable, true},
		{SymbolConstant, true},
		{SymbolModule, true},
		{SymbolNamespace, true},
		{SymbolPackage, true},
		{SymbolField, false},
		{SymbolProperty, false},
		{SymbolEnumMember, false},
		{SymbolTypeParameter, false},
	}
	for _, tt := range tests {
		got := isTopLevel(tt.kind)
		if got != tt.want {
			t.Errorf("isTopLevel(%d) = %v, want %v", tt.kind, got, tt.want)
		}
	}
}

func TestIsStructOrClass(t *testing.T) {
	tests := []struct {
		kind SymbolKind
		want bool
	}{
		{SymbolClass, true},
		{SymbolStruct, true},
		{SymbolInterface, true},
		{SymbolFunction, false},
		{SymbolMethod, false},
	}
	for _, tt := range tests {
		got := isStructOrClass(tt.kind)
		if got != tt.want {
			t.Errorf("isStructOrClass(%d) = %v, want %v", tt.kind, got, tt.want)
		}
	}
}

func TestPathToURI(t *testing.T) {
	uri := pathToURI("/home/user/file.go")
	if uri != "file:///home/user/file.go" {
		t.Errorf("pathToURI = %q, want %q", uri, "file:///home/user/file.go")
	}
}

func TestExtractDocComment(t *testing.T) {
	tests := []struct {
		name string
		data string // JSON hover response
		want string
	}{
		{
			name: "markdown content",
			data: `{"contents":{"kind":"markdown","value":"GetA returns the A value\n\nThis function does X"}}`,
			want: "GetA returns the A value\n\nThis function does X",
		},
		{
			name: "plain string",
			data: `{"contents":"simple doc string"}`,
			want: "simple doc string",
		},
		{
			name: "empty result",
			data: `{}`,
			want: "",
		},
		{
			name: "nil contents",
			data: `{"contents":null}`,
			want: "",
		},
		{
			name: "code block cleanup",
			data: `{"contents":{"kind":"markdown","value":"Some doc\n\nfunc Foo()\nHere"}}`,
			want: "Some doc\n\nfunc Foo()\nHere",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractDocComment([]byte(tt.data))
			if got != tt.want {
				t.Errorf("extractDocComment() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatDocComment(t *testing.T) {
	tests := []struct {
		comment  string
		language string
		want     string
	}{
		{
			comment:  "GetA returns the A value",
			language: "go",
			want:     "// GetA returns the A value",
		},
		{
			comment:  "Line1\nLine2",
			language: "go",
			want:     "// Line1\n// Line2",
		},
		{
			comment:  "A function",
			language: "python",
			want:     `"""A function"""`,
		},
		{
			comment:  "",
			language: "go",
			want:     "",
		},
		{
			comment:  "Multi\nline",
			language: "c",
			want:     "/*\n * Multi\n * line\n */",
		},
		{
			comment:  "A value",
			language: "unknown",
			want:     "# A value",
		},
	}
	for _, tt := range tests {
		got := formatDocComment(tt.comment, tt.language)
		if got != tt.want {
			t.Errorf("formatDocComment(%q, %q) = %q, want %q", tt.comment, tt.language, got, tt.want)
		}
	}
}

func TestExtractSignature(t *testing.T) {
	content := `package main

// GetA returns A
func GetA(a int) error {
	if a > 0 {
		return nil
	}
	return nil
}

// ModelType is a string type
type ModelType string

// Config holds configuration
type Config struct {
	Name string
	Value int
}
`
	tests := []struct {
		name string
		rng  Range
		want string
	}{
		{
			name: "function",
			rng:  Range{Start: Position{Line: 3, Character: 0}, End: Position{Line: 9, Character: 0}},
			want: "func GetA(a int) error {",
		},
		{
			name: "type alias",
			rng:  Range{Start: Position{Line: 11, Character: 0}, End: Position{Line: 11, Character: 21}},
			want: "type ModelType string",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSignature(content, tt.rng)
			if got != tt.want {
				t.Errorf("extractSignature() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractSignatureStruct(t *testing.T) {
	content := `package main

type Config struct {
	Name string
	Value int
}
`
	rng := Range{Start: Position{Line: 2, Character: 0}, End: Position{Line: 5, Character: 0}}
	got := extractSignature(content, rng)
	want := "type Config struct {"
	if got != want {
		t.Errorf("extractSignature() struct = %q, want %q", got, want)
	}
}

func TestParseDocumentSymbols(t *testing.T) {
	// 模拟 documentSymbol 返回的 JSON
	data := `[
		{
			"name": "GetA",
			"kind": 12,
			"range": {"start": {"line": 3, "character": 0}, "end": {"line": 9, "character": 0}},
			"selectionRange": {"start": {"line": 3, "character": 5}, "end": {"line": 3, "character": 9}},
			"children": []
		},
		{
			"name": "Config",
			"kind": 23,
			"range": {"start": {"line": 11, "character": 0}, "end": {"line": 14, "character": 0}},
			"selectionRange": {"start": {"line": 11, "character": 5}, "end": {"line": 11, "character": 11}},
			"children": [
				{
					"name": "Name",
					"kind": 8,
					"range": {"start": {"line": 12, "character": 1}, "end": {"line": 12, "character": 13}},
					"selectionRange": {"start": {"line": 12, "character": 1}, "end": {"line": 12, "character": 5}}
				}
			]
		}
	]`

	symbols, err := parseDocumentSymbols([]byte(data))
	if err != nil {
		t.Fatalf("parseDocumentSymbols failed: %v", err)
	}

	if len(symbols) != 2 {
		t.Fatalf("expected 2 symbols, got %d", len(symbols))
	}

	if symbols[0].Name != "GetA" || symbols[0].Kind != SymbolFunction {
		t.Errorf("first symbol: got %s/%d", symbols[0].Name, symbols[0].Kind)
	}
	if symbols[1].Name != "Config" || symbols[1].Kind != SymbolStruct {
		t.Errorf("second symbol: got %s/%d", symbols[1].Name, symbols[1].Kind)
	}
	if len(symbols[1].Children) != 1 {
		t.Errorf("expected 1 child for Config, got %d", len(symbols[1].Children))
	}
}

func TestParseDocumentSymbolsEmpty(t *testing.T) {
	symbols, err := parseDocumentSymbols([]byte(`[]`))
	if err != nil {
		t.Fatalf("parseDocumentSymbols([]) failed: %v", err)
	}
	if len(symbols) != 0 {
		t.Errorf("expected 0 symbols, got %d", len(symbols))
	}
}
