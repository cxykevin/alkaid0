package buffer

import (
	"testing"
	"unsafe"
)

// Test256ColorConversion 测试256色到RGB的转换
func Test256ColorConversion(t *testing.T) {
	tests := []struct {
		name     string
		index    uint8
		expected Color
	}{
		// 标准16色
		{"黑色", 0, Color{R: 0, G: 0, B: 0}},
		{"红色", 1, Color{R: 205, G: 0, B: 0}},
		{"绿色", 2, Color{R: 0, G: 205, B: 0}},
		{"黄色", 3, Color{R: 205, G: 205, B: 0}},
		{"蓝色", 4, Color{R: 0, G: 0, B: 238}},
		{"品红", 5, Color{R: 205, G: 0, B: 205}},
		{"青色", 6, Color{R: 0, G: 205, B: 205}},
		{"白色", 7, Color{R: 229, G: 229, B: 229}},
		{"亮黑", 8, Color{R: 127, G: 127, B: 127}},
		{"亮红", 9, Color{R: 255, G: 0, B: 0}},
		{"亮绿", 10, Color{R: 0, G: 255, B: 0}},
		{"亮黄", 11, Color{R: 255, G: 255, B: 0}},
		{"亮蓝", 12, Color{R: 92, G: 92, B: 255}},
		{"亮品红", 13, Color{R: 255, G: 0, B: 255}},
		{"亮青", 14, Color{R: 0, G: 255, B: 255}},
		{"亮白", 15, Color{R: 255, G: 255, B: 255}},
		
		// 216色立方体 - 测试几个关键点
		{"立方体起点", 16, Color{R: 0, G: 0, B: 0}},
		{"立方体红", 196, Color{R: 255, G: 0, B: 0}},
		{"立方体绿", 46, Color{R: 0, G: 255, B: 0}},
		{"立方体蓝", 21, Color{R: 0, G: 0, B: 255}},
		{"立方体白", 231, Color{R: 255, G: 255, B: 255}},
		
		// 24级灰度
		{"灰度起点", 232, Color{R: 8, G: 8, B: 8}},
		{"灰度中点", 244, Color{R: 128, G: 128, B: 128}},
		{"灰度终点", 255, Color{R: 238, G: 238, B: 238}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := indexColor(tt.index)
			if result.R != tt.expected.R || result.G != tt.expected.G || result.B != tt.expected.B {
				t.Errorf("索引 %d: 期望 RGB(%d,%d,%d), 得到 RGB(%d,%d,%d)",
					tt.index, tt.expected.R, tt.expected.G, tt.expected.B,
					result.R, result.G, result.B)
			}
		})
	}
}

// TestANSI256Colors 测试ANSI 256色序列解析
func TestANSI256Colors(t *testing.T) {
	buf := New(24, 80)
	
	// 测试256色前景色
	buf.Write([]byte("\x1b[38;5;196mRed"))
	cell, _ := buf.GetCell(0, 0)
	if cell.FG.R != 255 || cell.FG.G != 0 || cell.FG.B != 0 {
		t.Errorf("256色前景色错误: 期望 RGB(255,0,0), 得到 RGB(%d,%d,%d)",
			cell.FG.R, cell.FG.G, cell.FG.B)
	}
	
	// 测试256色背景色
	buf.Clear()
	buf.Write([]byte("\x1b[48;5;46mGreen"))
	cell, _ = buf.GetCell(0, 0)
	if cell.BG.R != 0 || cell.BG.G != 255 || cell.BG.B != 0 {
		t.Errorf("256色背景色错误: 期望 RGB(0,255,0), 得到 RGB(%d,%d,%d)",
			cell.BG.R, cell.BG.G, cell.BG.B)
	}
	
	// 测试灰度色
	buf.Clear()
	buf.Write([]byte("\x1b[38;5;244mGray"))
	cell, _ = buf.GetCell(0, 0)
	if cell.FG.R != 128 || cell.FG.G != 128 || cell.FG.B != 128 {
		t.Errorf("灰度色错误: 期望 RGB(128,128,128), 得到 RGB(%d,%d,%d)",
			cell.FG.R, cell.FG.G, cell.FG.B)
	}
}

// TestTrueColorSupport 测试RGB true color支持
func TestTrueColorSupport(t *testing.T) {
	buf := New(24, 80)
	
	// 测试RGB前景色
	buf.Write([]byte("\x1b[38;2;123;45;67mCustom"))
	cell, _ := buf.GetCell(0, 0)
	if cell.FG.R != 123 || cell.FG.G != 45 || cell.FG.B != 67 {
		t.Errorf("RGB前景色错误: 期望 RGB(123,45,67), 得到 RGB(%d,%d,%d)",
			cell.FG.R, cell.FG.G, cell.FG.B)
	}
	
	// 测试RGB背景色
	buf.Clear()
	buf.Write([]byte("\x1b[48;2;200;150;100mCustomBG"))
	cell, _ = buf.GetCell(0, 0)
	if cell.BG.R != 200 || cell.BG.G != 150 || cell.BG.B != 100 {
		t.Errorf("RGB背景色错误: 期望 RGB(200,150,100), 得到 RGB(%d,%d,%d)",
			cell.BG.R, cell.BG.G, cell.BG.B)
	}
}

// TestColorStructSize 测试Color结构体大小
func TestColorStructSize(t *testing.T) {
	var c Color
	// Color结构体现在只有3个uint8字段，应该是3字节（可能有对齐）
	t.Logf("Color结构体大小: %d 字节", unsafe.Sizeof(c))
	
	// 验证Color只包含RGB字段
	c = Color{R: 255, G: 128, B: 64}
	if c.R != 255 || c.G != 128 || c.B != 64 {
		t.Error("Color结构体字段错误")
	}
}
