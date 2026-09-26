package actions

import (
	"sync"

	runTool "github.com/cxykevin/alkaid0/tools/tools/run"
)

// workflowQueueCapacity 每会话 workflow 队列上限。队列存在的意义是把落库与广播
// 移出 Python stdout 拷贝协程；消费者明显落后时丢弃并告警，绝不反压输出。
const workflowQueueCapacity = 1024

// workflowFinalize workflow 终态：状态（job 的 running/finished/killed）与最终结果。
type workflowFinalize struct {
	status string
	result *runTool.Result
}

// workflowQueueItem 队列项：事件或终态，两者共用一条 FIFO，保证终态一定排在
// 该终端此前的全部事件之后。force 用于终态：即使队列已满也不丢弃，避免状态
// 永远停在 running。
type workflowQueueItem struct {
	sessionID string
	runID     string
	event     *runTool.WorkflowEvent
	finalize  *workflowFinalize
	force     bool
}

// workflowQueue 每会话的有序队列：单 worker 顺序执行 handler。
type workflowQueue struct {
	mu      sync.Mutex
	cond    *sync.Cond
	items   []workflowQueueItem
	closed  bool
	dropped uint64
	handler func(workflowQueueItem)
	done    chan struct{}
}

func newWorkflowQueue(handler func(workflowQueueItem)) *workflowQueue {
	q := &workflowQueue{handler: handler, done: make(chan struct{})}
	q.cond = sync.NewCond(&q.mu)
	go q.worker()
	return q
}

// enqueue 入队（不阻塞）。队列满时丢弃并计数告警：调用方是 Python stdout 拷贝
// 协程，任何阻塞都会反压工作流输出。
func (q *workflowQueue) enqueue(item workflowQueueItem) {
	if q == nil {
		return
	}
	q.mu.Lock()
	if !q.closed && (item.force || len(q.items) < workflowQueueCapacity) {
		q.items = append(q.items, item)
		q.mu.Unlock()
		q.cond.Signal()
		return
	}
	q.dropped++
	dropped := q.dropped
	closed := q.closed
	q.mu.Unlock()
	if closed {
		return
	}
	if dropped == 1 || dropped%100 == 0 {
		logger.Warn("workflow queue full, dropped %d event(s) for session %s", dropped, item.sessionID)
	}
}

// stop 停止队列：先标记关闭，再等待 worker 把已入队项处理完。可重复调用。
func (q *workflowQueue) stop() {
	if q == nil {
		return
	}
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		<-q.done
		return
	}
	q.closed = true
	q.mu.Unlock()
	q.cond.Broadcast()
	<-q.done
}

func (q *workflowQueue) worker() {
	defer close(q.done)
	for {
		q.mu.Lock()
		for len(q.items) == 0 && !q.closed {
			q.cond.Wait()
		}
		if len(q.items) == 0 {
			q.mu.Unlock()
			return
		}
		item := q.items[0]
		q.items = q.items[1:]
		q.mu.Unlock()
		if q.handler != nil {
			q.handler(item)
		}
	}
}

// enqueueWorkflowEvent 把一条 workflow 事件交给会话队列。事件类型为空时直接丢弃。
func enqueueWorkflowEvent(q *workflowQueue, sessionID, runID string, event any) {
	ev, ok := event.(runTool.WorkflowEvent)
	if !ok || ev.Type == "" {
		return
	}
	q.enqueue(workflowQueueItem{sessionID: sessionID, runID: runID, event: &ev})
}

// enqueueWorkflowFinalize 把 workflow 终态交给同一队列，保证排在已入队事件之后。
func enqueueWorkflowFinalize(q *workflowQueue, sessionID, runID, status string, result any) {
	r, _ := result.(*runTool.Result)
	// force：终态不能被丢弃，否则重启后状态永远停在 running。
	q.enqueue(workflowQueueItem{sessionID: sessionID, runID: runID, finalize: &workflowFinalize{status: status, result: r}, force: true})
}

// processWorkflowQueueItem 队列 worker 的处理函数：事件落库并广播，终态更新记录。
func processWorkflowQueueItem(item workflowQueueItem) {
	switch {
	case item.event != nil:
		broadcastWorkflowEvent(item.sessionID, item.runID, *item.event)
	case item.finalize != nil:
		applyWorkflowFinalize(item.sessionID, item.runID, item.finalize)
	}
}
