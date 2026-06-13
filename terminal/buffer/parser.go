package buffer

import (
	"fmt"
	"strconv"
	"strings"
)

// Parser VT/XTerm 转义序列解析器，将终端输出字节流转换为 Buffer 操作。
// 状态机使用 4 个状态处理不同种类的转义序列：
//
//	stateNormal  - 普通文本内容，直接写入 Buffer
//	stateEscape  - 收到 ESC (0x1B)，等待后续命令字节
//	stateCSI     - CSI 序列 (ESC [ params... command)，最常用的控制序列
//	stateOSC     - OSC 序列 (ESC ] ... ST/BEL)，操作系统命令，暂时忽略
//
// CSI 命令包括光标移动 (A/B/C/D/H/f)、清屏 (J)、清行 (K)、
// 颜色属性 (m) 以及滚动区域设置 (r) 等终端常用控制指令
type Parser struct {
	buffer       *Buffer
	state        parserState
	params       []int
	intermediate []byte
}

type parserState int

const (
	stateNormal parserState = iota // 普通文本模式
	stateEscape                    // 已收到 ESC，等待命令
	stateCSI                       // CSI 序列：ESC [ params... cmd
	stateOSC                       // OSC 序列：ESC ] ... ST/BEL
)

// NewParser 创建一个新的解析器
func NewParser(buf *Buffer) *Parser {
	return &Parser{
		buffer: buf,
		state:  stateNormal,
		params: make([]int, 0, 8),
	}
}

// Write 实现io.Writer接口
func (p *Parser) Write(data []byte) (n int, err error) {
	for _, b := range data {
		p.processByte(b)
	}
	return len(data), nil
}

// processByte 处理单个字节
func (p *Parser) processByte(b byte) {
	switch p.state {
	case stateNormal:
		p.processNormal(b)
	case stateEscape:
		p.processEscape(b)
	case stateCSI:
		p.processCSI(b)
	case stateOSC:
		p.processOSC(b)
	}
}

// processNormal 处理普通文本字符和控制字符
func (p *Parser) processNormal(b byte) {
	switch b {
	case 0x1B: // ESC - 转义序列起始，切换到 stateEscape 等待命令
		p.state = stateEscape
		p.params = p.params[:0]
		p.intermediate = p.intermediate[:0]
	case '\n': // 换行（LF）
		p.buffer.lineFeed()
	case '\r': // 回车（CR）
		p.buffer.carriageReturn()
	case '\b': // 退格（BS）
		p.buffer.backspace()
	case '\t': // 制表符（TAB）
		p.buffer.tab()
	case 0x07: // BEL - 响铃，忽略
	default:
		// 可打印字符（范围 0x20-0x7E）直接写入缓冲区
		if b >= 0x20 && b < 0x7F {
			p.buffer.WriteRune(rune(b))
		}
	}
}

// processEscape 处理转义序列
func (p *Parser) processEscape(b byte) {
	switch b {
	case '[':
		p.state = stateCSI
	case ']':
		p.state = stateOSC
	case 'M': // 反向换行
		p.buffer.cursorY--
		if p.buffer.cursorY < p.buffer.scrollTop {
			p.buffer.scrollDown(1)
			p.buffer.cursorY = p.buffer.scrollTop
		}
		p.state = stateNormal
	case '7': // 保存光标
		p.buffer.SaveCursor()
		p.state = stateNormal
	case '8': // 恢复光标
		p.buffer.RestoreCursor()
		p.state = stateNormal
	case 'c': // 重置
		p.buffer.clearUnlocked()
		p.buffer.currentFG = DefaultFG()
		p.buffer.currentBG = DefaultBG()
		p.buffer.currentAttrs = 0
		p.state = stateNormal
	default:
		p.state = stateNormal
	}
}

// processCSI 解析 CSI 序列参数（ESC [ params... intermediate command）。
// CSI 序列格式：ESC [ [参数] [中间字节] 最终字节
//   - 数字: 累积参数值（支持多位数）
//   - ';': 参数分隔符，新增一个参数槽
//   - 0x20-0x3F: 中间字节（如空格、感叹号等，扩展命令用）
//   - 其他: 最终命令字节，触发 executeCSI 执行对应操作
func (p *Parser) processCSI(b byte) {
	if b >= '0' && b <= '9' {
		// 累积数字参数（如 ESC [ 10;20H → params=[10, 20]）
		if len(p.params) == 0 {
			p.params = append(p.params, 0)
		}
		lastIdx := len(p.params) - 1
		p.params[lastIdx] = p.params[lastIdx]*10 + int(b-'0')
	} else if b == ';' {
		// 参数分隔符，新建一个参数槽（默认值 0）
		p.params = append(p.params, 0)
	} else if b >= 0x20 && b < 0x40 {
		// 中间字节，用于扩展命令（如 ESC [ ? 25 h 的 '?'）
		p.intermediate = append(p.intermediate, b)
	} else {
		// 最终命令字节，执行对应的终端操作
		p.executeCSI(b)
		p.state = stateNormal
	}
}

// executeCSI 执行 CSI 命令。常用命令说明：
//
//	A/B/C/D - 光标上/下/右/左移动 n 行/列
//	H/f     - 设置光标位置到 (row, col)，1-based
//	J       - 清除屏幕：0=光标到末尾, 1=开头到光标, 2=全清
//	K       - 清除行：0=光标到行尾, 1=行首到光标, 2=全行
//	m       - SGR (Select Graphic Rendition)，设置颜色/属性
//	r       - 设置滚动区域 (top, bottom)
//	s/u     - 保存/恢复光标位置
func (p *Parser) executeCSI(cmd byte) {
	// 确保至少有一个参数，默认值为 0
	if len(p.params) == 0 {
		p.params = append(p.params, 0)
	}

	switch cmd {
	case 'A': // CUU - 光标上移 n 行，不超过边界
		n := p.getParam(0, 1)
		p.buffer.cursorY -= n
		if p.buffer.cursorY < 0 {
			p.buffer.cursorY = 0
		}
	case 'B': // CUD - 光标下移 n 行，不超过边界
		n := p.getParam(0, 1)
		p.buffer.cursorY += n
		if p.buffer.cursorY >= p.buffer.rows {
			p.buffer.cursorY = p.buffer.rows - 1
		}
	case 'C': // CUF - 光标右移 n 列，不超过边界
		n := p.getParam(0, 1)
		p.buffer.cursorX += n
		if p.buffer.cursorX >= p.buffer.cols {
			p.buffer.cursorX = p.buffer.cols - 1
		}
	case 'D': // CUB - 光标左移 n 列，不超过边界
		n := p.getParam(0, 1)
		p.buffer.cursorX -= n
		if p.buffer.cursorX < 0 {
			p.buffer.cursorX = 0
		}
	case 'H', 'f': // CUP - 设置光标位置 (row, col)，参数为 1-based
		row := p.getParam(0, 1) - 1
		col := p.getParam(1, 1) - 1
		if row < 0 {
			row = 0
		}
		if row >= p.buffer.rows {
			row = p.buffer.rows - 1
		}
		if col < 0 {
			col = 0
		}
		if col >= p.buffer.cols {
			col = p.buffer.cols - 1
		}
		p.buffer.cursorY = row
		p.buffer.cursorX = col
	case 'J': // ED - 清除屏幕
		mode := p.getParam(0, 0)
		p.eraseDisplay(mode)
	case 'K': // EL - 清除行
		mode := p.getParam(0, 0)
		p.eraseLine(mode)
	case 'm': // SGR - 设置图形属性（颜色、加粗、下划线等）
		p.setGraphicsMode()
	case 'r': // DECSTBM - 设置滚动区域
		top := p.getParam(0, 1) - 1
		bottom := p.getParam(1, p.buffer.rows) - 1
		p.buffer.SetScrollRegion(top, bottom)
	case 's': // SCOSC - 保存光标位置
		p.buffer.SaveCursor()
	case 'u': // SCORC - 恢复光标位置
		p.buffer.RestoreCursor()
	case 'h': // SM - 设置模式
		// 暂时忽略，如 DECSET、DECKPAM 等
	case 'l': // RM - 重置模式
		// 暂时忽略
	}
}

// processOSC 处理OSC序列 (Operating System Command)
func (p *Parser) processOSC(b byte) {
	// 简单实现：忽略OSC序列直到遇到BEL或ST
	if b == 0x07 || b == 0x9C {
		p.state = stateNormal
	}
}

// getParam 获取参数值
func (p *Parser) getParam(index, defaultValue int) int {
	if index < len(p.params) && p.params[index] > 0 {
		return p.params[index]
	}
	return defaultValue
}

// eraseDisplay 清除显示
func (p *Parser) eraseDisplay(mode int) {
	switch mode {
	case 0: // 从光标到屏幕末尾
		for j := p.buffer.cursorX; j < p.buffer.cols; j++ {
			p.buffer.cells[p.buffer.cursorY][j] = Cell{Char: ' ', FG: DefaultFG(), BG: DefaultBG()}
		}
		for i := p.buffer.cursorY + 1; i < p.buffer.rows; i++ {
			for j := 0; j < p.buffer.cols; j++ {
				p.buffer.cells[i][j] = Cell{Char: ' ', FG: DefaultFG(), BG: DefaultBG()}
			}
		}
	case 1: // 从屏幕开始到光标
		for i := 0; i < p.buffer.cursorY; i++ {
			for j := 0; j < p.buffer.cols; j++ {
				p.buffer.cells[i][j] = Cell{Char: ' ', FG: DefaultFG(), BG: DefaultBG()}
			}
		}
		for j := 0; j <= p.buffer.cursorX; j++ {
			p.buffer.cells[p.buffer.cursorY][j] = Cell{Char: ' ', FG: DefaultFG(), BG: DefaultBG()}
		}
	case 2, 3: // 整个屏幕
		p.buffer.clearUnlocked()
	}
}

// eraseLine 清除行
func (p *Parser) eraseLine(mode int) {
	switch mode {
	case 0: // 从光标到行尾
		for j := p.buffer.cursorX; j < p.buffer.cols; j++ {
			p.buffer.cells[p.buffer.cursorY][j] = Cell{Char: ' ', FG: DefaultFG(), BG: DefaultBG()}
		}
	case 1: // 从行首到光标
		for j := 0; j <= p.buffer.cursorX; j++ {
			p.buffer.cells[p.buffer.cursorY][j] = Cell{Char: ' ', FG: DefaultFG(), BG: DefaultBG()}
		}
	case 2: // 整行
		for j := 0; j < p.buffer.cols; j++ {
			p.buffer.cells[p.buffer.cursorY][j] = Cell{Char: ' ', FG: DefaultFG(), BG: DefaultBG()}
		}
	}
}

// setGraphicsMode 设置图形模式（SGR）
func (p *Parser) setGraphicsMode() {
	if len(p.params) == 0 {
		p.params = append(p.params, 0)
	}

	for i := 0; i < len(p.params); i++ {
		param := p.params[i]
		switch param {
		case 0: // 重置
			p.buffer.currentFG = DefaultFG()
			p.buffer.currentBG = DefaultBG()
			p.buffer.currentAttrs = 0
		case 1: // 粗体
			p.buffer.currentAttrs |= AttrBold
		case 2: // 暗淡
			p.buffer.currentAttrs |= AttrDim
		case 3: // 斜体
			p.buffer.currentAttrs |= AttrItalic
		case 4: // 下划线
			p.buffer.currentAttrs |= AttrUnderline
		case 5: // 闪烁
			p.buffer.currentAttrs |= AttrBlink
		case 7: // 反转
			p.buffer.currentAttrs |= AttrReverse
		case 8: // 隐藏
			p.buffer.currentAttrs |= AttrHidden
		case 9: // 删除线
			p.buffer.currentAttrs |= AttrStrikethrough
		case 22: // 正常强度
			p.buffer.currentAttrs &^= (AttrBold | AttrDim)
		case 23: // 非斜体
			p.buffer.currentAttrs &^= AttrItalic
		case 24: // 非下划线
			p.buffer.currentAttrs &^= AttrUnderline
		case 25: // 非闪烁
			p.buffer.currentAttrs &^= AttrBlink
		case 27: // 非反转
			p.buffer.currentAttrs &^= AttrReverse
		case 28: // 非隐藏
			p.buffer.currentAttrs &^= AttrHidden
		case 29: // 非删除线
			p.buffer.currentAttrs &^= AttrStrikethrough
		case 30, 31, 32, 33, 34, 35, 36, 37: // 前景色（标准）
			p.buffer.currentFG = indexColor(uint8(param - 30))
		case 38: // 前景色（扩展）
			i = p.parseExtendedColor(i, true)
		case 39: // 默认前景色
			p.buffer.currentFG = DefaultFG()
		case 40, 41, 42, 43, 44, 45, 46, 47: // 背景色（标准）
			p.buffer.currentBG = indexColor(uint8(param - 40))
		case 48: // 背景色（扩展）
			i = p.parseExtendedColor(i, false)
		case 49: // 默认背景色
			p.buffer.currentBG = DefaultBG()
		case 90, 91, 92, 93, 94, 95, 96, 97: // 前景色（高亮）
			p.buffer.currentFG = indexColor(uint8(param - 90 + 8))
		case 100, 101, 102, 103, 104, 105, 106, 107: // 背景色（高亮）
			p.buffer.currentBG = indexColor(uint8(param - 100 + 8))
		}
	}
}

// parseExtendedColor 解析扩展颜色
func (p *Parser) parseExtendedColor(index int, isFG bool) int {
	if index+1 >= len(p.params) {
		return index
	}

	colorType := p.params[index+1]
	switch colorType {
	case 5: // 256色
		if index+2 < len(p.params) {
			colorIndex := uint8(p.params[index+2])
			if isFG {
				p.buffer.currentFG = indexColor(colorIndex)
			} else {
				p.buffer.currentBG = indexColor(colorIndex)
			}
			return index + 2
		}
	case 2: // RGB色
		if index+4 < len(p.params) {
			r := uint8(p.params[index+2])
			g := uint8(p.params[index+3])
			b := uint8(p.params[index+4])
			if isFG {
				p.buffer.currentFG = Color{R: r, G: g, B: b}
			} else {
				p.buffer.currentBG = Color{R: r, G: g, B: b}
			}
			return index + 4
		}
	}

	return index + 1
}

// indexColor 将索引颜色转换为RGB true color
func indexColor(index uint8) Color {
	// 标准16色映射
	if index < 16 {
		colors := []Color{
			{R: 0, G: 0, B: 0},       // 0: 黑色
			{R: 205, G: 0, B: 0},     // 1: 红色
			{R: 0, G: 205, B: 0},     // 2: 绿色
			{R: 205, G: 205, B: 0},   // 3: 黄色
			{R: 0, G: 0, B: 238},     // 4: 蓝色
			{R: 205, G: 0, B: 205},   // 5: 品红
			{R: 0, G: 205, B: 205},   // 6: 青色
			{R: 229, G: 229, B: 229}, // 7: 白色
			{R: 127, G: 127, B: 127}, // 8: 亮黑（灰）
			{R: 255, G: 0, B: 0},     // 9: 亮红
			{R: 0, G: 255, B: 0},     // 10: 亮绿
			{R: 255, G: 255, B: 0},   // 11: 亮黄
			{R: 92, G: 92, B: 255},   // 12: 亮蓝
			{R: 255, G: 0, B: 255},   // 13: 亮品红
			{R: 0, G: 255, B: 255},   // 14: 亮青
			{R: 255, G: 255, B: 255}, // 15: 亮白
		}
		return colors[index]
	}

	// 216色立方体 (16-231)
	if index >= 16 && index <= 231 {
		idx := index - 16
		r := (idx / 36) % 6
		g := (idx / 6) % 6
		b := idx % 6

		// 将0-5映射到0,95,135,175,215,255
		toRGB := func(v uint8) uint8 {
			if v == 0 {
				return 0
			}
			return 55 + v*40
		}

		return Color{
			R: toRGB(r),
			G: toRGB(g),
			B: toRGB(b),
		}
	}

	// 24级灰度 (232-255)
	if index >= 232 {
		gray := 8 + (index-232)*10
		return Color{R: gray, G: gray, B: gray}
	}

	// 默认返回白色
	return Color{R: 255, G: 255, B: 255}
}

// ParseANSI 解析ANSI转义序列（辅助函数）
func ParseANSI(s string) string {
	// 移除所有ANSI转义序列
	var result strings.Builder
	inEscape := false
	inCSI := false

	for _, r := range s {
		if r == 0x1B {
			inEscape = true
			continue
		}
		if inEscape {
			if r == '[' {
				inCSI = true
				inEscape = false
				continue
			}
			inEscape = false
			continue
		}
		if inCSI {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inCSI = false
			}
			continue
		}
		result.WriteRune(r)
	}

	return result.String()
}

// FormatSGR 格式化SGR参数为ANSI序列
func FormatSGR(params ...int) string {
	if len(params) == 0 {
		return "\x1b[0m"
	}

	var parts []string
	for _, p := range params {
		parts = append(parts, strconv.Itoa(p))
	}

	return fmt.Sprintf("\x1b[%sm", strings.Join(parts, ";"))
}
