package buffer

import (
	"io"
	"strings"
	"testing"
)

// 本文件补充 buffer.go 的边缘分支：非正数尺寸回退、WriteRune 的控制字符分派、
// GetLine 越界、GetContent 零值单元格、Resize 的非法尺寸与状态收敛、滚动量为 0、Reader。

func TestNewDefaultsOnNonPositiveSize(t *testing.T) {
	for _, tc := range []struct {
		rows, cols int
	}{
		{0, 0},
		{-3, -8},
	} {
		buf := New(tc.rows, tc.cols)
		rows, cols := buf.GetSize()
		if rows != 24 || cols != 80 {
			t.Errorf("New(%d, %d) 尺寸 = (%d, %d), 期望 (24, 80)", tc.rows, tc.cols, rows, cols)
		}
	}
}

func TestWriteRuneControlChars(t *testing.T) {
	buf := New(10, 20)

	buf.WriteRune('A')
	buf.WriteRune('\n')
	if _, y := buf.GetCursor(); y != 1 {
		t.Errorf("\\n 后光标 Y = %d, 期望 1", y)
	}

	buf.WriteRune('B')
	buf.WriteRune('\r')
	if x, _ := buf.GetCursor(); x != 0 {
		t.Errorf("\\r 后光标 X = %d, 期望 0", x)
	}

	buf.WriteRune('C')
	buf.WriteRune('\b')
	if x, _ := buf.GetCursor(); x != 0 {
		t.Errorf("\\b 后光标 X = %d, 期望 0", x)
	}

	// 已在行首：退格不再回退
	buf.WriteRune('\b')
	if x, _ := buf.GetCursor(); x != 0 {
		t.Errorf("行首 \\b 后光标 X = %d, 期望 0", x)
	}

	// 制表：跳到下一个 8 的倍数
	buf.WriteRune('\t')
	if x, _ := buf.GetCursor(); x != 8 {
		t.Errorf("\\t 后光标 X = %d, 期望 8", x)
	}

	// 制表后超出宽度：收敛到最后一列
	buf.SetCursor(18, 1)
	buf.WriteRune('\t')
	if x, _ := buf.GetCursor(); x != 19 {
		t.Errorf("超宽 \\t 后光标 X = %d, 期望 19", x)
	}

	// 控制字符本身不应写入单元格
	line, err := buf.GetLine(0)
	if err != nil {
		t.Fatalf("获取第 0 行失败: %v", err)
	}
	if strings.ContainsAny(line, "\n\r\b\t") {
		t.Errorf("第 0 行不应包含控制字符: %q", line)
	}
}

func TestGetLineOutOfRange(t *testing.T) {
	buf := New(3, 5)
	for _, row := range []int{-1, 3, 100} {
		if _, err := buf.GetLine(row); err == nil {
			t.Errorf("GetLine(%d) 应返回错误", row)
		}
	}
}

func TestGetContentWithZeroCell(t *testing.T) {
	buf := New(1, 3)
	buf.cells[0][1] = Cell{} // 零值单元格（Char == 0）按空格输出

	if content := buf.GetContent(); content != "   " {
		t.Errorf("GetContent() = %q, 期望三个空格", content)
	}
}

func TestResizeNonPositiveIgnored(t *testing.T) {
	buf := New(4, 6)
	buf.WriteRune('X')

	buf.Resize(0, 10)
	buf.Resize(10, 0)
	buf.Resize(-1, -1)

	if rows, cols := buf.GetSize(); rows != 4 || cols != 6 {
		t.Errorf("非法尺寸后大小 = (%d, %d), 期望 (4, 6)", rows, cols)
	}
	if line, _ := buf.GetLine(0); !strings.HasPrefix(line, "X") {
		t.Errorf("非法 Resize 不应丢内容, 第 0 行 = %q", line)
	}
}

func TestResizeClampsNegativeState(t *testing.T) {
	buf := New(4, 6)
	// 正常 API 不会产生负坐标，这里直接构造该状态，验证 Resize 会收敛到 0
	buf.cursorX = -3
	buf.cursorY = -2
	buf.savedCursor.x = -1
	buf.savedCursor.y = -4

	buf.Resize(8, 10)

	if x, y := buf.GetCursor(); x != 0 || y != 0 {
		t.Errorf("收敛后光标 = (%d, %d), 期望 (0, 0)", x, y)
	}
	if buf.savedCursor.x != 0 || buf.savedCursor.y != 0 {
		t.Errorf("收敛后保存光标 = (%d, %d), 期望 (0, 0)", buf.savedCursor.x, buf.savedCursor.y)
	}

	// 收敛后的状态应可正常使用
	buf.RestoreCursor()
	buf.WriteRune('Z')
	if x, y := buf.GetCursor(); x != 1 || y != 0 {
		t.Errorf("写入后光标 = (%d, %d), 期望 (1, 0)", x, y)
	}
}

func TestScrollNonPositiveAmountIsNoop(t *testing.T) {
	buf := New(4, 6)
	for i := range 4 {
		buf.SetCursor(0, i)
		buf.WriteRune(rune('a' + i))
	}
	before := buf.GetContent()

	buf.scrollUp(0)
	buf.scrollUp(-2)
	buf.scrollDown(0)
	buf.scrollDown(-3)

	if after := buf.GetContent(); after != before {
		t.Errorf("n <= 0 的滚动不应改变内容:\nbefore = %q\nafter  = %q", before, after)
	}
}

func TestReader(t *testing.T) {
	buf := New(2, 5)
	buf.WriteRune('H')
	buf.WriteRune('i')

	data, err := io.ReadAll(buf.Reader())
	if err != nil {
		t.Fatalf("读取 Reader 失败: %v", err)
	}
	if string(data) != buf.GetContent() {
		t.Errorf("Reader 内容 = %q, 期望 %q", data, buf.GetContent())
	}
	if !strings.HasPrefix(string(data), "Hi   ") {
		t.Errorf("Reader 内容 = %q, 期望以 'Hi   ' 开头", data)
	}
}
