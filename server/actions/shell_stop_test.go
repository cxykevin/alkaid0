package actions

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/context/codebase"
	"github.com/cxykevin/alkaid0/storage"
	runTool "github.com/cxykevin/alkaid0/tools/tools/run"
	"github.com/cxykevin/alkaid0/ui/funcs"
	u "github.com/cxykevin/alkaid0/utils"
)

// TestShellStopOnlyBroadcasts 回归：终端停止（shell_stop）只广播事件，不再作为内部
// 运行时通知注入 loop。
//
// 变更前 loadSession 注册的 ShellStopFn 在会话 idle/waiting 时会把
// "Background shell ... stopped" 交给 loop.NotifySystem，从而触发一轮无人要求的
// 模型请求（loop 已退出时还会先重建 loop）；变更后模型只在下一次用户输入、或主动
// 查询终端结果（run 的 wait / 读取 @temp/run/<n>）时才会看到命令输出
// （契约见 docs/acp/extension.md §2.3）。
//
// 本用例断言：事件照常广播且字段透传，同时 loop 没有收到内部通知
// （Chats.SystemPrompt 保持为空、没有任何模型输出）。
func TestShellStopOnlyBroadcasts(t *testing.T) {
	// 隔离全局注册表：本用例会经 loadSession 在真实目录上启动 loop goroutine。
	sessLock.Lock()
	oldSessions := sessions
	sessions = map[string]*sessionObj{}
	sessLock.Unlock()
	dbLock.Lock()
	oldDbs := dbs
	dbs = map[string]*dbObj{}
	dbLock.Unlock()
	// 恢复全局注册表必须用 t.Cleanup（而非 defer）注册，且注册早于下面的 closeSession：
	// t.Cleanup 逆序执行，而 defer 在测试函数返回时就跑 —— 用 defer 会先恢复注册表，
	// 随后 closeSession 在旧的 sessions 里查不到本会话，于是既不 cancel loop 也不
	// closeDB，连接一直开着（Linux 删已打开文件不报错，Windows 上 TempDir 清理必失败）。
	t.Cleanup(func() {
		// 释放的会话必须把该目录的 DB 连接一并关掉：closeDB 只在引用计数归零时才
		// Close 并从 dbs 摘除，残留条目意味着 TempDir 清理时文件仍被占用。
		dbLock.Lock()
		remaining := len(dbs)
		dbLock.Unlock()
		if remaining != 0 {
			t.Errorf("会话释放后仍残留 %d 个 DB 连接未关闭（Windows 上会导致 TempDir 清理失败）", remaining)
		}
		sessLock.Lock()
		sessions = oldSessions
		sessLock.Unlock()
		dbLock.Lock()
		dbs = oldDbs
		dbLock.Unlock()
	})

	// loadSession 会构造模型列表，依赖 config.GlobalConfig
	if config.GlobalConfig == nil {
		config.GlobalConfigSwap(cfgStructs.Config{})
	}

	tmpDir := t.TempDir()
	// loadSession 的异步索引 goroutine 会打开 codebase.sqlite 且连接常驻，
	// 须在 TempDir 清理前关闭（否则 Windows 上删除被占用文件失败）。
	t.Cleanup(func() {
		_ = codebase.CloseDirectory(tmpDir)
	})
	db, err := storage.InitStorage(filepath.Join(tmpDir, ".alkaid0"), "")
	if err != nil {
		t.Fatalf("InitStorage failed: %v", err)
	}
	defer u.Unwrap(db.DB()).Close()
	chatID, err := funcs.CreateChat(db, false)
	if err != nil {
		t.Fatalf("CreateChat failed: %v", err)
	}
	sessionID := cwd2SessionID(tmpDir, chatID)

	id := chatID
	sess, err := loadSession(tmpDir, &id, true)
	if err != nil {
		t.Fatalf("loadSession failed: %v", err)
	}
	sessLock.Lock()
	obj := sessions[sessionID]
	sessLock.Unlock()
	if obj == nil || obj.session == nil {
		t.Fatal("session should be registered after loadSession")
	}
	// loop goroutine 由 loadSession 启动，这里等异步索引收尾，避免其在清理期间重开索引库。
	if obj.indexDone != nil {
		select {
		case <-obj.indexDone:
		case <-time.After(10 * time.Second):
			t.Error("timeout waiting for async index goroutine")
		}
	}
	// 摘除会话并 join loop、关库（释放引用计数从 1 减到 0）
	t.Cleanup(func() { closeSession(sessionID) })

	// 广播捕获：shell_stop 是唯一应当出现的事件
	var mu sync.Mutex
	var updates []SessionUpdate
	registerCaptureConn(t, 71, sessionID, func(su SessionUpdate) {
		mu.Lock()
		updates = append(updates, su)
		mu.Unlock()
	})

	sess.PushShellStop("run-shell-stop", "sleep 30", &runTool.Result{Success: true, Killed: true})

	// 宽限期：给（变更前的）通知注入路径足够时间入队、起请求并广播。
	// 固定等待而非轮询 SystemPrompt：AppendSystemPrompt 不走锁，轮询会与
	// loop goroutine 并发读写同一字段（-race 下会报 data race）。
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()

	// 1) 事件本身仍要广播给客户端，字段按契约透传
	var stops []SessionUpdateUpdate
	for _, su := range updates {
		upd, ok := su.Update.(SessionUpdateUpdate)
		if !ok {
			t.Errorf("session/update 载荷类型异常：%#v", su.Update)
			continue
		}
		switch upd.SessionUpdate {
		case "alk.cxykevin.top/shell_stop":
			stops = append(stops, upd)
		case "user_message", "agent_message_chunk", "agent_thought_chunk":
			t.Errorf("终端停止不应触发模型输出，却收到 %s", upd.SessionUpdate)
		}
	}
	if len(stops) != 1 {
		t.Fatalf("期望广播 1 次 shell_stop，实际 %d 次（全部事件：%#v）", len(stops), updates)
	}
	got := stops[0]
	if got.RunID != "run-shell-stop" || got.TerminalID != "run-shell-stop" || got.Command != "sleep 30" ||
		got.Status != "stop" || !got.Success || !got.Killed {
		t.Errorf("shell_stop 字段不符合契约：%#v", got)
	}

	// 2) 没有内部运行时通知被注入 loop：注入会写 SystemPrompt 并立即起一轮模型请求
	if sess.SystemPrompt != "" {
		t.Errorf("终端停止被当作内部通知注入了 loop（SystemPrompt=%q）：会触发一轮无人要求的模型请求", sess.SystemPrompt)
	}
}
