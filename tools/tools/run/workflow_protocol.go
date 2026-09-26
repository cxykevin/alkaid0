package run

import (
	"bytes"
	"encoding/json"
)

const (
	workflowPasteStart = "\x1b[?2004h"
	workflowPasteEnd   = "\x1b[?2004l"

	// workflowRunMarker Flow.run() 启动时输出的一行运行标记（\x1e dynworkflow \x1f）。
	// 服务端不依赖它判定 workflow（判定见 python_task 的源码检测），这里只负责把
	// 整行从终端输出中剔除，避免控制字符进入内容快照。
	workflowRunMarker = "\x1edynworkflow\x1f"

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
	// dropMarkerEOL run marker 行尾的 \r?\n 尚未到达（标记与换行可能分属两次 read）。
	dropMarkerEOL bool
}

func (p *WorkflowOutputParser) Feed(chunk []byte) (string, []WorkflowEvent) {
	if len(chunk) > 0 {
		p.pending.Write(chunk)
	}
	var visible bytes.Buffer
	var events []WorkflowEvent
	// 标记行尾的换行可能在后续 read 才到达：先补上，避免多出一行空行。
	if p.dropMarkerEOL {
		p.consumeMarkerEOL()
	}
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
		startIdx := bytes.Index(data, []byte(workflowPasteStart))
		markerIdx := bytes.Index(data, []byte(workflowRunMarker))
		if markerIdx >= 0 && (startIdx < 0 || markerIdx < startIdx) {
			// 运行标记整行剔除；标记与换行可能分片，剩余部分交给 consumeMarkerEOL。
			visible.Write(data[:markerIdx])
			p.pending.Next(markerIdx + len(workflowRunMarker))
			p.dropMarkerEOL = true
			p.consumeMarkerEOL()
			continue
		}
		if startIdx >= 0 {
			visible.Write(data[:startIdx])
			p.pending.Next(startIdx + len(workflowPasteStart))
			p.inFrame = true
			continue
		}
		// 保留可能被切断的标记尾巴：粘贴标记沿用固定长度；run marker 只在数据
		// 末尾确实是它的前缀时才多留，避免无谓地缓冲普通输出。
		keep := len(workflowPasteStart) - 1
		if n := markerPrefixLen(data, workflowRunMarker); n > keep {
			keep = n
		}
		if len(data) > keep {
			visible.Write(data[:len(data)-keep])
			p.pending.Next(len(data) - keep)
		}
		break
	}
	return visible.String(), events
}

// markerPrefixLen 返回 data 末尾最长的、同时是 marker 前缀的后缀长度
// （不含完整 marker 本身）；没有这种后缀时返回 0。
func markerPrefixLen(data []byte, marker string) int {
	limit := len(marker) - 1
	if limit > len(data) {
		limit = len(data)
	}
	for n := limit; n > 0; n-- {
		if bytes.HasPrefix([]byte(marker), data[len(data)-n:]) {
			return n
		}
	}
	return 0
}

// consumeMarkerEOL 吃掉 run marker 行尾的 \r?\n。换行尚未到达时保持
// dropMarkerEOL，由下一次 Feed 继续处理。
func (p *WorkflowOutputParser) consumeMarkerEOL() {
	data := p.pending.Bytes()
	if len(data) == 0 {
		return
	}
	if data[0] == '\r' {
		p.pending.Next(1)
		data = p.pending.Bytes()
		if len(data) == 0 {
			return
		}
	}
	if data[0] == '\n' {
		p.pending.Next(1)
	}
	p.dropMarkerEOL = false
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
