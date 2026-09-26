package lsp

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	json5 "github.com/titanous/json5"
)

// TestCheckNoLSPFileSyntaxDispatch 扩展名分发：已知格式走各自检查器，未知格式不做检查。
func TestCheckNoLSPFileSyntaxDispatch(t *testing.T) {
	valid := []struct {
		ext     string
		content string
	}{
		{".json", `{"a": 1}`},
		{".jsonl", "{\"a\":1}\n{\"b\":2}\n"},
		{".yaml", "a: 1\n"},
		{".yml", "- 1\n- 2\n"},
		{".toml", "a = 1\n"},
		{".ini", "[s]\nk = v\n"},
		{".md", "# title\n\n```go\ncode\n```\n"},
		{".mdx", "```go\ncode\n```\n"},
	}
	for _, tc := range valid {
		if diags := CheckNoLSPFileSyntax(tc.ext, tc.content); len(diags) != 0 {
			t.Errorf("CheckNoLSPFileSyntax(%q) = %#v, want no diagnostics", tc.ext, diags)
		}
	}

	// .txt/.go 等不做语法检查，返回 nil（与空切片区分）。
	for _, ext := range []string{".txt", ".go", ".makefile", ""} {
		if diags := CheckNoLSPFileSyntax(ext, "not: valid: at all"); diags != nil {
			t.Errorf("CheckNoLSPFileSyntax(%q) = %#v, want nil", ext, diags)
		}
	}
}

// TestCheckJSON5AndJSONLDiagnostics JSON5 与 JSON Lines 的错误定位。
func TestCheckJSON5AndJSONLDiagnostics(t *testing.T) {
	json5Diags := CheckNoLSPFileSyntax(".json", `{"a": }`)
	if len(json5Diags) != 1 {
		t.Fatalf("json5 diags = %#v, want exactly 1", json5Diags)
	}
	if json5Diags[0].Severity != "error" || json5Diags[0].Message == "" {
		t.Errorf("json5 diag = %#v, want error with message", json5Diags[0])
	}

	jsonl := CheckNoLSPFileSyntax(".jsonl", "{\"a\":1}\n\nnot-json\n{\"b\":2}\n")
	if len(jsonl) != 1 {
		t.Fatalf("jsonl diags = %#v, want exactly 1", jsonl)
	}
	if jsonl[0].Line != 3 {
		t.Errorf("jsonl line = %d, want 3", jsonl[0].Line)
	}
	if !strings.Contains(jsonl[0].Message, "JSONL line 3") {
		t.Errorf("jsonl message = %q, want it to mention line 3", jsonl[0].Message)
	}
}

// TestCheckYAMLAndTOMLDiagnostics YAML / TOML 解析失败转成诊断，成功时无诊断。
func TestCheckYAMLAndTOMLDiagnostics(t *testing.T) {
	yamlDiags := CheckNoLSPFileSyntax(".yaml", "a: [1, 2\n")
	if len(yamlDiags) != 1 || yamlDiags[0].Severity != "error" || yamlDiags[0].Message == "" {
		t.Fatalf("yaml diags = %#v, want one error with message", yamlDiags)
	}
	if diags := CheckNoLSPFileSyntax(".yml", "- 1\n- 2\n"); len(diags) != 0 {
		t.Errorf("valid yaml diags = %#v, want none", diags)
	}

	tomlDiags := CheckNoLSPFileSyntax(".toml", "a = \n")
	if len(tomlDiags) != 1 || tomlDiags[0].Severity != "error" || tomlDiags[0].Message == "" {
		t.Fatalf("toml diags = %#v, want one error with message", tomlDiags)
	}
	if diags := CheckNoLSPFileSyntax(".toml", "a = 1\n[b]\nc = \"x\"\n"); len(diags) != 0 {
		t.Errorf("valid toml diags = %#v, want none", diags)
	}
}

// TestCheckINIDiagnostics INI 检查器的各条分支：注释/空行跳过、未闭合节头、空节头、
// 空键、节内 Python 风格 key: value、以及既非节头又非赋值的无法识别行。
func TestCheckINIDiagnostics(t *testing.T) {
	t.Run("comments and blank lines are skipped", func(t *testing.T) {
		if diags := CheckNoLSPFileSyntax(".ini", "; comment\n# comment\n\n[s]\nk = v\n"); len(diags) != 0 {
			t.Fatalf("diags = %#v, want none", diags)
		}
	})

	t.Run("unclosed section header", func(t *testing.T) {
		diags := CheckNoLSPFileSyntax(".ini", "[section\n")
		if len(diags) != 1 {
			t.Fatalf("diags = %#v, want exactly 1", diags)
		}
		if diags[0].Line != 1 || diags[0].Severity != "error" {
			t.Errorf("diag = %#v, want error on line 1", diags[0])
		}
		if !strings.Contains(diags[0].Message, "unclosed section header") {
			t.Errorf("message = %q", diags[0].Message)
		}
	})

	t.Run("empty section header", func(t *testing.T) {
		diags := CheckNoLSPFileSyntax(".ini", "[]\n")
		if len(diags) != 1 || !strings.Contains(diags[0].Message, "empty section header") {
			t.Fatalf("diags = %#v, want one empty-section error", diags)
		}
	})

	t.Run("empty key before equals", func(t *testing.T) {
		diags := CheckNoLSPFileSyntax(".ini", "= value\n")
		if len(diags) != 1 || !strings.Contains(diags[0].Message, "empty key before '='") {
			t.Fatalf("diags = %#v, want one empty-key error", diags)
		}
	})

	t.Run("python style key colon inside section", func(t *testing.T) {
		if diags := CheckNoLSPFileSyntax(".ini", "[s]\nk: v\n"); len(diags) != 0 {
			t.Fatalf("diags = %#v, want none", diags)
		}
		// 冒号前没有键名时无法识别，落到"无法识别"分支（warn 而不是 error）。
		diags := CheckNoLSPFileSyntax(".ini", "[s]\n: v\n")
		if len(diags) != 1 || diags[0].Severity != "warn" {
			t.Fatalf("diags = %#v, want one warn", diags)
		}
	})

	t.Run("unrecognized line", func(t *testing.T) {
		diags := CheckNoLSPFileSyntax(".ini", "just some text\n")
		if len(diags) != 1 || diags[0].Severity != "warn" || diags[0].Line != 1 {
			t.Fatalf("diags = %#v, want one warn on line 1", diags)
		}
		if !strings.Contains(diags[0].Message, "unrecognized INI syntax") {
			t.Errorf("message = %q", diags[0].Message)
		}
	})
}

// TestCheckMarkdownFences Markdown 围栏：反引号与波浪号两种标记，配对才闭合，
// 未闭合时按起始行号与起始标记报错。
func TestCheckMarkdownFences(t *testing.T) {
	t.Run("unclosed backtick fence", func(t *testing.T) {
		diags := CheckNoLSPFileSyntax(".md", "# title\n\n```go\ncode\n")
		if len(diags) != 1 {
			t.Fatalf("diags = %#v, want exactly 1", diags)
		}
		if diags[0].Line != 3 || diags[0].Severity != "error" {
			t.Errorf("diag = %#v, want error reported on the opening line 3", diags[0])
		}
		if !strings.Contains(diags[0].Message, "```") {
			t.Errorf("message = %q, want it to name the ``` marker", diags[0].Message)
		}
	})

	t.Run("unclosed tilde fence", func(t *testing.T) {
		diags := CheckNoLSPFileSyntax(".mdx", "~~~\ncode\n")
		if len(diags) != 1 || diags[0].Line != 1 {
			t.Fatalf("diags = %#v, want one diag on line 1", diags)
		}
		if !strings.Contains(diags[0].Message, "~~~") {
			t.Errorf("message = %q, want it to name the ~~~ marker", diags[0].Message)
		}
	})

	t.Run("mismatched markers do not close", func(t *testing.T) {
		diags := CheckNoLSPFileSyntax(".md", "```go\ncode\n~~~\n")
		if len(diags) != 1 || diags[0].Line != 1 {
			t.Fatalf("diags = %#v, want the ``` fence to stay open", diags)
		}
	})

	t.Run("properly closed fence is clean", func(t *testing.T) {
		if diags := CheckNoLSPFileSyntax(".md", "```go\ncode\n```\n\ntext\n"); len(diags) != 0 {
			t.Fatalf("diags = %#v, want none", diags)
		}
	})

	t.Run("reference link line is accepted", func(t *testing.T) {
		if diags := CheckNoLSPFileSyntax(".md", "see [text][ref]\n"); len(diags) != 0 {
			t.Fatalf("diags = %#v, want none", diags)
		}
	})
}

// TestErrorOffset 偏移量提取覆盖三种错误：json5 语法错误、标准库 json 语法错误、其它错误。
func TestErrorOffset(t *testing.T) {
	if got := errorOffset(&json5.SyntaxError{Offset: 7}); got != 7 {
		t.Errorf("json5.SyntaxError = %d, want 7", got)
	}
	if got := errorOffset(&json.SyntaxError{Offset: 11}); got != 11 {
		t.Errorf("json.SyntaxError = %d, want 11", got)
	}
	if got := errorOffset(errors.New("plain error")); got != 0 {
		t.Errorf("plain error = %d, want 0", got)
	}
}

// TestLineFromOffset 字节偏移换算行号的边界：非正偏移、超出内容长度、正常偏移。
func TestLineFromOffset(t *testing.T) {
	const content = "a\nb\nc"
	cases := []struct {
		name   string
		offset int64
		want   int
	}{
		{"zero offset", 0, 0},
		{"negative offset", -5, 0},
		{"offset past end is clamped", 100, 3},
		{"offset on second line", 2, 2},
	}
	for _, tc := range cases {
		if got := lineFromOffset(content, tc.offset); got != tc.want {
			t.Errorf("%s: lineFromOffset(%q, %d) = %d, want %d", tc.name, content, tc.offset, got, tc.want)
		}
	}
}

// TestLineFromYAMLError 从 yaml.v3 的错误消息里取行号，格式不符时返回 0。
func TestLineFromYAMLError(t *testing.T) {
	if got := lineFromYAMLError("yaml: line 12: mapping values are not allowed in this context"); got != 12 {
		t.Errorf("= %d, want 12", got)
	}
	if got := lineFromYAMLError("yaml: unknown problem"); got != 0 {
		t.Errorf("= %d, want 0", got)
	}
}

// TestLineFromTOMLError BurntSushi/toml 的 "near line N" 与 "at line N" 两种格式，均无法匹配时返回 0。
func TestLineFromTOMLError(t *testing.T) {
	if got := lineFromTOMLError("near line 4: expected key"); got != 4 {
		t.Errorf("near line = %d, want 4", got)
	}
	if got := lineFromTOMLError("at line 9: unclosed table"); got != 9 {
		t.Errorf("at line = %d, want 9", got)
	}
	if got := lineFromTOMLError("toml: something else"); got != 0 {
		t.Errorf("unmatched = %d, want 0", got)
	}
}
