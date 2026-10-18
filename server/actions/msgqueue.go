package actions

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cxykevin/alkaid0/provider/request"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/ui/funcs"
	"github.com/cxykevin/alkaid0/ui/state"
	u "github.com/cxykevin/alkaid0/utils"
	"gorm.io/gorm"
)

// msgQueueCapability 客户端在 initialize.capabilities 中显式声明该键后才接收
// msgqueue 推送（值可以是 {} 或 true；false/缺失均视为不支持）。
const msgQueueCapability = "alk.cxykevin.top/msgqueue"

// msgQueueCapacity 每会话队列上限。
const msgQueueCapacity = 32

// msgQueueUpdateName 队列变化通知的 sessionUpdate 名称。
const msgQueueUpdateName = "alk.cxykevin.top/msgqueue_update"

const (
	msgQueueKindPrompt  = "prompt"
	msgQueueKindCommand = "command"

	msgQueueReasonEnqueued = "enqueued"
	msgQueueReasonStarted  = "started"
	msgQueueReasonUpdated  = "updated"
	msgQueueReasonRemoved  = "removed"
	msgQueueReasonCleared  = "cleared"
	msgQueueReasonFlushed  = "flushed"
)

// msgQueueItem 队列项。prompt 项保存待发送文本；command 项保存参与 loop 的命令
// （/compress、/__alk_rewind、/init、/s <phrase>），出队时才执行。
type msgQueueItem struct {
	ID        string
	Seq       uint64
	Kind      string
	Command   string
	Args      string
	Text      string
	CreatedAt time.Time
	// MessageID 仅在出队落库后填充，用于 started 事件与客户端关联 user_message。
	MessageID string
}

// msgQueueItemView 对客户端可见的队列项。
type msgQueueItemView struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Command   string `json:"command,omitempty"`
	Args      string `json:"args,omitempty"`
	Text      string `json:"text"`
	CreatedAt string `json:"createdAt"`
	MessageID string `json:"messageId,omitempty"`
}

// msgQueueEvent session/update 的 update 载荷，每次推送剩余队列的全量快照。
type msgQueueEvent struct {
	SessionUpdate string             `json:"sessionUpdate"`
	Revision      uint64             `json:"revision"`
	Reason        string             `json:"reason"`
	Items         []msgQueueItemView `json:"items"`
	Item          *msgQueueItemView  `json:"item,omitempty"`
}

func (it msgQueueItem) view(messageID string) msgQueueItemView {
	created := ""
	if !it.CreatedAt.IsZero() {
		created = it.CreatedAt.UTC().Format(time.RFC3339)
	}
	return msgQueueItemView{
		ID:        it.ID,
		Kind:      it.Kind,
		Command:   it.Command,
		Args:      it.Args,
		Text:      it.Text,
		CreatedAt: created,
		MessageID: messageID,
	}
}

// msgQueue 每会话消息队列。
//
// 锁约定：
//   - opMu 串行化所有变更（入队/出队/修改/删除/清空）以及对应的数据库写；
//   - mu 只保护内存字段，临界区尽量短；
//   - onEvent 广播一律在释放锁之后调用，避免慢客户端阻塞队列。
type msgQueue struct {
	chatID   uint32
	db       *gorm.DB
	isIdle   func() bool
	dispatch func(*msgQueueItem) error
	onEvent  func(msgQueueEvent)

	opMu sync.Mutex
	mu   sync.Mutex

	items    []msgQueueItem
	seq      uint64
	revision uint64
	closed   bool

	wake   chan struct{}
	stopCh chan struct{}
	done   chan struct{}
}

func newMsgQueue(chatID uint32, db *gorm.DB, isIdle func() bool, dispatch func(*msgQueueItem) error, onEvent func(msgQueueEvent)) *msgQueue {
	return &msgQueue{
		chatID:   chatID,
		db:       db,
		isIdle:   isIdle,
		dispatch: dispatch,
		onEvent:  onEvent,
		wake:     make(chan struct{}, 1),
		stopCh:   make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// load 从队列表恢复未处理的项（服务端重启/会话重连后继续消费）。
func (q *msgQueue) load() error {
	if q == nil || q.db == nil {
		return nil
	}
	var rows []structs.MsgQueueItems
	if err := q.db.Where("chat_id = ?", q.chatID).Order("seq ASC").Limit(msgQueueCapacity).Find(&rows).Error; err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, row := range rows {
		q.items = append(q.items, msgQueueItem{
			ID:        row.ItemID,
			Seq:       row.Seq,
			Kind:      row.Kind,
			Command:   row.Command,
			Args:      row.Args,
			Text:      row.Text,
			CreatedAt: row.CreatedAt,
		})
		if row.Seq > q.seq {
			q.seq = row.Seq
		}
	}
	return nil
}

func (q *msgQueue) start() {
	if q == nil {
		return
	}
	go q.worker()
}

// snapshot 返回当前 revision 与全量队列快照。
func (q *msgQueue) snapshot() (uint64, []msgQueueItemView) {
	if q == nil {
		return 0, []msgQueueItemView{}
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.revision, q.snapshotLocked()
}

func (q *msgQueue) snapshotLocked() []msgQueueItemView {
	views := make([]msgQueueItemView, 0, len(q.items))
	for i := range q.items {
		views = append(views, q.items[i].view(q.items[i].MessageID))
	}
	return views
}

func (q *msgQueue) isClosed() bool {
	if q == nil {
		return true
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.closed
}

func (q *msgQueue) wakeup() {
	if q == nil {
		return
	}
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// enqueue 入队并持久化。prompt 项此时不写主消息库；command 项只保存命令与参数。
func (q *msgQueue) enqueue(kind, command, args, text string) (msgQueueItem, error) {
	if q == nil {
		return msgQueueItem{}, fmt.Errorf("message queue is not available")
	}
	q.opMu.Lock()

	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		q.opMu.Unlock()
		return msgQueueItem{}, fmt.Errorf("message queue is closed")
	}
	if len(q.items) >= msgQueueCapacity {
		q.mu.Unlock()
		q.opMu.Unlock()
		return msgQueueItem{}, fmt.Errorf("message queue is full (limit %d)", msgQueueCapacity)
	}
	q.seq++
	seq := q.seq
	q.mu.Unlock()

	now := time.Now()
	itemID := fmt.Sprintf("mq_%d_%d", q.chatID, seq)
	if q.db != nil {
		row := structs.MsgQueueItems{
			ChatID:    q.chatID,
			Seq:       seq,
			ItemID:    itemID,
			Kind:      kind,
			Command:   command,
			Args:      args,
			Text:      text,
			CreatedAt: now,
		}
		if err := q.db.Create(&row).Error; err != nil {
			q.opMu.Unlock()
			return msgQueueItem{}, fmt.Errorf("persist message queue item: %w", err)
		}
	}

	it := msgQueueItem{
		ID:        itemID,
		Seq:       seq,
		Kind:      kind,
		Command:   command,
		Args:      args,
		Text:      text,
		CreatedAt: now,
	}

	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		q.opMu.Unlock()
		return msgQueueItem{}, fmt.Errorf("message queue is closed")
	}
	q.items = append(q.items, it)
	q.revision++
	rev := q.revision
	views := q.snapshotLocked()
	q.mu.Unlock()
	q.opMu.Unlock()

	q.emit(msgQueueReasonEnqueued, &it, views, rev)
	return it, nil
}

// pop 取出队首（内存层）。数据库行在成功派发后再删除，语义为 at-least-once。
func (q *msgQueue) pop() (msgQueueItem, bool) {
	if q == nil {
		return msgQueueItem{}, false
	}
	q.opMu.Lock()
	defer q.opMu.Unlock()
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed || len(q.items) == 0 {
		return msgQueueItem{}, false
	}
	it := q.items[0]
	q.items = q.items[1:]
	q.revision++
	return it, true
}

// requeue 派发失败时把项放回队首。
func (q *msgQueue) requeue(it msgQueueItem) {
	if q == nil {
		return
	}
	q.opMu.Lock()
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		q.opMu.Unlock()
		return
	}
	q.items = append([]msgQueueItem{it}, q.items...)
	q.revision++
	rev := q.revision
	views := q.snapshotLocked()
	q.mu.Unlock()
	q.opMu.Unlock()

	q.emit(msgQueueReasonEnqueued, &it, views, rev)
}

// remove 取消一条排队项。
func (q *msgQueue) remove(itemID string) ([]msgQueueItemView, uint64, error) {
	if q == nil {
		return nil, 0, fmt.Errorf("message queue is not available")
	}
	q.opMu.Lock()

	q.mu.Lock()
	idx := -1
	for i := range q.items {
		if q.items[i].ID == itemID {
			idx = i
			break
		}
	}
	if idx < 0 {
		q.mu.Unlock()
		q.opMu.Unlock()
		return nil, 0, fmt.Errorf("queue item %s not found", itemID)
	}
	it := q.items[idx]
	q.mu.Unlock()

	if q.db != nil {
		if err := q.db.Where("chat_id = ? AND item_id = ?", q.chatID, itemID).
			Delete(&structs.MsgQueueItems{}).Error; err != nil {
			q.opMu.Unlock()
			return nil, 0, fmt.Errorf("delete message queue item: %w", err)
		}
	}

	q.mu.Lock()
	for i := range q.items {
		if q.items[i].ID == itemID {
			q.items = append(q.items[:i], q.items[i+1:]...)
			break
		}
	}
	q.revision++
	rev := q.revision
	views := q.snapshotLocked()
	q.mu.Unlock()
	q.opMu.Unlock()

	q.emit(msgQueueReasonRemoved, &it, views, rev)
	return views, rev, nil
}

// update 修改一条排队项。prompt 项改 text；command 项改 args 并重建 text。
func (q *msgQueue) update(itemID string, text, args *string) ([]msgQueueItemView, uint64, error) {
	if q == nil {
		return nil, 0, fmt.Errorf("message queue is not available")
	}
	q.opMu.Lock()

	q.mu.Lock()
	idx := -1
	for i := range q.items {
		if q.items[i].ID == itemID {
			idx = i
			break
		}
	}
	if idx < 0 {
		q.mu.Unlock()
		q.opMu.Unlock()
		return nil, 0, fmt.Errorf("queue item %s not found", itemID)
	}
	it := q.items[idx]
	q.mu.Unlock()

	updates := map[string]any{}
	next := it
	switch it.Kind {
	case msgQueueKindPrompt:
		if text == nil {
			q.opMu.Unlock()
			return nil, 0, fmt.Errorf("text is required for prompt queue items")
		}
		if strings.TrimSpace(*text) == "" {
			q.opMu.Unlock()
			return nil, 0, fmt.Errorf("text is empty")
		}
		next.Text = *text
		updates["text"] = next.Text
	case msgQueueKindCommand:
		if args == nil {
			q.opMu.Unlock()
			return nil, 0, fmt.Errorf("args is required for command queue items")
		}
		next.Args = *args
		next.Text = strings.TrimSpace(it.Command + " " + *args)
		updates["args"] = next.Args
		updates["text"] = next.Text
	default:
		q.opMu.Unlock()
		return nil, 0, fmt.Errorf("unsupported queue item kind %q", it.Kind)
	}

	if q.db != nil {
		if err := q.db.Model(&structs.MsgQueueItems{}).
			Where("chat_id = ? AND item_id = ?", q.chatID, itemID).
			Updates(updates).Error; err != nil {
			q.opMu.Unlock()
			return nil, 0, fmt.Errorf("update message queue item: %w", err)
		}
	}

	q.mu.Lock()
	for i := range q.items {
		if q.items[i].ID == itemID {
			q.items[i] = next
			break
		}
	}
	q.revision++
	rev := q.revision
	views := q.snapshotLocked()
	q.mu.Unlock()
	q.opMu.Unlock()

	q.emit(msgQueueReasonUpdated, &next, views, rev)
	return views, rev, nil
}

// clear 清空队列（不删除已经出队、正在派发的项）。
func (q *msgQueue) clear() ([]msgQueueItemView, uint64, error) {
	if q == nil {
		return nil, 0, fmt.Errorf("message queue is not available")
	}
	q.opMu.Lock()
	if q.db != nil {
		if err := q.db.Where("chat_id = ?", q.chatID).Delete(&structs.MsgQueueItems{}).Error; err != nil {
			q.opMu.Unlock()
			return nil, 0, fmt.Errorf("clear message queue: %w", err)
		}
	}
	q.mu.Lock()
	q.items = nil
	q.revision++
	rev := q.revision
	views := q.snapshotLocked()
	q.mu.Unlock()
	q.opMu.Unlock()

	q.emit(msgQueueReasonCleared, nil, views, rev)
	return views, rev, nil
}

// takeAll 取走全部队列项（用于非 msgqueue 客户端的合并降级）。
func (q *msgQueue) takeAll() ([]msgQueueItem, []msgQueueItemView, uint64) {
	if q == nil {
		return nil, nil, 0
	}
	q.opMu.Lock()
	q.mu.Lock()
	items := append([]msgQueueItem(nil), q.items...)
	q.items = nil
	q.revision++
	rev := q.revision
	views := q.snapshotLocked()
	q.mu.Unlock()
	q.opMu.Unlock()

	if q.db != nil {
		if err := q.db.Where("chat_id = ?", q.chatID).Delete(&structs.MsgQueueItems{}).Error; err != nil {
			logger.Warn("message queue: clear persisted rows for flush failed: %v", err)
		}
	}
	return items, views, rev
}

// deleteRow 删除已成功派发的数据库行。
func (q *msgQueue) deleteRow(it msgQueueItem) error {
	if q == nil || q.db == nil {
		return nil
	}
	return q.db.Where("chat_id = ? AND item_id = ?", q.chatID, it.ID).
		Delete(&structs.MsgQueueItems{}).Error
}

func (q *msgQueue) emit(reason string, item *msgQueueItem, views []msgQueueItemView, rev uint64) {
	if q == nil || q.onEvent == nil {
		return
	}
	var view *msgQueueItemView
	if item != nil {
		v := item.view(item.MessageID)
		view = &v
	}
	q.onEvent(msgQueueEvent{
		SessionUpdate: msgQueueUpdateName,
		Revision:      rev,
		Reason:        reason,
		Items:         views,
		Item:          view,
	})
}

// stop 停止 dispatcher 并等待其退出；可重复调用。
func (q *msgQueue) stop() {
	if q == nil {
		return
	}
	q.mu.Lock()
	if !q.closed {
		q.closed = true
		close(q.stopCh)
	}
	q.mu.Unlock()
	<-q.done
}

// worker 逐个消费队列：仅在会话 Idle 时出队，一次派发一条。
func (q *msgQueue) worker() {
	defer close(q.done)
	for {
		select {
		case <-q.stopCh:
			return
		case <-q.wake:
		}
		if q.isClosed() {
			return
		}
		if q.isIdle != nil && !q.isIdle() {
			continue
		}
		it, ok := q.pop()
		if !ok {
			continue
		}
		// pop 与 stop 之间可能刚好被置为 closed：不要再把这一条写给正在关闭的会话。
		if q.isClosed() {
			return
		}
		if q.dispatch != nil {
			if err := q.dispatch(&it); err != nil {
				logger.Warn("message queue dispatch failed in session %d: %v", q.chatID, err)
				q.requeue(it)
				continue
			}
		}
		if err := q.deleteRow(it); err != nil {
			logger.Warn("message queue: delete dispatched item %s failed: %v", it.ID, err)
		}
		rev, views := q.snapshot()
		q.emit(msgQueueReasonStarted, &it, views, rev)
	}
}

// dispatchMsgQueueItem 把一个出队项真正交给 loop（或执行入队命令）。
// 在 dispatcher goroutine 中调用，此时会话应处于 Idle。
func (obj *sessionObj) dispatchMsgQueueItem(item *msgQueueItem) error {
	if obj == nil || obj.session == nil || obj.loop == nil || item == nil {
		return fmt.Errorf("session is not available")
	}
	sessionID := cwd2SessionID(obj.cwd, obj.id)
	switch item.Kind {
	case msgQueueKindPrompt:
		userMsgID, err := funcs.UserAddMsgWithID(obj.session, item.Text, nil)
		if err != nil {
			return fmt.Errorf("persist queued message: %w", err)
		}
		item.MessageID = msgID(userMsgID)
		_ = broadcastSessionUpdate(sessionID, SessionUpdate{
			SessionID: sessionID,
			Update: SessionUpdateUpdate{
				SessionUpdate: "user_message",
				MessageID:     item.MessageID,
				Content:       []u.H{{"type": "text", "text": item.Text}},
			},
		}, 0)
		broadcastStateUpdate(sessionID, "running", "", "")
		if err := obj.loop.ChatWithID(item.Text, userMsgID, nil); err != nil {
			// 消息已入库并广播：不再重试，避免重复插入；直接收尾并告知客户端。
			broadcastStateUpdate(sessionID, "idle", "refusal", err.Error())
			return nil
		}
		return nil
	case msgQueueKindCommand:
		cmd, ok := commandMaps[item.Command]
		if !ok {
			broadcastStateUpdate(sessionID, "idle", "refusal", fmt.Sprintf("unknown command %q", item.Command))
			return nil
		}
		wait, err := cmd.Function(obj, item.Args)
		if err != nil {
			broadcastStateUpdate(sessionID, "idle", "refusal", err.Error())
			return nil
		}
		if !wait {
			broadcastStateUpdate(sessionID, "idle", "end_turn", "")
			// 立即返回的命令不会产生 loop 回调，主动唤醒以继续处理后续队列。
			obj.msgQueue.wakeup()
		}
		return nil
	default:
		return fmt.Errorf("unknown queue item kind %q", item.Kind)
	}
}

// cancelCurrentTurn 终止当前生成（非 msgqueue 客户端提交输入时的降级路径）。
func cancelCurrentTurn(obj *sessionObj, reason string) {
	if obj == nil || obj.loop == nil {
		return
	}
	obj.loop.Stop()
	if obj.session != nil && obj.session.GetState() == state.StateWaitApprove {
		_ = request.RejectToolCallsNoDeactivate(obj.session, reason, nil)
		signalPermission(obj, false)
	}
}

// flushQueueMerged 非 msgqueue 客户端的降级路径：
// 终止当前生成，按 FIFO 冲刷队列并把本次输入一并打包：连续 prompt 项合并成
// 一条用户消息发送，command 项各自执行（作为合并批次的分界）。
func (obj *sessionObj) flushQueueMerged(extra msgQueueItem) error {
	if obj == nil || obj.session == nil || obj.loop == nil {
		return fmt.Errorf("session not found")
	}
	sessionID := cwd2SessionID(obj.cwd, obj.id)
	if obj.session.GetState() != state.StateIdle {
		cancelCurrentTurn(obj, "interrupted by a combined message")
	}

	queued := []msgQueueItem{}
	if obj.msgQueue != nil {
		items, views, rev := obj.msgQueue.takeAll()
		queued = items
		obj.msgQueue.emit(msgQueueReasonFlushed, nil, views, rev)
	}
	queued = append(queued, extra)

	for _, action := range planMsgQueueFlush(queued) {
		if action.Command != nil {
			if err := obj.dispatchMsgQueueItem(action.Command); err != nil {
				logger.Warn("message queue: flush command %q failed: %v", action.Command.Command, err)
			}
			continue
		}
		userMsgID, err := funcs.UserAddMsgWithID(obj.session, action.Text, nil)
		if err != nil {
			return fmt.Errorf("persist merged message: %w", err)
		}
		_ = broadcastSessionUpdate(sessionID, SessionUpdate{
			SessionID: sessionID,
			Update: SessionUpdateUpdate{
				SessionUpdate: "user_message",
				MessageID:     msgID(userMsgID),
				Content:       []u.H{{"type": "text", "text": action.Text}},
			},
		}, 0)
		broadcastStateUpdate(sessionID, "running", "", "")
		if err := obj.loop.ChatWithID(action.Text, userMsgID, nil); err != nil {
			broadcastStateUpdate(sessionID, "idle", "refusal", err.Error())
		}
	}
	return nil
}

// msgQueueFlushAction 冲刷队列时的一步：合并后的 prompt 文本或一条待执行命令。
type msgQueueFlushAction struct {
	Text    string
	Command *msgQueueItem
}

// planMsgQueueFlush 按 FIFO 把队列项切成冲刷步骤：连续 prompt 合并为一条消息，
// command 项各自成一步并作为合并批次的分界，保证执行顺序不变。
func planMsgQueueFlush(items []msgQueueItem) []msgQueueFlushAction {
	actions := make([]msgQueueFlushAction, 0, len(items))
	var batch []string
	flush := func() {
		if len(batch) == 0 {
			return
		}
		actions = append(actions, msgQueueFlushAction{Text: strings.Join(batch, "\n\n")})
		batch = batch[:0]
	}
	for i := range items {
		it := items[i]
		switch it.Kind {
		case msgQueueKindPrompt:
			batch = append(batch, it.Text)
		case msgQueueKindCommand:
			flush()
			command := it
			actions = append(actions, msgQueueFlushAction{Command: &command})
		default:
			logger.Warn("message queue: skip unknown item kind %q", it.Kind)
		}
	}
	flush()
	return actions
}

// ---- 能力判定与推送 ----

// connDeclaresMsgQueue 判断 capabilities 是否显式声明 msgqueue。
func connDeclaresMsgQueue(caps u.H) bool {
	if caps == nil {
		return false
	}
	v, ok := caps[msgQueueCapability]
	if !ok {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case map[string]any:
		return true
	case u.H:
		return true
	default:
		return false
	}
}

func connHasMsgQueueCapability(connID uint64) bool {
	clientConnCapsMu.RLock()
	defer clientConnCapsMu.RUnlock()
	return connDeclaresMsgQueue(clientConnCaps[connID])
}

// broadcastMsgQueueEvent 只向声明了 msgqueue 能力的连接广播队列快照。
func broadcastMsgQueueEvent(sessionID string, ev msgQueueEvent) {
	sessionConnLock.Lock()
	connIDs := append([]uint64(nil), sessionConnMap[sessionID]...)
	sessionConnLock.Unlock()

	type target struct {
		id uint64
		fn func(string, any, *string) error
	}
	connCallLock.Lock()
	targets := make([]target, 0, len(connIDs))
	for _, cid := range connIDs {
		if fn, ok := connCallMap[cid]; ok {
			targets = append(targets, target{id: cid, fn: fn})
		}
	}
	connCallLock.Unlock()

	clientConnCapsMu.RLock()
	allowed := make([]target, 0, len(targets))
	for _, t := range targets {
		if connDeclaresMsgQueue(clientConnCaps[t.id]) {
			allowed = append(allowed, t)
		}
	}
	clientConnCapsMu.RUnlock()

	for _, t := range allowed {
		_ = t.fn("session/update", SessionUpdate{SessionID: sessionID, Update: ev}, nil)
	}
}

// pushMsgQueueSnapshotToConn 向单个连接补推队列快照（new/resume 时使用）。
func pushMsgQueueSnapshotToConn(connID uint64, sessionID string, call func(string, any, *string) error) {
	if call == nil || !connHasMsgQueueCapability(connID) {
		return
	}
	obj, ok := lookupSession(sessionID)
	if !ok || obj.msgQueue == nil {
		return
	}
	rev, items := obj.msgQueue.snapshot()
	ev := msgQueueEvent{
		SessionUpdate: msgQueueUpdateName,
		Revision:      rev,
		Reason:        "snapshot",
		Items:         items,
	}
	_ = call("session/update", SessionUpdate{SessionID: sessionID, Update: ev}, nil)
}

// notifyMsgQueueForConn initialize 晚于 session/new 时为已绑定会话补推快照。
func notifyMsgQueueForConn(connID uint64) {
	bindedSessionOnConnMu.Lock()
	sessionIDs := append([]string(nil), bindedSessionOnConn[connID]...)
	bindedSessionOnConnMu.Unlock()
	if len(sessionIDs) == 0 {
		return
	}
	connCallLock.Lock()
	call := connCallMap[connID]
	connCallLock.Unlock()
	if call == nil {
		return
	}
	for _, sessionID := range sessionIDs {
		pushMsgQueueSnapshotToConn(connID, sessionID, call)
	}
}

// ---- RPC 方法 ----

type MsgQueueListRequest struct {
	SessionID string `json:"sessionId"`
}

type MsgQueueListResponse struct {
	Revision uint64             `json:"revision"`
	Items    []msgQueueItemView `json:"items"`
}

type MsgQueueCancelRequest struct {
	SessionID string `json:"sessionId"`
	ItemID    string `json:"itemId"`
}

type MsgQueueUpdateRequest struct {
	SessionID string  `json:"sessionId"`
	ItemID    string  `json:"itemId"`
	Text      *string `json:"text,omitempty"`
	Args      *string `json:"args,omitempty"`
}

type MsgQueueClearRequest struct {
	SessionID string `json:"sessionId"`
}

type MsgQueueSnapshotResponse struct {
	Revision uint64             `json:"revision"`
	Items    []msgQueueItemView `json:"items"`
}

// msgQueueForConn 解析并校验会话，返回该会话的队列。
func msgQueueForConn(sessionID string, connID uint64) (*msgQueue, string, error) {
	if sessionID == "" {
		return nil, "", fmt.Errorf("sessionId is empty")
	}
	cwd, id, err := sessionID2Cwd(sessionID)
	if err != nil {
		return nil, "", fmt.Errorf("invalid sessionId: %v", err)
	}
	canonical := cwd2SessionID(cwd, id)
	if !sessionBoundToConn(connID, canonical) {
		return nil, "", fmt.Errorf("session %s is not bound to this connection", canonical)
	}
	obj, ok := lookupSession(canonical)
	if !ok || obj.msgQueue == nil {
		return nil, "", fmt.Errorf("session not found")
	}
	return obj.msgQueue, canonical, nil
}

// MsgQueueList 返回当前队列快照。
func MsgQueueList(req MsgQueueListRequest, _ func(string, any, *string) error, connID uint64) (MsgQueueListResponse, error) {
	q, _, err := msgQueueForConn(req.SessionID, connID)
	if err != nil {
		return MsgQueueListResponse{}, err
	}
	rev, items := q.snapshot()
	return MsgQueueListResponse{Revision: rev, Items: items}, nil
}

// MsgQueueCancel 取消（删除）一条排队项。
func MsgQueueCancel(req MsgQueueCancelRequest, _ func(string, any, *string) error, connID uint64) (MsgQueueSnapshotResponse, error) {
	if req.ItemID == "" {
		return MsgQueueSnapshotResponse{}, fmt.Errorf("itemId is empty")
	}
	q, _, err := msgQueueForConn(req.SessionID, connID)
	if err != nil {
		return MsgQueueSnapshotResponse{}, err
	}
	items, rev, err := q.remove(req.ItemID)
	if err != nil {
		return MsgQueueSnapshotResponse{}, err
	}
	return MsgQueueSnapshotResponse{Revision: rev, Items: items}, nil
}

// MsgQueueUpdate 修改一条排队项：prompt 改 text，command 改 args。
func MsgQueueUpdate(req MsgQueueUpdateRequest, _ func(string, any, *string) error, connID uint64) (MsgQueueSnapshotResponse, error) {
	if req.ItemID == "" {
		return MsgQueueSnapshotResponse{}, fmt.Errorf("itemId is empty")
	}
	q, _, err := msgQueueForConn(req.SessionID, connID)
	if err != nil {
		return MsgQueueSnapshotResponse{}, err
	}
	items, rev, err := q.update(req.ItemID, req.Text, req.Args)
	if err != nil {
		return MsgQueueSnapshotResponse{}, err
	}
	return MsgQueueSnapshotResponse{Revision: rev, Items: items}, nil
}

// MsgQueueClear 清空队列。
func MsgQueueClear(req MsgQueueClearRequest, _ func(string, any, *string) error, connID uint64) (MsgQueueSnapshotResponse, error) {
	q, _, err := msgQueueForConn(req.SessionID, connID)
	if err != nil {
		return MsgQueueSnapshotResponse{}, err
	}
	items, rev, err := q.clear()
	if err != nil {
		return MsgQueueSnapshotResponse{}, err
	}
	return MsgQueueSnapshotResponse{Revision: rev, Items: items}, nil
}
