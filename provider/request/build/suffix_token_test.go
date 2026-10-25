package build

import (
	"container/list"
	"strings"
	"testing"

	reqStruct "github.com/cxykevin/alkaid0/provider/request/structs"
)

// TestMessageTokensCountsToolCallArguments 断链成本必须算上 tool_calls 参数：
// edit 的整文件 text / run 的命令行都在 arguments 里，漏掉它会让「搬家」看起来几乎免费。
func TestMessageTokensCountsToolCallArguments(t *testing.T) {
	plain := reqStruct.Message{Role: reqStruct.RoleUser, Content: strings.Repeat("a", 400)}
	withCall := reqStruct.Message{
		Role:    reqStruct.RoleAssistant,
		Content: "ok",
		ToolCalls: []reqStruct.StreamToolCall{{
			ID:   "call_1",
			Type: "function",
			Function: &reqStruct.StreamToolCallFunc{
				Name:      "edit",
				Arguments: strings.Repeat("b", 4000),
			},
		}},
	}
	if got := messageTokens(plain); got < 90 || got > 110 {
		t.Errorf("普通消息应约 100 token: got %d", got)
	}
	if got := messageTokens(withCall); got < 1000 {
		t.Errorf("tool_calls 参数必须计入: got %d", got)
	}
	// 参数为空指针时不能 panic
	if got := messageTokens(reqStruct.Message{Role: reqStruct.RoleAssistant, ToolCalls: []reqStruct.StreamToolCall{{Function: nil}}}); got != 0 {
		t.Errorf("Function=nil 应返回 0: got %d", got)
	}
}

// TestSuffixTokenIndex 后缀索引从末尾反向累计，且不含自身。
func TestSuffixTokenIndex(t *testing.T) {
	l := list.New()
	first := l.PushBack(reqStruct.Message{Role: reqStruct.RoleUser, Content: strings.Repeat("a", 400)})
	second := l.PushBack(reqStruct.Message{
		Role: reqStruct.RoleAssistant,
		ToolCalls: []reqStruct.StreamToolCall{{
			Function: &reqStruct.StreamToolCallFunc{Name: "edit", Arguments: strings.Repeat("b", 4000)},
		}},
	})
	last := l.PushBack(reqStruct.Message{Role: reqStruct.RoleTool, Content: strings.Repeat("c", 400)})

	idx := suffixTokenIndex(l)
	if idx[last] != 0 {
		t.Errorf("末尾元素之后应为 0: got %d", idx[last])
	}
	if got := idx[second]; got < 90 || got > 110 {
		t.Errorf("second 之后只有最后一条 ~100 token: got %d", got)
	}
	if got := idx[first]; got < 1000 {
		t.Errorf("first 之后必须包含 tool_calls 参数: got %d", got)
	}
	if idx[first] <= idx[second] {
		t.Errorf("后缀应随位置前移单调增大: first=%d second=%d", idx[first], idx[second])
	}
}
