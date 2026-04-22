package actions

import (
	"fmt"
	"strings"

	u "github.com/cxykevin/alkaid0/utils"
)

// SessionPromptRequest prompt turn 的请求
type SessionPromptRequest struct {
	SessionID string `json:"sessionId"`
	Prompt    []u.H  `json:"prompt,omitempty"`
}

// SessionPromptResponse prompt turn 的响应，包含stopReason
type SessionPromptResponse struct {
	StopReason string  `json:"stopReason"`
	ErrorMsg   *string `json:"alk.cxykevin.top/error_msg,omitempty"`
}

// ContentBlock 内容块
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// SessionPrompt 处理 prompt turn 请求
func SessionPrompt(req SessionPromptRequest, call func(string, any) error, connID uint64) (SessionPromptResponse, error) {
	if req.SessionID == "" {
		return SessionPromptResponse{}, fmt.Errorf("sessionId is empty")
	}

	// 解析会话ID
	cwd, sid, err := sessionID2Cwd(req.SessionID)
	if err != nil {
		return SessionPromptResponse{}, fmt.Errorf("invalid sessionId: %v", err)
	}
	_ = cwd
	_ = sid

	// 获取会话对象
	sessLock.Lock()
	sessObj, ok := sessions[req.SessionID]
	if !ok {
		sessLock.Unlock()
		return SessionPromptResponse{}, fmt.Errorf("session not found")
	}
	sessLock.Unlock()

	// 从 prompt 中提取文本内容
	userMessage := ""
	for _, block := range req.Prompt {
		if blockType, ok := u.GetH[string](block, "type"); ok && blockType == "text" {
			if text, ok := u.GetH[string](block, "text"); ok {
				userMessage += text
			}
		}
	}

	if userMessage == "" {
		return SessionPromptResponse{}, fmt.Errorf("no text content in prompt")
	}

	// 步骤2：广播用户消息给所有其他连接的 client
	err = broadcastSessionUpdate(req.SessionID, SessionUpdate{
		SessionID: req.SessionID,
		Update: SessionUpdateUpdate{
			SessionUpdate: "user_message_chunk",
			Content: u.H{
				"type": "text",
				"text": userMessage,
			},
		},
	}, connID)

	broadcastSessionUpdate(req.SessionID, SessionUpdate{ // 空内容触发 Client 状态更新
		SessionID: req.SessionID,
		Update: SessionUpdateUpdate{
			SessionUpdate: "alk.cxykevin.top/session_start",
			Content:       u.H{},
		},
	}, 0)

	if err != nil {
		return SessionPromptResponse{}, fmt.Errorf("failed to broadcast user message: %v", err)
	}

	stopChan := make(chan StopMsg, 1)
	sessObj.waitStopChan <- &stopChan

	if strings.TrimSpace(userMessage) == "/approve" {
		err = sessObj.loop.Approve()
	} else {
		// 发送用户消息
		err = sessObj.loop.Chat(userMessage, nil)
	}

	// 等待结束
	ret := <-stopChan

	broadcastSessionUpdate(req.SessionID, SessionUpdate{ // 空内容触发 Client 状态更新
		SessionID: req.SessionID,
		Update: SessionUpdateUpdate{
			SessionUpdate: "alk.cxykevin.top/session_stop",
			Content: u.H{
				"stopReason":                 ret.StopReason,
				"alk.cxykevin.top/error_msg": ret.ErrorMsg,
			},
		},
	}, 0)

	// 返回停止原因
	return SessionPromptResponse{
		StopReason: ret.StopReason,
		ErrorMsg:   ret.ErrorMsg,
	}, err
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

// SessionCancel 处理 session/cancel 请求
// 取消正在进行的 prompt turn
func SessionCancel(req SessionCancelRequest, call func(string, any) error, connID uint64) (any, error) {
	if req.SessionID == "" {
		return nil, fmt.Errorf("sessionId is empty")
	}

	// activePromptsLock.Lock()
	// promptCtx, exists := activePrompts[req.SessionID]
	// activePromptsLock.Unlock()

	// if !exists {
	// 	return nil, fmt.Errorf("no prompt in progress for this session")
	// }

	sess, ok := sessions[req.SessionID]
	if !ok {
		return nil, fmt.Errorf("session not found")
	}

	sess.loop.Stop()

	// // 触发取消
	// if promptCtx.cancel != nil {
	// 	promptCtx.cancel()
	// }

	// 广播取消更新给所有连接的 client
	_ = broadcastSessionUpdate(req.SessionID, SessionUpdate{
		SessionID: req.SessionID,
		Update: SessionUpdateUpdate{
			SessionUpdate: "prompt_cancelled",
		},
	}, 0) // 不排除任何连接

	return nil, nil
}
