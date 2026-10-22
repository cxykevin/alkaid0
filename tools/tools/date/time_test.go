package date

import (
	"regexp"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/tools/toolobj"
)

// TestFormatTimeIn 验证本地时间格式与 UTC 偏移写法（同一时刻在不同时区）。
func TestFormatTimeIn(t *testing.T) {
	// 固定时刻：UTC 2026-10-22 14:14:56。
	base := time.Date(2026, 10, 22, 14, 14, 56, 0, time.UTC)

	cases := []struct {
		name string
		loc  *time.Location
		want string
	}{
		{"UTC", time.UTC, "2026-10-22 14:14:56 GMT+0"},
		{"GMT+8", time.FixedZone("CST", 8*3600), "2026-10-22 22:14:56 GMT+8"},
		{"GMT-8", time.FixedZone("PST", -8*3600), "2026-10-22 06:14:56 GMT-8"},
		{"GMT+5:30", time.FixedZone("IST", 5*3600+30*60), "2026-10-22 19:44:56 GMT+5:30"},
		{"GMT-3:30", time.FixedZone("NST", -(3*3600 + 30*60)), "2026-10-22 10:44:56 GMT-3:30"},
		{"GMT+5:45", time.FixedZone("NPT", 5*3600+45*60), "2026-10-22 19:59:56 GMT+5:45"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatTimeIn(base, tc.loc); got != tc.want {
				t.Errorf("formatTimeIn(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestFormatGMTOffset(t *testing.T) {
	cases := []struct {
		offset int
		want   string
	}{
		{0, "GMT+0"},
		{8 * 3600, "GMT+8"},
		{-8 * 3600, "GMT-8"},
		{5*3600 + 30*60, "GMT+5:30"},
		{-(9*3600 + 30*60), "GMT-9:30"},
		{45 * 60, "GMT+0:45"},
	}
	for _, tc := range cases {
		if got := formatGMTOffset(tc.offset); got != tc.want {
			t.Errorf("formatGMTOffset(%d) = %q, want %q", tc.offset, got, tc.want)
		}
	}
}

// TestRunTimeResult 验证工具返回结构与本机时间的正确性。
func TestRunTimeResult(t *testing.T) {
	pass, cross, ret, err := runTime(nil, map[string]*any{}, nil)
	if err != nil {
		t.Fatalf("runTime error: %v", err)
	}
	if pass {
		t.Fatalf("runTime pass = true, want false")
	}
	if len(cross) != 0 {
		t.Fatalf("runTime cross = %#v, want empty", cross)
	}

	success, ok := ret["success"]
	if !ok || success == nil {
		t.Fatalf("result has no success field: %#v", ret)
	}
	if v, ok := (*success).(bool); !ok || !v {
		t.Fatalf("success = %#v, want true", *success)
	}

	value, ok := ret["time"]
	if !ok || value == nil {
		t.Fatalf("result has no time field: %#v", ret)
	}
	s, ok := (*value).(string)
	if !ok {
		t.Fatalf("time = %#v, want string", *value)
	}

	pattern := regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} GMT[+-]\d{1,2}(:\d{2})?$`)
	if !pattern.MatchString(s) {
		t.Fatalf("time %q does not match expected format", s)
	}

	// 解析回本地时间，确认与当前时刻一致（容忍 2 秒执行开销）。
	parsed, err := time.ParseInLocation("2006-01-02 15:04:05", s[:19], time.Local)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	if diff := time.Since(parsed); diff > 2*time.Second || diff < -2*time.Second {
		t.Fatalf("time %q differs from now by %v", s, diff)
	}
}

// TestLoadTimeRegistersTool 验证 time 工具以空参数注册，并挂上了执行钩子。
func TestLoadTimeRegistersTool(t *testing.T) {
	if got := loadTime(); got != timeToolName {
		t.Fatalf("loadTime() = %q, want %q", got, timeToolName)
	}

	tool := toolobj.GetTool(timeToolName)
	if tool == nil {
		t.Fatalf("tool %q is not registered", timeToolName)
	}
	if len(tool.Parameters) != 0 {
		t.Fatalf("tool %q parameters = %#v, want empty", timeToolName, tool.Parameters)
	}
	if tool.UserDescription == "" {
		t.Fatalf("tool %q has no description", timeToolName)
	}

	hooks := toolobj.GetToolHooks(timeToolName)
	found := false
	for _, h := range hooks {
		if h.PostHook.Func != nil {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("tool %q has no PostHook", timeToolName)
	}
}
