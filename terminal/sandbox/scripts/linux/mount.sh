T=$(mktemp -d)
# /proc/self/mountinfo 中记录的是解析掉符号链接后的物理路径；若 TMPDIR 本身是
# 符号链接（如 TMPDIR=/var/tmp 而 /var/tmp -> /private/var/tmp），用 $T 直接做
# 前缀匹配会一个挂载点都匹配不到。这里取物理路径仅用于匹配，其余地方仍用 $T。
T_PHYS=$(cd "$T" 2>/dev/null && pwd -P) || :
[ -n "$T_PHYS" ] || T_PHYS=$T

# 递归只读时落盘的挂载点清单（按深度排序用），退出时一并清理
_ALK_RO_LIST=$(mktemp)
trap 'rm -f "$_ALK_RO_LIST" "$_ALK_RO_LIST.sorted" 2>/dev/null || true; umount -R "$T" 2>/dev/null || true; rm -rf "$T" 2>/dev/null || true' EXIT

# _alk_remount_ro_recursive ROOT
#	把 ROOT 及其下【所有子挂载点】逐个 remount 成只读。
#
#	为什么需要：mount --rbind / "$T" 会把宿主机的每一个子挂载（/proc、/sys、
#	/dev、/dev/shm、/run、以及宿主机上任何独立挂载点）都在本挂载命名空间里
#	复制成独立的挂载项。而 `mount -o remount,ro,bind "$T"` 只作用于 "$T" 这一个
#	挂载项、不会递归，于是根目录看着是只读的（写 $T/etc 报 EROFS），子挂载却
#	仍然可写 —— 沙盒进程可以绕过沙盒直接写宿主机。
#
#	为什么不用 mount -o remount,ro,bind -R 或 findmnt -R：-R 是 util-linux 的
#	扩展，busybox 的 mount 不认识会直接报错；findmnt 同样只随 util-linux 提供。
#	这里只依赖内核接口 /proc/self/mountinfo：第 5 个字段就是挂载点，字段之间以
#	空白分隔，且挂载点里的空白会被内核转义成 \040，因此按空白切分是安全的。
#
#	处理顺序由深到浅：单个 remount 只影响该挂载项自身，顺序本来无关紧要，
#	但由深到浅与内核/util-linux 的递归语义一致，即便将来改成递归 remount 也
#	不会因为父级先被固化而漏掉子级。
#
#	必须在后面 chroot 内部的"可写目录 remount,rw"之前执行，否则刚放开的
#	可写目录会被这一步重新置为只读。
_alk_remount_ro_recursive() {
	_alk_root=$1
	: > "$_ALK_RO_LIST"
	while read -r _alk_id _alk_par _alk_dev _alk_rootf _alk_mp _alk_opts _alk_rest; do
		case "$_alk_mp" in
			"$_alk_root") _alk_depth=0 ;;
			"$_alk_root"/*) _alk_depth=1 ;;
			*) continue ;;
		esac
		# 用剩余路径中的 / 数量把深度单调放大，供排序使用
		_alk_tail=${_alk_mp#"$_alk_root"}
		while [ -n "$_alk_tail" ]; do
			case "$_alk_tail" in
				*/*) _alk_depth=$((_alk_depth + 1)); _alk_tail=${_alk_tail#*/} ;;
				*) break ;;
			esac
		done
		echo "$_alk_depth $_alk_mp" >> "$_ALK_RO_LIST"
	done < /proc/self/mountinfo
	# 一个挂载点都解析不到，说明 /proc/self/mountinfo 不可用：此时无法保证
	# 子挂载被置为只读，宁可让整个沙盒直接失败，也不能默默降级成"假隔离"
	[ -s "$_ALK_RO_LIST" ] || return 1
	# 深度大的排前面；sort 不可用时保持原顺序（结果同样正确，只是顺序不同）
	if sort -rn "$_ALK_RO_LIST" > "$_ALK_RO_LIST.sorted" 2>/dev/null; then
		mv "$_ALK_RO_LIST.sorted" "$_ALK_RO_LIST" 2>/dev/null || :
	fi
	while read -r _alk_depth _alk_mp; do
		# mountinfo 里挂载点中的空格/制表/换行/反斜杠被内核转义成 \040 \011
		# \012 \134，交给 mount 之前需要还原。只有真的含反斜杠时才做解码，
		# 且解码结果为空（printf 不可用/被误改）时退回原值——保证这一段的
		# 成败只影响含转义的极少数路径，不会波及普通路径。
		# 警告：下面这个格式串在源码里是「双写百分号」形式，本文件会被 Go 的
		# fmt.Sprintf 渲染一次，渲染后才变成 printf 认识的样子。改动此处必须
		# 保持双写；写成单写会被 Sprintf 当成缺参数的格式动词，并顺带吃掉
		# 后面所有占位符的替换参数，导致整个脚本错位。
		# （本文件里除下面这一处和脚本原有的占位符外，不得出现任何裸百分号。）
		_alk_mp_real=$_alk_mp
		case "$_alk_mp" in
			*\\*) _alk_mp_real=$(printf '%%b' "$_alk_mp") ;;
		esac
		[ -n "$_alk_mp_real" ] || _alk_mp_real=$_alk_mp
		# 个别挂载点可能不支持 remount（如某些虚拟文件系统），保持尽力而为，
		# 与脚本中其它挂载步骤的 2>/dev/null || : 风格一致
		mount -o remount,ro,bind "$_alk_mp_real" 2>/dev/null || :
	done < "$_ALK_RO_LIST"
	return 0
}

# 阶段1: 外部只挂载rootfs
mount --rbind / "$T" || exit 1
# 递归只读：--rbind 复制进来的每个子挂载都必须单独置只读，详见函数注释。
# 必须在 chroot 内的可写目录 remount,rw 之前完成。
_alk_remount_ro_recursive "$T_PHYS" || exit 1
mount -o remount,ro,bind "$T" || exit 1

# 将真实用户名传给 chroot 内的脚本
REAL_USER=%s
export REAL_USER
# 可写目录与工作目录通过环境变量传入（含单引号路径也不会破坏内层单引号脚本）
%s

# 阶段2: chroot后内部完成所有挂载（关键：在此ns中，外部看不到）
# 保存命令的退出码
EXIT_CODE=0
chroot "$T" sh -uc '
	# 内部挂载虚拟文件系统（必需）
	mount -t proc proc /proc 2>/dev/null || :
	mount -t sysfs sysfs /sys 2>/dev/null || :
	[ -d /dev ] || mkdir /dev
	mount -t devtmpfs devtmpfs /dev 2>/dev/null || {
		mount -t tmpfs tmpfs /dev
		mknod -m 666 /dev/null c 1 3 2>/dev/null || :
		mknod -m 666 /dev/zero c 1 5 2>/dev/null || :
		mknod -m 666 /dev/random c 1 8 2>/dev/null || :
		mknod -m 666 /dev/urandom c 1 9 2>/dev/null || :
	}

	# 可写目录重新挂载（覆盖ro层）
	%s

	# 伪造 /etc/passwd 和 /etc/group，使 whoami/id 输出真实用户名
	# 实际 UID 是经 --map-users 映射后的目录属主（root 服务场景为双重映射），
	# 文件操作归属正确
	# 注意：getpwuid(0) 返回第一个匹配 UID 0 的条目，真实用户必须放第一行
	_alk_pwf=$(mktemp /tmp/.alk-sandbox-etc-password-XXXXXX 2>/dev/null)
	_alk_grf=$(mktemp /tmp/.alk-sandbox-etc-group-XXXXXX 2>/dev/null)
	# mktemp 默认 600，降权到工作目录属主后需能读取伪造的 passwd/group
	chmod 0644 "$_alk_pwf" "$_alk_grf" 2>/dev/null || :
	{
		echo "${REAL_USER}:x:0:0:${REAL_USER}:/home/${REAL_USER}:/bin/sh"
		echo "root:x:0:0:root:/root:/bin/sh"
		if [ -n "${ALK_RUN_UID:-}" ] && [ "${ALK_RUN_UID}" != "0" ]; then
			echo "${ALK_RUN_USER:-user}:x:${ALK_RUN_UID}:${ALK_RUN_GID:-0}:${ALK_RUN_USER:-user}:/home/${ALK_RUN_USER:-user}:/bin/sh"
		fi
	} > "$_alk_pwf" 2>/dev/null || :
	mount --bind "$_alk_pwf" /etc/passwd 2>/dev/null || :
	{
		echo "${REAL_USER}:x:0:"
		if [ -n "${ALK_RUN_UID:-}" ] && [ "${ALK_RUN_UID}" != "0" ]; then
			echo "${ALK_RUN_USER:-user}:x:${ALK_RUN_GID:-0}:"
		fi
	} > "$_alk_grf" 2>/dev/null || :
	mount --bind "$_alk_grf" /etc/group 2>/dev/null || :

	# 切换到工作目录并执行
	# 若指定了运行属主（root 服务且工作目录属主非 root），先降权到属主再进入执行，
	# 否则 chroot 内因 uid 映射不匹配而无法访问权限受限的工作目录
	if [ -n "${ALK_RUN_UID:-}" ] && [ "${ALK_RUN_UID}" != "0" ]; then
		# 注意：内层脚本被外层 sh -uc '...' 单引号包裹，此处不能使用单引号；
		# 用双引号 + \$ 转义，使 $ALK_WORKDIR / $@ 在子 sh 中展开
		exec setpriv --reuid="${ALK_RUN_UID}" --regid="${ALK_RUN_GID:-0}" --clear-groups \
			sh -c "cd \"\$ALK_WORKDIR\" && exec \"\$@\"" _sh %s "$@"
	else
		cd "$ALK_WORKDIR" || exit 1
		exec %s "$@"
	fi
' -- "$@" || EXIT_CODE=$?

# 清理会在trap中自动执行
exit $EXIT_CODE
