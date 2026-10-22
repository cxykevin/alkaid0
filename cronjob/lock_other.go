//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd && !dragonfly && !windows

package cronjob

// fileLock 其它平台（js/wasm、plan9、aix 等）的降级实现：不做跨进程互斥。
// 这些平台不是 alkaid0 的发布目标，调度器仍可正常工作，只是无法阻止同一配置
// 目录被多个进程同时调度。
type fileLock struct{}

func acquireFileLock(string) (*fileLock, error) { return &fileLock{}, nil }

func (l *fileLock) release() {}
