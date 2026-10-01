//go:build windows

package sandbox

import (
	"os"
	"os/user"
	"strings"
	"testing"

	winSandbox "github.com/cxykevin/alkaid0/terminal/sandbox/scripts/windows"
)

// TestSameWindowsAccount 覆盖账户名等价判定：用户名校验大小写，域名只在一方缺省时忽略。
func TestSameWindowsAccount(t *testing.T) {
	host, err := os.Hostname()
	if err != nil || host == "" {
		t.Skipf("无法获取计算机名: %v", err)
	}

	cases := []struct {
		a, b string
		want bool
	}{
		{"Administrator", "administrator", true},
		{"Administrator", `.\administrator`, true},
		{host + `\Administrator`, "administrator", true},
		{host + `\administrator`, `.\administrator`, true},
		{host + `\ADMINISTRATOR`, host + `\administrator`, true},
		{`otherhost\administrator`, "administrator", false},
		{`otherhost\administrator`, host + `\administrator`, false},
		{`otherhost\administrator`, `otherhost\Administrator`, true},
		{`otherhost\administrator`, `anotherhost\administrator`, false},
		{winSandbox.UserName, "administrator", false},
	}
	for _, c := range cases {
		if got := sameWindowsAccount(c.a, c.b); got != c.want {
			t.Errorf("sameWindowsAccount(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// TestWindowsRunAsResolution 覆盖 Windows 上的解析与回退语义：
// 当前用户静默忽略；其它用户不切换但给出告警；沙盒账户命中专用提示。
func TestWindowsRunAsResolution(t *testing.T) {
	cur, err := user.Current()
	if err != nil {
		t.Skipf("无法获取当前用户: %v", err)
	}

	// 「.\用户」是本机账户的常见写法，用当前用户派生，避免依赖具体测试机的用户名。
	local := cur.Username
	if idx := strings.LastIndex(local, `\`); idx >= 0 {
		local = local[idx+1:]
	}

	for _, name := range []string{cur.Username, strings.ToUpper(cur.Username), `.\` + local} {
		ru, err := resolveRunAsUser(name)
		if err != nil {
			t.Fatalf("resolveRunAsUser(%q) 失败: %v", name, err)
		}
		if !ru.sameAsCurrent {
			t.Errorf("resolveRunAsUser(%q) 未标记为当前用户", name)
		}
		if use, reason := prepareRunAs(ru); use || reason != "" {
			t.Errorf("prepareRunAs(%q) = (%v, %q)，期望静默忽略", name, use, reason)
		}
		if got := resolveRunAsOrWarn(name); got != nil {
			t.Errorf("resolveRunAsOrWarn(%q) 应为 nil（静默忽略）", name)
		}
	}

	ru, err := resolveRunAsUser("otheruser")
	if err != nil {
		t.Fatalf("resolveRunAsUser(otheruser) 失败: %v", err)
	}
	if use, reason := prepareRunAs(ru); use || reason == "" {
		t.Errorf("其它用户应回退当前用户并给出原因，得到 use=%v reason=%q", use, reason)
	}
	if resolveRunAsOrWarn("otheruser") != nil {
		t.Error("resolveRunAsOrWarn(otheruser) 应为 nil（不阻断命令）")
	}

	ru, err = resolveRunAsUser(winSandbox.UserName)
	if err != nil {
		t.Fatalf("resolveRunAsUser(%q) 失败: %v", winSandbox.UserName, err)
	}
	if _, reason := prepareRunAs(ru); !strings.Contains(reason, "沙盒账户") {
		t.Errorf("沙盒账户应命中专用提示，得到 %q", reason)
	}

	if _, err := resolveRunAsUser("   "); err == nil {
		t.Error("空白用户名应报错")
	}
	if resolveRunAsOrWarn("   ") != nil {
		t.Error("空白用户名应静默返回 nil")
	}
}
