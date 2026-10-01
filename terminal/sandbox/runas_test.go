package sandbox

import "testing"

// envMap 把 KEY=VALUE 列表转成 map，并统计重复键（重复键会让子进程取到哪个值
// 变得不确定，必须避免）。
func envMap(t *testing.T, env []string) map[string]string {
	t.Helper()
	out := make(map[string]string, len(env))
	for _, kv := range env {
		key, value, ok := splitEnv(kv)
		if !ok {
			t.Fatalf("非法环境变量项: %q", kv)
		}
		if _, dup := out[key]; dup {
			t.Fatalf("环境变量键重复: %s", key)
		}
		out[key] = value
	}
	return out
}

func splitEnv(kv string) (string, string, bool) {
	for i := 0; i < len(kv); i++ {
		if kv[i] == '=' {
			return kv[:i], kv[i+1:], true
		}
	}
	return "", "", false
}

// TestApplyRunAsEnvReplacesIdentity 验证以指定用户运行时会改写身份相关环境变量，
// 且其它变量与环境键唯一性不受影响。
func TestApplyRunAsEnvReplacesIdentity(t *testing.T) {
	env := []string{"PATH=/usr/bin", "HOME=/root", "USER=root", "LOGNAME=root", "TERM=xterm-256color"}
	got := envMap(t, applyRunAsEnv(env, &runAsUser{Name: "alice", Home: "/home/alice"}))

	if got["HOME"] != "/home/alice" {
		t.Errorf("HOME = %q, 期望 /home/alice", got["HOME"])
	}
	if got["USER"] != "alice" || got["LOGNAME"] != "alice" {
		t.Errorf("USER/LOGNAME = %q/%q, 期望 alice", got["USER"], got["LOGNAME"])
	}
	if got["PATH"] != "/usr/bin" || got["TERM"] != "xterm-256color" {
		t.Errorf("无关环境变量被改动: PATH=%q TERM=%q", got["PATH"], got["TERM"])
	}
}

// TestApplyRunAsEnvWithoutUser 未指定用户时环境保持不变。
func TestApplyRunAsEnvWithoutUser(t *testing.T) {
	env := []string{"HOME=/root", "USER=root"}
	got := applyRunAsEnv(env, nil)
	if len(got) != len(env) {
		t.Fatalf("环境变量数量变化: %d -> %d", len(env), len(got))
	}
	for i := range env {
		if got[i] != env[i] {
			t.Fatalf("环境变量被改动: %q -> %q", env[i], got[i])
		}
	}
}

// TestApplyRunAsEnvWithoutHome 家目录未知时不写入空 HOME（避免覆盖成空值）。
func TestApplyRunAsEnvWithoutHome(t *testing.T) {
	got := envMap(t, applyRunAsEnv([]string{"HOME=/root"}, &runAsUser{Name: "alice"}))
	if _, ok := got["HOME"]; ok {
		t.Fatalf("家目录未知时不应写入 HOME: %q", got["HOME"])
	}
	if got["USER"] != "alice" {
		t.Fatalf("USER = %q, 期望 alice", got["USER"])
	}
}

// TestResolveRunAsOrWarnEmpty 未配置用户时不产生 runAs（也不告警）。
func TestResolveRunAsOrWarnEmpty(t *testing.T) {
	if ru := resolveRunAsOrWarn(""); ru != nil {
		t.Fatalf("空用户名应返回 nil，得到 %+v", ru)
	}
}

// TestResolveRunAsOrWarnUnknownUser 用户不存在时回退当前用户（返回 nil）。
func TestResolveRunAsOrWarnUnknownUser(t *testing.T) {
	if ru := resolveRunAsOrWarn("alkaid0-no-such-user-xyz"); ru != nil {
		t.Fatalf("未知用户应返回 nil，得到 %+v", ru)
	}
}
