//go:build !unix

package startup

// startForceGCSignal 在非 Unix 平台是 no-op：这些平台没有 SIGUSR2，
// 只能通过 HTTP /gc?k=<key> 触发强制回收。
func startForceGCSignal() {}
