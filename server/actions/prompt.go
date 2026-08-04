package actions

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/cxykevin/alkaid0/provider/request"
	"github.com/cxykevin/alkaid0/ui/funcs"
	"github.com/cxykevin/alkaid0/ui/state"
	u "github.com/cxykevin/alkaid0/utils"
)

// SessionPromptRequest prompt turn 的请求
type SessionPromptRequest struct {
	SessionID string `json:"sessionId"`
	Prompt    []u.H  `json:"prompt,omitempty"`
}

// SessionPromptResponse prompt turn 的响应（ACP v2：纯确认，立即返回 {}）
type SessionPromptResponse struct{}

// ContentBlock 内容块
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// cmdMsgSeq 命令用户消息的合成 messageId 序号
var cmdMsgSeq atomic.Uint64

// cmdMsgID 生成命令用户消息的 messageId（命令不入库，使用合成 ID cmd_<chatID>_<seq>）
func cmdMsgID(obj *sessionObj) string {
	return fmt.Sprintf("cmd_%d_%d", obj.id, cmdMsgSeq.Add(1))
}

// SessionPrompt 处理 prompt turn 请求（ACP v2：立即 ack {}，状态经 state_update 广播）
func SessionPrompt(req SessionPromptRequest, call func(string, any, *string) error, connID uint64) (SessionPromptResponse, error) {
	if req.SessionID == "" {
		return SessionPromptResponse{}, fmt.Errorf("sessionId is empty")
	}

	// 获取会话对象
	sessLock.Lock()
	sessObj, ok := sessions[req.SessionID]
	sessLock.Unlock()
	if !ok {
		return SessionPromptResponse{}, fmt.Errorf("session not found")
	}

	// 从 prompt 中提取文本内容
	var userMessage strings.Builder
	for _, block := range req.Prompt {
		if blockType, ok := u.GetH[string](block, "type"); ok && blockType == "text" {
			if text, ok := u.GetH[string](block, "text"); ok {
				userMessage.WriteString(text)
			}
		}
	}

	if userMessage.String() == "" {
		return SessionPromptResponse{}, fmt.Errorf("no text content in prompt")
	}

	text := userMessage.String()
	// isCommand 标记本 turn 是否为斜杠命令轮（命令轮不算正常请求，不触发 AI 标题生成）
	isCommand := strings.HasPrefix(text, "/")

	if isCommand {
		// 命令轮：广播 user_message（合成 messageId）+ running，分发命令，立即返回
		broadcastSessionUpdate(req.SessionID, SessionUpdate{
			SessionID: req.SessionID,
			Update: SessionUpdateUpdate{
				SessionUpdate: "user_message",
				MessageID:     cmdMsgID(sessObj),
				Content:       []u.H{{"type": "text", "text": text}},
			},
		}, 0)
		broadcastStateUpdate(req.SessionID, "running", "", "")

		cmds := strings.SplitN(text, " ", 2)
		cmdArgs := ""
		if len(cmds) == 2 {
			cmdArgs = cmds[1]
		}
		obj, ok := commandMaps[cmds[0]]
		if !ok {
			broadcastStateUpdate(req.SessionID, "idle", "refusal", "invalid command")
			return SessionPromptResponse{}, fmt.Errorf("invalid command")
		}
		wait, err := obj.Function(sessObj, cmdArgs)
		if wait && err == nil {
			return SessionPromptResponse{}, nil // 异步命令：callback 负责发 idle
		}
		// wait=true 但 err != nil（如 sendQueue 满）时消息未入队，loop 不会回调 idle，需手动广播
		errMsg := ""
		reason := "end_turn"
		if err != nil {
			reason = "refusal"
			errMsg = err.Error()
		}
		broadcastStateUpdate(req.SessionID, "idle", reason, errMsg)
		return SessionPromptResponse{}, err
	}

	// 正常 prompt：持久化用户消息获取 DB ID（作为 messageId 基础，与回放一致）
	userMsgID, err := funcs.UserAddMsgWithID(sessObj.session, text, nil)
	if err != nil {
		broadcastStateUpdate(req.SessionID, "idle", "refusal", err.Error())
		return SessionPromptResponse{}, fmt.Errorf("failed to add user message: %v", err)
	}

	// 广播 user_message（发送方也要收到——ACP v2 以 agent 的 user_message 为 messageId 真相来源）+ running
	broadcastSessionUpdate(req.SessionID, SessionUpdate{
		SessionID: req.SessionID,
		Update: SessionUpdateUpdate{
			SessionUpdate: "user_message",
			MessageID:     msgID(userMsgID),
			Content:       []u.H{{"type": "text", "text": text}},
		},
	}, 0)
	broadcastStateUpdate(req.SessionID, "running", "", "")

	err = sessObj.loop.ChatWithID(text, userMsgID, nil)
	if err != nil {
		broadcastStateUpdate(req.SessionID, "idle", "refusal", err.Error())
		return SessionPromptResponse{}, err
	}
	return SessionPromptResponse{}, nil // 立即 ack
}

// // mapStopReason 将loop.StopReason映射到ACP协议中的stopReason字符串
// func mapStopReason(reason loop.StopReason) string {
// 	switch reason {
// 	case loop.StopReasonModel:
// 		return "end_turn"
// 	case loop.StopReasonUser:
// 		return "user_interrupted"
// 	case loop.StopReasonError:
// 		return "error"
// 	case loop.StopReasonPendingTool:
// 		return "end_turn"
// 	default:
// 		return "end_turn"
// 	}
// }

// SessionCancelRequest session 取消请求
type SessionCancelRequest struct {
	SessionID string `json:"sessionId"`
}

// SessionCancel 处理 session/cancel 请求（ACP v2：取消后经 idle state_update(stopReason=cancelled) 确认）
func SessionCancel(req SessionCancelRequest, call func(string, any, *string) error, connID uint64) (any, error) {
	if req.SessionID == "" {
		return nil, fmt.Errorf("sessionId is empty")
	}

	sessLock.Lock()
	sess, ok := sessions[req.SessionID]
	sessLock.Unlock()
	if !ok {
		return nil, fmt.Errorf("session not found")
	}

	sess.loop.Stop() // 触发 StopReasonUser 回调 → callback 广播 idle+cancelled

	// WaitApprove 时 loop 停在 sendQueue 上，Stop() 不产生回调，需手动拒绝并唤醒权限 goroutine。
	// 权限 goroutine 发现 State != WaitApprove 后直接退出，不做任何事。
	if sess.session.State == state.StateWaitApprove {
		_ = request.RejectToolCallsNoDeactivate(sess.session, "cancelled by user", nil)
		signalPermission(sess, false)
	}

	return nil, nil
}
