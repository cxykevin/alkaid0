package cronjob

import "time"

// NextRuns 返回从当前时刻起的接下来 n 次触发时间（本地时区）。
//
// 与调度器一致：每次调用都从 time.Now() 重新计算，服务停机期间错过的时刻不补跑，
// 因此返回值天然是「未来」的时间点。表达式非法或没有未来触发点时返回 nil。
func NextRuns(expr string, n int) []time.Time {
	if n <= 0 {
		return nil
	}
	sched, err := ParseSchedule(expr)
	if err != nil {
		logger.Warn("cronjob: compute next runs of %q: %v", expr, err)
		return nil
	}
	now := time.Now()
	out := make([]time.Time, 0, n)
	for i := 0; i < n; i++ {
		next := sched.Next(now)
		if next.IsZero() {
			break
		}
		out = append(out, next)
		now = next
	}
	return out
}
