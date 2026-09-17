//go:build windows

package windows

import (
	"bytes"
	"testing"
)

// funcWriter 模拟 run 工具的 writerFunc：函数类型，不可比较。
type funcWriter func([]byte) (int, error)

func (f funcWriter) Write(p []byte) (int, error) { return f(p) }

// TestInterfaceEqualUncomparable 回归：Start 里比较 stdout/stderr 是否为同一个
// writer 时，直接 a == b 会在动态类型相同且不可比较（函数类型）时 panic——
// run 工具正是把 stdout/stderr 设成同一个 writerFunc，导致 Windows 沙盒路径
// 每条命令都失败（runtime error: comparing uncomparable type run.writerFunc）。
func TestInterfaceEqualUncomparable(t *testing.T) {
	w := funcWriter(func([]byte) (int, error) { return 0, nil })
	var other funcWriter = func([]byte) (int, error) { return 0, nil }

	if interfaceEqual(w, other) {
		t.Error("expected uncomparable writer types to compare unequal")
	}
	var buf bytes.Buffer
	if !interfaceEqual(&buf, &buf) {
		t.Error("expected identical comparable writers to compare equal")
	}
	if interfaceEqual(&buf, nil) {
		t.Error("expected writer and nil to compare unequal")
	}
}
