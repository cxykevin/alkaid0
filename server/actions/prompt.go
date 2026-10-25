package actions

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/cxykevin/alkaid0/hooks"
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

	// 校验并映射到注册表中的规范键：客户端提供的 sessionId 不得直接当作
	// 注册表键使用，且只能操作本连接 new/resume 绑定过的会话。
	cwd, id, err := sessionID2Cwd(req.SessionID)
	if err != nil {
		return SessionPromptResponse{}, fmt.Errorf("invalid sessionId: %v", err)
	}
	sessionID := cwd2SessionID(cwd, id)
	if !sessionBoundToConn(connID, sessionID) {
		return SessionPromptResponse{}, fmt.Errorf("session %s is not bound to this connection", sessionID)
	}

	// 获取会话对象
	sessObj, ok := lookupSession(sessionID)
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
		// 命令轮：解析命令；除 NoCmdMessage 命令（如 /s 自行广播短语 user_message）外，
		// 广播命令文本 user_message（合成 messageId）+ running，分发命令，立即返回
		cmds := strings.SplitN(text, " ", 2)
		cmdArgs := ""
		if len(cmds) == 2 {
			cmdArgs = cmds[1]
		}
		obj, ok := commandMaps[cmds[0]]
		if !ok {
			broadcastStateUpdate(sessionID, "idle", "refusal", "invalid command")
			return SessionPromptResponse{}, fmt.Errorf("invalid command")
		}
		// 静默命令（如 /background）既不回显命令文本，也不广播 running/idle：
		// 它只修改服务端本地状态，不应在会话时间线上留下任何痕迹。
		if !obj.NoCmdMessage && !obj.Silent {
			broadcastSessionUpdate(sessionID, SessionUpdate{
				SessionID: sessionID,
				Update: SessionUpdateUpdate{
					SessionUpdate: "user_message",
					MessageID:     cmdMsgID(sessObj),
					Content:       []u.H{{"type": "text", "text": text}},
				},
			}, 0)
		}
		if !obj.Silent {
			broadcastStateUpdate(sessionID, "running", "", "")
		}

		// 参与 loop 的命令与普通 prompt 走同一队列，空闲时由 dispatcher 执行；
		// 立即返回的命令（/help、/index 等）保持原同步语义。
		if obj.Queueable != nil && obj.Queueable(cmdArgs) {
			extra := msgQueueItem{
				Kind:    msgQueueKindCommand,
				Command: cmds[0],
				Args:    cmdArgs,
				Text:    text,
			}
			if connHasMsgQueueCapability(connID) {
				if _, err := sessObj.msgQueue.enqueue(extra.Kind, extra.Command, extra.Args, extra.Text); err != nil {
					broadcastStateUpdate(sessionID, "idle", "refusal", err.Error())
					return SessionPromptResponse{}, err
				}
				sessObj.msgQueue.wakeup()
				return SessionPromptResponse{}, nil
			}
			// 非 msgqueue 客户端：终止当前生成，把队列内容与本次命令一起冲刷。
			if err := sessObj.flushQueueMerged(extra); err != nil {
				broadcastStateUpdate(sessionID, "idle", "refusal", err.Error())
				return SessionPromptResponse{}, err
			}
			return SessionPromptResponse{}, nil
		}

		wait, err := obj.Function(sessObj, cmdArgs)
		if wait && err == nil {
			return SessionPromptResponse{}, nil // 异步命令：callback 负责发 idle
		}
		if obj.Silent {
			// 静默命令不广播收尾状态（与不广播 running 对应），错误仍经 RPC 返回。
			return SessionPromptResponse{}, err
		}
		// wait=true 但 err != nil（如 sendQueue 满）时消息未入队，loop 不会回调 idle，需手动广播
		errMsg := ""
		reason := "end_turn"
		if err != nil {
			reason = "refusal"
			errMsg = err.Error()
		}
		broadcastStateUpdate(sessionID, "idle", reason, errMsg)
		return SessionPromptResponse{}, err
	}

	// 正常 prompt：入队（或非能力客户端降级合并）。真正写入 Messages、广播
	// user_message/running 与送入 loop 都在 dispatcher 中完成。
	//
	// 配置了 Hooks.OnUserMessage 时先同步执行钩子命令（阻塞），再入队：
	// 钩子拿到 SESSION_ID / SESSION_PATH，可用于外部通知或预处理。
	hooks.Run(hooks.EventUserMessage, sessObj.id, sessObj.session.Root)

	extra := msgQueueItem{Kind: msgQueueKindPrompt, Text: text}
	if connHasMsgQueueCapability(connID) {
		// 等待审批时收到新消息：与旧行为一致——先拒绝待审批工具，再把新消息入队。
		// 否则队列会一直等不到 idle（loop 停在审批等待上）。
		if sessObj.session.GetState() == state.StateWaitApprove {
			cancelCurrentTurn(sessObj, "rejected by new message")
		}
		if _, err := sessObj.msgQueue.enqueue(extra.Kind, "", "", extra.Text); err != nil {
			broadcastStateUpdate(sessionID, "idle", "refusal", err.Error())
			return SessionPromptResponse{}, err
		}
		sessObj.msgQueue.wakeup()
		return SessionPromptResponse{}, nil
	}
	// 非 msgqueue 客户端：终止当前生成，把已有队列与本次输入合并成一条发送。
	if err := sessObj.flushQueueMerged(extra); err != nil {
		broadcastStateUpdate(sessionID, "idle", "refusal", err.Error())
		return SessionPromptResponse{}, err
	}
	return SessionPromptResponse{}, nil
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

	// 校验并映射到注册表中的规范键；只能取消本连接绑定过的会话。
	cwd, id, err := sessionID2Cwd(req.SessionID)
	if err != nil {
		return nil, fmt.Errorf("invalid sessionId: %v", err)
	}
	sessionID := cwd2SessionID(cwd, id)
	if !sessionBoundToConn(connID, sessionID) {
		return nil, fmt.Errorf("session %s is not bound to this connection", sessionID)
	}
	sess, ok := lookupSession(sessionID)
	if !ok {
		return nil, fmt.Errorf("session not found")
	}

	// 触发 StopReasonUser 回调 → callback 广播 idle+cancelled；WaitApprove 时
	// loop 停在 sendQueue 上，Stop() 不产生回调，需手动拒绝并唤醒权限 goroutine。
	cancelCurrentTurn(sess, "cancelled by user")

	return nil, nil
}
