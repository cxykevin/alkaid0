package run

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
