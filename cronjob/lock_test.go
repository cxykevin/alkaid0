//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly || windows

package cronjob

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestFileLockIsExclusiveAndReusable(t *testing.T) {
	path := filepath.Join(t.TempDir(), LockFileName)

	first, err := acquireFileLock(path)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if _, err := acquireFileLock(path); !errors.Is(err, ErrLocked) {
		first.release()
		t.Fatalf("second acquire err = %v, want ErrLocked", err)
	}

	first.release()
	second, err := acquireFileLock(path)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	second.release()
}
