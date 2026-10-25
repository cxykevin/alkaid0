package actions

import (
	"bytes"
	"encoding/json"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/provider/mask"
)

// clientMaskKeys 是 session/update 及服务端请求中承载对话文本的字段名。
// 只有位于这些字段（及其子结构）中的字符串才会在客户端脱敏模式下被替换。
// 会话 / 消息 / 工具调用的 ID 等结构字段必须保持原样，否则客户端无法把
// 更新关联到正确的对象（sessionId 还包含工作目录路径）。
var clientMaskKeys = map[string]bool{
	"content":                    true, // 消息正文、工具调用内容块
	"text":                       true, // 内容块文本、消息队列项
	"args":                       true, // 工具参数
	"title":                      true, // 会话标题 / 工具调用标题
	"command":                    true, // shell_stop 命令行
	"plan":                       true, // 任务计划
	"alk.cxykevin.top/error_msg": true, // 回合错误信息（可能回显内容片段）
}

// clientMaskStreamingUpdates 是已经由 loop 的流式脱敏器处理过的更新类型。
// 它们不能再按整段文本脱敏：分片末尾可能恰好是某个敏感值的前半截，
// 整段检测会把半截当成新敏感值建映射。
var clientMaskStreamingUpdates = map[string]bool{
	"agent_message_chunk": true,
	"agent_thought_chunk": true,
}

// clientMasker 返回会话的客户端展示脱敏器；未开启时返回 nil。
func (obj *sessionObj) clientMasker() *mask.ClientMasker {
	if obj == nil || obj.session == nil || obj.session.DB == nil {
		return nil
	}
	cfg := config.GlobalConfigSafe().DataMask
	if !cfg.Enable || !cfg.MaskClientMessages {
		return nil
	}
	obj.clientMaskMu.Lock()
	defer obj.clientMaskMu.Unlock()
	if obj.clientMask == nil {
		obj.clientMask = mask.NewClientMasker(obj.session.DB)
	}
	if obj.clientMask != nil {
		obj.clientMask.SetConfig(cfg)
	}
	return obj.clientMask
}

// clientMaskerFor 按会话 ID 取客户端展示脱敏器。
func clientMaskerFor(sessionID string) *mask.ClientMasker {
	obj, ok := lookupSession(sessionID)
	if !ok {
		return nil
	}
	return obj.clientMasker()
}

// maskClientUpdate 在 DataMask.MaskClientMessages 开启时返回脱敏后的
// session/update 载荷；关闭时原样返回。
func maskClientUpdate(sessionID string, update any) any {
	cm := clientMaskerFor(sessionID)
	if cm == nil || update == nil {
		return update
	}
	if isStreamingClientUpdate(update) {
		return update
	}
	return maskClientPayload(cm, update)
}

// isStreamingClientUpdate 判断 update 是否是已由流式脱敏器处理过的 chunk 事件。
func isStreamingClientUpdate(update any) bool {
	var upd any
	switch v := update.(type) {
	case SessionUpdate:
		upd = v.Update
	case *SessionUpdate:
		if v == nil {
			return false
		}
		upd = v.Update
	default:
		return false
	}
	m, ok := upd.(SessionUpdateUpdate)
	if !ok {
		return false
	}
	return clientMaskStreamingUpdates[m.SessionUpdate]
}

// maskClientPayload 把任意可 JSON 序列化的载荷中的对话文本替换为脱敏内容。
// 通过 JSON 往返保留原有结构；解码使用 json.Number，避免 uint64 ID 精度损失。
func maskClientPayload(cm *mask.ClientMasker, payload any) any {
	if cm == nil || payload == nil {
		return payload
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return payload
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return payload
	}
	return maskClientValue(cm, v, false)
}

// maskClientValue 递归处理解码后的 JSON 值。masked 表示当前位于一个
// 会话内容字段内部（其下所有字符串都要脱敏）。
func maskClientValue(cm *mask.ClientMasker, v any, masked bool) any {
	switch t := v.(type) {
	case string:
		if !masked {
			return t
		}
		return cm.Text(t)
	case map[string]any:
		for k, val := range t {
			t[k] = maskClientValue(cm, val, masked || clientMaskKeys[k])
		}
		return t
	case []any:
		for i := range t {
			t[i] = maskClientValue(cm, t[i], masked)
		}
		return t
	default:
		return v
	}
}

// callSessionUpdate 通过连接级 call 发送 session/update，并按客户端脱敏
// 配置处理内容（历史回放、单连接快照等不走 broadcast 的路径使用）。
func callSessionUpdate(sessionID string, call func(string, any, *string) error, update any) error {
	if call == nil {
		return nil
	}
	return call("session/update", maskClientUpdate(sessionID, update), nil)
}

// maskMsgQueueItems 返回按客户端脱敏配置处理后的队列项快照
// （队列 RPC 的返回值不经 session/update，需要单独处理）。
func maskMsgQueueItems(sessionID string, items []msgQueueItemView) []msgQueueItemView {
	cm := clientMaskerFor(sessionID)
	if cm == nil || len(items) == 0 {
		return items
	}
	out := make([]msgQueueItemView, len(items))
	copy(out, items)
	for i := range out {
		out[i].Text = cm.Text(out[i].Text)
		out[i].Args = cm.Text(out[i].Args)
	}
	return out
}
