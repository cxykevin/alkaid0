package cron

import (
	_ "embed" // embed
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/cxykevin/alkaid0/cronjob"
	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/index"
	"github.com/cxykevin/alkaid0/tools/toolobj"
	"github.com/cxykevin/alkaid0/tools/tools/date"
	"github.com/cxykevin/alkaid0/tools/tools/trace"
	"gorm.io/gorm"
)

const toolName = "cron"

//go:embed prompt.md
var prompt string

var logger = log.New("tools:cron")

const (
	// maxPromptRunes 提示词长度上限（不含投递时的 "[cronjob] " 前缀）。
	maxPromptRunes = 200
	// maxRepeatingJobsPerSession 每个会话允许的周期任务条数上限。
	maxRepeatingJobsPerSession = 4
	// nextRunCount 返回给模型的最近触发时间条数。
	nextRunCount = 4
)

// paras 工具参数：全部可选，按 type 分支校验（具体必填项在 prompt 与运行时说明）。
//
// 描述统一用英文：与其它内置工具一致，且模型对英文参数说明的遵循更稳定。
var paras = map[string]parser.ToolParameters{
	"type": {
		Type:        parser.ToolTypeString,
		Required:    true,
		Description: `Operation: "add", "edit" or "delete"`,
	},
	"id": {
		Type:        parser.ToolTypeNumber,
		Required:    false,
		Description: `Job id (the number before "(" in @cron). Required for edit/delete; must be omitted for add`,
	},
	"cron": {
		Type:        parser.ToolTypeString,
		Required:    false,
		Description: `Standard 5-field cron (minute hour day month weekday) or shorthand like "@daily"/"@hourly". Required for add; omit to keep it unchanged on edit`,
	},
	"prompt": {
		Type:        parser.ToolTypeString,
		Required:    false,
		Description: `Prompt delivered to this session when the job fires; fewer than 200 characters (the "[cronjob] " prefix does not count). Required for add; omit to keep it unchanged on edit`,
	},
	"run_once": {
		Type:        parser.ToolTypeBoolean,
		Required:    false,
		Description: `true = delete the job after its first successful delivery (default for add), false = repeat (max 4 repeating jobs per session). Must not be sent for edit/delete`,
	},
}

func init() {
	// @cron 作为虚拟对象接入 trace 的落位/差分机制，与 @task 同一套注入路径。
	trace.RegisterVirtualContent("@cron", CronContent)
}

// loadCron 注册 cron 工具。
func loadCron() string {
	actions.AddTool(&toolobj.Tools{
		Scope:           "", // Global Tools
		Name:            toolName,
		UserDescription: prompt,
		Parameters:      paras,
		ID:              toolName,
	})
	if err := actions.HookTool(toolName, &toolobj.Hook{
		Scope: "",
		// PreHook 为 nil：工具说明只在被调用时才需要，不需要每轮注入。
		// OnHook 为 nil：ExecToolOnHook 的兜底逻辑按统一格式补 calling_info。
		PostHook: toolobj.PostHookFunction{
			Priority: 100,
			Func:     runCron,
		},
	}); err != nil {
		panic(err)
	}
	return toolName
}

func init() {
	index.AddIndex(loadCron)
}

// CronContent 返回 @cron 内容块：当前工作目录 + 会话下的定时任务列表。
//
// 只读、只列本会话的任务；一行一条，格式固定为
//
//   - {id}({cron表达式}) {提示词}
//   - {id}({cron表达式} once) {提示词}
//
// 行首 id 就是 edit/delete 要的 id。不加时间/下一次触发等易变信息，让这块内容
// 在任务不变时保持稳定（前缀缓存友好）。
func CronContent(session *structs.Chats) (string, bool) {
	store := cronjob.Default()
	if store == nil {
		return "Virtual object `@cron` is unavailable: the cron store could not be opened in this process (see server logs).", true
	}
	if session == nil || strings.TrimSpace(session.Root) == "" || session.ID == 0 {
		return "Virtual object `@cron` is unavailable: this session has no workdir.", true
	}
	jobs, err := store.ListBySession(session.Root, session.ID)
	if err != nil {
		return "Virtual object `@cron` is unavailable: " + err.Error(), true
	}
	if len(jobs) == 0 {
		return "Virtual object `@cron` is empty. Use the `cron` tool (type=add) to schedule a prompt for this session.", true
	}
	lines := make([]string, 0, len(jobs))
	for _, j := range jobs {
		if j.RunOnce {
			lines = append(lines, fmt.Sprintf("- %d(%s once) %s", j.ID, j.Cron, j.Prompt))
			continue
		}
		lines = append(lines, fmt.Sprintf("- %d(%s) %s", j.ID, j.Cron, j.Prompt))
	}
	return strings.Join(lines, "\n"), true
}

// runCron 执行 cron 工具。
func runCron(session *structs.Chats, args map[string]*any, cross []*any) (bool, []*any, map[string]*any, error) {
	store := cronjob.Default()
	if store == nil {
		return fail(cross, "the cron store is unavailable in this process (cron.sqlite could not be opened; check server logs)")
	}
	if session == nil || strings.TrimSpace(session.Root) == "" || session.ID == 0 {
		return fail(cross, "the current session has no workdir; cron jobs must belong to a session")
	}
	cwd := filepath.Clean(session.Root)

	typ, provided, err := stringArg(args, "type")
	if err != nil {
		return fail(cross, err.Error())
	}
	if !provided || strings.TrimSpace(typ) == "" {
		return fail(cross, `type is required: "add", "edit" or "delete"`)
	}

	switch strings.ToLower(strings.TrimSpace(typ)) {
	case "add":
		return addJob(store, session, cwd, args, cross)
	case "edit":
		return editJob(store, session, cwd, args, cross)
	case "delete":
		return deleteJob(store, session, cwd, args, cross)
	default:
		return fail(cross, fmt.Sprintf(`unknown type %q: expected "add", "edit" or "delete"`, typ))
	}
}

// addJob 新建一条任务。
func addJob(store *cronjob.Store, session *structs.Chats, cwd string, args map[string]*any, cross []*any) (bool, []*any, map[string]*any, error) {
	if hasArg(args, "id") {
		return fail(cross, "id must not be provided when type=add: the new job gets an id automatically")
	}

	expr, provided, err := stringArg(args, "cron")
	if err != nil {
		return fail(cross, err.Error())
	}
	if !provided || strings.TrimSpace(expr) == "" {
		return fail(cross, "cron is required when type=add (standard 5-field expression, e.g. \"0 9 * * *\" or \"@daily\")")
	}
	// 简写（@daily 等）先翻译成 5 段表达式：校验、落库、回显与 next_runs 用同一形态。
	expr = cronjob.ExpandCron(expr)
	if err := cronjob.ValidateCron(expr); err != nil {
		return fail(cross, err.Error())
	}

	text, provided, err := stringArg(args, "prompt")
	if err != nil {
		return fail(cross, err.Error())
	}
	if !provided {
		return fail(cross, "prompt is required when type=add")
	}
	text, err = cleanPrompt(text)
	if err != nil {
		return fail(cross, err.Error())
	}

	runOnce := true
	if v, present, err := boolArg(args, "run_once"); err != nil {
		return fail(cross, err.Error())
	} else if present {
		runOnce = v
	}

	if !runOnce {
		jobs, err := store.ListBySession(cwd, session.ID)
		if err != nil {
			return fail(cross, "read cron jobs failed: "+err.Error())
		}
		if n := countRepeating(jobs); n >= maxRepeatingJobsPerSession {
			return fail(cross, fmt.Sprintf("this session already has %d repeating cron jobs (limit %d); delete one first or use run_once=true",
				n, maxRepeatingJobsPerSession))
		}
	}

	job, err := store.Add(cronjob.Job{
		Cron:      expr,
		Workdir:   cwd,
		SessionID: session.ID,
		Prompt:    text,
		RunOnce:   runOnce,
	})
	if err != nil {
		return fail(cross, "create cron job failed: "+err.Error())
	}
	logger.Info("cron job %d created: (%s) run_once=%v session %d @ %s", job.ID, job.Cron, job.RunOnce, job.SessionID, job.Workdir)
	return jobResult(cross, job, nextRuns(job.Cron, job.RunOnce))
}

// editJob 修改一条任务：只改显式传入的字段。
func editJob(store *cronjob.Store, session *structs.Chats, cwd string, args map[string]*any, cross []*any) (bool, []*any, map[string]*any, error) {
	id, provided, err := idArg(args, "id")
	if err != nil {
		return fail(cross, err.Error())
	}
	if !provided {
		return fail(cross, "id is required when type=edit")
	}
	if hasArg(args, "run_once") {
		return fail(cross, "run_once cannot be changed by edit: delete the job and add a new one instead")
	}

	job, err := sessionJob(store, cwd, session.ID, id)
	if err != nil {
		return fail(cross, err.Error())
	}

	expr, hasCron, err := stringArg(args, "cron")
	if err != nil {
		return fail(cross, err.Error())
	}
	text, hasPrompt, err := stringArg(args, "prompt")
	if err != nil {
		return fail(cross, err.Error())
	}
	if !hasCron && !hasPrompt {
		return fail(cross, "edit requires at least one of cron / prompt")
	}

	changedCron := false
	if hasCron {
		// 与 add 一致：简写先翻译，再和库里的 5 段表达式比较，避免 @daily ≡ 0 0 * * *
		// 被误判成“改了表达式”。
		expr = cronjob.ExpandCron(expr)
		if err := cronjob.ValidateCron(expr); err != nil {
			return fail(cross, err.Error())
		}
		if expr != job.Cron {
			job.Cron = expr
			changedCron = true
		}
	}
	if hasPrompt {
		text, err = cleanPrompt(text)
		if err != nil {
			return fail(cross, err.Error())
		}
		if text != job.Prompt {
			job.Prompt = text
		}
	}

	if err := store.Update(job); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fail(cross, fmt.Sprintf("cron %d not found in this session", id))
		}
		return fail(cross, "update cron job failed: "+err.Error())
	}
	logger.Info("cron job %d updated: (%s) run_once=%v session %d @ %s", job.ID, job.Cron, job.RunOnce, job.SessionID, job.Workdir)

	// 只有表达式变了才重算触发时间；只改提示词时不必让模型重读一遍调度信息。
	var runs []string
	if changedCron {
		runs = nextRuns(job.Cron, job.RunOnce)
	}
	return jobResult(cross, job, runs)
}

// deleteJob 删除一条任务。
func deleteJob(store *cronjob.Store, session *structs.Chats, cwd string, args map[string]*any, cross []*any) (bool, []*any, map[string]*any, error) {
	id, provided, err := idArg(args, "id")
	if err != nil {
		return fail(cross, err.Error())
	}
	if !provided {
		return fail(cross, "id is required when type=delete")
	}
	if hasArg(args, "run_once") {
		return fail(cross, "run_once must not be provided when type=delete")
	}

	job, err := sessionJob(store, cwd, session.ID, id)
	if err != nil {
		return fail(cross, err.Error())
	}
	if err := store.Delete(job.ID); err != nil {
		return fail(cross, "delete cron job failed: "+err.Error())
	}
	logger.Info("cron job %d deleted: session %d @ %s", job.ID, job.SessionID, job.Workdir)

	success := any(true)
	deleted := any(job.ID)
	return false, cross, map[string]*any{"success": &success, "id": &deleted}, nil
}

// sessionJob 按 id 取任务，并校验它属于当前工作目录 + 会话。
//
// 归属不匹配时报「not found」而不是「no permission」：避免会话之间互相探测 id。
func sessionJob(store *cronjob.Store, cwd string, sessionID uint32, id uint32) (cronjob.Job, error) {
	job, err := store.Get(id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return job, fmt.Errorf("cron %d not found in this session", id)
		}
		return job, fmt.Errorf("read cron job %d failed: %v", id, err)
	}
	if job.SessionID != sessionID || filepath.Clean(job.Workdir) != cwd {
		return job, fmt.Errorf("cron %d not found in this session", id)
	}
	return job, nil
}

// jobResult 构造成功返回：{"success":true,"job":{...}}，next_runs 仅在非空时出现。
func jobResult(cross []*any, job cronjob.Job, runs []string) (bool, []*any, map[string]*any, error) {
	success := any(true)
	obj := map[string]any{
		"id":         job.ID,
		"cron":       job.Cron,
		"run_once":   job.RunOnce,
		"prompt":     job.Prompt,
		"workdir":    job.Workdir,
		"session_id": job.SessionID,
	}
	if len(runs) > 0 {
		obj["next_runs"] = runs
	}
	jobAny := any(obj)
	return false, cross, map[string]*any{"success": &success, "job": &jobAny}, nil
}

// fail 构造失败返回：{"success":false,"error":"..."}，由模型据此修正参数。
func fail(cross []*any, msg string) (bool, []*any, map[string]*any, error) {
	success := any(false)
	errAny := any(msg)
	return false, cross, map[string]*any{"success": &success, "error": &errAny}, nil
}

// countRepeating 统计周期任务（run_once=false）条数。
func countRepeating(jobs []cronjob.Job) int {
	n := 0
	for _, j := range jobs {
		if !j.RunOnce {
			n++
		}
	}
	return n
}

// cleanPrompt 校验并规范化提示词：去首尾空白、非空、按 rune 计长度。
func cleanPrompt(raw string) (string, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return "", fmt.Errorf("prompt is empty")
	}
	if n := utf8.RuneCountInString(text); n >= maxPromptRunes {
		return "", fmt.Errorf("prompt is too long: %d characters (must be fewer than %d, excluding the \"[cronjob] \" prefix)",
			n, maxPromptRunes)
	}
	return text, nil
}

// nextRuns 计算接下来若干次触发时间（本地时区，格式与 time 工具一致）。
//
// 一次性任务只给 1 个时间点；计算逻辑与 RPC 的 cron/list 共用 cronjob.NextRuns。
func nextRuns(expr string, runOnce bool) []string {
	n := nextRunCount
	if runOnce {
		n = 1
	}
	times := cronjob.NextRuns(expr, n)
	out := make([]string, 0, len(times))
	for _, t := range times {
		out = append(out, date.FormatLocal(t))
	}
	return out
}

// hasArg 报告参数是否被显式提供（含传 null）。
func hasArg(args map[string]*any, key string) bool {
	p, ok := args[key]
	return ok && p != nil
}

// stringArg 读取字符串参数。类型不符直接报错，避免把 "true"/"3" 之类的写法静默忽略。
func stringArg(args map[string]*any, key string) (string, bool, error) {
	p, ok := args[key]
	if !ok || p == nil {
		return "", false, nil
	}
	v, ok := (*p).(string)
	if !ok {
		return "", true, fmt.Errorf("%s must be a string", key)
	}
	return v, true, nil
}

// boolArg 读取布尔参数。字符串 "true"/"false" 也接受（部分模型会写成字符串）。
func boolArg(args map[string]*any, key string) (bool, bool, error) {
	p, ok := args[key]
	if !ok || p == nil {
		return false, false, nil
	}
	switch v := (*p).(type) {
	case bool:
		return v, true, nil
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return false, true, fmt.Errorf("%s must be a boolean (true or false)", key)
		}
		return b, true, nil
	default:
		return false, true, fmt.Errorf("%s must be a boolean (true or false)", key)
	}
}

// idArg 读取正整数 id（JSON 数字、数字字符串都接受）。
func idArg(args map[string]*any, key string) (uint32, bool, error) {
	p, ok := args[key]
	if !ok || p == nil {
		return 0, false, nil
	}
	var id uint64
	switch v := (*p).(type) {
	case float64:
		if v <= 0 || v != math.Trunc(v) || v > math.MaxUint32 {
			return 0, true, fmt.Errorf("%s must be a positive integer", key)
		}
		id = uint64(v)
	case int:
		if v <= 0 {
			return 0, true, fmt.Errorf("%s must be a positive integer", key)
		}
		id = uint64(v)
	case int64:
		if v <= 0 {
			return 0, true, fmt.Errorf("%s must be a positive integer", key)
		}
		id = uint64(v)
	case int32:
		if v <= 0 {
			return 0, true, fmt.Errorf("%s must be a positive integer", key)
		}
		id = uint64(v)
	case uint:
		if v == 0 {
			return 0, true, fmt.Errorf("%s must be a positive integer", key)
		}
		id = uint64(v)
	case uint32:
		if v == 0 {
			return 0, true, fmt.Errorf("%s must be a positive integer", key)
		}
		id = uint64(v)
	case uint64:
		if v == 0 {
			return 0, true, fmt.Errorf("%s must be a positive integer", key)
		}
		id = v
	case json.Number:
		n, err := v.Int64()
		if err != nil || n <= 0 {
			return 0, true, fmt.Errorf("%s must be a positive integer", key)
		}
		id = uint64(n)
	case string:
		n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 32)
		if err != nil || n == 0 {
			return 0, true, fmt.Errorf("%s must be a positive integer", key)
		}
		id = n
	default:
		return 0, true, fmt.Errorf("%s must be a positive integer", key)
	}
	if id > math.MaxUint32 {
		return 0, true, fmt.Errorf("%s must be a positive integer", key)
	}
	return uint32(id), true, nil
}
