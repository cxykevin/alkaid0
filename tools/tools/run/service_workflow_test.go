package run

import (
	"sync"
	"testing"
)

// TestNotifyWorkflowStop 验证 workflow 终态回调只在注册时触发，并携带最终的
// state 与 result（execute 的 defer 在正常结束与 panic 路径都会调用它）。
func TestNotifyWorkflowStop(t *testing.T) {
	s := newService()
	job := &Job{ID: "@temp/run/1", State: JobKilled}
	job.result = &Result{Success: false, ErrString: "killed"}

	var mu sync.Mutex
	var gotID string
	var gotState JobState
	var gotResult *Result
	req := &Request{WorkflowStopFn: func(runID string, result *Result, state JobState) {
		mu.Lock()
		defer mu.Unlock()
		gotID, gotState, gotResult = runID, state, result
	}}
	s.notifyWorkflowStop(job, req)

	mu.Lock()
	defer mu.Unlock()
	if gotID != "@temp/run/1" || gotState != JobKilled || gotResult == nil || gotResult.ErrString != "killed" {
		t.Fatalf("notify = %q %v %#v", gotID, gotState, gotResult)
	}
	// 未注册回调 / 空请求时为空操作。
	s.notifyWorkflowStop(job, &Request{})
	s.notifyWorkflowStop(job, nil)
}
