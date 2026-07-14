T=$(mktemp -d)
trap 'umount -R "$T" 2>/dev/null || true; rm -rf "$T" 2>/dev/null || true' EXIT

# 阶段1: 外部只挂载rootfs
mount --rbind / "$T" || exit 1
mount -o remount,ro,bind "$T" || exit 1

# 将真实用户名传给 chroot 内的脚本
REAL_USER="%s"
export REAL_USER

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
	# 实际 UID 仍是 0（--map-root-user 映射），文件操作归属正确
	# 注意：getpwuid(0) 返回第一个匹配 UID 0 的条目，真实用户必须放第一行
	_alk_pwf=$(mktemp /tmp/.alk-sandbox-etc-password-XXXXXX 2>/dev/null)
	_alk_grf=$(mktemp /tmp/.alk-sandbox-etc-group-XXXXXX 2>/dev/null)
	{
		echo "${REAL_USER}:x:0:0:${REAL_USER}:/home/${REAL_USER}:/bin/sh"
		echo "root:x:0:0:root:/root:/bin/sh"
	} > "$_alk_pwf" 2>/dev/null || :
	mount --bind "$_alk_pwf" /etc/passwd 2>/dev/null || :
	echo "${REAL_USER}:x:0:" > "$_alk_grf" 2>/dev/null || :
	mount --bind "$_alk_grf" /etc/group 2>/dev/null || :

	# 切换到工作目录并执行
	cd %q || exit 1
	exec %s "$@"
' -- "$@" || EXIT_CODE=$?

# 清理会在trap中自动执行
exit $EXIT_CODE
