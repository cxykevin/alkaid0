T=$(mktemp -d)
trap 'umount -R "$T" 2>/dev/null || true; rm -rf "$T" 2>/dev/null || true' EXIT

# 阶段1: 外部只挂载rootfs
mount --rbind / "$T" || exit 1
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
