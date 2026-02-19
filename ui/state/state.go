package state

// State 会话状态
type State int32

// 状态 Enum
const (
	StateIdle State = iota
	StateWaiting
	StateGeneratingPrompt
	StateRequesting
	StateReciving
	StateWaitApprove
	StateToolCalling
)
