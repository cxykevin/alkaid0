package storage

import (
	"os"
	"testing"
)

func TestInit(t *testing.T) {
	// 使用内存数据库进行测试
	os.Setenv("ALKAID_DEBUG_SQLITEFILE", ":memory:")
	InitStorage("", "")
}

func TestConcurrentInit(t *testing.T) {
	// 使用内存数据库进行并发测试
	os.Setenv("ALKAID_DEBUG_SQLITEFILE", ":memory:")

	const numGoroutines = 10
	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			_, err := InitStorage("", "")
			if err != nil {
				t.Errorf("InitStorage failed: %v", err)
			}
			done <- true
		}()
	}

	for i := 0; i < numGoroutines; i++ {
		<-done
	}
}
