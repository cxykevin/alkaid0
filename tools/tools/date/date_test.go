package date

import (
	"fmt"
	"testing"
	"time"
)

// TestTodayDateInDefaultDash 验证默认（未设置环境变量）日期分隔符一律为 -
func TestTodayDateInDefaultDash(t *testing.T) {
	// 确保环境变量未启用（空值与未设置对 Getenv 等价）
	t.Setenv("FXXK_ANTHROPIC", "")

	cases := []struct {
		name string
		loc  *time.Location
	}{
		{"UTC", time.UTC},
		{"UTC+8", time.FixedZone("CST", 8*60*60)},
		{"UTC-8", time.FixedZone("PST", -8*60*60)},
		{"UTC+5:30", time.FixedZone("IST", 5*60*60 + 30*60)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := todayDateIn(tc.loc)
			now := time.Now().In(tc.loc)
			want := fmt.Sprintf("Today date is: %s", now.Format("2006-01-02"))
			if got != want {
				t.Errorf("todayDateIn(%s) = %q, want %q", tc.name, got, want)
			}
		})
	}
}

// TestTodayDateInEnvSetUTC8Slash 验证设置环境变量 FXXK_ANTHROPIC=1 且时区为 UTC+8 时日期分隔符为 /
func TestTodayDateInEnvSetUTC8Slash(t *testing.T) {
	t.Setenv("FXXK_ANTHROPIC", "1")

	loc := time.FixedZone("CST", 8*60*60)
	got := todayDateIn(loc)
	now := time.Now().In(loc)
	want := fmt.Sprintf("Today date is: %s", now.Format("2006/01/02"))
	if got != want {
		t.Errorf("todayDateIn(UTC+8, env=1) = %q, want %q", got, want)
	}
}

// TestTodayDateInEnvSetOtherZonesDash 验证设置环境变量但时区非 UTC+8 时日期分隔符仍为 -
func TestTodayDateInEnvSetOtherZonesDash(t *testing.T) {
	t.Setenv("FXXK_ANTHROPIC", "1")

	cases := []struct {
		name string
		loc  *time.Location
	}{
		{"UTC", time.UTC},
		{"UTC-8", time.FixedZone("PST", -8*60*60)},
		{"UTC+5:30", time.FixedZone("IST", 5*60*60 + 30*60)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := todayDateIn(tc.loc)
			now := time.Now().In(tc.loc)
			want := fmt.Sprintf("Today date is: %s", now.Format("2006-01-02"))
			if got != want {
				t.Errorf("todayDateIn(%s, env=1) = %q, want %q", tc.name, got, want)
			}
		})
	}
}

// TestBuildGlobalPrompt 验证 PreHook 注入文本前缀正确
func TestBuildGlobalPrompt(t *testing.T) {
	got, err := buildGlobalPrompt(nil)
	if err != nil {
		t.Fatalf("buildGlobalPrompt error: %v", err)
	}
	if len(got) < len("Today date is: ") {
		t.Errorf("buildGlobalPrompt result too short: %q", got)
	}
	if got[:len("Today date is: ")] != "Today date is: " {
		t.Errorf("buildGlobalPrompt prefix = %q, want %q", got, "Today date is: ...")
	}
}
