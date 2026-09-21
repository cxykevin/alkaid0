package run

import (
	"bytes"
	"encoding/json"
)

const (
	workflowPasteStart = "\x1b[?2004h"
	workflowPasteEnd   = "\x1b[?2004l"

	// maxWorkflowFrameBytes workflow 握手帧的缓冲上限。帧内内容在收到结束标记前
	// 会一直累积：Python 侧只发开始标记（崩溃、协议实现错误或被恶意构造）时，
	// 没有上限就会把服务端内存吃光。超限后把已缓冲内容作为可见输出返回并结束
	// 帧状态：内存有界，且不静默吞掉输出。
	maxWorkflowFrameBytes = 1 << 20
)

type WorkflowEvent struct {
	Type string
	Raw  json.RawMessage
	Data map[string]any
}

type WorkflowOutputParser struct {
	pending bytes.Buffer
	frame   bytes.Buffer
	inFrame bool
}

func (p *WorkflowOutputParser) Feed(chunk []byte) (string, []WorkflowEvent) {
	if len(chunk) > 0 {
		p.pending.Write(chunk)
	}
	var visible bytes.Buffer
	var events []WorkflowEvent
	for p.pending.Len() > 0 {
		data := p.pending.Bytes()
		if p.inFrame {
			if i := bytes.Index(data, []byte(workflowPasteEnd)); i >= 0 {
				p.frame.Write(data[:i])
				p.pending.Next(i + len(workflowPasteEnd))
				p.inFrame = false
				events = append(events, parseFrame(p.frame.Bytes(), &visible)...)
				p.frame.Reset()
				continue
			}
			keep := len(workflowPasteEnd) - 1
			if len(data) > keep {
				p.frame.Write(data[:len(data)-keep])
				p.pending.Next(len(data) - keep)
			}
			// 帧超限：协议已不可信，把已缓冲内容作为可见输出返回（不静默吞掉），
			// 并结束帧状态，避免内存无界增长。
			if p.frame.Len() >= maxWorkflowFrameBytes {
				visible.Write(p.frame.Bytes())
				visible.WriteString("\n[alkaid0] workflow frame exceeded limit, remaining content treated as plain output\n")
				p.frame.Reset()
				p.inFrame = false
			}
			break
		}
		if i := bytes.Index(data, []byte(workflowPasteStart)); i >= 0 {
			visible.Write(data[:i])
			p.pending.Next(i + len(workflowPasteStart))
			p.inFrame = true
			continue
		}
		keep := len(workflowPasteStart) - 1
		if len(data) > keep {
			visible.Write(data[:len(data)-keep])
			p.pending.Next(len(data) - keep)
		}
		break
	}
	return visible.String(), events
}

// Flush 返回命令结束时仍未发布的可见输出。
// 未闭合的握手帧无法确认是协议内容：按普通输出返回，不能静默丢弃
// （此前直接 frame.Reset()，命令崩溃在帧中途时整段输出消失）。
func (p *WorkflowOutputParser) Flush() string {
	var out bytes.Buffer
	if p.inFrame {
		out.Write(p.frame.Bytes())
		p.frame.Reset()
		p.inFrame = false
	}
	out.Write(p.pending.Bytes())
	p.pending.Reset()
	return out.String()
}

// parseFrame 解析一个已闭合的握手帧：可识别的事件进 events，无法解析为协议事件
// 的行写入 visible（此前被静默丢弃：既不出现在终端输出里，也没有对应事件）。
func parseFrame(frame []byte, visible *bytes.Buffer) []WorkflowEvent {
	events, unparsed := parseWorkflowEvents(frame)
	if unparsed != "" {
		visible.WriteString(unparsed)
	}
	return events
}

// parseWorkflowEvents 解析帧内 JSONL 事件；无法解析为协议事件的行通过第二个
// 返回值交回调用方，由调用方作为可见输出保留。
func parseWorkflowEvents(frame []byte) ([]WorkflowEvent, string) {
	var result []WorkflowEvent
	var unparsed bytes.Buffer
	for len(frame) > 0 {
		frame = bytes.TrimLeft(frame, " \t\r\n")
		if len(frame) == 0 {
			break
		}
		line := frame
		if i := bytes.IndexByte(frame, '\n'); i >= 0 {
			line, frame = frame[:i], frame[i+1:]
		} else {
			frame = nil
		}
		var data map[string]any
		if json.Unmarshal(bytes.TrimSpace(line), &data) == nil {
			if typ, ok := data["type"].(string); ok && typ != "" {
				result = append(result, WorkflowEvent{Type: typ, Raw: append(json.RawMessage(nil), bytes.TrimSpace(line)...), Data: data})
				continue
			}
		}
		unparsed.Write(bytes.TrimSpace(line))
		unparsed.WriteByte('\n')
	}
	return result, unparsed.String()
}
