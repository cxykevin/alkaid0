package actions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cxykevin/alkaid0/cronjob"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/ui/funcs"
	"github.com/cxykevin/alkaid0/ui/state"
)

func TestChatExists(t *testing.T) {
	cwd := t.TempDir()
	db, err := loadDB(cwd)
	if err != nil {
		t.Fatalf("loadDB: %v", err)
	}
	id, err := funcs.CreateChat(db, false)
	if err != nil {
		closeDB(cwd)
		t.Fatalf("CreateChat: %v", err)
	}
	closeDB(cwd)

	if ok, err := chatExists(cwd, id); err != nil || !ok {
		t.Fatalf("chatExists(existing) = (%t, %v), want (true, nil)", ok, err)
	}
	if ok, err := chatExists(cwd, id+1); err != nil || ok {
		t.Fatalf("chatExists(missing) = (%t, %v), want (false, nil)", ok, err)
	}
	if _, err := chatExists(filepath.Join(cwd, "no-such-dir"), id); !errors.Is(err, cronjob.ErrWorkdirMissing) {
		t.Fatalf("chatExists(no workdir) err = %v, want ErrWorkdirMissing", err)
	}

	// 未初始化的工作目录：没有会话库 → 会话不存在，且不得为检查而创建 .alkaid0。
	empty := t.TempDir()
	if ok, err := chatExists(empty, 1); err != nil || ok {
		t.Fatalf("chatExists(uninitialized) = (%t, %v), want (false, nil)", ok, err)
	}
	if _, err := os.Stat(filepath.Join(empty, ".alkaid0")); !os.IsNotExist(err) {
		t.Fatalf("chatExists must not create .alkaid0, stat err = %v", err)
	}
}

func TestDispatchCronPromptInvalidTargets(t *testing.T) {
	cwd := t.TempDir()

	if err := dispatchCronPrompt(filepath.Join(cwd, "no-such-dir"), 1, "hello"); !errors.Is(err, cronjob.ErrWorkdirMissing) {
		t.Fatalf("dispatch missing workdir err = %v, want ErrWorkdirMissing", err)
	}
	// 目录存在但没有会话库：会话不存在（不会新建库）。
	if err := dispatchCronPrompt(cwd, 1, "hello"); !errors.Is(err, cronjob.ErrSessionMissing) {
		t.Fatalf("dispatch missing session err = %v, want ErrSessionMissing", err)
	}
}

// TestDispatchCronPromptSkipsBusySession 会话不空闲时本次触发直接跳过（不排队）。
func TestDispatchCronPromptSkipsBusySession(t *testing.T) {
	cwd := t.TempDir()
	db, err := loadDB(cwd)
	if err != nil {
		t.Fatalf("loadDB: %v", err)
	}
	id, err := funcs.CreateChat(db, false)
	if err != nil {
		closeDB(cwd)
		t.Fatalf("CreateChat: %v", err)
	}
	closeDB(cwd)

	sessID := cwd2SessionID(cwd, id)
	chat := &structs.Chats{ID: id}
	chat.SetState(state.StateRequesting)
	sessLock.Lock()
	sessions[sessID] = &sessionObj{cwd: cwd, id: id, session: chat, ctx: context.Background()}
	sessLock.Unlock()
	t.Cleanup(func() {
		sessLock.Lock()
		if obj, ok := sessions[sessID]; ok && obj.releaseTimer != nil {
			obj.releaseTimer.Stop()
			obj.releaseTimer = nil
		}
		delete(sessions, sessID)
		sessLock.Unlock()
	})

	if err := dispatchCronPrompt(cwd, id, "hello"); !errors.Is(err, cronjob.ErrBusy) {
		t.Fatalf("dispatch on busy session err = %v, want ErrBusy", err)
	}
	if err := dispatchCronPrompt(cwd, id, "   "); err == nil || errors.Is(err, cronjob.ErrBusy) {
		t.Fatalf("dispatch empty prompt err = %v, want empty-prompt error", err)
	}
}
