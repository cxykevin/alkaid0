//go:build unix

package startup

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/cxykevin/alkaid0/server/actions"
)

// startForceGCSignal 监听 SIGUSR2：收到信号后立即回收所有不活跃会话，
// 并在 1 秒后触发 GC、尝试把空闲内存归还操作系统。
// 与 HTTP /gc?k=<key> 共用 actions.ForceGC；信号处理在独立 goroutine 中
// 串行执行，避免连续信号并发释放同一批会话。
func startForceGCSignal() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGUSR2)
	go func() {
		for range ch {
			res := actions.ForceGC()
			logger.Info("SIGUSR2: force gc released %d inactive session(s), %d retained",
				res.Released, res.Retained)
		}
	}()
}
