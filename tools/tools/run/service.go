package run

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cxykevin/alkaid0/terminal/sandbox"
)

// JobState 后台命令执行任务的状态。
type JobState int

const (
	// JobRunning 任务正在执行
	JobRunning JobState = iota
	// JobFinished 任务已结束（成功或失败）
	JobFinished
	// JobKilled 任务被终止（context 取消或显式 kill）
	JobKilled
)

func (s JobState) String() string {
	switch s {
	case JobRunning:
		return "running"
	case JobFinished:
		return "finished"
	case JobKilled:
		return "killed"
	default:
		return "unknown"
	}
}

// Request 一次命令执行请求。
type Request struct {
	SessionID        uint32
	AgentID          string
	ToolID           string
	Command          string
	Reason           string
	Shell            string
	Env              []string
	WorkDir          string
	Timeout          time.Duration
	Sandbox          bool
	SandboxSpecified bool
	// RunID background 模式的 temp obj 内部路径（如 "run/xxx"），作为 runid 供 wait 查询
	RunID string
	// UpdateFn background 模式的运行状态刷新回调（写入 temp obj）
	UpdateFn func(content string)
}

// Result 命令执行结果。
type Result struct {
	Success   bool
	ErrString string // 前置错误/降级说明（拼接在输出之前）
	Output    string // 命令 stdout/stderr 内容
	Fallback  bool   // 是否走非沙盒降级（降级时输出直接作为 path/error，不写 trace）
	Killed    bool   // 命令是否因 context 取消被终止
	CreateErr error  // sandbox 创建阶段失败（非降级），直接作为工具错误返回
}

// Job 一次后台命令执行服务实例。
// 每次运行命令即创建一个 Job（后台服务），调用方通过 Wait 等待其响应。
type Job struct {
	ID        string
	State     JobState
	Command   string
	Reason    string
	CreatedAt time.Time

	done chan struct{} // 关闭表示命令执行结束

	resultMu sync.Mutex
	result   *Result

	// UpdateFn 后台任务运行状态刷新回调（background 模式，写入 temp obj）
	UpdateFn func(content string)

	killFnMu      sync.Mutex
	killFn        func()
	killRequested bool
}

// setKillFn 设置命令终止回调（命令启动后调用）。若期间 kill 已被请求则立即触发。
func (j *Job) setKillFn(fn func()) {
	j.killFnMu.Lock()
	j.killFn = fn
	if j.killRequested && fn != nil {
		fn()
	}
	j.killFnMu.Unlock()
}

// kill 终止命令（幂等）。始终记录终止请求（供状态判断），
// 若命令尚未启动则标记待终止，启动后经 setKillFn 立即执行。
func (j *Job) kill() {
	j.killFnMu.Lock()
	j.killRequested = true
	if j.killFn != nil {
		j.killFn()
	}
	j.killFnMu.Unlock()
}

// wasKilled 返回任务是否被显式终止过。
func (j *Job) wasKilled() bool {
	j.killFnMu.Lock()
	defer j.killFnMu.Unlock()
	return j.killRequested
}

// Wait 等待任务完成并返回结果。ctx 取消时终止命令并等待清理完成。
func (j *Job) Wait(ctx context.Context) *Result {
	select {
	case <-j.done:
	case <-ctx.Done():
		j.kill()
		<-j.done
	}
	j.resultMu.Lock()
	defer j.resultMu.Unlock()
	return j.result
}

// Status 返回任务当前状态（为 background 预留）。
func (j *Job) Status() JobState {
	j.resultMu.Lock()
	defer j.resultMu.Unlock()
	return j.State
}

// Done 返回任务完成信号 channel（关闭表示命令执行结束）。
// 与 Wait 不同，Done 不会触发 kill，仅用于阻塞等待后台任务结束。
func (j *Job) Done() <-chan struct{} {
	return j.done
}

// serviceReq 后台服务请求（事件循环中按类型分发）。
type serviceReq interface{}

type submitReq struct {
	req  *Request
	ctx  context.Context
	resp chan *Job
}

type killReq struct {
	id   string
	resp chan error
}

type statusReq struct {
	id   string
	resp chan *Job
}

// Service 全局后台命令执行服务。
// 内部运行唯一的全局 goroutine（loop）作为事件循环，串行处理所有
// 提交/终止/状态请求；每个 job 的命令执行在各自独立 goroutine 中运行，
// 避免阻塞整个后台服务。
type Service struct {
	reqChan chan serviceReq
	mu      sync.Mutex
	jobs    map[string]*Job
	// runs 记录 background 模式的 runid（temp obj 内部路径）→ job，供 wait 查询
	runs map[string]*Job
	seq  int64
}

// Default 全局后台命令执行服务单例。
var Default = newService()

func newService() *Service {
	s := &Service{
		reqChan: make(chan serviceReq, 64),
		jobs:    make(map[string]*Job),
		runs:    make(map[string]*Job),
	}
	go s.loop()
	return s
}

// Find 按 runid（temp obj 路径，如 "@temp/run/xxx" 或 "run/xxx"）查找后台任务。
func (s *Service) Find(runid string) *Job {
	v, _ := strings.CutPrefix(runid, "@temp/")
	v = strings.TrimPrefix(v, "/")
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs[v]
}

// Submit 提交一次命令执行：等价于"新建一个后台服务（job）并启动"。
// 立即返回 job，调用方通过 job.Wait 等待响应。
func (s *Service) Submit(ctx context.Context, req *Request) (*Job, error) {
	resp := make(chan *Job, 1)
	s.reqChan <- &submitReq{req: req, ctx: ctx, resp: resp}
	job := <-resp
	if job == nil {
		return nil, fmt.Errorf("submit run job failed")
	}
	return job, nil
}

// Kill 终止指定 job（幂等）。
func (s *Service) Kill(id string) error {
	resp := make(chan error, 1)
	s.reqChan <- &killReq{id: id, resp: resp}
	return <-resp
}

// Status 按 ID 查询 job（为 background 预留）。
func (s *Service) Status(id string) *Job {
	resp := make(chan *Job, 1)
	s.reqChan <- &statusReq{id: id, resp: resp}
	return <-resp
}

// loop 后台服务唯一的事件循环 goroutine。
func (s *Service) loop() {
	for req := range s.reqChan {
		switch r := req.(type) {
		case *submitReq:
			r.resp <- s.doSubmit(r.ctx, r.req)
		case *killReq:
			r.resp <- s.doKill(r.id)
		case *statusReq:
			r.resp <- s.doStatus(r.id)
		}
	}
}

func (s *Service) doSubmit(ctx context.Context, req *Request) *Job {
	s.mu.Lock()
	s.seq++
	id := fmt.Sprintf("run_%d", s.seq)
	job := &Job{
		ID:        id,
		State:     JobRunning,
		Command:   req.Command,
		Reason:    req.Reason,
		CreatedAt: time.Now(),
		done:      make(chan struct{}),
		UpdateFn:  req.UpdateFn,
	}
	s.jobs[id] = job
	if req.RunID != "" {
		s.runs[req.RunID] = job
	}
	s.mu.Unlock()

	logger.Info("background service: new job %s (session=%d, agent=%s, runid=%s) cmd=%q", id, req.SessionID, req.AgentID, req.RunID, req.Command)
	go s.execute(ctx, job, req)
	return job
}

func (s *Service) doKill(id string) error {
	s.mu.Lock()
	job, ok := s.jobs[id]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("job %s not found", id)
	}
	job.kill()
	return nil
}

func (s *Service) doStatus(id string) *Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jobs[id]
}

// execute 在独立 goroutine 中执行命令并写入结果，最后关闭 done。
func (s *Service) execute(ctx context.Context, job *Job, req *Request) {
	// background 模式：每 backgroundUpdateInterval 刷新一次 temp obj 运行状态
	var tickerStop, tickerDone chan struct{}
	stopTicker := func() {
		if tickerStop != nil {
			close(tickerStop)
			<-tickerDone
			tickerStop = nil
		}
	}
	if job.UpdateFn != nil {
		tickerStop = make(chan struct{})
		tickerDone = make(chan struct{})
		go func() {
			defer close(tickerDone)
			t := time.NewTicker(backgroundUpdateInterval)
			defer t.Stop()
			for {
				select {
				case <-t.C:
					job.UpdateFn(bgRunningContent(req.Command, job.CreatedAt))
				case <-tickerStop:
					return
				}
			}
		}()
	}

	defer func() {
		stopTicker()
		if r := recover(); r != nil {
			logger.Error("background job %s panicked: %v", job.ID, r)
			job.resultMu.Lock()
			job.result = &Result{Success: false, ErrString: fmt.Sprintf("[System] background job %s panicked: %v\n", job.ID, r)}
			job.State = JobFinished
			job.resultMu.Unlock()
		}
		close(job.done)
	}()

	result := s.runCommand(ctx, job, req)

	// 停止定时刷新，写最终结果（命令结束后最后一次更新 temp obj）
	stopTicker()
	if job.UpdateFn != nil {
		job.UpdateFn(bgFinalContent(req.Command, result))
	}

	job.resultMu.Lock()
	job.result = result
	if result.Killed || job.wasKilled() {
		job.State = JobKilled
	} else {
		job.State = JobFinished
	}
	job.resultMu.Unlock()
}

// backgroundUpdateInterval 后台任务 temp obj 运行状态的刷新间隔。
var backgroundUpdateInterval = 60 * time.Second

// bgInitialContent 后台任务提交时的初始状态文本（由 runTask 创建 temp obj 时写入）。
func bgInitialContent(command string) string {
	return fmt.Sprintf("[agent execute] $ %s\n\n[Background] Submitted, waiting to start...\n", command)
}

// bgRunningContent 后台任务运行中的状态文本。
func bgRunningContent(command string, start time.Time) string {
	return fmt.Sprintf("[agent execute] $ %s\n\n[Background] Running... (elapsed: %s)\n", command, time.Since(start).Round(time.Second))
}

// bgFinalContent 后台任务结束后的最终状态文本。
func bgFinalContent(command string, r *Result) string {
	return fmt.Sprintf("[agent execute] $ %s\n\n%s%s[Background] Finished: success=%v\n", command, r.ErrString, r.Output, r.Success)
}

// runCommand 在沙盒中执行命令（含非沙盒降级），并填充结果。
func (s *Service) runCommand(ctx context.Context, job *Job, req *Request) *Result {
	isolateMode := sandbox.IsolationNone
	if req.Sandbox {
		isolateMode = sandbox.IsolationOS
	}

	// 只有显式指定了超时时才设置 sandbox timeout，否则为 0（无超时）
	var sandTimeout time.Duration
	if req.Timeout > 0 {
		sandTimeout = req.Timeout + 1*time.Second
	}

	sand, err := sandbox.New(sandbox.Config{
		WorkDir:       req.WorkDir,
		Env:           req.Env,
		Timeout:       sandTimeout,
		IsolationMode: isolateMode,
	})
	if err != nil {
		return &Result{CreateErr: err}
	}

	startCmd := []string{}
	switch req.Shell {
	case "powershell", "powershell.exe", "pwsh", "pwsh.exe":
		startCmd = []string{"-Command", req.Command}
	case "cmd", "cmd.exe":
		startCmd = []string{"/C", req.Command}
	default:
		startCmd = []string{"-c", req.Command}
	}

	c, err := sand.Execute(req.Shell, startCmd...)
	if err != nil {
		return &Result{CreateErr: err}
	}

	// 注册终止回调：loop.Stop()/context 取消经 Kill(id) 终止此命令
	job.setKillFn(func() { _ = c.Kill() })

	// 命令启动前已被终止请求：不执行，直接返回（避免无效启动后漏杀）
	if job.wasKilled() {
		logger.Info("job %s killed before command start, skip execution", job.ID)
		return &Result{Success: false, ErrString: "[System] Command killed before start\n", Killed: true}
	}

	var buf bytes.Buffer

	// 监听 context 取消，强制 kill 进程（runCmd 内部处理）
	err = runCmd(ctx, c, &buf, req.Command)

	// 只有未显式指定沙盒时，unshare 错误才降级到非沙盒重试
	if err != nil && req.Sandbox && !req.SandboxSpecified && strings.Contains(err.Error(), "unshare") {
		errString := "[System] Sandbox unavailable, fallback to non-sandbox\n"
		sand2, err2 := sandbox.New(sandbox.Config{
			WorkDir:       req.WorkDir,
			Env:           req.Env,
			Timeout:       sandTimeout,
			IsolationMode: sandbox.IsolationNone,
		})
		if err2 != nil {
			errString += fmt.Sprintf("[System] Command Execute Error: %v\n", err)
			return &Result{Success: false, ErrString: errString, Output: buf.String(), Fallback: true}
		}
		c2, err2 := sand2.Execute(req.Shell, startCmd...)
		if err2 != nil {
			errString += fmt.Sprintf("[System] Command Execute Error: %v\n", err2)
			return &Result{Success: false, ErrString: errString, Output: buf.String(), Fallback: true}
		}
		// 覆盖终止回调为新进程
		job.setKillFn(func() { _ = c2.Kill() })
		if job.wasKilled() {
			logger.Info("job %s killed before fallback command start, skip execution", job.ID)
			return &Result{Success: false, ErrString: errString + "[System] Command killed before start\n", Killed: true}
		}

		var buf2 bytes.Buffer
		err2 = runCmd(ctx, c2, &buf2, req.Command)

		if err2 != nil {
			errString += fmt.Sprintf("[System] Command Execute Error: %v\n", err2)
		}
		return &Result{
			Success:   err2 == nil,
			ErrString: errString,
			Output:    buf2.String(),
			Fallback:  true,
			Killed:    ctx.Err() != nil,
		}
	}

	if err != nil {
		return &Result{
			Success:   false,
			ErrString: fmt.Sprintf("[System] Command Execute Error: %v\n", err),
			Output:    buf.String(),
			Killed:    ctx.Err() != nil,
		}
	}
	return &Result{Success: true, Output: buf.String()}
}
