package sandbox

import "strings"

// runAsUser 终端任务的目标运行用户（由配置 Agent.User 指定）与用户组（Agent.UserGroup）。
//
// 各平台的解析与切换能力差异很大：
//   - Linux：root 时可通过 setpriv 降权（沙盒内由 mount.sh 的 setpriv 完成，沙盒外由
//     wrapIsolateNoneCommand 包装命令）；用户组（Agent.UserGroup）仅在 Linux 生效；
//   - Windows：跨用户启动进程需要凭据，暂不支持切换，仅做解析与告警；
//   - 其它平台（含 macOS）：暂不支持，配置会被忽略并告警。
type runAsUser struct {
	// Name 用户名（规范化后的登录名）
	Name string
	// Group 目标用户组名（配置 Agent.UserGroup 指定；空表示用户主组）。
	// 仅 Linux 使用：其它平台不支持用户组，配置会被忽略并告警。
	Group string
	// UID/GID Unix 用户 ID 与组 ID（Windows 上不使用）。
	// GID 为 Group 指定组的 ID；Group 为空时为用户主组 ID。
	UID int
	GID int
	// Home 家目录，用于把 HOME 指向目标用户（非 Unix 或未知时为空）
	Home string
	// sameAsCurrent 目标用户就是当前进程用户：无需切换，也不应告警
	sameAsCurrent bool
}

// envKeysToReplace runAs 生效时需要改写的身份相关环境变量。
// 只处理 Unix 风格键：Windows 上 runAs 不会生效（见 prepareRunAs）。
var envKeysToReplace = map[string]bool{
	"HOME":    true,
	"USER":    true,
	"LOGNAME": true,
}

// resolveRunAsOrWarn 解析配置指定的运行用户与用户组（Agent.User / Agent.UserGroup）。
//
// 用户名/用户组无效、或当前进程没有能力切换（非 root/无 setpriv/平台不支持）时，
// 记录一条警告并返回 nil：调用方继续以当前用户运行（静默忽略，不影响命令执行）。
// group 为空表示使用用户的主组；非空时仅在 Linux 生效。
func resolveRunAsOrWarn(name, group string) *runAsUser {
	name = strings.TrimSpace(name)
	group = strings.TrimSpace(group)
	if name == "" {
		if group != "" {
			// 用户组是运行用户的补充：没有运行用户时无从附加
			logger.Warn("忽略运行用户组 %q：未配置 Agent.User，用户组仅在指定运行用户时生效", group)
		}
		return nil
	}
	ru, err := resolveRunAsUser(name, group)
	if err != nil {
		logger.Warn("忽略运行用户 %q（%v），继续以当前用户运行命令", name, err)
		return nil
	}
	use, reason := prepareRunAs(ru)
	if !use {
		if reason != "" {
			logger.Warn("忽略运行用户 %q（%s），继续以当前用户运行命令", name, reason)
		}
		return nil
	}
	return ru
}

// applyRunAsEnv 把环境变量里的身份相关项改写为目标用户。
//
// 以其它用户运行命令时，若仍带着调用者的 HOME/USER，会出现"以 A 身份运行却把
// 配置写进 B 的家目录"的错位（git/gh 等工具还会去读错误身份的 keyring）。
// exec.Cmd.Env 是数组，同名键的取值行为随平台而异，因此这里先删除再追加。
func applyRunAsEnv(env []string, ru *runAsUser) []string {
	if ru == nil {
		return env
	}
	out := make([]string, 0, len(env)+3)
	for _, kv := range env {
		key, _, ok := strings.Cut(kv, "=")
		if ok && envKeysToReplace[key] {
			continue
		}
		out = append(out, kv)
	}
	if ru.Home != "" {
		out = append(out, "HOME="+ru.Home)
	}
	if ru.Name != "" {
		out = append(out, "USER="+ru.Name, "LOGNAME="+ru.Name)
	}
	return out
}
