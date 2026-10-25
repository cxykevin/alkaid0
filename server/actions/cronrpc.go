package actions

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/cxykevin/alkaid0/cronjob"
	"github.com/cxykevin/alkaid0/tools/tools/date"
	"gorm.io/gorm"
)

// 私有 ACP 方法：alk.cxykevin.top/session/cron/list | .../cron/delete。
//
// 定时任务归属于「工作目录 + 会话」，与 cron 工具 / @cron 注入同一套归属规则：
// 客户端只能操作已绑定到本连接的会话下的任务。这里不提供新增/修改——创建任务仍
// 交给 AI（cron 工具），私有协议只给前端做展示与删除。
//
// 会话允许「冷」状态：服务端重启后内存注册表为空，但客户端重新 resume 过的会话
// 已经重新绑定，sessionId 能解析出 cwd 与 chat id。

// cronRPCNextRuns 前端列表里展示的「接下来几次触发」条数。
const cronRPCNextRuns = 4

// CronListRequest 列出定时任务。
type CronListRequest struct {
	SessionID string `json:"sessionId"`
	// All true = 列出本机 cron.sqlite 里的全部任务（跨会话、跨工作目录），用于
	// 前端「本机定时任务」总览。sessionId 仍须由当前连接绑定（鉴权口径不变），
	// 只是不再参与结果过滤。
	All bool `json:"all,omitempty"`
}

// CronJobView 一条定时任务的前端视图。
type CronJobView struct {
	// ID 任务 id（与 @cron 行首的数字、cron 工具的 id 一致）
	ID uint32 `json:"id"`
	// SessionID 任务所属会话（all=true 总览时用于区分不同会话的任务）
	SessionID string `json:"sessionId"`
	// Cron 标准 5 段表达式（简写在入库时已展开）
	Cron string `json:"cron"`
	// Prompt 到点后投递到会话的提示词（不含 "[cronjob] " 前缀）
	Prompt string `json:"prompt"`
	// RunOnce true = 投递一次后自动删除
	RunOnce bool `json:"runOnce"`
	// Workdir 任务所属工作目录（绝对路径）
	Workdir string `json:"workdir"`
	// NextRuns 接下来 4 次（一次性任务 1 次）触发时间，格式同 time 工具
	NextRuns []string `json:"nextRuns"`
}

// CronListResponse 列出定时任务的响应。
type CronListResponse struct {
	Jobs []CronJobView `json:"jobs"`
}

// CronDeleteRequest 删除一条定时任务。
type CronDeleteRequest struct {
	SessionID string `json:"sessionId"`
	// ID 任务 id；必须是当前会话下的任务
	ID uint32 `json:"id"`
}

// CronDeleteResponse 删除结果。
type CronDeleteResponse struct {
	ID uint32 `json:"id"`
}

// cronStoreForConn 解析 sessionId 并校验连接绑定，返回存储与任务归属。
//
// 与 msgqueue 一致：sessionId 必须由本连接 resume/new 过，否则拒绝——否则任何
// 连接都能凭一个字符串删掉别的会话的定时任务。
func cronStoreForConn(sessionID string, connID uint64) (*cronjob.Store, string, uint32, error) {
	if sessionID == "" {
		return nil, "", 0, fmt.Errorf("sessionId is empty")
	}
	store := cronjob.Default()
	if store == nil {
		return nil, "", 0, fmt.Errorf("cron is not available in this process")
	}
	cwd, chatID, err := resolveSessionTarget(sessionID)
	if err != nil {
		return nil, "", 0, fmt.Errorf("invalid sessionId: %v", err)
	}
	canonical := cwd2SessionID(cwd, chatID)
	if !sessionBoundToConn(connID, canonical) {
		return nil, "", 0, fmt.Errorf("session %s is not bound to this connection", canonical)
	}
	return store, cwd, chatID, nil
}

// CronList 列出定时任务（含接下来几次触发时间）。
//
// all=false（默认）只列当前会话的任务；all=true 列本机 cron.sqlite 中的全部任务
// （跨会话、跨工作目录）。两种模式都要求 sessionId 已绑定到本连接，用于鉴权。
func CronList(req CronListRequest, _ func(string, any, *string) error, connID uint64) (CronListResponse, error) {
	store, cwd, chatID, err := cronStoreForConn(req.SessionID, connID)
	if err != nil {
		return CronListResponse{}, err
	}
	var jobs []cronjob.Job
	if req.All {
		jobs, err = store.List()
	} else {
		jobs, err = store.ListBySession(cwd, chatID)
	}
	if err != nil {
		return CronListResponse{}, err
	}
	views := make([]CronJobView, 0, len(jobs))
	for _, j := range jobs {
		n := cronRPCNextRuns
		if j.RunOnce {
			n = 1
		}
		times := cronjob.NextRuns(j.Cron, n)
		runs := make([]string, 0, len(times))
		for _, t := range times {
			runs = append(runs, date.FormatLocal(t))
		}
		views = append(views, CronJobView{
			ID:        j.ID,
			SessionID: cwd2SessionID(j.Workdir, j.SessionID),
			Cron:      j.Cron,
			Prompt:    j.Prompt,
			RunOnce:   j.RunOnce,
			Workdir:   j.Workdir,
			NextRuns:  runs,
		})
	}
	return CronListResponse{Jobs: views}, nil
}

// CronDelete 删除当前会话下的一条定时任务。
func CronDelete(req CronDeleteRequest, _ func(string, any, *string) error, connID uint64) (CronDeleteResponse, error) {
	if req.ID == 0 {
		return CronDeleteResponse{}, fmt.Errorf("id must be a positive integer")
	}
	store, cwd, chatID, err := cronStoreForConn(req.SessionID, connID)
	if err != nil {
		return CronDeleteResponse{}, err
	}
	job, err := store.Get(req.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return CronDeleteResponse{}, fmt.Errorf("cron %d not found in this session", req.ID)
		}
		return CronDeleteResponse{}, fmt.Errorf("read cron job %d failed: %v", req.ID, err)
	}
	// 归属不符与合作工具一致：报 not found，不暴露别的会话有没有这个 id。
	if job.SessionID != chatID || filepath.Clean(job.Workdir) != filepath.Clean(cwd) {
		return CronDeleteResponse{}, fmt.Errorf("cron %d not found in this session", req.ID)
	}
	if err := store.Delete(req.ID); err != nil {
		return CronDeleteResponse{}, fmt.Errorf("delete cron job %d failed: %v", req.ID, err)
	}
	logger.Info("cron job %d deleted via RPC (session %d @ %s)", req.ID, chatID, cwd)
	return CronDeleteResponse{ID: req.ID}, nil
}
