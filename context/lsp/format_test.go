package lsp

import "testing"

// TestApplyTextEditsUTF16 验证 TextEdit 的 UTF-16 列号被正确转换为字节偏移，
// 修复前直接按字节下标切分，遇到非 ASCII 字符会切坏 UTF-8 编码
func TestApplyTextEditsUTF16(t *testing.T) {
	tests := []struct {
		name  string
		text  string
		edits []TextEdit
		want  string
	}{
		{
			name: "ASCII 单行替换",
			text: "hello world\n",
			edits: []TextEdit{{
				Range:   Range{Start: Position{Line: 0, Character: 6}, End: Position{Line: 0, Character: 11}},
				NewText: "there",
			}},
			want: "hello there\n",
		},
		{
			name: "中文标识符替换",
			text: "package main\n\nvar 变量 = 1\n",
			edits: []TextEdit{{
				Range:   Range{Start: Position{Line: 2, Character: 4}, End: Position{Line: 2, Character: 6}},
				NewText: "名称",
			}},
			want: "package main\n\nvar 名称 = 1\n",
		},
		{
			name: "emoji 代理对替换",
			text: "x := \"\U0001F600\U0001F600\"\n",
			edits: []TextEdit{{
				Range:   Range{Start: Position{Line: 0, Character: 8}, End: Position{Line: 0, Character: 10}},
				NewText: "A",
			}},
			want: "x := \"\U0001F600A\"\n",
		},
		{
			name: "多行替换保留结束行剩余内容",
			text: "a\nb\nc\n",
			edits: []TextEdit{{
				Range:   Range{Start: Position{Line: 0, Character: 0}, End: Position{Line: 1, Character: 0}},
				NewText: "X\n",
			}},
			want: "X\nb\nc\n",
		},
		{
			name: "中文行尾追加",
			text: "var 名称 = 1\n",
			edits: []TextEdit{{
				Range:   Range{Start: Position{Line: 0, Character: 10}, End: Position{Line: 0, Character: 10}},
				NewText: " // 注释",
			}},
			want: "var 名称 = 1 // 注释\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := applyTextEdits(tt.text, tt.edits)
			if got != tt.want {
				t.Fatalf("applyTextEdits() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUTF16OffsetToBytes(t *testing.T) {
	tests := []struct {
		line   string
		offset int
		want   int
	}{
		{"abc", 0, 0},
		{"abc", 2, 2},
		{"abc", 3, 3},
		{"abc", 99, 3}, // 超出范围按行尾处理
		{"变量", 0, 0},
		{"变量", 1, 3}, // 一个中文字符占 1 个 UTF-16 码元、3 个字节
		{"变量", 2, 6},
		{"a\U0001F600b", 1, 1}, // emoji 占 2 个 UTF-16 码元
		{"a\U0001F600b", 3, 5},
		{"a\U0001F600b", 4, 6},
	}
	for _, tt := range tests {
		if got := utf16OffsetToBytes(tt.line, tt.offset); got != tt.want {
			t.Errorf("utf16OffsetToBytes(%q, %d) = %d, want %d", tt.line, tt.offset, got, tt.want)
		}
	}
}
