package run

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
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

// RunIDPrefix 终端 ID 与 run id 统一的格式前缀：两者同为 @temp/run/<n>。
const RunIDPrefix = "@temp/run/"

// runSeq 每个工作目录（workspace）独立的 run 序号：同一目录内递增，换目录重新从 1 开始。
// workspace 是一个工作目录（一个目录可以有多个会话），因此终端 ID/run id（@temp/run/<n>）
// 在**工作目录**内唯一；服务端按会话解析出其工作目录后在该目录内匹配（见 Service.Status / Stop / Kill）。
var runSeqMu sync.Mutex
var runSeq = make(map[string]uint64)

// NewRunID 为指定工作目录分配下一个终端 ID / run id
// （@temp/run/<n>，序号 base36，按工作目录重置）。
func NewRunID(workspace string) string {
	runSeqMu.Lock()
	runSeq[workspace]++
	n := strconv.FormatUint(runSeq[workspace], 36)
	runSeqMu.Unlock()
	return RunIDPrefix + n
}

// NormalizeID 校验并规范化终端 ID / run id：
//   - 规范形式 @temp/run/<n>；
//   - 兼容 temp obj 内部路径形式 run/<n>（内部调用与历史记录）。
//
// 前缀不符、序号为空或含路径分隔符时返回 ok=false。
func NormalizeID(id string) (string, bool) {
	suffix, ok := strings.CutPrefix(id, RunIDPrefix)
	if !ok {
		suffix, ok = strings.CutPrefix(id, "run/")
	}
	if !ok || suffix == "" || strings.ContainsAny(suffix, "/\\") {
		return "", false
	}
	return RunIDPrefix + suffix, true
}

// TempPath 把统一的终端 ID / run id 转换为 temp obj 内部路径（@temp/run/7 → run/7）。
// 前缀不合法时返回 ok=false。
func TempPath(id string) (string, bool) {
	suffix, ok := strings.CutPrefix(id, RunIDPrefix)
	if !ok || suffix == "" || strings.ContainsAny(suffix, "/\\") {
		return "", false
	}
	return "run/" + suffix, true
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
	WritableDirs     []string
	// RunID 该终端的 ID（@temp/run/<n>，与 run id 统一）：同时作为 wait/kill 的 run id
	// 与终端内容持久化路径（内部路径为 run/<n>）。留空时由服务按 Workspace 分配。
	RunID string
	// Workspace 该终端所属的**工作目录**（终端 ID 的命名空间；一个工作目录可有多个会话，
	// 它们共享同一序号空间）。留空时退回 WorkDir（进程工作目录）。
	Workspace string
	// UpdateFn background 模式的运行状态刷新回调（写入 temp obj）
	UpdateFn func(content string)
	// BackgroundKind identifies the terminal's lifecycle kind (for example, background).
	BackgroundKind string
	// TerminalUpdateFn reports terminal content/status changes to clients.
	TerminalUpdateFn func(terminalID, status, content string)
	// ShellStopFn reports completion of a background shell job.
	ShellStopFn func(runID, command string, result *Result)
	// WorkflowOutputFn receives filtered output and dynworkflow events from Python stdout.
	WorkflowOutputFn func(runID, visible string, events []WorkflowEvent)
	// PromoteFn is called when a foreground command is exposed as a background run.
	PromoteFn        func(runID string, job *Job)
	InteractiveStdin bool

	// 结构化执行字段（Python 类型等）
	Program        string   // 可执行文件路径（优先于 Shell）
	Args           []string // 程序参数
	Stdin          string   // 标准输入内容
	DisplayCommand string   // 用于日志/trace/background 状态的脱敏显示命令
	CleanupFn      func()   // 任务结束时的清理回调（销毁临时 key 等）
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
	// ID 终端 ID / run id（统一为 @temp/run/<n>）：终端推送、终端查询接口、wait/kill
	// 与终端内容持久化路径（内部路径 TempPath(ID)）都用它。
	ID string
	// Workspace 该终端的工作目录：ID 在工作目录内唯一，因此服务以 (Workspace, ID) 索引任务。
	Workspace string
	State     JobState
	Command   string
	Reason    string
	CreatedAt time.Time

	done chan struct{} // 关闭表示命令执行结束

	resultMu sync.Mutex
	result   *Result

	// UpdateFn 后台任务运行状态刷新回调（background 模式，写入 temp obj）
	UpdateFn func(content string)

	// Ownership and display metadata exposed to private terminal APIs.
	SessionID        uint32
	AgentID          string
	ToolID           string
	DisplayCommand   string
	BackgroundKind   string
	TerminalUpdateFn func(terminalID, status, content string)
	ShellStopFn      func(runID, command string, result *Result)
	WorkflowOutputFn func(runID, visible string, events []WorkflowEvent)
	stdinMu          sync.Mutex
	stdinWriter      io.Writer
	stdinClosed      bool
	promoteOnce      sync.Once
	stdinBlocked     chan struct{}
	stdinBlockOnce   sync.Once
	contentMu        sync.RWMutex
	content          string

	killFnMu      sync.Mutex
	killFn        func()
	killRequested bool
	killCancel    context.CancelFunc

	cleanupFn func() // 任务结束时的清理回调（销毁临时 key 等）
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
	if j.killCancel != nil {
		j.killCancel()
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

// WriteStdin writes one control line to an interactive Python workflow.
func (j *Job) WriteStdin(data []byte) error {
	j.stdinMu.Lock()
	defer j.stdinMu.Unlock()
	if j.stdinClosed || j.stdinWriter == nil {
		return fmt.Errorf("job stdin is not available")
	}
	_, err := j.stdinWriter.Write(data)
	return err
}

func (j *Job) closeStdin() {
	j.stdinMu.Lock()
	defer j.stdinMu.Unlock()
	if !j.stdinClosed && j.stdinWriter != nil {
		if closer, ok := j.stdinWriter.(io.Closer); ok {
			_ = closer.Close()
		}
		j.stdinClosed = true
	}
}

// Status 返回任务当前状态（为 background 预留）。
func (j *Job) setContent(content string) {
	j.contentMu.Lock()
	j.content = content
	j.contentMu.Unlock()
}

// Content returns the latest terminal content snapshot.
func (j *Job) Content() string {
	j.contentMu.RLock()
	defer j.contentMu.RUnlock()
	return j.content
}

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
type serviceReq any

type submitReq struct {
	req  *Request
	ctx  context.Context
	resp chan *Job
}

type killReq struct {
	sessionID uint32
	workspace string
	id        string
	resp      chan error
}

type statusReq struct {
	workspace string
	id        string
	resp      chan *Job
}

type activeReq struct {
	sessionID uint32
	resp      chan []*Job
}

// endedReq 查询已结束任务（terminal/history 私有接口使用）。
type endedReq struct {
	sessionID uint32
	resp      chan []*Job
}

type stopReq struct {
	sessionID uint32
	workspace string
	id        string
	resp      chan error
}

// Service 全局后台命令执行服务。
// 内部运行唯一的全局 goroutine（loop）作为事件循环，串行处理所有
// 提交/终止/状态请求；每个 job 的命令执行在各自独立 goroutine 中运行，
// 避免阻塞整个后台服务。
type Service struct {
	reqChan chan serviceReq
	mu      sync.Mutex
	// jobs/active 以 (工作目录, 终端 ID) 为键：run 序号按工作目录重置，
	// 因此 ID 只在工作目录内唯一（一个工作目录可以有多个会话）。
	jobs   map[string]*Job
	active map[string]*Job // running jobs only; completed jobs remain in jobs for wait/status compatibility
	// runs 记录 (workspace, runid) → job，供 wait / kill 按 run id 查询
	runs map[string]*Job
}

// jobKey 任务键：终端 ID 在工作目录内唯一，故以 (工作目录, ID) 索引
// （与 runid → job 的 runs 索引同一个键）。
func jobKey(workspace, id string) string {
	return runKey(workspace, id)
}

// Default 全局后台命令执行服务单例。
var Default = newService()

func newService() *Service {
	s := &Service{
		reqChan: make(chan serviceReq, 64),
		jobs:    make(map[string]*Job),
		active:  make(map[string]*Job),
		runs:    make(map[string]*Job),
	}
	go s.loop()
	return s
}

// Find 按 runid（temp obj 路径，如 "@temp/run/xxx" 或 "run/xxx"）查找后台任务。
func normalizeRunID(runid string) string {
	v, _ := strings.CutPrefix(runid, "@temp/")
	return strings.TrimPrefix(v, "/")
}

func runKey(workspace, runid string) string {
	return workspace + "\x00" + normalizeRunID(runid)
}

func (s *Service) Find(runid string) *Job {
	v := normalizeRunID(runid)
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, job := range s.runs {
		if strings.HasSuffix(key, "\x00"+v) {
			return job
		}
	}
	return nil
}

// FindInWorkspace resolves a public run ID within its workspace.
func (s *Service) FindInWorkspace(workspace, runid string) *Job {
	if workspace == "" {
		return s.Find(runid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runs[runKey(workspace, runid)]
}

// Submit 提交一次命令执行：等价于"新建一个后台服务（job）并启动"。
// 立即返回 job，调用方通过 job.Wait 等待响应。
// req.RunID 非空时校验其格式（必须是 @temp/run/<n>，也接受内部路径 run/<n>）。
// 任务以 (Workspace, RunID) 索引；Workspace 留空时退回 WorkDir。
func (s *Service) Submit(ctx context.Context, req *Request) (*Job, error) {
	if req.Workspace == "" {
		req.Workspace = req.WorkDir
	}
	if req.RunID != "" {
		id, ok := NormalizeID(req.RunID)
		if !ok {
			return nil, fmt.Errorf("invalid run id %q: expected %s<n>", req.RunID, RunIDPrefix)
		}
		req.RunID = id
	}
	resp := make(chan *Job, 1)
	s.reqChan <- &submitReq{req: req, ctx: ctx, resp: resp}
	job := <-resp
	if job == nil {
		return nil, fmt.Errorf("submit run job failed")
	}
	return job, nil
}

// Kill 终止指定工作目录内的 job（幂等）。终端 ID 在工作目录内唯一，因此按工作目录定位。
func (s *Service) Kill(workspace, id string) error {
	resp := make(chan error, 1)
	s.reqChan <- &killReq{workspace: workspace, id: id, resp: resp}
	return <-resp
}

// PromoteBackground exposes a still-running foreground job as a background run.
func (s *Service) PromoteBackground(workspace, runID string, job *Job, req *Request) error {
	if job == nil || req == nil || runID == "" || job.Status() != JobRunning {
		return fmt.Errorf("job is no longer running")
	}
	req.RunID = runID
	req.BackgroundKind = "shell"
	job.Workspace = workspace
	job.UpdateFn = req.UpdateFn
	job.BackgroundKind = req.BackgroundKind
	job.TerminalUpdateFn = req.TerminalUpdateFn
	job.ShellStopFn = req.ShellStopFn
	s.mu.Lock()
	s.runs[runKey(workspace, runID)] = job
	s.mu.Unlock()
	if job.UpdateFn != nil {
		job.setContent(bgRunningContent(job.Command, job.CreatedAt))
		job.UpdateFn(job.Content())
		if job.TerminalUpdateFn != nil {
			job.TerminalUpdateFn(job.ID, "start", job.Content())
		}
		go func() {
			t := time.NewTicker(backgroundUpdateInterval)
			defer t.Stop()
			for {
				select {
				case <-t.C:
					content := bgRunningContent(job.Command, job.CreatedAt)
					job.setContent(content)
					job.UpdateFn(content)
					if job.TerminalUpdateFn != nil {
						job.TerminalUpdateFn(job.ID, "running", content)
					}
				case <-job.Done():
					return
				}
			}
		}()
	}
	return nil
}

// WriteRunStdin sends raw input bytes to a running background job.
func (s *Service) WriteRunStdin(workspace string, sessionID uint32, runID string, data []byte) error {
	job := s.FindInWorkspace(workspace, runID)
	if job == nil {
		return fmt.Errorf("run id not found: %s", runID)
	}
	if job.SessionID != sessionID {
		return fmt.Errorf("run %s does not belong to session", runID)
	}
	if job.Status() != JobRunning {
		return fmt.Errorf("run %s is not running", runID)
	}
	return job.WriteStdin(data)
}

// KillRun terminates a background job using its public workspace run ID.
func (s *Service) KillRun(workspace string, sessionID uint32, runID string) error {
	runID = normalizeRunID(runID)
	s.mu.Lock()
	job, ok := s.runs[runKey(workspace, runID)]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("run id not found: %s", runID)
	}
	if job.SessionID != sessionID {
		return fmt.Errorf("run %s does not belong to session", runID)
	}
	job.kill()
	return nil
}

// Status 按 (工作目录, 终端 ID/run id) 查询 job：序号按工作目录重置，同名 ID 可能出现在
// 不同工作目录，因此调用方必须给出工作目录（服务端在 actions 层由 sessionId 解析得到）。
func (s *Service) Status(workspace, id string) *Job {
	resp := make(chan *Job, 1)
	s.reqChan <- &statusReq{workspace: workspace, id: id, resp: resp}
	return <-resp
}

// Active returns a snapshot of running jobs owned by sessionID.
func (s *Service) Active(sessionID uint32) []*Job {
	resp := make(chan []*Job, 1)
	s.reqChan <- &activeReq{sessionID: sessionID, resp: resp}
	return <-resp
}

// ListActive is the descriptive alias for Active.
func (s *Service) ListActive(sessionID uint32) []*Job { return s.Active(sessionID) }

// ListEnded returns ended jobs (finished or killed) owned by sessionID.
// The content of each job is the final terminal snapshot written when the
// command ended, which is the same data the terminal full push carries.
func (s *Service) ListEnded(sessionID uint32) []*Job {
	resp := make(chan []*Job, 1)
	s.reqChan <- &endedReq{sessionID: sessionID, resp: resp}
	return <-resp
}

// Stop terminates a job located by (工作目录, 终端 ID)，并校验其归属会话。
func (s *Service) Stop(sessionID uint32, workspace, id string) error {
	resp := make(chan error, 1)
	s.reqChan <- &stopReq{sessionID: sessionID, workspace: workspace, id: id, resp: resp}
	return <-resp
}

func (s *Service) doActive(sessionID uint32) []*Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs := make([]*Job, 0, len(s.active))
	for _, job := range s.active {
		if sessionID == 0 || job.SessionID == sessionID {
			jobs = append(jobs, job)
		}
	}
	return jobs
}

// doListEnded 收集指定会话中已结束（非 running）的任务。
func (s *Service) doListEnded(sessionID uint32) []*Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs := make([]*Job, 0, len(s.jobs))
	for _, job := range s.jobs {
		if job.SessionID != sessionID || job.Status() == JobRunning {
			continue
		}
		jobs = append(jobs, job)
	}
	return jobs
}

func (s *Service) doStop(sessionID uint32, workspace, id string) error {
	s.mu.Lock()
	job, ok := s.active[jobKey(workspace, id)]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("job %s not found", id)
	}
	if job.SessionID != sessionID {
		return fmt.Errorf("job %s does not belong to session %d", id, sessionID)
	}
	job.kill()
	return nil
}

// loop 后台服务唯一的事件循环 goroutine。
func (s *Service) loop() {
	for req := range s.reqChan {
		switch r := req.(type) {
		case *submitReq:
			r.resp <- s.doSubmit(r.ctx, r.req)
		case *killReq:
			r.resp <- s.doKill(r.workspace, r.id)
		case *statusReq:
			r.resp <- s.doStatus(r.workspace, r.id)
		case *activeReq:
			r.resp <- s.doActive(r.sessionID)
		case *endedReq:
			r.resp <- s.doListEnded(r.sessionID)
		case *stopReq:
			r.resp <- s.doStop(r.sessionID, r.workspace, r.id)
		}
	}
}

func (s *Service) doSubmit(ctx context.Context, req *Request) *Job {
	// 终端 ID 与 run id 统一：调用方（run 工具）指定时沿用，否则按工作目录分配下一个序号。
	terminalID := req.RunID
	if terminalID == "" {
		terminalID = NewRunID(req.Workspace)
	}
	s.mu.Lock()
	displayCmd := req.Command
	if req.DisplayCommand != "" {
		displayCmd = req.DisplayCommand
	}
	job := &Job{
		stdinBlocked:     make(chan struct{}),
		ID:               terminalID,
		State:            JobRunning,
		Command:          displayCmd,
		Reason:           req.Reason,
		CreatedAt:        time.Now(),
		done:             make(chan struct{}),
		UpdateFn:         req.UpdateFn,
		cleanupFn:        req.CleanupFn,
		SessionID:        req.SessionID,
		Workspace:        req.Workspace,
		AgentID:          req.AgentID,
		ToolID:           req.ToolID,
		DisplayCommand:   displayCmd,
		BackgroundKind:   req.BackgroundKind,
		TerminalUpdateFn: req.TerminalUpdateFn,
		ShellStopFn:      req.ShellStopFn,
		WorkflowOutputFn: req.WorkflowOutputFn,
	}
	if req.InteractiveStdin {
		job.stdinWriter = nil // initialized immediately before command start
	}
	// 以 (工作目录, 终端 ID) 为键：序号按工作目录重置，因此 ID 只在工作目录内唯一。
	s.jobs[jobKey(req.Workspace, terminalID)] = job
	s.active[jobKey(req.Workspace, terminalID)] = job
	// 同一索引同时供 wait / kill 按 run id 查询。
	s.runs[jobKey(req.Workspace, terminalID)] = job
	s.mu.Unlock()

	logger.Info("background service: new job %s (session=%d, agent=%s, runid=%s) cmd=%q", terminalID, req.SessionID, req.AgentID, terminalID, displayCmd)
	go s.execute(ctx, job, req)
	return job
}

func (s *Service) doKill(workspace, id string) error {
	s.mu.Lock()
	job, ok := s.jobs[jobKey(workspace, id)]
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("job %s not found", id)
	}
	job.kill()
	return nil
}

func (s *Service) doStatus(workspace, id string) *Job {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jobs[jobKey(workspace, id)]
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
		if job.TerminalUpdateFn != nil {
			job.TerminalUpdateFn(job.ID, "start", job.Content())
		}
		tickerStop = make(chan struct{})
		tickerDone = make(chan struct{})
		go func() {
			defer close(tickerDone)
			t := time.NewTicker(backgroundUpdateInterval)
			defer t.Stop()
			for {
				select {
				case <-t.C:
					content := bgRunningContent(job.Command, job.CreatedAt)
					job.setContent(content)
					job.UpdateFn(content)
					if job.TerminalUpdateFn != nil {
						job.TerminalUpdateFn(job.ID, "running", content)
					}
				case <-tickerStop:
					return
				}
			}
		}()
	}

	defer func() {
		job.closeStdin()
		stopTicker()
		if job.cleanupFn != nil {
			job.cleanupFn()
		}
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

	// 停止定时刷新，写最终结果（命令结束后最后一次更新 temp obj）。
	stopTicker()
	job.resultMu.Lock()
	job.result = result
	if result.Killed || job.wasKilled() {
		job.State = JobKilled
	} else {
		job.State = JobFinished
	}
	job.resultMu.Unlock()
	// 最终内容快照对前台/后台任务都写入：终端全量推送与已结束终端内容查询
	// （alk.cxykevin.top/session/terminal/history）共用这一份数据。
	content := bgFinalContent(job.Command, result)
	job.setContent(content)
	if job.UpdateFn != nil {
		job.UpdateFn(content)
	}

	// Remove the job before broadcasting the final snapshot, otherwise the
	// snapshot still reports this finished job as active/running.
	s.mu.Lock()
	delete(s.active, jobKey(job.Workspace, job.ID))
	s.mu.Unlock()
	if job.TerminalUpdateFn != nil {
		job.TerminalUpdateFn(job.ID, "stop", content)
	}
	if req.ShellStopFn != nil && req.BackgroundKind == "shell" {
		req.ShellStopFn(job.ID, job.DisplayCommand, result)
	}
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

type writerFunc func([]byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// runCommand 在沙盒中执行命令（含非沙盒降级），并填充结果。
func (s *Service) runCommand(ctx context.Context, job *Job, req *Request) *Result {
	// Avoid creating or starting a command after the caller has already cancelled.
	// Kill() cannot terminate an os/exec command before Start, so the watcher alone
	// is not sufficient for a pre-cancelled context.
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return &Result{
				Success:   false,
				ErrString: fmt.Sprintf("[System] Command cancelled before start: %v\n", err),
				Killed:    true,
			}
		}
	}

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
		Context:       ctx,
		Timeout:       sandTimeout,
		IsolationMode: isolateMode,
		WritableDirs:  req.WritableDirs,
	})
	if err != nil {
		return &Result{CreateErr: err}
	}

	var c *sandbox.Command
	var displayCmd string

	// 结构化执行：Program 非空时使用程序+参数，否则使用 shell+command
	if req.Program != "" {
		displayCmd = req.DisplayCommand
		if displayCmd == "" {
			displayCmd = req.Program
		}
		c, err = sand.Execute(req.Program, req.Args...)
		if err != nil {
			return &Result{CreateErr: err}
		}
		if req.Stdin != "" {
			c.SetStdin(strings.NewReader(req.Stdin))
		}
	} else {
		displayCmd = req.Command
		startCmd := []string{}
		switch req.Shell {
		case "powershell", "powershell.exe", "pwsh", "pwsh.exe":
			startCmd = []string{"-Command", req.Command}
		case "cmd", "cmd.exe":
			startCmd = []string{"/C", req.Command}
		default:
			startCmd = []string{"-c", req.Command}
		}

		c, err = sand.Execute(req.Shell, startCmd...)
		if err != nil {
			return &Result{CreateErr: err}
		}
	}

	// 注册终止回调：loop.Stop()/context 取消经 Kill(id) 终止此命令
	job.setKillFn(func() { _ = c.Kill() })

	// 命令启动前已被终止请求：不执行，直接返回（避免无效启动后漏杀）
	if job.wasKilled() {
		logger.Info("job %s killed before command start, skip execution", job.ID)
		return &Result{Success: false, ErrString: "[System] Command killed before start\n", Killed: true}
	}

	var buf bytes.Buffer
	var workflowParser WorkflowOutputParser
	var outputMu sync.Mutex
	output := io.Writer(&buf)
	if req.InteractiveStdin {
		stdinReader, stdinWriter := io.Pipe()
		job.stdinMu.Lock()
		job.stdinWriter = stdinWriter
		job.stdinClosed = false
		job.stdinMu.Unlock()
		c.SetStdin(stdinReader)
	}
	if req.WorkflowOutputFn != nil && req.Program != "" {
		output = writerFunc(func(p []byte) (int, error) {
			outputMu.Lock()
			defer outputMu.Unlock()
			visible, events := workflowParser.Feed(p)
			if visible != "" {
				_, _ = buf.WriteString(visible)
			}
			req.WorkflowOutputFn(job.ID, visible, events)
			return len(p), nil
		})
	}

	// 监听 context 取消，强制 kill 进程（runCmd 内部处理）
	err = runCmdWithWriter(ctx, c, output, displayCmd, req.Program == "", job)
	if req.WorkflowOutputFn != nil && req.Program != "" {
		if tail := workflowParser.Flush(); tail != "" {
			_, _ = buf.WriteString(tail)
			req.WorkflowOutputFn(job.ID, tail, nil)
		}
	}

	// 只有未显式指定沙盒时，unshare 错误才降级到非沙盒重试
	if err != nil && req.Sandbox && !req.SandboxSpecified && strings.Contains(err.Error(), "unshare") {
		errString := "[System] Sandbox unavailable, fallback to non-sandbox\n"
		sand2, err2 := sandbox.New(sandbox.Config{
			WorkDir:       req.WorkDir,
			Env:           req.Env,
			Context:       ctx,
			Timeout:       sandTimeout,
			IsolationMode: sandbox.IsolationNone,
			WritableDirs:  req.WritableDirs,
		})
		if err2 != nil {
			errString += fmt.Sprintf("[System] Command Execute Error: %v\n", err)
			return &Result{Success: false, ErrString: errString, Output: buf.String(), Fallback: true}
		}

		var c2 *sandbox.Command
		if req.Program != "" {
			c2, err2 = sand2.Execute(req.Program, req.Args...)
			if err2 == nil && req.Stdin != "" {
				c2.SetStdin(strings.NewReader(req.Stdin))
			}
		} else {
			startCmd := []string{}
			switch req.Shell {
			case "powershell", "powershell.exe", "pwsh", "pwsh.exe":
				startCmd = []string{"-Command", req.Command}
			case "cmd", "cmd.exe":
				startCmd = []string{"/C", req.Command}
			default:
				startCmd = []string{"-c", req.Command}
			}
			c2, err2 = sand2.Execute(req.Shell, startCmd...)
		}

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
		err2 = runCmd(ctx, c2, &buf2, displayCmd, req.Program == "")

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
