package run

// sandboxForceDisabled 临时强制禁用沙盒：所有平台一律在沙盒外执行命令。
//
// 背景：沙盒当前有已知缺陷——Linux 沙盒内没有 /dev/shm（mount.sh 在 chroot 内用
// devtmpfs 覆盖 /dev，而宿主的 /dev/shm 是独立 tmpfs），任何用到 Python
// multiprocessing（SemLock/Queue/Lock）的代码都会直接 FileNotFoundError，
// dynworkflow 文档推荐的 Flow.run() 正好踩中。修复并重新验证之前，忽略请求参数
// sandbox、三层配置（全局/Agent）与 ALKAID0_DISABLE_SANDBOX 环境变量。
//
// 恢复沙盒：把该常量改回 false 即可，调用处的原开关逻辑保持不变。
const sandboxForceDisabled = true
