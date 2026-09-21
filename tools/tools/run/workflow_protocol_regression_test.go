package run

import (
	"bytes"
	"strings"
	"testing"
)

// TestWorkflowParserUnterminatedFrameKeepsOutput 回归：未闭合的握手帧在 Flush 时
// 不能把内容吞掉（此前直接 frame.Reset() 丢弃，命令崩溃在帧中途时整段输出全丢）。
func TestWorkflowParserUnterminatedFrameKeepsOutput(t *testing.T) {
	var p WorkflowOutputParser
	p.Feed([]byte(workflowPasteStart + `{"type":"node"}` + "\n" + "plain-line"))
	tail := p.Flush()
	if !strings.Contains(tail, "plain-line") {
		t.Fatalf("未闭合帧的内容被吞掉: %q", tail)
	}
}

// TestWorkflowParserBoundsFrame 回归：握手帧必须有缓冲上限。
// 修复前 frame 无上限，Python 侧只发开始标记（崩溃、协议实现错误或被恶意构造）
// 就会把服务端内存吃光，而且缓冲内容最终被静默丢弃。
func TestWorkflowParserBoundsFrame(t *testing.T) {
	const frameCap = 1 << 20
	var p WorkflowOutputParser
	if visible, _ := p.Feed([]byte(workflowPasteStart)); visible != "" {
		t.Fatalf("开始标记不应产生可见输出: %q", visible)
	}
	big := bytes.Repeat([]byte("A"), 2<<20)
	visible, events := p.Feed(big)
	if len(events) != 0 {
		t.Fatalf("超限帧不应产生事件: %#v", events)
	}
	if p.frame.Len() > frameCap {
		t.Fatalf("帧缓冲无上限: %d 字节", p.frame.Len())
	}
	if len(visible) < frameCap {
		t.Fatalf("超限帧内容被吞掉: 可见输出仅 %d 字节", len(visible))
	}
	if p.inFrame {
		t.Error("超限后应结束帧状态，避免继续无界累积")
	}
}

// TestWorkflowParserSurfacesUnparsedLines 回归：帧内无法解析为协议事件的行
// 不能静默丢弃（此前只返回解析成功的 events，非法行既不进终端输出也不进事件）。
func TestWorkflowParserSurfacesUnparsedLines(t *testing.T) {
	var p WorkflowOutputParser
	visible, events := p.Feed([]byte(workflowPasteStart + "not-json" + "\n" + `{"type":"node"}` + "\n" + workflowPasteEnd))
	if len(events) != 1 || events[0].Type != "node" {
		t.Fatalf("events=%#v", events)
	}
	if !strings.Contains(visible, "not-json") {
		t.Fatalf("无法解析的协议行被静默丢弃: %q", visible)
	}
}
