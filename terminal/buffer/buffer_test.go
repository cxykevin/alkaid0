package buffer

import (
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	buf := New(24, 80)
	if buf == nil {
		t.Fatal("缓冲区为nil")
	}

	rows, cols := buf.GetSize()
	if rows != 24 || cols != 80 {
		t.Errorf("缓冲区大小 = (%d, %d), 期望 (24, 80)", rows, cols)
	}

	x, y := buf.GetCursor()
	if x != 0 || y != 0 {
		t.Errorf("初始光标位置 = (%d, %d), 期望 (0, 0)", x, y)
	}
}

func TestWriteRune(t *testing.T) {
	buf := New(24, 80)

	buf.WriteRune('H')
	buf.WriteRune('e')
	buf.WriteRune('l')
	buf.WriteRune('l')
	buf.WriteRune('o')

	line, err := buf.GetLine(0)
	if err != nil {
		t.Fatalf("获取行失败: %v", err)
	}

	if !strings.HasPrefix(line, "Hello") {
		t.Errorf("行内容 = %q, 期望以 'Hello' 开头", line)
	}
}

func TestLineFeed(t *testing.T) {
	buf := New(24, 80)

	buf.WriteRune('A')
	buf.Write([]byte("\n"))
	buf.WriteRune('B')

	_, y := buf.GetCursor()
	if y != 1 {
		t.Errorf("换行后光标Y = %d, 期望 1", y)
	}

	line0, _ := buf.GetLine(0)
	line1, _ := buf.GetLine(1)

	if !strings.HasPrefix(line0, "A") {
		t.Errorf("第0行 = %q, 期望以 'A' 开头", line0)
	}
	// 换行后光标在第1行开头，写入B后光标移动到位置1
	line1Trimmed := strings.TrimSpace(line1)
	if !strings.HasPrefix(line1Trimmed, "B") {
		t.Errorf("第1行 = %q (去空格后: %q), 期望以 'B' 开头", line1, line1Trimmed)
	}
}

func TestCarriageReturn(t *testing.T) {
	buf := New(24, 80)

	buf.WriteRune('H')
	buf.WriteRune('e')
	buf.WriteRune('l')
	buf.Write([]byte("\r"))

	x, y := buf.GetCursor()
	if x != 0 {
		t.Errorf("回车后光标X = %d, 期望 0", x)
	}
	if y != 0 {
		t.Errorf("回车后光标Y = %d, 期望 0", y)
	}
	_ = y // 使用y变量
}

func TestClear(t *testing.T) {
	buf := New(24, 80)

	buf.Write([]byte("Hello World"))
	buf.Clear()

	content := buf.GetContent()
	if strings.TrimSpace(content) != "" {
		t.Error("清空后缓冲区应该为空")
	}

	x, y := buf.GetCursor()
	if x != 0 || y != 0 {
		t.Errorf("清空后光标位置 = (%d, %d), 期望 (0, 0)", x, y)
	}
}

func TestResize(t *testing.T) {
	buf := New(24, 80)

	buf.Write([]byte("Test"))
	buf.Resize(30, 100)

	rows, cols := buf.GetSize()
	if rows != 30 || cols != 100 {
		t.Errorf("调整后大小 = (%d, %d), 期望 (30, 100)", rows, cols)
	}

	line, _ := buf.GetLine(0)
	if !strings.HasPrefix(line, "Test") {
		t.Error("调整大小后内容应该保留")
	}
}

func TestSetCursor(t *testing.T) {
	buf := New(24, 80)

	buf.SetCursor(10, 5)
	x, y := buf.GetCursor()

	if x != 10 || y != 5 {
		t.Errorf("设置光标后位置 = (%d, %d), 期望 (10, 5)", x, y)
	}

	// 测试边界
	buf.SetCursor(100, 100)
	x, y = buf.GetCursor()
	if x != 10 || y != 5 {
		t.Error("设置超出边界的光标位置应该被忽略")
	}
}

func TestANSISequences(t *testing.T) {
	buf := New(24, 80)

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"简单文本", "Hello", "Hello"},
		{"换行", "Line1\nLine2", "Line1"},
		{"清屏", "\x1b[2JTest", "Test"},
		{"光标移动", "\x1b[5;10HX", ""},
		{"颜色", "\x1b[31mRed\x1b[0m", "Red"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf.Clear()
			buf.Write([]byte(tt.input))

			line, _ := buf.GetLine(0)
			if tt.expected != "" && !strings.Contains(line, tt.expected) {
				t.Errorf("输入 %q, 第0行 = %q, 期望包含 %q", tt.input, line, tt.expected)
			}
		})
	}
}

func TestSaveCursor(t *testing.T) {
	buf := New(24, 80)

	buf.SetCursor(10, 5)
	buf.SaveCursor()

	buf.SetCursor(20, 15)
	x, y := buf.GetCursor()
	if x != 20 || y != 15 {
		t.Errorf("移动后光标 = (%d, %d), 期望 (20, 15)", x, y)
	}

	buf.RestoreCursor()
	x, y = buf.GetCursor()
	if x != 10 || y != 5 {
		t.Errorf("恢复后光标 = (%d, %d), 期望 (10, 5)", x, y)
	}
}

func TestScrolling(t *testing.T) {
	buf := New(5, 10)

	// 填满缓冲区
	for i := 0; i < 10; i++ {
		buf.Write([]byte("Line\n"))
	}

	// 检查是否发生滚动
	content := buf.GetContent()
	lines := strings.Split(content, "\n")

	if len(lines) > 5 {
		t.Error("缓冲区行数超过限制")
	}
}

func TestGetCell(t *testing.T) {
	buf := New(24, 80)

	buf.WriteRune('A')
	cell, err := buf.GetCell(0, 0)
	if err != nil {
		t.Fatalf("获取单元格失败: %v", err)
	}

	if cell.Char != 'A' {
		t.Errorf("单元格字符 = %c, 期望 'A'", cell.Char)
	}

	// 测试越界
	_, err = buf.GetCell(100, 100)
	if err == nil {
		t.Error("获取越界单元格应该返回错误")
	}
}

func TestConcurrentAccess(t *testing.T) {
	buf := New(24, 80)

	done := make(chan bool)

	// 并发写入
	go func() {
		for i := 0; i < 100; i++ {
			buf.Write([]byte("A"))
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			buf.Write([]byte("B"))
		}
		done <- true
	}()

	// 并发读取
	go func() {
		for i := 0; i < 100; i++ {
			buf.GetContent()
		}
		done <- true
	}()

	// 等待完成
	for i := 0; i < 3; i++ {
		<-done
	}
}

func TestParseANSI(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"无转义序列", "Hello", "Hello"},
		{"简单颜色", "\x1b[31mRed\x1b[0m", "Red"},
		{"光标移动", "\x1b[5;10HText", "Text"},
		{"清屏", "\x1b[2JContent", "Content"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseANSI(tt.input)
			if result != tt.expected {
				t.Errorf("ParseANSI(%q) = %q, 期望 %q", tt.input, result, tt.expected)
			}
		})
	}
}

func BenchmarkWrite(b *testing.B) {
	buf := New(24, 80)
	data := []byte("Hello World\n")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Write(data)
	}
}

func BenchmarkWriteRune(b *testing.B) {
	buf := New(24, 80)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.WriteRune('A')
	}
}

func BenchmarkGetContent(b *testing.B) {
	buf := New(24, 80)
	buf.Write([]byte("Test content\n"))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.GetContent()
	}
}
