package structs

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	u "github.com/cxykevin/alkaid0/utils"
)

// ToolCallingInfoType 工具调用参数块（alkaid0 私有扩展）的 content type。
const ToolCallingInfoType = "alk.cxykevin.top/calling_info"

// RenderToolCallingText 把工具调用的**完整原始参数**渲染为展示文本：
// 每个参数一行 "Key: value"（键排序；值为 null/缺失的跳过；非字符串值用 JSON 表示）。
//
// 直播（工具 OnHook 写入 content）与 session/resume 历史回放共用这一份渲染，
// 因此同一个工具调用在两端展示逐字节一致。参数一律不省略——面向 AI 的历史回放
// 同样回放完整参数（provider/request/build.replayToolArguments）。
func RenderToolCallingText(params any) string {
	args := NormalizeToolCallingParams(params)
	m, ok := args.(map[string]any)
	if !ok || len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var sb strings.Builder
	for _, k := range keys {
		if k == "" || m[k] == nil {
			continue
		}
		sb.WriteString(paramLabel(k) + ": " + paramText(m[k]) + "\n")
	}
	return sb.String()
}

// paramLabel 参数名首字母大写（与工具自建文本 "Command: xxx" 的风格一致）；
// 按 rune 处理，避免非 ASCII 参数名被截断。
func paramLabel(key string) string {
	first, size := utf8.DecodeRuneInString(key)
	return string(unicode.ToUpper(first)) + key[size:]
}

// paramText 参数的展示值：字符串原样使用，其它类型用 JSON 表示。
func paramText(val any) string {
	if s, ok := val.(string); ok {
		return s
	}
	if b, err := json.Marshal(val); err == nil {
		return string(b)
	}
	return fmt.Sprintf("%v", val)
}

// NormalizeToolCallingParams 把原始参数归一化为 JSON 形态的对象（map[string]any）：
// 解指针、抹平库内 Slot 类型。直播侧拿到的参数是指针 map，落库回放侧拿到的是
// JSON 解出的对象，两者经此归一化后逐字节一致，直播与回放的 content 才能完全相同。
// nil / 非法输入返回 nil。
func NormalizeToolCallingParams(params any) any {
	if params == nil {
		return nil
	}
	var plain map[string]any
	switch v := params.(type) {
	case map[string]any:
		plain = v
	case u.H:
		plain = map[string]any(v)
	case map[string]*any:
		plain = make(map[string]any, len(v))
		for k, p := range v {
			if p == nil {
				plain[k] = nil
				continue
			}
			plain[k] = *p
		}
	default:
		plain = map[string]any{}
	}
	b, err := json.Marshal(plain)
	if err != nil {
		return nil
	}
	out := map[string]any{}
	if err := json.Unmarshal(b, &out); err != nil {
		return nil
	}
	return out
}

// NormalizeToolCallingContent 规范化工具调用的展示 content：
//   - 文本块（type="content"）的文本替换为 RenderToolCallingText(完整原始参数)；
//   - alk.cxykevin.top/calling_info 的 args 替换为完整原始参数；
//   - 其它块（edit 的 diff、terminal 等）原样保留；没有文本块时补一个。
//
// 规范化发生在写入 ToolCallingContext 时（即直播广播与落库之前），
// 因此广播出去的 content 与 session/resume 回放的 content 必然一致。
// raw 为 nil 或 content 不是 []u.H 时原样返回。
func NormalizeToolCallingContent(content any, raw any) any {
	if raw == nil || content == nil {
		return content
	}
	blocks, ok := content.([]u.H)
	if !ok {
		return content
	}
	text := RenderToolCallingText(raw)
	outCap := len(blocks)
	if outCap < math.MaxInt {
		outCap++
	}
	out := make([]u.H, 0, outCap)
	textReplaced := false
	for _, block := range blocks {
		cloneSize := len(block)
		if cloneSize < math.MaxInt {
			cloneSize++
		}
		clone := make(u.H, cloneSize)
		maps.Copy(clone, block)
		switch typ, _ := block["type"].(string); typ {
		case "content":
			// 只替换第一段文本（工具自建的参数预览）；工具追加的其它文本块保持原样。
			if !textReplaced && text != "" {
				clone["content"] = u.H{"type": "text", "text": text}
				textReplaced = true
			}
		case ToolCallingInfoType:
			clone["args"] = raw
		}
		out = append(out, clone)
	}
	if !textReplaced && text != "" {
		out = append([]u.H{{
			"type":    "content",
			"content": u.H{"type": "text", "text": text},
		}}, out...)
	}
	return out
}

// BuildToolCallingContent 按规范化规则构造工具调用的展示 content
// （文本块 + alk.cxykevin.top/calling_info），供 session/resume 回放早期数据
// （tool_calling_content 为空）时重建，与直播经 NormalizeToolCallingContent 的结果同形。
func BuildToolCallingContent(toolName string, messageID uint64, params any) []u.H {
	raw := NormalizeToolCallingParams(params)
	args := raw
	if args == nil {
		args = map[string]any{}
	}
	content := make([]u.H, 0, 2)
	if text := RenderToolCallingText(raw); text != "" {
		content = append(content, u.H{
			"type":    "content",
			"content": u.H{"type": "text", "text": text},
		})
	}
	return append(content, u.H{
		"type":      ToolCallingInfoType,
		"name":      toolName,
		"messageID": messageID,
		"args":      args,
	})
}
