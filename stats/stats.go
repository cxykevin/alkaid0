// Package stats 提供跨项目全局的 token 用量统计。
//
// 统计数据持久化在配置文件同目录的 usage.json（仿全局 memory 的存放位置），
// 只累计主对话请求（provider/request.SendRequest 路径），嵌入、标题总结等不计入。
// 所有统计按模型（ModelID）分别累计。
package stats

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/internal/configutil"
	"github.com/cxykevin/alkaid0/log"
)

// Info 对外只读快照，供 /info 等端点 JSON 序列化。
type Info struct {
	UpdatedAt time.Time   `json:"updated_at"`
	Total     TotalStat   `json:"total"`
	Models    []ModelStat `json:"models"` // 按 ModelID 升序
}

// TotalStat 全局总计（跨所有模型）。
type TotalStat struct {
	PromptTokens     uint64  `json:"prompt_tokens"`
	CompletionTokens uint64  `json:"completion_tokens"`
	CachedTokens     uint64  `json:"cached_tokens"`
	CacheHitRatio    float64 `json:"cache_hit_ratio"` // = cached / prompt，保留 4 位小数
	Requests         uint64  `json:"requests"`
}

// ModelStat 单个模型的统计。
type ModelStat struct {
	ModelID          uint32  `json:"model_id"`
	ModelName        string  `json:"model_name"`
	PromptTokens     uint64  `json:"prompt_tokens"`
	CompletionTokens uint64  `json:"completion_tokens"`
	CachedTokens     uint64  `json:"cached_tokens"`
	CacheHitRatio    float64 `json:"cache_hit_ratio"`
	Requests         uint64  `json:"requests"`
}

// modelRecord 内部持久化结构（写盘 schema 的一部分）。
type modelRecord struct {
	ModelID          uint32 `json:"model_id"`
	ModelName        string `json:"model_name"`
	PromptTokens     uint64 `json:"prompt_tokens"`
	CompletionTokens uint64 `json:"completion_tokens"`
	CachedTokens     uint64 `json:"cached_tokens"`
	Requests         uint64 `json:"requests"`
}

// totalRecord 内部持久化的总计结构。
type totalRecord struct {
	PromptTokens     uint64 `json:"prompt_tokens"`
	CompletionTokens uint64 `json:"completion_tokens"`
	CachedTokens     uint64 `json:"cached_tokens"`
	Requests         uint64 `json:"requests"`
}

// fileData usage.json 的磁盘 schema。缓存比属于派生字段，读时计算、不落盘。
type fileData struct {
	SchemaVersion int                    `json:"schema_version"`
	UpdatedAt     time.Time              `json:"updated_at"`
	Total         totalRecord            `json:"total"`
	Models        map[uint32]modelRecord `json:"models"`
}

// 磁盘 schema 版本，结构变更时递增并处理迁移。
const schemaVersion = 1

var logger = log.New("stats")

var (
	dataMu   sync.RWMutex
	data     *fileData
	loadOnce sync.Once
	// filePath 为空时按配置文件同目录推导；测试通过 SetFilePath 覆写。
	filePath string
)

// AddUsage 累计一次对话请求的 token 用量，并同步持久化到磁盘。
// 三个 token 数均为 0 时忽略（不产生记录、不写盘）。
func AddUsage(modelID uint32, modelName string, promptTokens, completionTokens, cachedTokens uint32) {
	if promptTokens == 0 && completionTokens == 0 && cachedTokens == 0 {
		return
	}
	ensureLoaded()
	dataMu.Lock()
	defer dataMu.Unlock()
	if data == nil {
		data = newFileData()
	}
	rec := data.Models[modelID]
	rec.ModelID = modelID
	rec.ModelName = modelName // 配置改名后以最近一次请求的显示名覆盖
	rec.PromptTokens += uint64(promptTokens)
	rec.CompletionTokens += uint64(completionTokens)
	rec.CachedTokens += uint64(cachedTokens)
	rec.Requests++
	data.Models[modelID] = rec
	data.Total.PromptTokens += uint64(promptTokens)
	data.Total.CompletionTokens += uint64(completionTokens)
	data.Total.CachedTokens += uint64(cachedTokens)
	data.Total.Requests++
	data.UpdatedAt = time.Now()
	persistLocked()
}

// Snapshot 返回当前统计的深拷贝快照，可安全并发调用。
func Snapshot() Info {
	ensureLoaded()
	dataMu.RLock()
	defer dataMu.RUnlock()
	if data == nil {
		return Info{Models: []ModelStat{}}
	}
	snap := Info{
		UpdatedAt: data.UpdatedAt,
		Total: TotalStat{
			PromptTokens:     data.Total.PromptTokens,
			CompletionTokens: data.Total.CompletionTokens,
			CachedTokens:     data.Total.CachedTokens,
			CacheHitRatio:    cacheHitRatio(data.Total.PromptTokens, data.Total.CachedTokens),
			Requests:         data.Total.Requests,
		},
	}
	for _, id := range sortedModelIDs(data.Models) {
		rec := data.Models[id]
		snap.Models = append(snap.Models, ModelStat{
			ModelID:          rec.ModelID,
			ModelName:        rec.ModelName,
			PromptTokens:     rec.PromptTokens,
			CompletionTokens: rec.CompletionTokens,
			CachedTokens:     rec.CachedTokens,
			CacheHitRatio:    cacheHitRatio(rec.PromptTokens, rec.CachedTokens),
			Requests:         rec.Requests,
		})
	}
	return snap
}

// SetFilePath 覆盖持久化文件路径，仅测试用，须在首次 AddUsage/Snapshot 前调用。
func SetFilePath(p string) {
	dataMu.Lock()
	defer dataMu.Unlock()
	filePath = p
}

// ResetForTest 重置单例与懒加载标志，仅测试用。
func ResetForTest() {
	dataMu.Lock()
	defer dataMu.Unlock()
	data = nil
	loadOnce = sync.Once{}
}

// ensureLoaded 懒加载磁盘数据，保证全进程只读一次。
func ensureLoaded() {
	loadOnce.Do(func() {
		b, err := os.ReadFile(usagePath())
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				logger.Warn("read usage file: %v", err)
			}
			return
		}
		var fd fileData
		if err := json.Unmarshal(b, &fd); err != nil {
			logger.Warn("parse usage file: %v", err)
			return
		}
		if fd.Models == nil {
			fd.Models = make(map[uint32]modelRecord)
		}
		data = &fd
	})
}

// newFileData 返回空统计。
func newFileData() *fileData {
	return &fileData{
		SchemaVersion: schemaVersion,
		Models:        make(map[uint32]modelRecord),
	}
}

// persistLocked 原子写盘（临时文件 + 重命名），调用方必须已持有 dataMu.Lock()。
// 写盘失败只记录日志，不阻塞对话请求。
func persistLocked() {
	path := usagePath()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		logger.Error("create usage dir %s: %v", dir, err)
		return
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		logger.Error("marshal usage data: %v", err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		logger.Error("write usage tmp file %s: %v", tmp, err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		logger.Error("rename usage file %s: %v", path, err)
		_ = os.Remove(tmp)
	}
}

// usagePath 返回持久化文件路径：优先测试覆写的 filePath，否则取配置文件同目录。
func usagePath() string {
	if filePath != "" {
		return filePath
	}
	return filepath.Join(
		filepath.Dir(configutil.ExpandPath(config.Path())),
		"usage.json",
	)
}

// cacheHitRatio 计算缓存命中比（cached/prompt），prompt 为 0 时返回 0 避免除零。
func cacheHitRatio(prompt, cached uint64) float64 {
	if prompt == 0 {
		return 0
	}
	return math.Round(float64(cached)/float64(prompt)*10000) / 10000
}

// sortedModelIDs 返回按 ModelID 升序的键列表，保证输出稳定。
func sortedModelIDs(m map[uint32]modelRecord) []uint32 {
	ids := make([]uint32, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
