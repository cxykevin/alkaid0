//go:build windows

package windows

import (
	"fmt"
	"os"
	"path"
	"syscall"
	"unsafe"

	winExtra "github.com/cxykevin/alkaid0/terminal/sandbox/scripts/windows/windows_extra"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// getToken 获取当前进程令牌。
//
// 注意：调用方**必须**在拿到令牌后 defer tkn.Close()。OpenProcessToken 返回的是
// 一个真实的内核句柄（windows.Token 底层就是 Handle，Close 走 CloseHandle），
// 不关闭就会每次调用泄漏一个句柄 —— 而 GetDACL/GetDenyDACL 是每个沙盒命令、
// 每个可写目录都要调用一次的，泄漏速度很快。
//
// 关闭句柄不影响已经取出的数据：GetTokenUser 返回的 TOKEN_USER 是 x/sys 在 Go
// 堆上分配的副本（getInfo 里 make([]byte, n)），与句柄生命周期无关。
func getToken() (*windows.Token, error) {
	process := windows.CurrentProcess()
	var token windows.Token
	err := windows.OpenProcessToken(process, windows.TOKEN_ALL_ACCESS, &token)
	return &token, err
}

// WellknownSIDs 已知SID
type WellknownSIDs struct {
	Admin               *windows.SID
	PowerUser           *windows.SID
	WriteRestrictedCode *windows.SID
	BuiltinUsers        *windows.SID
	World               *windows.SID
	Network             *windows.SID
}

// createWellknownSIDs 获取已知SID
func createWellknownSIDs() (*WellknownSIDs, error) {
	SIDAdmin, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return nil, err
	}
	SIDPowerUser, err := windows.CreateWellKnownSid(windows.WinBuiltinPowerUsersSid)
	if err != nil {
		return nil, err
	}
	SIDRestrictedCode, err := windows.CreateWellKnownSid(windows.WinWriteRestrictedCodeSid)
	if err != nil {
		return nil, err
	}
	SIDBuiltinUsers, err := windows.CreateWellKnownSid(windows.WinBuiltinUsersSid)
	if err != nil {
		return nil, err
	}
	SIDWorld, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		return nil, err
	}
	SIDNetwork, err := windows.CreateWellKnownSid(windows.WinNetworkServiceSid)
	if err != nil {
		return nil, err
	}
	return &WellknownSIDs{SIDAdmin, SIDPowerUser, SIDRestrictedCode, SIDBuiltinUsers, SIDWorld, SIDNetwork}, nil
}

var securityAuthority = windows.SidIdentifierAuthority{
	Value: [6]byte{0, 0, 0, 0, 0, 16},
} // S-1-16

// createMediumIntegritySID 创建中完整性SID
func createMediumIntegritySID() (*windows.SID, error) {
	var SID *windows.SID
	err := windows.AllocateAndInitializeSid(
		&securityAuthority,
		1, // 一个有效SID
		winExtra.SecurityMandatoryMediumRID,
		114514,
		1919810,
		0,
		0,
		0,
		0,
		0,
		&SID,
	)
	return SID, err
}

// getPrivilegeLUID 查询特权 LUID
func getPrivilegeLUID(name string) (windows.LUID, error) {
	var luid windows.LUID
	// 将特权名称转换为 UTF16
	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return luid, err
	}

	err = windows.LookupPrivilegeValue(nil, namePtr, &luid)
	return luid, err
}

// createRestrictedToken 创建受限令牌
func createRestrictedToken(currentToken *windows.Token, wkSIDs *WellknownSIDs) (*windows.Token, error) {
	disableSIDs := []windows.SIDAndAttributes{
		{
			Sid:        wkSIDs.Admin,
			Attributes: windows.SE_GROUP_USE_FOR_DENY_ONLY,
		},
		{
			Sid:        wkSIDs.PowerUser,
			Attributes: windows.SE_GROUP_USE_FOR_DENY_ONLY,
		},
	}

	backupPriv, err := getPrivilegeLUID("SeBackupPrivilege")
	if err != nil {
		return nil, err
	}
	restorePriv, err := getPrivilegeLUID("SeRestorePrivilege")
	if err != nil {
		return nil, err
	}
	debugPriv, err := getPrivilegeLUID("SeDebugPrivilege")
	if err != nil {
		return nil, err
	}
	shutdownPriv, err := getPrivilegeLUID("SeShutdownPrivilege")
	if err != nil {
		return nil, err
	}
	securityPriv, err := getPrivilegeLUID("SeSecurityPrivilege")
	if err != nil {
		return nil, err
	}
	assignPrimaryTokenPriv, err := getPrivilegeLUID("SeAssignPrimaryTokenPrivilege")
	if err != nil {
		return nil, err
	}
	// changeNotifyPriv, err := GetPrivilegeLUID("SeChangeNotifyPrivilege")
	// if err != nil {
	// 	return nil, err
	// }
	impersonatePriv, err := getPrivilegeLUID("SeImpersonatePrivilege")
	if err != nil {
		return nil, err
	}

	deletPrivs := []windows.LUIDAndAttributes{
		{
			Luid:       backupPriv,
			Attributes: windows.SE_PRIVILEGE_REMOVED,
		},
		{
			Luid:       restorePriv,
			Attributes: windows.SE_PRIVILEGE_REMOVED,
		},
		{
			Luid:       debugPriv,
			Attributes: windows.SE_PRIVILEGE_REMOVED,
		},
		{
			Luid:       shutdownPriv,
			Attributes: windows.SE_PRIVILEGE_REMOVED,
		},
		{
			Luid:       securityPriv,
			Attributes: windows.SE_PRIVILEGE_REMOVED,
		},
		{
			Luid:       assignPrimaryTokenPriv,
			Attributes: windows.SE_PRIVILEGE_REMOVED,
		},
		{
			Luid:       impersonatePriv,
			Attributes: windows.SE_PRIVILEGE_REMOVED,
		},
		// {
		// 	Luid:       changeNotifyPriv,
		// 	Attributes: windows.SE_PRIVILEGE_REMOVED,
		// },
	}

	usr, err := currentToken.GetTokenUser()
	if err != nil {
		return nil, err
	}

	restrictSIDs := []windows.SIDAndAttributes{
		{
			Sid:        wkSIDs.WriteRestrictedCode,
			Attributes: 0,
		},
		{
			Sid:        wkSIDs.Network,
			Attributes: 0,
		},
		{
			Sid:        wkSIDs.BuiltinUsers,
			Attributes: 0,
		},
		{
			Sid:        wkSIDs.World,
			Attributes: 0,
		},
		{
			Sid:        usr.User.Sid,
			Attributes: 0,
		},
		// {
		// 	Sid:        wkSIDs.Admin,
		// 	Attributes: 0,
		// },
	}

	token, err := winExtra.LibCreateRestrictedToken(
		*currentToken,
		winExtra.LibCreateRestrictedTokenFlagsDisableMaxPrivilege,
		uint32(len(disableSIDs)),
		&disableSIDs[0],
		uint32(len(deletPrivs)),
		&deletPrivs[0],
		uint32(len(restrictSIDs)),
		&restrictSIDs[0],
	)

	// token, err := libCreateRestrictedToken(
	// 	*currentToken,
	// 	LibCreateRestrictedTokenFlagsLUAToken, // 使用普通用户权限防止乱 Kill
	// 	0,
	// 	nil,
	// 	0,
	// 	nil,
	// 	0,
	// 	nil,
	// )
	return &token, err
}

// setTokenIntegrityMediumLevel 设置令牌完整性
func setTokenIntegrityMediumLevel(token *windows.Token) error {

	// tokenMandatoryLabel 结构体
	type tokenMandatoryLabel struct {
		Label windows.SIDAndAttributes
	}

	SID, err := createMediumIntegritySID()
	if err != nil {
		return err
	}
	defer windows.FreeSid(SID)

	// 计算所需缓冲区大小
	sidLen := windows.GetLengthSid(SID)
	tmlSize := unsafe.Sizeof(tokenMandatoryLabel{}) + uintptr(sidLen)

	// 分配内存
	buf := make([]byte, tmlSize)
	tml := (*tokenMandatoryLabel)(unsafe.Pointer(&buf[0]))

	// 设置 SID 指针（紧跟在结构体后面）
	tml.Label.Sid = (*windows.SID)(
		unsafe.Add(
			unsafe.Pointer(&buf[0]),
			unsafe.Sizeof(tokenMandatoryLabel{}),
		),
	)

	// 复制 SID
	err = windows.CopySid(uint32(sidLen), tml.Label.Sid, SID)
	if err != nil {
		return fmt.Errorf("CopySid failed: %w", err)
	}

	// SE_GROUP_INTEGRITY | SE_GROUP_INTEGRITY_ENABLED
	tml.Label.Attributes = windows.SE_GROUP_INTEGRITY | windows.SE_GROUP_INTEGRITY_ENABLED

	err = windows.SetTokenInformation(
		*token,
		windows.TokenIntegrityLevel,
		&buf[0],
		uint32(tmlSize),
	)
	return err
}

// UserName 沙盒用户名
const UserName = "alk-sandbox$"

// UserFullName 沙盒用户名全名
const UserFullName = "Alkaid0 Sandbox Account"

// UserNameNotice 沙盒用户名提示
const UserNameNotice = "Alkaid0 sandbox account, for agent sandbox use only."

const dpapiEntropy = "alkaid0-sandbox-registry-password-v1"

// 杂项配置
const (
	codePage    = 65001
	countryCode = 0
)

// getAccountSID 获取账户 SID
func getAccountSID(accountName string) (*windows.SID, error) {
	SID, _, _, err := windows.LookupSID("", accountName)

	return SID, err
}

// grantBatchLogonRight 赋予用户批处登录权限
func grantBatchLogonRight(accountName string) error {
	SID, err := getAccountSID(accountName)
	if err != nil {
		return err
	}
	// defer windows.FreeSid(SID)
	access := winExtra.LsaPolicyCreateAccount | winExtra.LsaPolicyLookupNames
	policyHandle, err := winExtra.LibLsaOpenPolicy("", access)
	if err != nil {
		return err
	}
	defer winExtra.LibLsaClose(policyHandle)
	err = winExtra.LibLsaAddAccountRights(policyHandle, SID, "SeBatchLogonRight")
	return err
	// return nil
}

// grantCurrentUserAssignLogonRight 赋予用户切换令牌权限
func grantCurrentUserAssignLogonRight() error {
	tkn, err := getToken()
	if err != nil {
		return err
	}
	defer tkn.Close() // OpenProcessToken 的句柄必须关闭，否则每次调用泄漏一个
	usr, err := tkn.GetTokenUser()
	if err != nil {
		return err
	}
	SID := usr.User.Sid
	access := winExtra.LsaPolicyCreateAccount | winExtra.LsaPolicyLookupNames
	policyHandle, err := winExtra.LibLsaOpenPolicy("", access)
	if err != nil {
		return err
	}
	defer winExtra.LibLsaClose(policyHandle)
	err = winExtra.LibLsaAddAccountRights(policyHandle, SID, "SeAssignPrimaryTokenPrivilege")
	if err != nil {
		return err
	}
	err = winExtra.LibLsaAddAccountRights(policyHandle, SID, "SeIncraseQuotaPrivilege")
	if err != nil {
		return err
	}
	return nil
}

// InitAlkaid0SandboxUser 初始化沙盒用户
func InitAlkaid0SandboxUser() error {
	key, _, err := registry.CreateKey(registry.LOCAL_MACHINE, "Software\\Alkaid0\\sandbox", registry.ALL_ACCESS)
	if err != nil {
		return err
	}
	defer key.Close()

	_, _, err = key.GetBinaryValue("accountPassword")
	if err == nil {
		if err = winExtra.SetRegistryKeyDACL(key); err != nil {
			return err
		}
		return nil
	}

	if legacyPassword, _, legacyErr := key.GetStringValue("accountPassword"); legacyErr == nil {
		encrypted, err := winExtra.LibCryptProtectData([]byte(legacyPassword), []byte(dpapiEntropy), winExtra.CryptProtectLocalMachine)
		if err != nil {
			return err
		}
		defer func() {
			for i := range encrypted {
				encrypted[i] = 0
			}
		}()
		if err = key.SetBinaryValue("accountPassword", encrypted); err != nil {
			return err
		}
		if err = winExtra.SetRegistryKeyDACL(key); err != nil {
			return err
		}
		return nil
	}

	if err := grantCurrentUserAssignLogonRight(); err != nil {
		// LSA 策略授权失败时返回错误，避免后续 CreateProc 因权限不足静默失败
		return err
	}

	userName, err := windows.UTF16PtrFromString(UserName)
	if err != nil {
		return err
	}
	userFull, err := windows.UTF16PtrFromString(UserFullName)
	if err != nil {
		return err
	}
	userNotice, err := windows.UTF16PtrFromString(UserNameNotice)
	if err != nil {
		return err
	}
	passwdStr := randomPasswordGen()
	passwd, err := windows.UTF16PtrFromString(passwdStr)
	if err != nil {
		return err
	}
	// 计算所需缓冲区大小
	tmlSize := unsafe.Sizeof(winExtra.UserInfoLevel2{})

	// 分配内存
	buf := make([]byte, tmlSize)
	tml := (*winExtra.UserInfoLevel2)(unsafe.Pointer(&buf[0]))

	tml.Name = userName
	tml.FullName = userFull
	tml.Comment = userNotice
	tml.Password = passwd
	tml.Priv = winExtra.UserInfoPrivUser
	tml.Workstations = nil
	tml.LogonHours = nil
	tml.CountryCode = countryCode
	tml.CodePage = codePage
	tml.LogonServer = winExtra.UserInfoLogonServerAllowAll
	tml.AcctExpires = winExtra.UserInfoTimeQForever
	tml.UnitsPerWeek = winExtra.UserInfoSamHoursPerWeek
	tml.MaxStorage = winExtra.UserInfoMaxstorageUnlimited
	tml.Flags = winExtra.UFDontExpirePasswd | winExtra.UFNormalAccount | winExtra.UFPasswdCantChange

	var errCode uint32
	ret, err := winExtra.LibNetUserAdd(nil, winExtra.LibNetUserAddLevel2, &buf[0], &errCode)
	// if err != nil {
	// 	return err
	// }
	if ret == 2224 { // 用户已经存在
		// 上次初始化可能中途失败，留下"账户已存在、注册表却没有 accountPassword"
		// 的状态。此处直接返回成功会让 createRunToken 永远登录失败，沙箱永久不可用。
		// 用本次生成的随机密码重置该账户密码，再走下面的共同流程把密码记录下来。
		ui := winExtra.UserInfo1003{Password: passwd}
		ret, err = winExtra.LibNetUserSetInfo(nil, userName, winExtra.LibNetUserSetInfoLevel1003, (*byte)(unsafe.Pointer(&ui)), &errCode)
		if ret != 0 {
			return fmt.Errorf("重置已存在沙盒账户的密码失败: %d(%v)", ret, err)
		}
	} else if ret != 0 {
		return fmt.Errorf("NetUserAdd failed: %d(%v)", ret, err)
	}

	if err = winExtra.SetRegistryKeyDACL(key); err != nil {
		return err
	}

	err = grantBatchLogonRight(UserName)
	if err != nil {
		return err
	}

	encrypted, err := winExtra.LibCryptProtectData([]byte(passwdStr), []byte(dpapiEntropy), winExtra.CryptProtectLocalMachine)
	if err != nil {
		return err
	}
	defer func() {
		for i := range encrypted {
			encrypted[i] = 0
		}
	}()

	err = key.SetBinaryValue("accountPassword", encrypted)
	if err != nil {
		return err
	}

	return nil
}

// createRunToken 创建运行令牌
func createRunToken() (*windows.Token, error) {

	key, err := registry.OpenKey(registry.LOCAL_MACHINE, "Software\\Alkaid0\\sandbox", registry.QUERY_VALUE)
	if err != nil {
		return nil, fmt.Errorf("get sandbox registry key failed: %v", err)
	}
	defer key.Close()
	passwordEnc, _, err := key.GetBinaryValue("accountPassword")
	if err != nil {
		return nil, fmt.Errorf("get key failed: %v", err)
	}
	passwordBytes, err := winExtra.LibCryptUnprotectData(passwordEnc, []byte(dpapiEntropy), winExtra.CryptProtectLocalMachine)
	if err != nil {
		return nil, err
	}
	defer func() {
		for i := range passwordBytes {
			passwordBytes[i] = 0
		}
	}()
	password := string(passwordBytes)

	wkSIDs, err := createWellknownSIDs()
	if err != nil {
		return nil, err
	}

	token, err := winExtra.LibLogonUserW(UserName, ".", password, 4, 0)
	if err != nil {
		return nil, err
	}
	// 登录令牌用完即关闭，避免每次沙盒化进程创建泄漏句柄
	defer token.Close()

	restrctTkn, err := createRestrictedToken(token, wkSIDs)
	if err != nil {
		return nil, err
	}

	err = setTokenIntegrityMediumLevel(restrctTkn)
	if err != nil {
		return nil, err
	}

	return restrctTkn, nil
}

// GetDACL 获取 DACL
func GetDACL() (*windows.ACL, error) {
	sid, err := getAccountSID("alk-sandbox$")
	if err != nil {
		return nil, err
	}
	wksids, err := createWellknownSIDs()
	if err != nil {
		return nil, err
	}
	tkn, err := getToken()
	if err != nil {
		return nil, err
	}
	defer tkn.Close() // OpenProcessToken 的句柄必须关闭，否则每次调用泄漏一个
	usr, err := tkn.GetTokenUser()
	if err != nil {
		return nil, err
	}
	currSID := usr.User.Sid

	access := []windows.EXPLICIT_ACCESS{
		{
			AccessPermissions: windows.GENERIC_ALL | windows.GENERIC_EXECUTE | windows.GENERIC_READ | windows.GENERIC_WRITE,
			AccessMode:        windows.GRANT_ACCESS,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
			Inheritance: windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		},
		{
			AccessPermissions: windows.GENERIC_ALL | windows.GENERIC_EXECUTE | windows.GENERIC_READ | windows.GENERIC_WRITE,
			AccessMode:        windows.GRANT_ACCESS,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(wksids.Admin),
			},
			Inheritance: windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		},
		{
			AccessPermissions: windows.GENERIC_ALL | windows.GENERIC_EXECUTE | windows.GENERIC_READ | windows.GENERIC_WRITE,
			AccessMode:        windows.GRANT_ACCESS,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(currSID),
			},
			Inheritance: windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		},
	}
	dacl, err := windows.ACLFromEntries(access, nil)
	if err != nil {
		return nil, err
	}

	return dacl, nil
}

// GetDenyDACL 获取拒绝 DACL，用来保护 .alkaid0 避免修改规则提权及回收
func GetDenyDACL() (*windows.ACL, error) {
	sid, err := getAccountSID("alk-sandbox$")
	if err != nil {
		return nil, err
	}
	wksids, err := createWellknownSIDs()
	if err != nil {
		return nil, err
	}
	tkn, err := getToken()
	if err != nil {
		return nil, err
	}
	defer tkn.Close() // OpenProcessToken 的句柄必须关闭，否则每次调用泄漏一个
	usr, err := tkn.GetTokenUser()
	if err != nil {
		return nil, err
	}
	currSID := usr.User.Sid

	access := []windows.EXPLICIT_ACCESS{
		{
			AccessPermissions: windows.GENERIC_ALL | windows.GENERIC_EXECUTE | windows.GENERIC_READ | windows.GENERIC_WRITE,
			AccessMode:        windows.DENY_ACCESS,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(sid),
			},
			Inheritance: windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		},
		{
			AccessPermissions: windows.GENERIC_ALL | windows.GENERIC_EXECUTE | windows.GENERIC_READ | windows.GENERIC_WRITE,
			AccessMode:        windows.GRANT_ACCESS,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(wksids.Admin),
			},
			Inheritance: windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		},
		{
			AccessPermissions: windows.GENERIC_ALL | windows.GENERIC_EXECUTE | windows.GENERIC_READ | windows.GENERIC_WRITE,
			AccessMode:        windows.GRANT_ACCESS,
			Trustee: windows.TRUSTEE{
				TrusteeForm:  windows.TRUSTEE_IS_SID,
				TrusteeType:  windows.TRUSTEE_IS_USER,
				TrusteeValue: windows.TrusteeValueFromSID(currSID),
			},
			Inheritance: windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		},
	}
	dacl, err := windows.ACLFromEntries(access, nil)

	if err != nil {
		return nil, err
	}

	return dacl, nil
}

// ApplyDACL 应用 DACL
func ApplyDACL(path string, dacl *windows.ACL) error {
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	)
}

// addPrivilegeToCurrentToken 添加权限
func addPrivilegeToCurrentToken(priv string) error {
	tkn, err := getToken()
	if err != nil {
		return err
	}
	defer tkn.Close() // OpenProcessToken 的句柄必须关闭，否则每次调用泄漏一个
	privLUID, err := getPrivilegeLUID(priv)
	if err != nil {
		return err
	}

	newStateBuffer := make([]byte, 4+unsafe.Sizeof(windows.LUIDAndAttributes{}))
	newState := (*windows.Tokenprivileges)(unsafe.Pointer(&newStateBuffer[0]))
	newState.PrivilegeCount = 1
	newState.Privileges[0].Luid = privLUID
	newState.Privileges[0].Attributes = windows.SE_PRIVILEGE_ENABLED

	err = windows.AdjustTokenPrivileges(*tkn, false, newState, uint32(len(newStateBuffer)), nil, nil)
	if err != nil {
		return err
	}
	return nil
}

func getSecurityDescriptor(DACL *windows.ACL) (*windows.SecurityAttributes, error) {
	sec := windows.SecurityAttributes{
		InheritHandle:      1,
		SecurityDescriptor: &windows.SECURITY_DESCRIPTOR{},
	}

	sd, err := windows.NewSecurityDescriptor()
	if err != nil {
		return nil, err
	}

	sd.SetDACL(DACL, true, false)

	sec.SecurityDescriptor, err = sd.ToSelfRelative()
	if err != nil {
		return nil, err
	}
	return &sec, nil
}

// CreateProc 创建线程
func CreateProc(appName string, commandLine string, workDir string, startupInfo *windows.StartupInfoEx, envPtr *uint16) (windows.ProcessInformation, error) {
	return createProc(appName, commandLine, workDir, startupInfo, envPtr, 0)
}

// createProc 与 CreateProc 相同，但允许附加 CreateProcess 创建标志。
// exec.Cmd.Start 传 CREATE_SUSPENDED：先加入 Job Object 再恢复运行，
// 避免进程在加入 Job 之前抢先派生出逃出 Job 树的孙进程。
func createProc(appName string, commandLine string, workDir string, startupInfo *windows.StartupInfoEx, envPtr *uint16, extraFlags uint32) (windows.ProcessInformation, error) {
	err := InitAlkaid0SandboxUser()
	if err != nil {
		return windows.ProcessInformation{}, fmt.Errorf("init user failed: %v", err)
	}

	err = addPrivilegeToCurrentToken("SeAssignPrimaryTokenPrivilege")
	if err != nil {
		return windows.ProcessInformation{}, fmt.Errorf("add privilege failed: %v", err)
	}
	err = addPrivilegeToCurrentToken("SeIncreaseQuotaPrivilege")
	if err != nil {
		return windows.ProcessInformation{}, fmt.Errorf("add privilege failed: %v", err)
	}
	token, err := createRunToken()
	if err != nil {
		return windows.ProcessInformation{}, fmt.Errorf("create token failed: %v", err)
	}
	defer token.Close()
	var pAppName *uint16 = nil
	if appName != "" {
		pAppName = windows.StringToUTF16Ptr(appName)
	}
	pCommandLine := windows.StringToUTF16Ptr(commandLine)

	dacl, err := GetDACL()
	if err != nil {
		return windows.ProcessInformation{}, err
	}
	sec, err := getSecurityDescriptor(dacl)
	if err != nil {
		return windows.ProcessInformation{}, err
	}

	var pWorkDir *uint16 = nil
	if workDir != "" {
		pWorkDir = windows.StringToUTF16Ptr(workDir)
	}

	var procInfo windows.ProcessInformation
	inheritHandles := startupInfo != nil && ((startupInfo.Flags&windows.STARTF_USESTDHANDLES) != 0 || startupInfo.ProcThreadAttributeList != nil)

	err = winExtra.CreateProcessAsUserEx(
		*token,
		pAppName,
		pCommandLine,
		sec,
		nil,
		inheritHandles,
		windows.CREATE_UNICODE_ENVIRONMENT|windows.EXTENDED_STARTUPINFO_PRESENT|extraFlags,
		envPtr,
		pWorkDir,
		startupInfo,
		&procInfo,
	)
	if err != nil {
		return windows.ProcessInformation{}, err
	}
	return procInfo, nil
}

// SetLimitToWorkdir 设置工作目录权限
// saveDACL 读取目录的 DACL 快照，供清理时恢复
func saveDACL(path string) (*windows.ACL, error) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return nil, err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return nil, err
	}
	return dacl, nil
}

// restoreDACL 恢复目录原始 DACL（不带 PROTECTED，让继承规则重新生效）
func restoreDACL(path string, dacl *windows.ACL) error {
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil,
	)
}

func SetLimitToWorkdir(workDir string) (func() error, error) {
	cleanFunc := func() error {
		return nil
	}
	// 保存原始 DACL，供清理时恢复（ApplyDACL 用 PROTECTED 整体替换会丢失目录原有的继承 ACE）
	origDACL, err := saveDACL(workDir)
	if err != nil {
		// 读取原始 DACL 失败时保留现状（清理仍按原逻辑收紧权限），不阻塞限制设置
		origDACL = nil
	}
	DACL, err := GetDACL()
	if err != nil {
		return cleanFunc, err
	}
	err = ApplyDACL(workDir, DACL)
	if err != nil {
		return cleanFunc, err
	}

	// workDir 已被改动，立刻把 cleanFunc 换成真正的还原逻辑：
	// 之后任何一步失败（.alkaid0 的 GetDenyDACL/ApplyDACL）都必须能回滚 workDir，
	// 否则调用方一旦失败返回，授予沙盒账户的写权限就永久留在了 workDir 上。
	cleanFunc = func() error {
		// 优先恢复原始 DACL，避免目录的继承 ACE（SYSTEM/Users 等）被永久移除
		if origDACL != nil {
			return restoreDACL(workDir, origDACL)
		}
		DenyDACL, err := GetDenyDACL()
		if err != nil {
			return err
		}
		err = ApplyDACL(workDir, DenyDACL)
		if err != nil {
			return err
		}
		return nil
	}

	_, err = os.Stat(path.Join(workDir, ".alkaid0"))
	// os.IsExist 只对"已存在"类错误返回 true，对成功 Stat（err==nil）恒为 false，
	// 必须用 err==nil 判断目录存在，否则保护规则目录的 DenyDACL 永远不会被应用。
	if err == nil {
		DenyDACL, err := GetDenyDACL()
		if err != nil {
			return cleanFunc, err
		}
		err = ApplyDACL(path.Join(workDir, ".alkaid0"), DenyDACL)
		if err != nil {
			return cleanFunc, err
		}
	}

	return cleanFunc, nil
}

// SetLimitToDir 为给定的若干目录临时授予沙盒账户访问权限，返回清理函数。
//
// 清理语义必须与 SetLimitToWorkdir 保持一致：**恢复每个目录的原始 DACL**。
//
// 这里绝对不能回填 GetDenyDACL()。ApplyDACL 带 PROTECTED_DACL_SECURITY_INFORMATION，
// 会整体替换目标目录的 DACL 并切断继承；而 GetDenyDACL 只包含三条 ACE
// （拒绝沙盒账户 + 授予 Administrators + 授予当前用户）。用它做"清理"意味着：
//   - 目标目录原有的继承 ACE（SYSTEM、Users、以及任何自定义授权）被永久删除；
//   - 由于该 DACL 带 SUB_CONTAINERS_AND_OBJECTS_INHERIT，破坏会向下传播到整棵子树
//     （例如 %TEMP% 与整个工作区），备份/索引/杀毒等以 SYSTEM 身份运行的组件从此
//     失去访问权；
//   - 若同时 SetLimitToWorkdir 恢复了 workDir，后执行的这一步会把恢复结果重新覆盖掉。
//
// 唯一"应当保留拒绝"的地方是 <workDir>/.alkaid0：那是会话数据库所在目录，
// 由 SetLimitToWorkdir 单独施加 DenyDACL 且刻意不恢复（见 GetDenyDACL 注释）。
// 因此调用方必须把 workDir 排除在本函数的列表之外（见 sandbox_windows.go）。
func SetLimitToDir(workDir []string) (func() error, error) {
	// 每个已施加限制的目录及其原始 DACL 快照；origDACL 为 nil 表示快照失败。
	type daclSnapshot struct {
		dir      string
		origDACL *windows.ACL
	}
	snapshots := make([]daclSnapshot, 0, len(workDir))

	cleanFunc := func() error {
		var firstErr error
		// 逆序恢复，与施加顺序相反
		for i := len(snapshots) - 1; i >= 0; i-- {
			snap := snapshots[i]
			var err error
			if snap.origDACL != nil {
				err = restoreDACL(snap.dir, snap.origDACL)
			} else {
				// 读取原始 DACL 失败时无法还原：退化为收紧沙盒账户权限。
				// 失败要"关闭"而不是把写权限留给沙盒账户（与 SetLimitToWorkdir 一致）。
				var denyDACL *windows.ACL
				if denyDACL, err = GetDenyDACL(); err == nil {
					err = ApplyDACL(snap.dir, denyDACL)
				}
			}
			if err != nil && firstErr == nil {
				firstErr = err
			}
		}
		// 幂等：重复调用不再重复恢复（例如错误路径已回滚过一次）
		snapshots = snapshots[:0]
		return firstErr
	}

	for _, dir := range workDir {
		origDACL, err := saveDACL(dir)
		if err != nil {
			// 与 SetLimitToWorkdir 相同：快照失败不阻塞限制设置，清理时退化为 DenyDACL
			origDACL = nil
		}
		DACL, err := GetDACL()
		if err != nil {
			return cleanFunc, err
		}
		err = ApplyDACL(dir, DACL)
		if err != nil {
			return cleanFunc, err
		}
		snapshots = append(snapshots, daclSnapshot{dir: dir, origDACL: origDACL})
	}

	return cleanFunc, nil
}
