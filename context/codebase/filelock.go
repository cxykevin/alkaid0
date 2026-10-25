package codebase

import (
	"path/filepath"
	"strings"
	"sync"
)

// 文件级读写锁 + 刷新代次，用于协调「全量索引 RunIndex」与「单文件刷新 RefreshFile」。
//
// 锁的语义：
//   - 刷新持写锁（Lock）：索引对同一文件 TryRLock 失败 → 索引跳过该文件，
//     不会拿扫描阶段的旧内容覆盖刚写入的新数据；
//   - 索引持读锁（RLock）：刷新等待该文件处理完，再写入最新内容。
//
// 只靠锁不够：索引第一遍扫描读入内容，若扫描后、处理前发生了刷新，TryRLock 会成功
// （刷新已释放锁），索引仍会用旧内容覆盖。refreshSeq 记录刷新代次，索引处理前
// 比对代次，不一致即跳过。
var (
	fileLockMu sync.Mutex
	fileLocks  = map[string]*sync.RWMutex{}
	fileSeqs   = map[string]uint64{}
)

// fileKey 生成文件级锁/代次的键（目录 + 相对路径）。
func fileKey(cwd, relPath string) string {
	dir := filepath.Clean(cwd)
	rel := filepath.ToSlash(filepath.Clean(strings.TrimSpace(relPath)))
	return dir + "\x00" + rel
}

// fileRWLock 返回（必要时创建）某文件的读写锁。
func fileRWLock(key string) *sync.RWMutex {
	fileLockMu.Lock()
	defer fileLockMu.Unlock()
	l, ok := fileLocks[key]
	if !ok {
		l = &sync.RWMutex{}
		fileLocks[key] = l
	}
	return l
}

// fileRefreshSeq 返回该文件最近一次刷新的代次。
func fileRefreshSeq(key string) uint64 {
	fileLockMu.Lock()
	defer fileLockMu.Unlock()
	return fileSeqs[key]
}

// bumpFileRefreshSeq 递增刷新代次；调用方必须已持有该文件的写锁。
func bumpFileRefreshSeq(key string) uint64 {
	fileLockMu.Lock()
	defer fileLockMu.Unlock()
	fileSeqs[key]++
	return fileSeqs[key]
}

// tryLockFileForIndex 索引处理单文件前调用：拿不到读锁（有刷新在写）返回 ok=false。
func tryLockFileForIndex(key string) (unlock func(), ok bool) {
	l := fileRWLock(key)
	if !l.TryRLock() {
		return nil, false
	}
	return l.RUnlock, true
}

// lockFileForRefresh 刷新单文件时调用：独占，等待索引处理完当前文件。
// 加锁后立即递增刷新代次并返回该值：
//   - 索引扫描后看到代次变化即跳过，不会用旧内容覆盖；
//   - 本次刷新入队的向量任务带上该代次，之后再次刷新会使旧任务过期。
func lockFileForRefresh(key string) (unlock func(), seq uint64) {
	l := fileRWLock(key)
	l.Lock()
	return l.Unlock, bumpFileRefreshSeq(key)
}
