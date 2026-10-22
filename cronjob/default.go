package cronjob

import "sync"

// 进程内默认存储。
//
// cron 工具与 @cron 虚拟对象都在 init/请求渲染阶段被调用，拿不到装配期的
// 返回值，因此 Start 打开数据库后把 Store 登记在这里。Start 失败或 Stop 之后
// 为 nil，调用方需要按「不可用」处理并告知原因。
//
// 注意：锁只决定「谁负责调度」，不影响这里的登记——非调度实例也要能读写
// cron.sqlite（SQLite 并发写由 busy_timeout 兜底）。
var (
	defaultMu    sync.RWMutex
	defaultStore *Store
)

// SetDefault 登记默认存储（无条件覆盖）。测试可用来注入临时库。
func SetDefault(s *Store) {
	defaultMu.Lock()
	defaultStore = s
	defaultMu.Unlock()
}

// registerDefault 在尚未登记时登记默认存储。
//
// Start 用这个而不是 SetDefault：同一进程里第二次 Start（锁被别的进程占着）不该
// 把已经在调度的那份存储顶掉，否则它 Stop 之后工具就再也拿不到库了。
func registerDefault(s *Store) {
	if s == nil {
		return
	}
	defaultMu.Lock()
	if defaultStore == nil {
		defaultStore = s
	}
	defaultMu.Unlock()
}

// Default 返回进程内默认存储；未启动或打开失败时为 nil。
func Default() *Store {
	defaultMu.RLock()
	defer defaultMu.RUnlock()
	return defaultStore
}

// clearDefault 仅在当前默认就是 s 时清空。
//
// 同一进程可能存在「调度实例」与「只读写实例」两个 Scheduler，非调度实例
// Stop 时不能把调度实例登记好的默认存储一起清掉。
func clearDefault(s *Store) {
	if s == nil {
		return
	}
	defaultMu.Lock()
	if defaultStore == s {
		defaultStore = nil
	}
	defaultMu.Unlock()
}
