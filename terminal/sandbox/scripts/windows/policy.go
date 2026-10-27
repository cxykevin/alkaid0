//go:build windows

package windows

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// 沙盒路径策略在 Windows 上的落地方式：DACL。
//
// 与 Linux（只读根 + 定向 remount / tmpfs 覆盖）不同，Windows 没有挂载命名空间，
// 只能用 ACL 表达同一份策略（规则本身由 terminal/sandbox.Policy 生成）：
//
//   - 可读写目录：给沙盒账户追加继承性"完全控制"ACE（SetLimitToDir / SetLimitToWorkdir）；
//   - 不可读写目录（配置目录、日志目录）：追加继承性"拒绝"ACE，内容完全隐藏；
//   - 仅可列出（工作区 .alkaid0）：目录自身只拒绝新建/删除（保留列出与遍历），
//     其下除白名单（skills/temp/images）之外的条目逐个拒绝读写。
//
// 拒绝 ACE 必须排在 DACL 最前面：Windows 按 ACE 顺序求值，排在允许 ACE 之后的
// 拒绝 ACE 不会生效。x/sys 只提供 SetEntriesInAcl 语义的 ACLFromEntries（合并/追加），
// 所以这里手工拼一个"拒绝在前"的 ACL 二进制。
//
// 注意（已知限制）：Windows 上"全局可读不可写"最终仍取决于系统 ACL —— 沙盒令牌保留
// 了当前用户身份（否则用户自己的 .gitconfig 等文件都读不到），因此当前用户拥有写权限
// 的目录理论上仍可写。本实现保证策略显式列出的保护项（配置/日志/.alkaid0）与白名单
// 项（工作目录/临时目录/venv/.git 等）的语义与 Linux 一致。

const (
	aclHeaderSize = 8
	// aclRevision ACL_REVISION：只含基本 ACE 的 ACL 版本号。
	aclRevision = 2
)

// rawACL 与 windows.ACL 内存布局一致；x/sys 把 size/revision 字段设成了未导出，
// 手工拼接 ACL 时只能通过这个镜像结构读取。
type rawACL struct {
	revision byte
	sbz1     byte
	size     uint16
	aceCount uint16
	sbz2     uint16
}

// x/sys 没有导出全部文件访问位，这里补上标准位定义。
const (
	fileWriteData      = 0x00000002
	fileAppendData     = 0x00000004
	fileDeleteChild    = 0x00000040
	standardDelete     = 0x00010000
	standardWriteDAC   = 0x00040000
	standardWriteOwner = 0x00080000
)

// 拒绝掩码。
const (
	// denyAll 完全隐藏（连列目录都拒绝）。
	denyAll = windows.GENERIC_READ | windows.GENERIC_WRITE | windows.GENERIC_EXECUTE | standardDelete
	// denyDirMutation 目录自身：禁止新建/删除/改权限，保留列出与遍历。
	denyDirMutation = fileWriteData | fileAppendData | fileDeleteChild |
		standardDelete | standardWriteDAC | standardWriteOwner
	// denyChild 子项：拒绝读写与删除。
	denyChild = windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE | standardDelete
	// denyDirContent 不可读写目录自身：禁止列出（GENERIC_READ 含 FILE_LIST_DIRECTORY）、
	// 写入、删除与改权限，但保留遍历（GENERIC_EXECUTE 未拒绝），否则白名单子目录进不去。
	denyDirContent = windows.GENERIC_READ | windows.GENERIC_WRITE | standardDelete | standardWriteDAC | standardWriteOwner
	// denyWrite 只读路径：拒绝写入与删除（保留读取）。
	denyWrite = windows.FILE_GENERIC_WRITE | standardDelete
)

// denySpec 一条待插入的拒绝 ACE。
type denySpec struct {
	mask        uint32
	inheritance uint32
}

// Policy 路径策略在 Windows 侧的输入（与 terminal/sandbox.Policy 的规则对应）。
type Policy struct {
	// ReadWrite 可读写路径（目录或文件）
	ReadWrite []string
	// Deny 不可读写路径
	Deny []string
	// ReadOnly 只读路径（可读、不可写不可删）
	ReadOnly []string
	// ListOnly 不可读写但可列出条目的目录
	ListOnly []string
	// AllowedChildren 在 ListOnly 目录内仍然可读写的子路径
	AllowedChildren map[string][]string
}

// ApplyPolicy 按策略临时调整 DACL，返回还原函数。
//
// 任何一步失败都会先回滚已改动项再返回错误：宁可本次命令起不来，也不能把
// "已放开/已封禁"的 ACL 永久留在磁盘上。
func ApplyPolicy(p Policy) (func() error, error) {
	type snapshot struct {
		path string
		dacl *windows.ACL
	}
	var snaps []snapshot

	restore := func() error {
		var firstErr error
		// 逆序还原，与施加顺序相反
		for i := len(snaps) - 1; i >= 0; i-- {
			if err := restoreDACL(snaps[i].path, snaps[i].dacl); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		snaps = snaps[:0]
		return firstErr
	}
	fail := func(err error) (func() error, error) {
		_ = restore()
		return nil, err
	}
	apply := func(path string, specs []denySpec) error {
		orig, err := saveDACL(path)
		if err != nil {
			return fmt.Errorf("读取 %s 的 DACL 失败: %w", path, err)
		}
		if err := addDenyACEs(path, specs); err != nil {
			return fmt.Errorf("设置 %s 的拒绝 ACE 失败: %w", path, err)
		}
		snaps = append(snaps, snapshot{path: path, dacl: orig})
		return nil
	}
	// applyChildren 对 dir 下不在白名单里的每个条目追加拒绝 ACE（封禁其自身与子树）。
	applyChildren := func(dir string, allow []string, mask uint32) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			// 连服务进程都列不出来时无从逐项封禁；目录自身的拒绝 ACE 仍然生效
			return nil
		}
		for _, entry := range entries {
			child := filepath.Join(dir, entry.Name())
			if listAllowed(child, allow) {
				continue
			}
			if err := apply(child, []denySpec{{mask: mask, inheritance: windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT}}); err != nil {
				return err
			}
		}
		return nil
	}

	// 1) 可读写：追加继承性完全控制（原有语义）
	for _, path := range p.ReadWrite {
		if !pathExists(path) {
			continue
		}
		orig, err := saveDACL(path)
		if err != nil {
			// 快照失败不阻塞授予，但清理时只能退化为收紧权限（与 SetLimitToDir 一致）
			orig = nil
		}
		dacl, err := GetDACL()
		if err != nil {
			return fail(err)
		}
		if err := ApplyDACL(path, dacl); err != nil {
			return fail(fmt.Errorf("授予 %s 沙盒写权限失败: %w", path, err))
		}
		if orig != nil {
			snaps = append(snaps, snapshot{path: path, dacl: orig})
		}
	}

	// 2) 不可读写：目录自身不可列出/写入（保留遍历），子项逐个完全封禁。
	//    不能直接在目录上挂继承性 denyAll：白名单子目录（例如配置目录里的 venv）
	//    会被继承的拒绝 ACE 封死，而拒绝 ACE 无法用授予覆盖。
	for _, path := range p.Deny {
		if !pathExists(path) {
			continue
		}
		if err := apply(path, []denySpec{{mask: denyDirContent}}); err != nil {
			return fail(err)
		}
		if err := applyChildren(path, p.AllowedChildren[path], denyAll); err != nil {
			return fail(err)
		}
	}

	// 3) 仅可列出：目录可列、内容不可读写
	for _, path := range p.ListOnly {
		if !pathExists(path) {
			continue
		}
		// 目录自身：不继承，避免把拒绝传播到白名单子目录（拒绝 ACE 无法被授予覆盖）
		if err := apply(path, []denySpec{{mask: denyDirMutation}}); err != nil {
			return fail(err)
		}
		if err := applyChildren(path, p.AllowedChildren[path], denyChild); err != nil {
			return fail(err)
		}
	}

	// 4) 只读：拒绝写入与删除（继承），读取保持放开
	for _, path := range p.ReadOnly {
		if !pathExists(path) {
			continue
		}
		if err := apply(path, []denySpec{{mask: denyWrite, inheritance: windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT}}); err != nil {
			return fail(err)
		}
	}

	return restore, nil
}

// addDenyACEs 把拒绝 ACE 插到 DACL 最前面（保留原有 ACE）。
func addDenyACEs(path string, specs []denySpec) error {
	if len(specs) == 0 {
		return nil
	}
	sid, err := getAccountSID(UserName)
	if err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	existing, _, err := sd.DACL()
	if err != nil {
		return err
	}
	acl, buf, err := buildACLWithDeny(existing, specs, sid)
	if err != nil {
		return err
	}
	err = windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil,
	)
	runtime.KeepAlive(buf)
	return err
}

// buildACLWithDeny 手工拼出 "拒绝 ACE 在前 + 原有 ACE 在后" 的 ACL 二进制。
// 返回的 ACL 指向 buf，调用方必须保证 buf 在 SetNamedSecurityInfo 期间存活。
func buildACLWithDeny(existing *windows.ACL, specs []denySpec, sid *windows.SID) (*windows.ACL, []byte, error) {
	sidLen := int(windows.GetLengthSid(sid))
	if sidLen <= 0 {
		return nil, nil, fmt.Errorf("非法的 SID 长度: %d", sidLen)
	}

	var deny []byte
	for _, spec := range specs {
		aceLen := 4 + 4 + sidLen // ACE_HEADER + ACCESS_MASK + SID
		ace := make([]byte, aceLen)
		ace[0] = windows.ACCESS_DENIED_ACE_TYPE
		ace[1] = byte(spec.inheritance)
		binary.LittleEndian.PutUint16(ace[2:], uint16(aceLen))
		binary.LittleEndian.PutUint32(ace[4:], spec.mask)
		if err := windows.CopySid(uint32(sidLen), (*windows.SID)(unsafe.Pointer(&ace[8])), sid); err != nil {
			return nil, nil, err
		}
		deny = append(deny, ace...)
	}

	revision := byte(aclRevision)
	aceCount := uint16(len(specs))
	var rest []byte
	if existing != nil {
		header := (*rawACL)(unsafe.Pointer(existing))
		if header.size > aclHeaderSize {
			if header.revision >= aclRevision {
				revision = header.revision
			}
			aceCount += header.aceCount
			raw := unsafe.Slice((*byte)(unsafe.Pointer(existing)), int(header.size))
			rest = raw[aclHeaderSize:]
		}
	}

	total := aclHeaderSize + len(deny) + len(rest)
	if total > 0xFFFF {
		return nil, nil, fmt.Errorf("ACL 超过 64KB 上限: %d", total)
	}
	buf := make([]byte, total)
	buf[0] = revision
	binary.LittleEndian.PutUint16(buf[2:], uint16(total))
	binary.LittleEndian.PutUint16(buf[4:], aceCount)
	copy(buf[aclHeaderSize:], deny)
	copy(buf[aclHeaderSize+len(deny):], rest)
	return (*windows.ACL)(unsafe.Pointer(&buf[0])), buf, nil
}

// pathExists 报告路径是否存在（任意类型）。
func pathExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	_, err := os.Lstat(path)
	return err == nil
}

// listAllowed 报告 path 是否在允许名单内（Windows 路径大小写不敏感）。
func listAllowed(path string, allow []string) bool {
	clean := filepath.Clean(path)
	for _, dir := range allow {
		if strings.EqualFold(filepath.Clean(dir), clean) {
			return true
		}
	}
	return false
}
