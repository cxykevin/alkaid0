package buffer

import (
	"strings"
	"testing"
)

// TestParserMalformedSequencesDoNotPanic 表驱动回归：畸形/超长转义序列不得让光标
// 越界，也不得在后续写入/清屏操作中 panic。
//
// 修复前：CSI 参数按 int 累加会溢出成巨值甚至负数（例如 ESC [ 9223372036854775807 C
// 让光标变成负数），eraseLine/eraseDisplay 直接用 cells[cursorY][cursorX] 索引 → panic。
func TestParserMalformedSequencesDoNotPanic(t *testing.T) {
	long := strings.Repeat("9", 64)
	cases := []struct {
		name  string
		input string
	}{
		{"CSI 巨值右移", "\x1b[3C\x1b[" + long + "C\x1b[K"},
		{"CSI 巨值左移", "\x1b[3C\x1b[" + long + "D\x1b[K"},
		{"CSI 巨值下移", "\x1b[2B\x1b[" + long + "B\x1b[J"},
		{"CSI 巨值上移", "\x1b[2B\x1b[" + long + "A\x1b[J"},
		{"CSI MaxInt64 右移", "\x1b[3C\x1b[9223372036854775807C\x1b[K"},
		{"CSI MaxInt64 下移", "\x1b[2B\x1b[9223372036854775807B\x1b[J"},
		{"CSI 巨值定位", "\x1b[" + long + ";" + long + "H\x1b[K"},
		{"CSI 巨值滚动区域", "\x1b[" + long + ";" + long + "r\x1bM"},
		{"CSI 参数洪水", "\x1b[" + strings.Repeat("9;", 10000) + "m"},
		{"CSI 截断", "\x1b[12;34"},
		{"CSI 空", "\x1b["},
		{"ESC 孤立", "\x1b"},
		{"OSC 无终止", "\x1b]0;title"},
		{"OSC ST 终止后输出", "\x1b]0;title\x1b\\after"},
		{"OSC ESC 非 ST", "\x1b]0;a\x1bXb\x07after"},
		{"非法 UTF-8", "\x80\x80\xff\xfeX"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := New(24, 80)
			buf.Write([]byte(tc.input))
			// 光标越界时，后续写入/清屏会直接索引 cells[负数或超界]
			buf.Write([]byte("X\x1b[K\x1b[J"))
			x, y := buf.GetCursor()
			if x < 0 || x >= 80 || y < 0 || y >= 24 {
				t.Fatalf("畸形序列后光标越界: (%d, %d)", x, y)
			}
			_ = buf.GetContent()
		})
	}
}

// TestResizeConvergesScrollRegion 回归：Resize 后的滚动区域必须收敛到新尺寸内。
// 修复前：Resize 只重设 scrollBottom，scrollTop 可能大于新行数，
// 之后 ESC M（RI）触发 scrollDown 会用越界行号索引 cells → panic。
func TestResizeConvergesScrollRegion(t *testing.T) {
	buf := New(24, 80)
	buf.Write([]byte("\x1b[21;24r")) // 滚动区域为第 21~24 行
	buf.Resize(10, 80)
	buf.Write([]byte("line1\n\x1bMy")) // RI（ESC M）+ 文本：修复前在这里 panic
	_ = buf.GetContent()
}

// TestResizeConvergesSavedCursor 回归：Resize 后保存/恢复的光标不能指向越界坐标。
// 修复前：savedCursor 不收敛，ESC 8 恢复出 (79, 23)，随后 ESC [ 1 K
// （eraseLine(1)）用越界行号索引 cells → panic。
func TestResizeConvergesSavedCursor(t *testing.T) {
	buf := New(24, 80)
	buf.Write([]byte("\x1b[24;80H\x1b7")) // 光标移到 (23,79) 并保存
	buf.Resize(10, 40)
	buf.Write([]byte("\x1b8\x1b[1K"))
	_ = buf.GetContent()
}

// TestOSCTerminatedByST 回归：OSC 以 ST（ESC 反斜杠）结束时必须回到普通状态。
// 修复前：解析器只认 BEL，OSC 状态永不退出，后续输出全部被吞掉。
func TestOSCTerminatedByST(t *testing.T) {
	buf := New(24, 80)
	buf.Write([]byte("\x1b]0;window title\x1b\\visible-after-osc"))
	content := buf.GetContent()
	if !strings.Contains(content, "visible-after-osc") {
		t.Fatalf("ST 结束的 OSC 之后输出被吞掉: %q", content)
	}
	if strings.Contains(content, "window title") {
		t.Errorf("OSC 内容不应出现在终端内容里: %q", content)
	}

	// ST 可能跨 read 分片
	buf2 := New(24, 80)
	buf2.Write([]byte("\x1b]0;title\x1b"))
	buf2.Write([]byte("\\tail"))
	if !strings.Contains(buf2.GetContent(), "tail") {
		t.Fatalf("分片 ST 未识别，输出被吞掉: %q", buf2.GetContent())
	}

	// BEL 与 8 位 ST（0x9C）仍然有效
	buf3 := New(24, 80)
	buf3.Write([]byte("\x1b]0;a\x07b\x1b]0;c\x9cd"))
	c := buf3.GetContent()
	if !strings.Contains(c, "b") || !strings.Contains(c, "d") {
		t.Fatalf("BEL/8bit ST 结束的 OSC 之后输出被吞掉: %q", c)
	}
}

// FuzzParserNoPanic 用任意字节流喂解析器（含随机 Resize），保证畸形输入不会 panic、
// 光标不会越界。
func FuzzParserNoPanic(f *testing.F) {
	f.Add([]byte("\x1b[2J\x1b[10;20Hhello\x1b[31mred\x1b[0m"))
	f.Add([]byte("\x1b]0;title\x1b\\after"))
	f.Add([]byte("\x1b[" + strings.Repeat("9", 64) + "C"))
	f.Add([]byte("\x1b[21;24r\x1bM"))
	f.Fuzz(func(t *testing.T, data []byte) {
		buf := New(24, 80)
		if len(data) >= 2 {
			buf.Resize(int(data[0])%32+1, int(data[1])%100+1)
		}
		buf.Write(data)
		buf.Write([]byte("X\x1b[K\x1b[J\x1b8\x1bM\x1b[99999999999999999999C"))
		rows, cols := buf.GetSize()
		x, y := buf.GetCursor()
		if x < 0 || x >= cols || y < 0 || y >= rows {
			t.Fatalf("光标越界: (%d, %d), 缓冲区 (%d, %d)", x, y, rows, cols)
		}
		_ = buf.GetContent()
	})
}
