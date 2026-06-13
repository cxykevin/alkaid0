package state

// State 会话状态
type State int32

// 状态 Enum
const (
	StateIdle             State = iota // 空闲
	StateWaiting                       // 等待中
	StateGeneratingPrompt              // 正在生成 prompt
	StateRequesting                    // 正在请求 LLM
	StateReciving                      // 正在接收响应
	StateWaitApprove                   // 等待用户审批
	StateToolCalling                   // 正在调用工具
)
