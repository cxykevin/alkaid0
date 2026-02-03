package buffer

import (
	"bytes"
	"fmt"
	"io"
	"sync"
)

// Cell 表示终端中的一个字符单元
type Cell struct {
	Char  rune
	FG    Color // 前景色
	BG    Color // 背景色
	Attrs Attributes
}

// Color 表示颜色（统一使用RGB true color）
type Color struct {
	R, G, B uint8
}

// Attributes 表示字符属性
type Attributes uint16

const (
	AttrBold Attributes = 1 << iota
	AttrDim
	AttrItalic
	AttrUnderline
	AttrBlink
	AttrReverse
	AttrHidden
	AttrStrikethrough
)

// Buffer 表示终端缓冲区
type Buffer struct {
	rows    int
	cols    int
	cells   [][]Cell
	cursorX int
	cursorY int
	
	// 当前属性
	currentFG    Color
	currentBG    Color
	currentAttrs Attributes
	
	// 滚动区域
	scrollTop    int
	scrollBottom int
	
	// 状态
	mu           sync.RWMutex
	savedCursor  struct{ x, y int }
	
	// 解析器状态
	parser *Parser
}

// New 创建一个新的终端缓冲区
func New(rows, cols int) *Buffer {
	if rows <= 0 {
		rows = 24
	}
	if cols <= 0 {
		cols = 80
	}

	cells := make([][]Cell, rows)
	for i := range cells {
		cells[i] = make([]Cell, cols)
		for j := range cells[i] {
			cells[i][j] = Cell{
				Char: ' ',
				FG:   DefaultFG(),
				BG:   DefaultBG(),
			}
		}
	}

	b := &Buffer{
		rows:         rows,
		cols:         cols,
		cells:        cells,
		cursorX:      0,
		cursorY:      0,
		currentFG:    DefaultFG(),
		currentBG:    DefaultBG(),
		scrollTop:    0,
		scrollBottom: rows - 1,
	}
	
	b.parser = NewParser(b)
	return b
}

// DefaultFG 返回默认前景色
func DefaultFG() Color {
	return Color{R: 255, G: 255, B: 255}
}

// DefaultBG 返回默认背景色
func DefaultBG() Color {
	return Color{R: 0, G: 0, B: 0}
}

// Write 实现io.Writer接口，处理VT/XTerm序列
func (b *Buffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	return b.parser.Write(p)
}

// WriteRune 写入一个字符
func (b *Buffer) WriteRune(r rune) {
	if r == '\n' {
		b.lineFeed()
		return
	}
	if r == '\r' {
		b.carriageReturn()
		return
	}
	if r == '\b' {
		b.backspace()
		return
	}
	if r == '\t' {
		b.tab()
		return
	}

	// 写入字符
	if b.cursorY >= 0 && b.cursorY < b.rows && b.cursorX >= 0 && b.cursorX < b.cols {
		b.cells[b.cursorY][b.cursorX] = Cell{
			Char:  r,
			FG:    b.currentFG,
			BG:    b.currentBG,
			Attrs: b.currentAttrs,
		}
		b.cursorX++
		
		// 自动换行
		if b.cursorX >= b.cols {
			b.cursorX = 0
			b.cursorY++
			if b.cursorY > b.scrollBottom {
				b.scrollUp(1)
				b.cursorY = b.scrollBottom
			}
		}
	}
}

// GetCell 获取指定位置的单元格
func (b *Buffer) GetCell(row, col int) (Cell, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	
	if row < 0 || row >= b.rows || col < 0 || col >= b.cols {
		return Cell{}, fmt.Errorf("坐标越界: (%d, %d)", row, col)
	}
	
	return b.cells[row][col], nil
}

// GetLine 获取指定行的内容
func (b *Buffer) GetLine(row int) (string, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	
	if row < 0 || row >= b.rows {
		return "", fmt.Errorf("行号越界: %d", row)
	}
	
	var buf bytes.Buffer
	for _, cell := range b.cells[row] {
		if cell.Char != 0 {
			buf.WriteRune(cell.Char)
		}
	}
	
	return buf.String(), nil
}

// GetContent 获取整个缓冲区的内容
func (b *Buffer) GetContent() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	
	var buf bytes.Buffer
	for i := 0; i < b.rows; i++ {
		for j := 0; j < b.cols; j++ {
			if b.cells[i][j].Char != 0 {
				buf.WriteRune(b.cells[i][j].Char)
			} else {
				buf.WriteRune(' ')
			}
		}
		if i < b.rows-1 {
			buf.WriteRune('\n')
		}
	}
	
	return buf.String()
}

// Clear 清空缓冲区
func (b *Buffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.clearUnlocked()
}

// clearUnlocked 清空缓冲区（不加锁版本，内部使用）
func (b *Buffer) clearUnlocked() {
	for i := 0; i < b.rows; i++ {
		for j := 0; j < b.cols; j++ {
			b.cells[i][j] = Cell{
				Char: ' ',
				FG:   DefaultFG(),
				BG:   DefaultBG(),
			}
		}
	}
	b.cursorX = 0
	b.cursorY = 0
}

// Resize 调整缓冲区大小
func (b *Buffer) Resize(rows, cols int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	if rows <= 0 || cols <= 0 {
		return
	}
	
	newCells := make([][]Cell, rows)
	for i := range newCells {
		newCells[i] = make([]Cell, cols)
		for j := range newCells[i] {
			if i < b.rows && j < b.cols {
				newCells[i][j] = b.cells[i][j]
			} else {
				newCells[i][j] = Cell{
					Char: ' ',
					FG:   DefaultFG(),
					BG:   DefaultBG(),
				}
			}
		}
	}
	
	b.cells = newCells
	b.rows = rows
	b.cols = cols
	b.scrollBottom = rows - 1
	
	// 调整光标位置
	if b.cursorX >= cols {
		b.cursorX = cols - 1
	}
	if b.cursorY >= rows {
		b.cursorY = rows - 1
	}
}

// GetSize 获取缓冲区大小
func (b *Buffer) GetSize() (rows, cols int) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.rows, b.cols
}

// GetCursor 获取光标位置
func (b *Buffer) GetCursor() (x, y int) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.cursorX, b.cursorY
}

// SetCursor 设置光标位置
func (b *Buffer) SetCursor(x, y int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	if x >= 0 && x < b.cols {
		b.cursorX = x
	}
	if y >= 0 && y < b.rows {
		b.cursorY = y
	}
}

// 内部方法

func (b *Buffer) lineFeed() {
	b.cursorY++
	if b.cursorY > b.scrollBottom {
		b.scrollUp(1)
		b.cursorY = b.scrollBottom
	}
}

func (b *Buffer) carriageReturn() {
	b.cursorX = 0
}

func (b *Buffer) backspace() {
	if b.cursorX > 0 {
		b.cursorX--
	}
}

func (b *Buffer) tab() {
	// 移动到下一个8的倍数位置
	b.cursorX = ((b.cursorX / 8) + 1) * 8
	if b.cursorX >= b.cols {
		b.cursorX = b.cols - 1
	}
}

func (b *Buffer) scrollUp(n int) {
	if n <= 0 {
		return
	}
	
	// 向上滚动n行
	for i := b.scrollTop; i <= b.scrollBottom-n; i++ {
		copy(b.cells[i], b.cells[i+n])
	}
	
	// 清空底部n行
	for i := b.scrollBottom - n + 1; i <= b.scrollBottom; i++ {
		for j := 0; j < b.cols; j++ {
			b.cells[i][j] = Cell{
				Char: ' ',
				FG:   DefaultFG(),
				BG:   DefaultBG(),
			}
		}
	}
}

func (b *Buffer) scrollDown(n int) {
	if n <= 0 {
		return
	}
	
	// 向下滚动n行
	for i := b.scrollBottom; i >= b.scrollTop+n; i-- {
		copy(b.cells[i], b.cells[i-n])
	}
	
	// 清空顶部n行
	for i := b.scrollTop; i < b.scrollTop+n; i++ {
		for j := 0; j < b.cols; j++ {
			b.cells[i][j] = Cell{
				Char: ' ',
				FG:   DefaultFG(),
				BG:   DefaultBG(),
			}
		}
	}
}

// SaveCursor 保存光标位置
func (b *Buffer) SaveCursor() {
	b.savedCursor.x = b.cursorX
	b.savedCursor.y = b.cursorY
}

// RestoreCursor 恢复光标位置
func (b *Buffer) RestoreCursor() {
	b.cursorX = b.savedCursor.x
	b.cursorY = b.savedCursor.y
}

// SetScrollRegion 设置滚动区域
func (b *Buffer) SetScrollRegion(top, bottom int) {
	if top >= 0 && top < b.rows && bottom >= top && bottom < b.rows {
		b.scrollTop = top
		b.scrollBottom = bottom
	}
}

// Reader 返回一个可以读取缓冲区内容的Reader
func (b *Buffer) Reader() io.Reader {
	return bytes.NewBufferString(b.GetContent())
}
