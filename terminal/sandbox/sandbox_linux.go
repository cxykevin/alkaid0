//go:build linux

package sandbox

import (
	"context"
	_ "embed" // embed
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"syscall"
)

//go:embed scripts/linux/mount.sh
var mountScript string

// createIsolatedCommand 创建OS级隔离的命令
func (s *Sandbox) createIsolatedCommand(ctx context.Context, name string, args ...string) (*Command, error) {
	cmd, err := s.createLinuxIsolatedCommand(ctx, name, args...)
	if err != nil {
		return nil, err
	}

	return &Command{
		cmd:     CreateExecFromCmd(cmd, func() {}),
		ctx:     ctx,
		name:    name,
		args:    args,
		workDir: s.workDir,
		env:     s.env,
	}, nil
}

// createLinuxIsolatedCommand 创建Linux隔离命令
func (s *Sandbox) createLinuxIsolatedCommand(ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
	// 构建路径策略的挂载命令（返回外部 export 与内层挂载命令）
	policyExports, policyCmds := s.generatePolicyMounts()

	// 工作目录处理（确保在chroot内存在）
	chrootWorkDir := s.workDir
	if !strings.HasPrefix(chrootWorkDir, "/") {
		chrootWorkDir = "/" + chrootWorkDir
	}

	// 先chroot，再内部挂载
	// 注意：进程在 user namespace 内以 UID 0 运行，映射关系由 --map-users 决定：
	//   - root 服务场景：双重映射 0→0（保留 mount 能力）+ owner→owner（供降权后访问工作目录）
	//   - 普通用户场景：单映射 0→调用者（等价 --map-root-user，调用者通常即工作目录属主）
	// 沙盒命令最终以工作目录实际属主身份运行（见 mount.sh 中的 setpriv），
	// 避免 root 服务下因 uid 映射不匹配而无法访问权限受限的工作目录
	realUser := os.Getenv("USER")
	if realUser == "" {
		realUser = "user"
	}

	// 运行属主：优先使用配置指定的用户（Agents.User），否则回落工作目录实际属主。
	// 沙盒命令最终以该身份运行（mount.sh 中按 ALK_RUN_UID 做 setpriv 降权）。
	runUid, runGid, runUser := s.sandboxRunOwner()

	// uid/gid 映射参数
	var mapArgs []string
	if os.Geteuid() == 0 {
		// root：双重映射。0→0 保留 mount 能力；owner→owner 供 setpriv 降权后访问工作目录
		mapArgs = []string{
			"--map-users=0:0:1",
			"--map-groups=0:0:1",
		}
		if runUid != 0 {
			mapArgs = append(mapArgs, fmt.Sprintf("--map-users=%d:%d:1", runUid, runUid))
		}
		if runGid != 0 {
			mapArgs = append(mapArgs, fmt.Sprintf("--map-groups=%d:%d:1", runGid, runGid))
		}
	} else {
		// 非 root：单映射到调用者（等价 --map-root-user），无需降权
		mapArgs = []string{"--map-root-user"}
		runUid, runGid = 0, 0
	}

	// 工作目录与可写目录通过环境变量传给内层脚本，
	// 避免含单引号路径拼进 sh -uc '...' 单引号字符串导致脚本语法破坏/注入
	exports := policyExports
	if exports != "" {
		exports += "\n"
	}
	exports += "export ALK_WORKDIR=" + shellQuote(chrootWorkDir)
	exports += "\nexport ALK_RUN_UID=" + shellQuote(strconv.Itoa(runUid))
	exports += "\nexport ALK_RUN_GID=" + shellQuote(strconv.Itoa(runGid))
	exports += "\nexport ALK_RUN_USER=" + shellQuote(runUser)

	script := fmt.Sprintf(mountScript,
		shellQuote(realUser),
		exports,
		policyCmds,
		shellQuote(name),
		shellQuote(name),
	)

	// 直接通过 unshare 执行，无需临时文件
	unshareArgs := []string{
		"--user",  // 创建用户命名空间（允许非 root 创建其他命名空间）
		"--mount", // 创建挂载命名空间（关键：隔离所有mount操作）
		"--pid",   // 创建PID命名空间
		"--fork",  // fork子进程作为PID 1
		"--ipc",   // IPC命名空间（可选，增强隔离）
		"--uts",   // UTS命名空间（可选，隔离hostname）
	}
	unshareArgs = append(unshareArgs, mapArgs...)
	unshareArgs = append(unshareArgs, "sh", "-c", script)
	cmd := exec.CommandContext(ctx, "unshare", unshareArgs...)
	// 将调用方环境传给 unshare；否则隔离进程会丢失任务注入的凭据和配置。
	cmd.Env = s.env
	// 输出 writer 不是 *os.File 时会起拷贝 goroutine；沙盒命令留下持有管道的
	// 后代进程时 Wait 会永久阻塞（任务停在 running、kill/ESC 失效）。
	// WaitDelay 到点后 os/exec 强制关管道并返回 exec.ErrWaitDelay（见 ExecCmd.Wait）。
	cmd.WaitDelay = drainTimeout
	// 设置进程组，确保超时时可以杀死整个进程树
	// unshare --pid --fork 的子进程会成为孤儿进程，通过进程组 kill 可防止残留
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			// 负 PID 表示向进程组发送信号
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}

	// 传递原始参数
	cmd.Args = append(cmd.Args, "--")
	cmd.Args = append(cmd.Args, args...)

	return cmd, nil
}

// getWorkDirOwner 获取工作目录的实际属主 uid/gid。
// 沙盒命令最终将以该属主身份运行（mount.sh 中 setpriv 降权），
// 解决 root 服务下 chroot 内因 uid 映射不匹配而无法访问权限受限工作目录的问题。
func (s *Sandbox) getWorkDirOwner() (int, int) {
	info, err := os.Stat(s.workDir)
	if err != nil {
		return 0, 0
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0
	}
	return int(st.Uid), int(st.Gid)
}

// generatePolicyMounts 生成路径策略对应的 mount 命令。
//
// 返回两部分：
//   - exports：在 chroot **外**执行的 export 语句（路径经 shellQuote 转义，
//     可以安全包含单引号/百分号）；
//   - cmds：在 chroot **内**执行的命令，只能引用上面导出的变量（内层脚本被
//     单引号包裹，因此这里绝不能出现单引号，也不能出现裸百分号）。
//
// 约定：
//   - 内层脚本开始时先把沙盒临时目录 remount rw（scratch 需要可写）；
//   - 真实内容在被 tmpfs 覆盖前先 bind 到 scratch，再 bind 回目标路径，
//     否则覆盖后就再也拿不到真实内容；
//   - 规则按路径深度从浅到深应用，深层规则覆盖浅层规则；
//   - 最后把所有"封存"（deny / list-only）挂载点 remount 成只读，
//     避免沙箱内进程在封存视图里新建文件。
func (s *Sandbox) generatePolicyMounts() (string, string) {
	p := s.policy
	if p == nil || len(p.rules) == 0 {
		return "", ""
	}

	rules := p.Rules()
	var exports []string
	var cmds []string

	exports = append(exports, "export ALK_TMP="+shellQuote(s.tmpDir))
	names := make([]string, len(rules))
	for i, r := range rules {
		names[i] = fmt.Sprintf("ALK_P%d", i)
		exports = append(exports, fmt.Sprintf("export %s=%s", names[i], shellQuote(r.Path)))
	}

	// 封存点（deny / list-only）下标
	var seals []int
	for i, r := range rules {
		if r.Access == AccessDeny || r.Access == AccessListOnly {
			seals = append(seals, i)
		}
	}

	// 规则是否被某个封存点覆盖（覆盖后必须从 scratch 取真实内容）
	sealOf := make(map[int]int)
	for _, si := range seals {
		for i, r := range rules {
			if i == si {
				continue
			}
			if isWithin(r.Path, rules[si].Path) {
				if _, ok := sealOf[i]; !ok {
					sealOf[i] = si
				}
			}
		}
	}

	// 变量兜底：内层脚本以 sh -uc 执行（nounset）。正常情况下这些变量都由外层
	// export 好；兜底只为让脚本在单独运行时（测试/诊断）也不会因未绑定而中断。
	defs := []string{`: "${ALK_TMP:=/tmp}"`}
	for i := range rules {
		defs = append(defs, fmt.Sprintf(`: "${%s:=/nonexistent-alkaid0-slot-%d}"`, names[i], i))
	}

	// 沙盒临时目录先放开：下面的 scratch（被封存内容的真实来源）要写在这里。
	// scratch 里的 bind 在命令结束后逐个 umount + rmdir（不用 rm -rf：scratch
	// 里的目录是 bind 挂载点，递归删除会把真实内容删掉）。
	cmds = append(cmds, strings.Join(defs, "\n")+`
mkdir -p "$ALK_TMP" 2>/dev/null || :
mount --rbind "$ALK_TMP" "$ALK_TMP" 2>/dev/null || :
mount -o remount,rw,bind "$ALK_TMP" 2>/dev/null || :
_ALK_SCRATCH=$(mktemp -d "$ALK_TMP/.alk-sandbox-mnt-XXXXXX" 2>/dev/null) || _ALK_SCRATCH=/nonexistent-alkaid0-scratch`)

	// 阶段 A：在被封存覆盖之前，把真实内容 bind 到 scratch。
	// 每个封存点一个真实来源（用于列出条目），每条被覆盖的规则一个来源。
	for _, si := range seals {
		cmds = append(cmds, fmt.Sprintf(`
mkdir -p "$_ALK_SCRATCH/s%d" 2>/dev/null || :
mount --rbind "$%s" "$_ALK_SCRATCH/s%d" 2>/dev/null || :
mount -o remount,ro,bind "$_ALK_SCRATCH/s%d" 2>/dev/null || :`,
			si, names[si], si, si))
	}
	for i := range sealOf {
		cmds = append(cmds, fmt.Sprintf(`
mkdir -p "$_ALK_SCRATCH/r%d" 2>/dev/null || :
mount --rbind "$%s" "$_ALK_SCRATCH/r%d" 2>/dev/null || :`,
			i, names[i], i))
	}

	// 阶段 B：按深度从浅到深应用规则。
	for i, r := range rules {
		// 源路径必须加引号：workspace 路径里带空格时未加引号的 $ALK_Pn 会被
		// shell 拆成多个参数，mount 直接失败（沙盒静默变成只读）。
		source := fmt.Sprintf("\"$%s\"", names[i])
		if _, ok := sealOf[i]; ok {
			source = fmt.Sprintf("\"$_ALK_SCRATCH/r%d\"", i)
			// 被封存目录里的占位条目是指向不存在目标的符号链接，而挂载点必须
			// 是真的目录：先删掉占位。只删符号链接——封存没生效时这里仍是真实
			// 路径，绝不能误删真实内容。
			cmds = append(cmds, fmt.Sprintf(`[ -L "$%s" ] && rm -f "$%s" 2>/dev/null || :`, names[i], names[i]))
		}
		switch r.Access {
		case AccessReadWrite:
			// remount 必须带 bind：外层只读化用的是 remount,ro,bind（只改挂载项
			// 自身的 ro 标志），对称地恢复写权限也必须用 remount,rw,bind。不带 bind
			// 时 util-linux 会尝试修改整个 superblock 的 ro 标志，在 user namespace
			// 中必然 EPERM（实测 rc=32）→ 可写目录保持只读。
			cmds = append(cmds, fmt.Sprintf(`
mkdir -p "$%s" 2>/dev/null || :
mount --rbind %s "$%s" 2>/dev/null || :
mount -o remount,rw,bind "$%s" 2>/dev/null || :`,
				names[i], source, names[i], names[i]))
		case AccessReadOnly:
			cmds = append(cmds, fmt.Sprintf(`
mkdir -p "$%s" 2>/dev/null || :
mount --rbind %s "$%s" 2>/dev/null || :
mount -o remount,ro,bind "$%s" 2>/dev/null || :`,
				names[i], source, names[i], names[i]))
		case AccessDeny:
			// 空 tmpfs 覆盖：目录存在但内容完全不可见（配置目录、日志目录）。
			cmds = append(cmds, fmt.Sprintf(`
mkdir -p "$%s" 2>/dev/null || :
mount -t tmpfs tmpfs "$%s" 2>/dev/null || :`,
				names[i], names[i]))
		case AccessListOnly:
			// 可视但不可读：tmpfs 覆盖后按真实条目的名字建占位符号链接。
			// 占位链接指向不存在的路径，所以读内容报 ENOENT、写入也落不到任何地方，
			// 而 `ls` 仍能看到条目名。
			cmds = append(cmds, fmt.Sprintf(`
mkdir -p "$%s" 2>/dev/null || :
mount -t tmpfs tmpfs "$%s" 2>/dev/null || :
for _alk_e in "$_ALK_SCRATCH/s%d"/* "$_ALK_SCRATCH/s%d"/.[!.]* "$_ALK_SCRATCH/s%d"/..?*; do
	if [ -e "$_alk_e" ] || [ -L "$_alk_e" ]; then
		ln -s "$_ALK_SCRATCH/s%d/.alkaid0-sealed" "$%s/${_alk_e##*/}" 2>/dev/null || :
	fi
done`,
				names[i], names[i], i, i, i, i, names[i]))
		}
	}

	// 阶段 C：封存点置为只读（深层优先，与内核递归语义一致）。
	for _, i := range slices.Backward(seals) {
		cmds = append(cmds, fmt.Sprintf(`mount -o remount,ro,bind "$%s" 2>/dev/null || :`, names[i]))
	}

	// 阶段 D：清掉 scratch 里的 bind（只 umount + rmdir，绝不递归删除）。
	cmds = append(cmds, `
for _alk_d in "$_ALK_SCRATCH"/*; do
	[ -d "$_alk_d" ] || continue
	umount "$_alk_d" 2>/dev/null || :
	rmdir "$_alk_d" 2>/dev/null || :
done
rmdir "$_ALK_SCRATCH" 2>/dev/null || :`)

	return strings.Join(exports, "\n"), strings.Join(cmds, "\n")
}

// shellQuote 转义shell参数
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	// 简单处理：单引号包裹，内部单引号转义
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
