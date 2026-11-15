package run

import (
	"os"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/storage/structs"
)

// sandboxForceDisabled 临时强制禁用沙盒：所有平台一律在沙盒外执行命令。
//
// 该策略原本用于暂时绕开沙盒的已知缺陷（Linux 沙盒内缺少 /dev/shm，导致
// Python multiprocessing 的 SemLock/Queue 直接 FileNotFoundError）。这些缺陷
// 已经修复（mount.sh 在 chroot 内重建 /dev/shm），沙盒恢复为默认启用：
//
//   - 请求参数 sandbox=true（默认）→ 走 OS 级隔离；
//   - 三层配置（全局 / Agent）或 ALKAID0_DISABLE_SANDBOX=true 可显式关闭；
//   - 隔离环境不支持时（如容器内无法创建 user namespace）自动降级。
//
// 仍需要临时兜底时把该常量改回 true 即可，调用处的开关逻辑保持不变。
const sandboxForceDisabled = false

// sandboxDisabledByConfig 返回配置层面是否禁用沙盒（不含"环境不支持"降级）。
//
// 生效 agent 配置由 EffectiveAgentConfig 决定：活跃子代理 > 会话 role >
// 零值（默认主 agent）。role 在该会话里等价于主 agent，因此它配置的
// DisableSandbox 与全局/子代理配置取或。
func sandboxDisabledByConfig(session *structs.Chats) bool {
	if sandboxForceDisabled {
		return true
	}
	if config.GlobalConfig != nil && config.GlobalConfig.Agent.DisableSandbox {
		return true
	}
	if session != nil && session.EffectiveAgentConfig().DisableSandbox {
		return true
	}
	return os.Getenv("ALKAID0_DISABLE_SANDBOX") == "true"
}
