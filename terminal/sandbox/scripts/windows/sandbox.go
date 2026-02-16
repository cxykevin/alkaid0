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

// getToken 获取令牌
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

	grantCurrentUserAssignLogonRight()

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
		if err = winExtra.SetRegistryKeyDACL(key); err != nil {
			return err
		}
		return nil
	}
	if ret != 0 {
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
		windows.CREATE_UNICODE_ENVIRONMENT|windows.EXTENDED_STARTUPINFO_PRESENT,
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
func SetLimitToWorkdir(workDir string) (func() error, error) {
	cleanFunc := func() error {
		return nil
	}
	DACL, err := GetDACL()
	if err != nil {
		return cleanFunc, err
	}
	err = ApplyDACL(workDir, DACL)
	if err != nil {
		return cleanFunc, err
	}

	_, err = os.Stat(path.Join(workDir, ".alkaid0"))
	if os.IsExist(err) {
		DenyDACL, err := GetDenyDACL()
		if err != nil {
			return cleanFunc, err
		}
		err = ApplyDACL(path.Join(workDir, ".alkaid0"), DenyDACL)
		if err != nil {
			return cleanFunc, err
		}
	}
	cleanFunc = func() error {
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

	return cleanFunc, nil
}

// SetLimitToDir 设置一般权限
func SetLimitToDir(workDir []string) (func() error, error) {
	cleanFunc := func() error {
		for _, dir := range workDir {
			DenyDACL, err := GetDenyDACL()
			if err != nil {
				return err
			}
			err = ApplyDACL(dir, DenyDACL)
			if err != nil {
				return err
			}
		}
		return nil
	}
	for _, dir := range workDir {
		DACL, err := GetDACL()
		if err != nil {
			return cleanFunc, err
		}
		err = ApplyDACL(dir, DACL)
		if err != nil {
			return cleanFunc, err
		}
	}

	return cleanFunc, nil
}
