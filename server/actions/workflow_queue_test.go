package actions

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

// TestWorkflowQueueOrderAndStop 验证会话队列按入队顺序处理，stop 排空并支持重复调用。
func TestWorkflowQueueOrderAndStop(t *testing.T) {
	var mu sync.Mutex
	var got []string
	q := newWorkflowQueue(func(item workflowQueueItem) {
		mu.Lock()
		got = append(got, item.runID)
		mu.Unlock()
	})
	const n = 200
	for i := 0; i < n; i++ {
		q.enqueue(workflowQueueItem{sessionID: "s", runID: strconv.Itoa(i)})
	}
	q.stop()

	mu.Lock()
	defer mu.Unlock()
	if len(got) != n {
		t.Fatalf("processed %d items, want %d", len(got), n)
	}
	for i, id := range got {
		if id != strconv.Itoa(i) {
			t.Fatalf("item[%d] = %s, want %d（队列必须保持入队顺序）", i, id, i)
		}
	}
	// stop 可重复调用；关闭后入队被丢弃且不阻塞。
	q.stop()
	q.enqueue(workflowQueueItem{runID: "late"})
}

// TestWorkflowQueueDropsWhenFull 验证消费者落后时入队不阻塞（stdout 拷贝协程
// 不能被反压），而是丢弃并计数。
func TestWorkflowQueueDropsWhenFull(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	q := newWorkflowQueue(func(workflowQueueItem) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-release
	})
	q.enqueue(workflowQueueItem{sessionID: "s", runID: "head"})
	<-started // worker 已阻塞在 handler 上

	done := make(chan struct{})
	go func() {
		for i := 0; i < workflowQueueCapacity+10; i++ {
			q.enqueue(workflowQueueItem{sessionID: "s", runID: "filler"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("队列满时入队不应阻塞")
	}

	q.mu.Lock()
	dropped := q.dropped
	q.mu.Unlock()
	if dropped == 0 {
		t.Fatal("队列满时应丢弃并计数")
	}
	close(release)
	q.stop()
}
