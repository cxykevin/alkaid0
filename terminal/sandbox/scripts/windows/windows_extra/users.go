//go:build windows

package windows

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var netUserAdd = dllNetapi.NewProc("NetUserAdd")

// UserInfoLevel2 用户信息结构体
type UserInfoLevel2 struct {
	// 继承自 USER_INFO_1 的字段
	Name        *uint16 // 用户名
	Password    *uint16 // 密码
	PasswordAge uint32  // 密码使用天数（只读）
	Priv        uint32  // 权限级别
	HomeDir     *uint16 // 主目录路径
	Comment     *uint16 // 用户描述
	Flags       uint32  // 用户标志
	ScriptPath  *uint16 // 登录脚本路径

	// USER_INFO_2 新增的字段
	AuthFlags    uint32  // 认证标志（AF_OP_PRINT 等）
	FullName     *uint16 // 用户全名
	UsrComment   *uint16 // 用户注释
	Parms        *uint16 // 应用程序参数
	Workstations *uint16 // 允许登录的工作站列表（逗号分隔，空表示全部）
	LastLogon    uint32  // 最后登录时间（秒，自1970-01-01）
	LastLogoff   uint32  // 最后登出时间（秒）
	AcctExpires  uint32  // 账户过期时间（秒，TIMEQ_FOREVER 表示永不过期）
	MaxStorage   uint32  // 最大存储空间（字节，USER_MAXSTORAGE_UNLIMITED 表示无限制）
	UnitsPerWeek uint32  // 每周登录小时数单位（通常为 168 小时(userInfoSamHoursPerWeek)）
	LogonHours   *uint16 // 登录时间限制（21字节，每比特代表1小时）
	BadPwCount   uint32  // 错误密码次数（只读）
	NumLogons    uint32  // 成功登录次数（只读）
	LogonServer  *uint16 // 登录服务器（"\\*" 表示任意域控制器）
	CountryCode  uint32  // 国家代码（如 86 表示中国）
	CodePage     uint32  // 代码页
}

// UserInfoLogonServerAllowAll 允许登录的服务器
var UserInfoLogonServerAllowAll = windows.StringToUTF16Ptr("\\*")

// 杂项常量表
const (
	UserInfoTimeQForever        uint32 = 0xFFFFFFFF
	UserInfoMaxstorageUnlimited uint32 = 0xFFFFFFFF
	UserInfoSamHoursPerWeek     uint32 = 168
)

// userInfoPriv 用户权限级别
const (
	UserInfoPrivGuest uint32 = 0 // 来宾
	UserInfoPrivUser  uint32 = 1 // 普通用户
	UserInfoPrivAdmin uint32 = 2 // 管理员
)

// LibNetUserAddLevel 用户级别
type LibNetUserAddLevel uint32

// 级别常量
const (
	LibNetUserAddLevel1 LibNetUserAddLevel = 1
	LibNetUserAddLevel2 LibNetUserAddLevel = 2
	LibNetUserAddLevel3 LibNetUserAddLevel = 3
	LibNetUserAddLevel4 LibNetUserAddLevel = 4
)

// LibNetUserAdd 添加一个新用户
// Windows API NetUserAdd 的 Go 绑定
func LibNetUserAdd(serverName *string, level LibNetUserAddLevel, buf *byte, parmErr *uint32) (uint32, error) {
	ret, _, err := netUserAdd.Call(
		uintptr(unsafe.Pointer(serverName)),
		uintptr(uint32(level)),
		uintptr(unsafe.Pointer(buf)),
		uintptr(unsafe.Pointer(parmErr)),
	)
	if ret == 0 {
		return 0, err
	}
	return uint32(ret), nil
}

// Flags 位掩码组合（按位或运算）
const (
	UFScript                             = 0x00000001 // 1 << 0
	UFAccountDisable                     = 0x00000002 // 1 << 1
	UFLockout                            = 0x00000010 // 1 << 4
	UFPasswdNotReqd                      = 0x00000020 // 1 << 5
	UFPasswdCantChange                   = 0x00000040 // 1 << 6
	UFEncryptedTextPasswordAllowed       = 0x00000080 // 1 << 7
	UFTempDuplicateAccount               = 0x00000100 // 1 << 8
	UFNormalAccount                      = 0x00000200 // 1 << 9
	UFInterdomainTrustAccount            = 0x00000800 // 1 << 11
	UFWorkstationTrustAccount            = 0x00001000 // 1 << 12
	UFServerTrustAccount                 = 0x00002000 // 1 << 13
	UFDontExpirePasswd                   = 0x00010000 // 1 << 16
	UFMnsLogonAccount                    = 0x00020000 // 1 << 17
	UFSmartcardRequired                  = 0x00040000 // 1 << 18
	UFTrustedForDelegation               = 0x00080000 // 1 << 19
	UFNotDelegated                       = 0x00100000 // 1 << 20
	UFUseDESKeyOnly                      = 0x00200000 // 1 << 21
	UFDontRequirePreauth                 = 0x00400000 // 1 << 22
	UFPasswordExpired                    = 0x00800000 // 1 << 23
	UFTrustedToAuthenticateForDelegation = 0x01000000 // 1 << 24
	UFNoAuthDataRequired                 = 0x02000000 // 1 << 25
	UFPartialSecretsAccount              = 0x04000000 // 1 << 26
	UFUseAesKeys                         = 0x08000000 // 1 << 27
)

// LSA 访问权限常量
const (
	LsaPolicyViewLocalInfomation   uint32 = 0x00000001
	LsaPolicyViewAduitInfomation   uint32 = 0x00000002
	LsaPolicyGetPrivateInfomation  uint32 = 0x00000004
	LsaPolicyTrustAdmin            uint32 = 0x00000008
	LsaPolicyCreateAccount         uint32 = 0x00000010
	LsaPolicyCreateSecret          uint32 = 0x00000020
	LsaPolicyCreatePrivilege       uint32 = 0x00000040
	LsaPolicySetDefaultQuotaLimits uint32 = 0x00000080
	LsaPolicySetAuditRequirements  uint32 = 0x00000100
	LsaPolicyAduitLogAdmin         uint32 = 0x00000200
	LsaPolicyServerAdmin           uint32 = 0x00000400
	LsaPolicyLookupNames           uint32 = 0x00000800
	LsaPolicyNotification          uint32 = 0x00001000
)
