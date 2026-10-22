package cron

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/cronjob"
	"github.com/cxykevin/alkaid0/storage/structs"
)

// timePattern 与 time 工具一致的本地时间格式：2026-10-23 09:00:00 GMT+8。
var timePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} GMT[+-]\d{1,2}(:\d{2})?$`)

// setup 用临时库替换进程内默认存储，返回会话与工作目录。
func setup(t *testing.T) (*cronjob.Store, *structs.Chats, string) {
	t.Helper()
	store, err := cronjob.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	prev := cronjob.Default()
	cronjob.SetDefault(store)
	t.Cleanup(func() {
		cronjob.SetDefault(prev)
		_ = store.Close()
	})
	dir := t.TempDir()
	return store, &structs.Chats{ID: 1, Root: dir}, dir
}

func ptr(v any) *any {
	a := v
	return &a
}

// exec 调用工具并把结果转成 map 返回。
func exec(t *testing.T, session *structs.Chats, args map[string]any) map[string]*any {
	t.Helper()
	mp := make(map[string]*any, len(args))
	for k, v := range args {
		mp[k] = ptr(v)
	}
	_, _, ret, err := runCron(session, mp, nil)
	if err != nil {
		t.Fatalf("runCron returned error: %v", err)
	}
	return ret
}

func requireSuccess(t *testing.T, ret map[string]*any) map[string]any {
	t.Helper()
	if v, _ := (*ret["success"]).(bool); !v {
		t.Fatalf("expected success, got %v", resultText(ret))
	}
	job, ok := (*ret["job"]).(map[string]any)
	if !ok {
		t.Fatalf("result has no job object: %v", resultText(ret))
	}
	return job
}

func requireFailure(t *testing.T, ret map[string]*any, want string) {
	t.Helper()
	if v, _ := (*ret["success"]).(bool); v {
		t.Fatalf("expected failure containing %q, got success: %v", want, resultText(ret))
	}
	msg, _ := (*ret["error"]).(string)
	if msg == "" {
		t.Fatalf("failure result has no error message: %v", resultText(ret))
	}
	if want != "" && !strings.Contains(msg, want) {
		t.Fatalf("error = %q, want it to contain %q", msg, want)
	}
}

func resultText(ret map[string]*any) string {
	var b strings.Builder
	for k, v := range ret {
		b.WriteString(k)
		b.WriteString("=")
		if v != nil {
			b.WriteString(strings.TrimSpace(strings.Join(strings.Fields(sprint(v)), " ")))
		}
		b.WriteString(" ")
	}
	return b.String()
}

func sprint(v *any) string {
	if v == nil || *v == nil {
		return "null"
	}
	switch x := (*v).(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	return "…"
}

func TestAddDefaultsToRunOnce(t *testing.T) {
	_, session, _ := setup(t)

	job := requireSuccess(t, exec(t, session, map[string]any{
		"type":   "add",
		"cron":   "0 9 * * *",
		"prompt": "check the build",
	}))
	if job["run_once"] != true {
		t.Fatalf("run_once = %v, want default true", job["run_once"])
	}
	if job["cron"] != "0 9 * * *" || job["prompt"] != "check the build" {
		t.Fatalf("job = %v", job)
	}
	runs, ok := job["next_runs"].([]string)
	if !ok || len(runs) != 1 {
		t.Fatalf("next_runs = %#v, want exactly 1 entry for run_once", job["next_runs"])
	}
	if !timePattern.MatchString(runs[0]) {
		t.Fatalf("next_runs[0] = %q, want the time-tool format", runs[0])
	}
}

func TestAddRepeatingReturnsFourNextRuns(t *testing.T) {
	_, session, _ := setup(t)

	job := requireSuccess(t, exec(t, session, map[string]any{
		"type":     "add",
		"cron":     "*/30 * * * *",
		"prompt":   "poll",
		"run_once": false,
	}))
	if job["run_once"] != false {
		t.Fatalf("run_once = %v, want false", job["run_once"])
	}
	runs, ok := job["next_runs"].([]string)
	if !ok || len(runs) != 4 {
		t.Fatalf("next_runs = %#v, want 4 entries", job["next_runs"])
	}
	for _, r := range runs {
		if !timePattern.MatchString(r) {
			t.Fatalf("bad next_runs entry %q", r)
		}
	}
	// 顺序必须是递增的。
	for i := 1; i < len(runs); i++ {
		if runs[i] <= runs[i-1] {
			t.Fatalf("next_runs not increasing: %v", runs)
		}
	}
}

func TestAddRejectsInvalidArguments(t *testing.T) {
	_, session, _ := setup(t)

	requireFailure(t, exec(t, session, map[string]any{
		"type": "add", "cron": "0 9 * * *", "prompt": "x", "id": 3,
	}), "id must not be provided")
	requireFailure(t, exec(t, session, map[string]any{
		"type": "add", "prompt": "x",
	}), "cron is required")
	requireFailure(t, exec(t, session, map[string]any{
		"type": "add", "cron": "not a cron", "prompt": "x",
	}), "invalid cron")
	requireFailure(t, exec(t, session, map[string]any{
		"type": "add", "cron": "* * * * * *", "prompt": "x",
	}), "invalid cron")
	requireFailure(t, exec(t, session, map[string]any{
		"type": "add", "cron": "0 9 * * *",
	}), "prompt is required")
	requireFailure(t, exec(t, session, map[string]any{
		"type": "add", "cron": "0 9 * * *", "prompt": "   ",
	}), "prompt is empty")
	requireFailure(t, exec(t, session, map[string]any{
		"type": "add", "cron": "0 9 * * *", "prompt": strings.Repeat("字", maxPromptRunes),
	}), "too long")
	requireFailure(t, exec(t, session, map[string]any{"type": "nope"}), "unknown type")
	requireFailure(t, exec(t, session, map[string]any{}), "type is required")
}

func TestAddPromptLengthBoundary(t *testing.T) {
	_, session, _ := setup(t)

	// 199 个 rune 可以，200 个不行（按 rune 而非字节计）。
	prompt := strings.Repeat("字", maxPromptRunes-1)
	job := requireSuccess(t, exec(t, session, map[string]any{
		"type": "add", "cron": "0 9 * * *", "prompt": prompt,
	}))
	if job["prompt"] != prompt {
		t.Fatalf("prompt not preserved: %v", job["prompt"])
	}
	requireFailure(t, exec(t, session, map[string]any{
		"type": "add", "cron": "0 9 * * *", "prompt": prompt + "字",
	}), "too long")
}

func TestAddShorthandIsExpanded(t *testing.T) {
	store, session, dir := setup(t)

	job := requireSuccess(t, exec(t, session, map[string]any{
		"type": "add", "cron": "@daily", "prompt": "midnight run",
	}))
	if job["cron"] != "0 0 * * *" {
		t.Fatalf("cron = %v, want expanded 0 0 * * *", job["cron"])
	}
	stored, err := store.ListBySession(dir, session.ID)
	if err != nil || len(stored) != 1 {
		t.Fatalf("ListBySession = %#v (err %v)", stored, err)
	}
	if stored[0].Cron != "0 0 * * *" {
		t.Fatalf("stored cron = %q, want expanded form", stored[0].Cron)
	}
}

func TestAddRepeatingLimitPerSession(t *testing.T) {
	_, session, _ := setup(t)

	for i := 0; i < maxRepeatingJobsPerSession; i++ {
		requireSuccess(t, exec(t, session, map[string]any{
			"type": "add", "cron": "* * * * *", "prompt": "repeat", "run_once": false,
		}))
	}
	requireFailure(t, exec(t, session, map[string]any{
		"type": "add", "cron": "* * * * *", "prompt": "repeat", "run_once": false,
	}), "limit 4")

	// 一次性任务不占额度。
	requireSuccess(t, exec(t, session, map[string]any{
		"type": "add", "cron": "* * * * *", "prompt": "once",
	}))

	// 别的会话不受本会话额度影响。
	other := &structs.Chats{ID: 2, Root: session.Root}
	requireSuccess(t, exec(t, other, map[string]any{
		"type": "add", "cron": "* * * * *", "prompt": "repeat", "run_once": false,
	}))
}

func TestEditOnlyPassedFieldsAndNextRunsRule(t *testing.T) {
	store, session, dir := setup(t)

	created := requireSuccess(t, exec(t, session, map[string]any{
		"type": "add", "cron": "0 9 * * *", "prompt": "original",
	}))
	id := created["id"]

	// 只改提示词：cron / run_once 不变，且不返回 next_runs。
	ret := exec(t, session, map[string]any{"type": "edit", "id": id, "prompt": "updated"})
	job := requireSuccess(t, ret)
	if job["prompt"] != "updated" || job["cron"] != "0 9 * * *" || job["run_once"] != true {
		t.Fatalf("edit changed unexpected fields: %v", job)
	}
	if _, ok := job["next_runs"]; ok {
		t.Fatalf("prompt-only edit must not return next_runs: %v", job)
	}
	stored, err := store.ListBySession(dir, session.ID)
	if err != nil || len(stored) != 1 || stored[0].Prompt != "updated" {
		t.Fatalf("stored job = %#v (err %v)", stored, err)
	}

	// 改 cron：返回 next_runs（一次性任务 1 个）。
	job = requireSuccess(t, exec(t, session, map[string]any{"type": "edit", "id": id, "cron": "*/5 * * * *"}))
	if job["cron"] != "*/5 * * * *" {
		t.Fatalf("cron = %v", job["cron"])
	}
	runs, ok := job["next_runs"].([]string)
	if !ok || len(runs) != 1 {
		t.Fatalf("next_runs = %#v, want 1 entry (job is run_once)", job["next_runs"])
	}

	// 用等价的简写改 cron 不算“改了表达式”，同样不重复返回 next_runs。
	job = requireSuccess(t, exec(t, session, map[string]any{"type": "edit", "id": id, "cron": "  */5 * * * *  "}))
	if _, ok := job["next_runs"]; ok {
		t.Fatalf("unchanged cron must not return next_runs: %v", job)
	}
}

func TestEditRejectsRunOnceAndMissingFields(t *testing.T) {
	_, session, _ := setup(t)
	created := requireSuccess(t, exec(t, session, map[string]any{
		"type": "add", "cron": "0 9 * * *", "prompt": "x",
	}))
	id := created["id"]

	requireFailure(t, exec(t, session, map[string]any{
		"type": "edit", "id": id, "prompt": "y", "run_once": false,
	}), "run_once cannot be changed")
	requireFailure(t, exec(t, session, map[string]any{"type": "edit", "id": id}), "at least one of cron / prompt")
	requireFailure(t, exec(t, session, map[string]any{"type": "edit", "prompt": "y"}), "id is required")
	requireFailure(t, exec(t, session, map[string]any{"type": "edit", "id": 999, "prompt": "y"}), "not found")
	requireFailure(t, exec(t, session, map[string]any{"type": "edit", "id": id, "cron": "bad"}), "invalid cron")
}

func TestEditDeleteRequireSameSession(t *testing.T) {
	_, session, _ := setup(t)
	created := requireSuccess(t, exec(t, session, map[string]any{
		"type": "add", "cron": "0 9 * * *", "prompt": "x",
	}))
	id := created["id"]

	otherSession := &structs.Chats{ID: 2, Root: session.Root}
	requireFailure(t, exec(t, otherSession, map[string]any{"type": "edit", "id": id, "prompt": "y"}), "not found")
	requireFailure(t, exec(t, otherSession, map[string]any{"type": "delete", "id": id}), "not found")

	otherRoot := &structs.Chats{ID: session.ID, Root: t.TempDir()}
	requireFailure(t, exec(t, otherRoot, map[string]any{"type": "delete", "id": id}), "not found")

	// 本会话仍然可以删。
	ret := exec(t, session, map[string]any{"type": "delete", "id": id})
	if v, _ := (*ret["success"]).(bool); !v {
		t.Fatalf("delete failed: %v", resultText(ret))
	}
	if ret["id"] == nil || *ret["id"] != id {
		t.Fatalf("delete returned id %v, want %v", ret["id"], id)
	}
}

func TestDeleteRejectsRunOnce(t *testing.T) {
	_, session, _ := setup(t)
	id := requireSuccess(t, exec(t, session, map[string]any{
		"type": "add", "cron": "0 9 * * *", "prompt": "x",
	}))["id"]

	requireFailure(t, exec(t, session, map[string]any{
		"type": "delete", "id": id, "run_once": false,
	}), "run_once must not be provided")
	// delete 忽略 cron/prompt。
	ret := exec(t, session, map[string]any{"type": "delete", "id": id, "cron": "1 2 3 4 5", "prompt": "ignored"})
	if v, _ := (*ret["success"]).(bool); !v {
		t.Fatalf("delete with ignored cron/prompt failed: %v", resultText(ret))
	}
	// 再删一次就找不到了。
	requireFailure(t, exec(t, session, map[string]any{"type": "delete", "id": id}), "not found")
}

func TestCronContentListsSessionJobs(t *testing.T) {
	_, session, _ := setup(t)

	if got, ok := CronContent(session); !ok || !strings.Contains(got, "is empty") {
		t.Fatalf("empty @cron = %q (ok %v)", got, ok)
	}

	repeating := requireSuccess(t, exec(t, session, map[string]any{
		"type": "add", "cron": "0 9 * * *", "prompt": "daily job", "run_once": false,
	}))
	once := requireSuccess(t, exec(t, session, map[string]any{
		"type": "add", "cron": "@weekly", "prompt": "weekly once",
	}))
	// 其它会话的任务不应出现。
	other := &structs.Chats{ID: 2, Root: session.Root}
	requireSuccess(t, exec(t, other, map[string]any{
		"type": "add", "cron": "0 0 1 1 *", "prompt": "other session job",
	}))

	got, ok := CronContent(session)
	if !ok {
		t.Fatal("CronContent ok = false")
	}
	// 行首就是 edit/delete 要的 id。
	want := fmt.Sprintf("- %v(0 9 * * *) daily job\n- %v(0 0 * * 0 once) weekly once",
		repeating["id"], once["id"])
	if got != want {
		t.Fatalf("@cron =\n%s\nwant\n%s", got, want)
	}

	// @cron 行首的 id 能直接喂回 edit / delete。
	requireSuccess(t, exec(t, session, map[string]any{
		"type": "edit", "id": repeating["id"], "prompt": "daily job edited",
	}))
	ret := exec(t, session, map[string]any{"type": "delete", "id": once["id"]})
	if v, _ := (*ret["success"]).(bool); !v {
		t.Fatalf("delete by id from @cron failed: %v", resultText(ret))
	}
}

func TestCronContentWithoutStore(t *testing.T) {
	prev := cronjob.Default()
	cronjob.SetDefault(nil)
	t.Cleanup(func() { cronjob.SetDefault(prev) })

	got, ok := CronContent(&structs.Chats{ID: 1, Root: t.TempDir()})
	if !ok || !strings.Contains(got, "unavailable") {
		t.Fatalf("CronContent without store = %q (ok %v)", got, ok)
	}
}

func TestRunCronWithoutStore(t *testing.T) {
	prev := cronjob.Default()
	cronjob.SetDefault(nil)
	t.Cleanup(func() { cronjob.SetDefault(prev) })

	ret := exec(t, &structs.Chats{ID: 1, Root: t.TempDir()}, map[string]any{"type": "add"})
	requireFailure(t, ret, "cron store is unavailable")
}
