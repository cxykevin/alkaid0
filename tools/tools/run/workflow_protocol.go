package run

import (
	"bytes"
	"encoding/json"
)

const (
	workflowPasteStart = "\x1b[?2004h"
	workflowPasteEnd   = "\x1b[?2004l"
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
				events = append(events, parseWorkflowEvents(p.frame.Bytes())...)
				p.frame.Reset()
				continue
			}
			keep := len(workflowPasteEnd) - 1
			if len(data) > keep {
				p.frame.Write(data[:len(data)-keep])
				p.pending.Next(len(data) - keep)
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

func (p *WorkflowOutputParser) Flush() string {
	if p.inFrame {
		p.frame.Reset()
		p.inFrame = false
	}
	out := p.pending.String()
	p.pending.Reset()
	return out
}

func parseWorkflowEvents(frame []byte) []WorkflowEvent {
	var result []WorkflowEvent
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
			}
		}
	}
	return result
}
