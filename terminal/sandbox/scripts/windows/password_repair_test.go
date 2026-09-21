//go:build windows

package windows

import (
	"os"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// TestInitAlkaid0SandboxUserRepairsMissingPassword 回归测试：账户已存在但注册表
// 没有 accountPassword（上次初始化中途失败留下的状态）时，InitAlkaid0SandboxUser
// 不能像旧实现那样直接返回成功——那样 createRunToken 永远拿不到密码、登录失败，
// 沙箱永久不可用。必须重置现有账户密码并写回注册表。
//
// 需要管理员权限并设置 ALKAID0_TEST_SANDBOX=true（会创建/修改本机沙盒账户）。
func TestInitAlkaid0SandboxUserRepairsMissingPassword(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_SANDBOX") == "" {
		t.Skip("跳过隔离测试（设置 ALKAID0_TEST_SANDBOX=true 启用）")
	}
	if err := InitAlkaid0SandboxUser(); err != nil {
		t.Fatalf("首次初始化失败: %v", err)
	}

	key, _, err := registry.CreateKey(registry.LOCAL_MACHINE, `Software\Alkaid0\sandbox`, registry.ALL_ACCESS)
	if err != nil {
		t.Fatalf("打开注册表键失败: %v", err)
	}
	defer key.Close()

	// 只删密码记录、保留账户，复现"账户已建、密码未存"的中途失败状态。
	if err := key.DeleteValue("accountPassword"); err != nil {
		t.Fatalf("删除 accountPassword 失败: %v", err)
	}

	if err := InitAlkaid0SandboxUser(); err != nil {
		t.Fatalf("修复初始化失败: %v", err)
	}
	pw, _, err := key.GetBinaryValue("accountPassword")
	if err != nil || len(pw) == 0 {
		t.Fatalf("账户已存在时必须重置密码并写回 accountPassword（旧实现返回成功但不写）: len=%d err=%v", len(pw), err)
	}

	// 修复后的密码必须真的能登录，否则沙箱仍然不可用。
	tkn, err := createRunToken()
	if err != nil {
		t.Fatalf("修复后 createRunToken 失败: %v", err)
	}
	tkn.Close()
}
