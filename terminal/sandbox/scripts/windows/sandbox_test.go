//go:build windows

package windows

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var securityTestMutex sync.Mutex

func TestGetToken(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_SANDBOX") == "" {
		t.Skip("跳过隔离测试（设置 ALKAID0_TEST_SANDBOX=true 启用）")
	}
	tkn, err := getToken()
	if err != nil {
		t.Error(err)
	}
	defer tkn.Close()
	t.Log(tkn)
}

func TestCreateWellknownSIDs(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_SANDBOX") == "" {
		t.Skip("跳过隔离测试（设置 ALKAID0_TEST_SANDBOX=true 启用）")
	}
	sids, err := createWellknownSIDs()
	if err != nil {
		t.Error(err)
	}
	t.Log(sids)
}

func TestCreateMediumIntegritySID(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_SANDBOX") == "" {
		t.Skip("跳过隔离测试（设置 ALKAID0_TEST_SANDBOX=true 启用）")
	}
	sid, err := createMediumIntegritySID()
	if err != nil {
		t.Error(err)
	}
	t.Log(sid)
}

func TestGetPrivilegeLUID(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_SANDBOX") == "" {
		t.Skip("跳过隔离测试（设置 ALKAID0_TEST_SANDBOX=true 启用）")
	}
	backupPriv, err := getPrivilegeLUID("SeBackupPrivilege")
	if err != nil {
		t.Errorf("get backupPrivilegeLUID failed: %v", err)
	}
	t.Logf("backupPrivilegeLUID: %v", backupPriv)
	restorePriv, err := getPrivilegeLUID("SeRestorePrivilege")
	if err != nil {
		t.Errorf("get restorePrivilegeLUID failed: %v", err)
	}
	t.Logf("restorePrivilegeLUID: %v", restorePriv)
	debugPriv, err := getPrivilegeLUID("SeDebugPrivilege")
	if err != nil {
		t.Errorf("get debugPrivilegeLUID failed: %v", err)
	}
	t.Logf("debugPrivilegeLUID: %v", debugPriv)
	changeNotifyPriv, err := getPrivilegeLUID("SeChangeNotifyPrivilege")
	if err != nil {
		t.Errorf("get changeNotifyPrivilegeLUID failed: %v", err)
	}
	t.Logf("changeNotifyPrivilegeLUID: %v", changeNotifyPriv)
}

func TestCreateRestrictedToken(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_SANDBOX") == "" {
		t.Skip("跳过隔离测试（设置 ALKAID0_TEST_SANDBOX=true 启用）")
	}
	tkn, err := getToken()
	if err != nil {
		t.Error(err)
	}
	defer tkn.Close()
	sids, err := createWellknownSIDs()
	if err != nil {
		t.Error(err)
	}
	restrictedToken, err := createRestrictedToken(tkn, sids)
	if err != nil {
		t.Error(err)
	}
	defer restrictedToken.Close()
	t.Log(restrictedToken)
}

func TestInitAlkaid0SandboxUser(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_SANDBOX") == "" {
		t.Skip("跳过隔离测试（设置 ALKAID0_TEST_SANDBOX=true 启用）")
	}
	securityTestMutex.Lock()
	defer securityTestMutex.Unlock()

	cmd := exec.Command("cmd", "/c", "net user alk-sandbox$ /delete")
	cmd.Run()

	key1, exist1, err := registry.CreateKey(registry.LOCAL_MACHINE, "Software\\Alkaid0\\sandbox", registry.ALL_ACCESS)
	if err != nil {
		t.Error(err)
	}
	if exist1 {
		key1.DeleteValue("accountPassword")
	}

	err = InitAlkaid0SandboxUser()
	if err != nil {
		t.Error(err)
	}

	cmd2 := exec.Command("cmd", "/c", "net user alk-sandbox$")
	err = cmd2.Run()
	if err != nil {
		t.Fatal(err)
	}

	// key, exist, err := registry.CreateKey(registry.LOCAL_MACHINE, "Software\\Alkaid0\\sandbox", registry.ALL_ACCESS)
	// if err != nil {
	// 	t.Error(err)
	// }
	// if !exist {
	// 	t.Error("registry key not exist")
	// }
	// val, _, err := key.GetStringValue("accountPassword")
	// if err != nil {
	// 	t.Error(err)
	// }
	// t.Logf("password: %v", val)

	// err = registry.DeleteKey(key, "")
	// if err != nil {
	// 	t.Error(err)
	// }

	// cmd4 := exec.Command("cmd", "/c", "net user alk-sandbox$ /delete")
	// cmd4.Run()
}

func TestGetAccountSID(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_SANDBOX") == "" {
		t.Skip("跳过隔离测试（设置 ALKAID0_TEST_SANDBOX=true 启用）")
	}
	sid, err := getAccountSID("SYSTEM")
	if err != nil {
		t.Error(err)
	}
	t.Logf("SYSTEM sid: %v", sid)
	if sid.String() != "S-1-5-18" {
		t.Error("SYSTEM sid error")
	}

	sid, err = getAccountSID("Administrator")
	if err != nil {
		t.Error(err)
	}
	t.Logf("Administrator sid: %v", sid)

}

// func TestGetAccountSIDForTest(t *testing.T) {
// 	sid, err := GetAccountSID("alk-sandbox$")
// 	if err != nil {
// 		t.Error(err)
// 	}
// 	t.Logf("sid: %v", sid)
// }

func TestCreateRunToken(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_SANDBOX") == "" {
		t.Skip("跳过隔离测试（设置 ALKAID0_TEST_SANDBOX=true 启用）")
	}
	securityTestMutex.Lock()
	defer securityTestMutex.Unlock()

	tkn, err := createRunToken()
	if err != nil {
		t.Fatalf("CreateRunToken failed: %v", err)
	}
	defer tkn.Close()
	t.Logf("Token: %v", tkn)

}

func TestGetDACL(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_SANDBOX") == "" {
		t.Skip("跳过隔离测试（设置 ALKAID0_TEST_SANDBOX=true 启用）")
	}
	time.Sleep(3 * time.Second)
	securityTestMutex.Lock()
	defer securityTestMutex.Unlock()

	DACL, err := GetDACL()
	if err != nil {
		t.Fatalf("CreateRunToken failed: %v", err)
	}
	t.Logf("DACL: %v", DACL)
}

func TestApplyDACL(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_SANDBOX") == "" {
		t.Skip("跳过隔离测试（设置 ALKAID0_TEST_SANDBOX=true 启用）")
	}
	securityTestMutex.Lock()
	defer securityTestMutex.Unlock()

	dir, err := os.MkdirTemp("", "sandbox-acl-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(dir)
	DACL, err := GetDACL()
	if err != nil {
		t.Fatalf("GetDACL failed: %v", err)
	}
	err = ApplyDACL(dir, DACL)
	if err != nil {
		t.Fatalf("ApplyDACL failed: %v", err)
	}
	DACL, err = GetDenyDACL()
	if err != nil {
		t.Fatalf("GetDenyDACL failed: %v", err)
	}
	err = ApplyDACL(dir, DACL)
	if err != nil {
		t.Fatalf("ApplyDACL failed: %v", err)
	}
}

func TestCreateProc(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_SANDBOX") == "" {
		t.Skip("跳过隔离测试（设置 ALKAID0_TEST_SANDBOX=true 启用）")
	}
	securityTestMutex.Lock()
	defer securityTestMutex.Unlock()

	dir, err := os.MkdirTemp("", "sandbox-acl-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(dir)
	var stupInfo windows.StartupInfoEx
	stupInfo.Cb = uint32(unsafe.Sizeof(stupInfo))
	proc, err := CreateProc("C:\\Windows\\System32\\cmd.exe", "cmd /C \"echo hello world!\"", dir, &stupInfo, nil)
	if err != nil {
		t.Fatalf("CreateProc failed: %v", err)
	}
	t.Logf("Proc: %v", proc)
	time.Sleep(1 * time.Second)
}

// sddlOf 返回路径安全描述符的 SDDL 字符串（含 DACL），用于比较清理前后的权限。
func sddlOf(t *testing.T, path string) string {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo(%s) failed: %v", path, err)
	}
	if sd == nil {
		t.Fatalf("GetNamedSecurityInfo(%s) returned nil descriptor", path)
	}
	return sd.String()
}

// TestSetLimitToDirRestoresOriginalDACL 回归测试：SetLimitToDir 的清理必须**恢复原始
// DACL**，而不是回填 GetDenyDACL()。
//
// 背景：旧实现的清理函数对每个目录调用 ApplyDACL(dir, GetDenyDACL())。ApplyDACL 带
// PROTECTED_DACL_SECURITY_INFORMATION，会整体替换目录 DACL 并切断继承；GetDenyDACL
// 只有"拒绝沙盒账户 + 授予 Administrators + 授予当前用户"三条 ACE，于是目标目录原有的
// 继承 ACE（SYSTEM、Users 等）被永久删除，并因 SUB_CONTAINERS_AND_OBJECTS_INHERIT
// 向下传播到整棵子树（%TEMP%、整个工作区）。
//
// 本测试断言：清理后不得出现原始 DACL 中不存在的拒绝 ACE（旧实现必然残留），
// 且清理必须无错误返回。
func TestSetLimitToDirRestoresOriginalDACL(t *testing.T) {
	if os.Getenv("ALKAID0_TEST_SANDBOX") == "" {
		t.Skip("跳过隔离测试（设置 ALKAID0_TEST_SANDBOX=true 启用）")
	}
	securityTestMutex.Lock()
	defer securityTestMutex.Unlock()

	dir, err := os.MkdirTemp("", "sandbox-setlimit-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(dir)

	before := sddlOf(t, dir)
	if before == "" {
		t.Fatalf("无法读取 %s 的初始 SDDL，测试无法判定", dir)
	}
	beforeACL, err := mustDACL(t, dir)
	if err != nil {
		t.Fatalf("读取原始 DACL 失败: %v", err)
	}
	beforeAceCount := beforeACL.AceCount

	cleanup, err := SetLimitToDir([]string{dir})
	if err != nil {
		t.Fatalf("SetLimitToDir failed: %v", err)
	}

	during := sddlOf(t, dir)
	if during == before {
		t.Fatalf("SetLimitToDir 未改变 DACL，测试前提不成立：%s", before)
	}

	if err := cleanup(); err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}

	after := sddlOf(t, dir)
	if after == "" {
		t.Fatalf("清理后无法读取 %s 的 SDDL", dir)
	}

	// 原始 DACL 中没有拒绝 ACE，而 GetDenyDACL 必然写入一条 —— 这是旧实现最可靠的判别特征
	if !strings.Contains(before, "D;") && strings.Contains(after, "D;") {
		t.Errorf("清理后残留了拒绝 ACE，原始 DACL 被 DenyDACL 覆盖:\n before=%s\n after =%s", before, after)
	}

	// 清理后 ACE 数量应与原始一致（恢复而非替换）
	afterACL, err := mustDACL(t, dir)
	if err != nil {
		t.Fatalf("读取清理后 DACL 失败: %v", err)
	}
	if beforeAceCount != afterACL.AceCount {
		t.Errorf("清理后 ACE 数量不一致：before=%d after=%d\n before=%s\n after =%s",
			beforeAceCount, afterACL.AceCount, before, after)
	}
}

// mustDACL 取路径的 DACL
func mustDACL(t *testing.T, path string) (*windows.ACL, error) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return nil, err
	}
	if sd == nil {
		return nil, fmt.Errorf("nil security descriptor for %s", path)
	}
	dacl, _, err := sd.DACL()
	return dacl, err
}
