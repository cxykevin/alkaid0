package actions

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/provider/mask"
	"github.com/cxykevin/alkaid0/storage/structs"
	"gorm.io/gorm"
)

// keyMappingEvictLast 记录本进程内每个工作目录上次清理脱敏映射的时间（节流用）。
//
// 淘汰本身很便宜（一次索引扫描 + 批量删），但 loadSession 会被反复调用
// （会话加载、cron 投递、workflow 事件……），没有节流会变成每次开库都扫一遍。
var (
	keyMappingEvictMu   sync.Mutex
	keyMappingEvictLast = map[string]time.Time{}
)

// keyMappingEvictInterval 返回同一工作目录两次清理的最小间隔。
//
// 取空闲阈值的 1/4（最少 1h）：比阈值小得多，清理时机足够及时；
// 又不会随会话加载频率抖动。
func keyMappingEvictInterval(idleHours int) time.Duration {
	h := idleHours / 4
	if h < 1 {
		h = 1
	}
	return time.Duration(h) * time.Hour
}

// maybeEvictKeyMappings 在会话加载这种天然空闲的时刻顺手淘汰过期的脱敏映射。
//
// 触发条件（任一不满足就直接返回，零成本）：
//   - 脱敏引擎启用且淘汰开关打开；
//   - 该库没有本进程正在进行的流式请求（不在回合中间动库）；
//   - 距离上次清理超过 keyMappingEvictInterval。
//
// 失败只记日志，不影响会话加载。
func maybeEvictKeyMappings(cwd string, db *gorm.DB) {
	if db == nil {
		return
	}
	cfg := config.GlobalConfigSafe().DataMask
	if !cfg.Enable || !cfg.KeyMappingEvictEnable {
		return
	}
	if structs.HasActiveStreams(db) {
		// 有回合在跑：映射正被使用，推迟到下一次会话加载。
		return
	}
	policy := mask.EvictPolicyFromConfig()
	interval := keyMappingEvictInterval(policy.IdleHours)

	keyMappingEvictMu.Lock()
	if last, ok := keyMappingEvictLast[cwd]; ok && time.Since(last) < interval {
		keyMappingEvictMu.Unlock()
		return
	}
	keyMappingEvictLast[cwd] = time.Now()
	keyMappingEvictMu.Unlock()

	evictKeyMappings(db, policy)
}

// evictKeyMappings 执行一次淘汰并记录结果（不做节流，供 /mask clean 复用）。
func evictKeyMappings(db *gorm.DB, policy mask.EvictPolicy) mask.EvictResult {
	start := time.Now()
	res, err := mask.EvictKeyMappings(db, policy)
	if err != nil {
		logger.Error("key mapping evict failed: %v", err)
		return res
	}
	cost := time.Since(start)
	if res.Deleted > 0 {
		logger.Info("key mapping evict: deleted=%d remaining=%d oldest_idle=%s cost=%s",
			res.Deleted, res.Remaining, formatOldestIdle(res.OldestIdle), cost)
	}
	if res.OverMaxRows {
		// 剩下的行都还在热窗口内（仍可能被发送），不能越过空闲阈值强删。
		logger.Warn("key mapping table still has %d rows (max %d) but all are hot; "+
			"they will be evicted after %dh of inactivity (see DataMask.KeyMappingIdleHours)",
			res.Remaining, policy.MaxRows, res.IdleHours)
	} else if res.Deleted == 0 {
		logger.Debug("key mapping evict: nothing to evict (remaining=%d)", res.Remaining)
	}
	return res
}

// formatOldestIdle 把最早一行的 last_used 格式化成便于阅读的空闲时长。
func formatOldestIdle(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return fmt.Sprintf("%s ago", time.Since(t).Truncate(time.Minute))
}

// maskCleanCommand 实现 /mask clean [--force]：立即清理一次过期的脱敏映射。
//
// 默认按配置的空闲阈值清理（等价于自动淘汰提前跑一次）；--force 忽略空闲阈值，
// 删除所有非 custom 的映射 —— 会让仍在历史里的原值下次出现时换到新假值，
// 仅用于数据库已经很大、且接受一次前缀缓存失效的场景。
func maskCleanCommand(obj *sessionObj, arg string) (string, error) {
	if obj == nil || obj.session == nil {
		return "", fmt.Errorf("session not found")
	}
	policy := mask.EvictPolicyFromConfig()
	policy.Enable = true
	switch strings.TrimSpace(arg) {
	case "":
	case "--force":
		policy.Force = true
	default:
		return "", fmt.Errorf("Usage: /mask clean [--force]")
	}
	res := evictKeyMappings(obj.session.DB, policy)
	oldest := "-"
	if !res.OldestIdle.IsZero() {
		oldest = res.OldestIdle.Format("2006-01-02 15:04:05")
	}
	return fmt.Sprintf("**mask cleaned**: 删除 %d 条过期映射，剩余 %d 条；最早一条最后使用于 %s（阈值 %d 小时）。",
		res.Deleted, res.Remaining, oldest, res.IdleHours), nil
}
