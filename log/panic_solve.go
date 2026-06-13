package log

import (
	"fmt"
	"os"
	"runtime"
)

// panicExitCode panic 恢复失败后的进程退出码
const panicExitCode = 127

// SolvePanic 捕获并记录 panic 堆栈信息后退出进程
func SolvePanic() {
	// recover
	if err := recover(); err != nil {
		defer func() {
			// 预防这段代码panic
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "\n\nrecovered panic failed: %v\n\nrecover panic details: %v\n\n", err, r)
				os.Exit(panicExitCode)
			}
		}()
		panicLogObj := New("panic")
		panicLogObj.Error("Panic! Error: %v", err)

		buf := make([]byte, 4096)
		n := runtime.Stack(buf, false)
		panicLogObj.Error("Panic Stack: %s", string(buf[:n]))
		os.Exit(panicExitCode)
	}
}
