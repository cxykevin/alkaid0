//go:build linux

package sandbox

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"testing"
)

// chownCall 记录一次 chownFn 调用。
type chownCall struct {
	path     string
	uid, gid int
}

// stubChown 替换 chownFn 记录调用（测试结束自动恢复），返回记录切片。
func stubChown(t *testing.T) *[]chownCall {
	t.Helper()
	orig := chownFn
	calls := &[]chownCall{}
	chownFn = func(path string, uid, gid int) error {
		*calls = append(*calls, chownCall{path: path, uid: uid, gid: gid})
		return nil
	}
	t.Cleanup(func() { chownFn = orig })
	return calls
}

// TestResolveFileOwnerDisabled 未配置用户与用户组时不设置属主、也不告警。
func TestResolveFileOwnerDisabled(t *testing.T) {
	owner, warn := resolveFileOwner("", "")
	if owner == nil || owner.active {
		t.Fatalf("未配置时应为 inactive，得到 %+v", owner)
	}
	if warn != "" {
		t.Fatalf("未配置时不应告警，得到 %q", warn)
	}
}

// TestResolveFileOwnerGroupWithoutUser 只配置用户组时忽略配置并告警。
func TestResolveFileOwnerGroupWithoutUser(t *testing.T) {
	owner, warn := resolveFileOwner("", "root")
	if owner.active {
		t.Fatalf("只配置用户组时应为 inactive，得到 %+v", owner)
	}
	if warn == "" {
		t.Fatalf("只配置用户组时应告警提示未配置 Agent.User")
	}
}

// TestResolveFileOwnerUnknown 用户或用户组不存在时忽略配置并告警。
func TestResolveFileOwnerUnknown(t *testing.T) {
	if _, warn := resolveFileOwner("alkaid0-no-such-user-xyz", ""); warn == "" {
		t.Errorf("未知用户应告警")
	}
	self, err := user.Current()
	if err != nil {
		t.Skipf("无法获取当前用户: %v", err)
	}
	if _, warn := resolveFileOwner(self.Username, "alkaid0-no-such-group-xyz"); warn == "" {
		t.Errorf("未知用户组应告警")
	}
}

// TestResolveFileOwnerCurrentUserInactive 目标身份与当前进程一致时不设置属主、也不告警。
func TestResolveFileOwnerCurrentUserInactive(t *testing.T) {
	self, err := user.Current()
	if err != nil {
		t.Skipf("无法获取当前用户: %v", err)
	}
	uid, err := strconv.Atoi(self.Uid)
	if err != nil || uid != os.Geteuid() {
		t.Skip("当前用户 uid 与进程 euid 不一致，该分支不适用")
	}
	gid, err := strconv.Atoi(self.Gid)
	if err != nil || gid != os.Getegid() {
		t.Skip("当前用户主组与进程 egid 不一致，该分支不适用")
	}
	owner, warn := resolveFileOwner(self.Username, "")
	if owner.active {
		t.Fatalf("与当前身份一致时应为 inactive，得到 %+v", owner)
	}
	if warn != "" {
		t.Fatalf("与当前身份一致时不应告警，得到 %q", warn)
	}
}

// TestCachedFileOwnerReusesResult 相同配置复用同一份解析结果（避免重复告警）。
func TestCachedFileOwnerReusesResult(t *testing.T) {
	first := cachedFileOwner("alkaid0-no-such-user-xyz", "")
	second := cachedFileOwner("alkaid0-no-such-user-xyz", "")
	if first != second {
		t.Fatalf("相同配置应复用缓存实例")
	}
}

// TestFileOwnerApplyInactiveIsNoop inactive 时不触碰文件属主。
func TestFileOwnerApplyInactiveIsNoop(t *testing.T) {
	calls := stubChown(t)
	(&fileOwner{uid: 1000, gid: 1000}).apply("alkaid0-inactive")
	if len(*calls) != 0 {
		t.Fatalf("inactive 时不应调用 chown，得到 %v", *calls)
	}
}

// TestFileOwnerApplyActive 生效时按目标 uid/gid 调用 chown。
func TestFileOwnerApplyActive(t *testing.T) {
	calls := stubChown(t)
	(&fileOwner{uid: 1234, gid: 5678, active: true}).apply("/tmp/alkaid0-owner-test")
	if len(*calls) != 1 {
		t.Fatalf("应调用一次 chown，得到 %v", *calls)
	}
	got := (*calls)[0]
	if got.path != "/tmp/alkaid0-owner-test" || got.uid != 1234 || got.gid != 5678 {
		t.Fatalf("chown 参数异常: %+v", got)
	}
}

// TestFileOwnerApplyFailureStillRetries 设置失败不阻断后续调用（每次仍会尝试，仅告警一次）。
func TestFileOwnerApplyFailureStillRetries(t *testing.T) {
	orig := chownFn
	t.Cleanup(func() { chownFn = orig })
	attempts := 0
	chownFn = func(string, int, int) error {
		attempts++
		return errors.New("boom")
	}
	owner := &fileOwner{uid: 1, gid: 2, active: true}
	owner.apply("a")
	owner.apply("b")
	if attempts != 2 {
		t.Fatalf("每次 apply 都应尝试 chown，得到 %d 次", attempts)
	}
}

// TestMissingDirs 返回尚未存在的各级祖先（由浅到深），已存在的返回空。
func TestMissingDirs(t *testing.T) {
	if got := missingDirs(os.TempDir()); len(got) != 0 {
		t.Fatalf("已存在的目录应返回空，得到 %v", got)
	}
	base := t.TempDir()
	target := filepath.Join(base, "a", "b", "c")
	got := missingDirs(target)
	want := []string{
		filepath.Join(base, "a"),
		filepath.Join(base, "a", "b"),
		filepath.Join(base, "a", "b", "c"),
	}
	if len(got) != len(want) {
		t.Fatalf("missingDirs = %v，期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("missingDirs[%d] = %q，期望 %q", i, got[i], want[i])
		}
	}
}
