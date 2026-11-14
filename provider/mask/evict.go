package mask

import (
	"time"

	"github.com/cxykevin/alkaid0/config"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"gorm.io/gorm"
)

// evictMinIdleHours 淘汰阈值的下限（小时）。
//
// 只有当一条映射"很久没被出站脱敏命中"时才会被删，而删掉意味着同一个原值下次
// 出现时会被换成**另一个**假值。为了让这种变化永远不可能落在 provider 的
// prompt prefix cache 仍然有效的时间范围内，阈值必须不小于任何模型的缓存保留期
// （DeepSeek 3h、Anthropic 5m–1h、OpenAI 分钟级）。6h 是留给"用户手工调小"的
// 安全底线，默认 24h。
const evictMinIdleHours = 6

// 淘汰策略的默认值（配置缺省时兜底；正常路径由 config 的 default tag 填好）。
const (
	evictDefaultIdleHours = 24
	evictDefaultMaxRows   = 5000
	evictDefaultBatch     = 500
)

// evictNow 便于测试注入时钟。
var evictNow = time.Now

// EvictPolicy 是映射淘汰策略。
type EvictPolicy struct {
	// Enable 为 false 时只统计、不删除（配置关闭淘汰时用于展示现状）。
	Enable bool
	// IdleHours 空闲多少小时可淘汰（0 取默认 24；1..5 clamp 到 6）。
	IdleHours int
	// MaxRows 行数软上限：仅用于告警，不会越过 IdleHours 强删。
	MaxRows int
	// Batch 单批删除行数。
	Batch int
	// Force 忽略 IdleHours 直接删掉所有非 custom 映射（/mask clean --force）。
	Force bool
}

// normalized 补齐默认值并施加安全下限。
func (p EvictPolicy) normalized() EvictPolicy {
	switch {
	case p.IdleHours == 0:
		p.IdleHours = evictDefaultIdleHours
	case p.IdleHours < evictMinIdleHours:
		p.IdleHours = evictMinIdleHours
	}
	if p.MaxRows <= 0 {
		p.MaxRows = evictDefaultMaxRows
	}
	if p.Batch <= 0 {
		p.Batch = evictDefaultBatch
	}
	return p
}

// EvictPolicyFromConfig 用当前配置构造淘汰策略。
func EvictPolicyFromConfig() EvictPolicy {
	cfg := config.GlobalConfigSafe().DataMask
	return EvictPolicy{
		Enable:    cfg.KeyMappingEvictEnable,
		IdleHours: cfg.KeyMappingIdleHours,
		MaxRows:   cfg.KeyMappingMaxRows,
		Batch:     cfg.KeyMappingEvictBatch,
	}.normalized()
}

// EvictResult 是一次淘汰的结果（也用于 /mask clean 的回报）。
type EvictResult struct {
	// Deleted 本次删除的行数。
	Deleted int
	// Remaining 清理后表里的行数。
	Remaining int
	// OldestIdle 最早一行的 last_used（零值表示表为空）。
	OldestIdle time.Time
	// IdleHours 生效的空闲阈值（Force 时为 0）。
	IdleHours int
	// OverMaxRows 清理后仍超过 MaxRows（说明剩下的行都还在热窗口内）。
	OverMaxRows bool
}

// EvictKeyMappings 删除「已长时间未使用」的脱敏映射。
//
// 安全性前提（见 storage/structs.KeyMapping 的说明）：
//   - 只有 LastUsed 早于 now-IdleHours 的行才是候选，而仍会被发送的原值每一轮
//     出站脱敏都会刷新 LastUsed，因此候选行不可能出现在任何还在发送的内容里；
//   - IdleHours 不小于 prompt 缓存保留期，即使某条渲染路径没有被刷新覆盖，
//     也已经过了缓存寿命；
//   - key_type = custom（用户 /mask add）永不自动删除。
//
// 删除是分批的（每批一个事务），单次调用内循环到没有候选为止，因此一次就能清干净；
// 只在真正删到行时才递增 mappingVersion（让各 Engine/ClientMasker 下次用到时重建
// 自动机，而不是逐行抖动）。
func EvictKeyMappings(db *gorm.DB, policy EvictPolicy) (EvictResult, error) {
	policy = policy.normalized()
	res := EvictResult{IdleHours: policy.IdleHours}
	if db == nil || !db.Migrator().HasTable(&storageStructs.KeyMapping{}) {
		return res, nil
	}
	if policy.Force {
		res.IdleHours = 0
	} else if !policy.Enable {
		res.Remaining, res.OldestIdle = keyMappingStats(db)
		res.OverMaxRows = res.Remaining > policy.MaxRows
		return res, nil
	}

	// last_used 是 unix 秒；Force 时用最大值把全部非 custom 行都纳入候选。
	idleCut := uint64(1<<63 - 1)
	if !policy.Force {
		idleCut = uint64(evictNow().Unix() - int64(policy.IdleHours)*3600)
	}

	for {
		var ids []uint64
		if err := db.Model(&storageStructs.KeyMapping{}).
			Where("key_type <> ? AND last_used < ?", TypeCustom, idleCut).
			Order("last_used ASC").
			Limit(policy.Batch).
			Pluck("id", &ids).Error; err != nil {
			return res, err
		}
		if len(ids) == 0 {
			break
		}
		if err := db.Where("id IN ?", ids).Delete(&storageStructs.KeyMapping{}).Error; err != nil {
			return res, err
		}
		res.Deleted += len(ids)
	}

	res.Remaining, res.OldestIdle = keyMappingStats(db)
	res.OverMaxRows = res.Remaining > policy.MaxRows
	if res.Deleted > 0 {
		mappingVersion.Add(1)
	}
	return res, nil
}

// keyMappingStats 返回表内行数与最早一行的 last_used。
func keyMappingStats(db *gorm.DB) (int, time.Time) {
	var row struct {
		Total int64
		Min   int64
	}
	if err := db.Model(&storageStructs.KeyMapping{}).
		Select("count(*) AS total, coalesce(min(last_used), 0) AS min").
		Scan(&row).Error; err != nil {
		logger.Warn("data mask: key mapping stats: %v", err)
		return 0, time.Time{}
	}
	oldest := time.Time{}
	if row.Min > 0 {
		oldest = time.Unix(row.Min, 0)
	}
	return int(row.Total), oldest
}
