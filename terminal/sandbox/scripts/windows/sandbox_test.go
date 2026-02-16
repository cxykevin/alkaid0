//go:build windows

package windows

import (
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var securityTestMutex sync.Mutex

func TestGetToken(t *testing.T) {
	tkn, err := getToken()
	if err != nil {
		t.Error(err)
	}
	defer tkn.Close()
	t.Log(tkn)
}

func TestCreateWellknownSIDs(t *testing.T) {
	sids, err := createWellknownSIDs()
	if err != nil {
		t.Error(err)
	}
	t.Log(sids)
}

func TestCreateMediumIntegritySID(t *testing.T) {
	sid, err := createMediumIntegritySID()
	if err != nil {
		t.Error(err)
	}
	t.Log(sid)
}

func TestGetPrivilegeLUID(t *testing.T) {
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
