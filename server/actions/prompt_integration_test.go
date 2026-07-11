package actions

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/mock/openai"
	u "github.com/cxykevin/alkaid0/utils"
)

// setupConfigForTest 配置使用 mock openai
func setupConfigForTest() {
	apiKey := "test-key"
	if config.GlobalConfig == nil {
		config.GlobalConfigSwap(cfgStructs.Config{})
	}
	config.GlobalConfig.Version = 1
	config.GlobalConfig.Model = cfgStructs.ModelsConfig{
		Models: map[int32]cfgStructs.ModelConfig{
			1: {
				ModelName:   "test-chat",
				ModelID:     "test-chat",
				ProviderURL: "http://localhost:56108/v1",
				ProviderKey: apiKey,
			},
		},
		DefaultModelID: 1,
	}
	config.GlobalConfig.Agent = cfgStructs.AgentsConfig{
		MaxCallCount:        1,
		DisableSandbox:      true,
		IgnoreBuiltinAgents: true,
		IgnoreDefaultRules:  true,
		DefaultAutoApprove:  "true",
		SummaryModel:        1,
		Agents:              map[string]cfgStructs.AgentConfig{},
	}
}

// ReceivedCall 用于测试中接收 call 调用
type ReceivedCall struct {
	Name string
	Data any
}

// waitForUpdate 等待满足条件的调用
func waitForUpdate(ch <-chan ReceivedCall, match func(ReceivedCall) bool, timeout time.Duration) (ReceivedCall, bool) {
	deadline := time.After(timeout)
	for {
		select {
		case v := <-ch:
			if match(v) {
				return v, true
			}
		case <-deadline:
			return ReceivedCall{}, false
		}
	}
}

// TestPromptIntegration_SingleClient 从创建会话到发送 prompt 的完整流程，单客户端
func TestPromptIntegration_SingleClient(t *testing.T) {
	if os.Getenv("ALKAID0_DEBUG_MOCKSERVER") != "true" {
		t.Skip("ALKAID0_DEBUG_MOCKSERVER not set, skipping test")
		return
	}

	// 启动 mock server（serverOnce.Do 保证只启动一次）
	openai.StartServerTask()

	setupConfigForTest()

	// 清理全局状态，避免与其他测试互相影响
	sessions = map[string]*sessionObj{}
	sessLock = &sync.Mutex{}
	connCallMap = map[uint64]func(string, any, *string) error{}
	connCallLock = &sync.Mutex{}
	sessionConnMap = map[string][]uint64{}
	sessionConnLock = &sync.Mutex{}
	bindedSessionOnConn = map[uint64][]string{}

	// 创建临时工作目录
	tmpDir, err := os.MkdirTemp("", "alkaid0_test_")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// call 收集通道（buffer 需足够容纳所有流式 broadcast）
	calls := make(chan ReceivedCall, 100)

	// 注册第一个客户端并创建 session
	callFunc := func(name string, data any, _ *string) error {
		calls <- ReceivedCall{Name: name, Data: data}
		return nil
	}

	fmt.Println("[TEST] calling SessionNew to create session")
	// 创建会话目录
	// loadSession / SessionNew 会自动创建 .alkaid0
	resp, err := SessionNew(SessionNewRequest{Cwd: tmpDir}, callFunc, 1)
	if err != nil {
		t.Fatalf("SessionNew failed: %v", err)
	}

	sessionID := resp.SessionID
	if sessionID == "" {
		t.Fatal("empty session id")
	}

	fmt.Println("[TEST] calling SessionLoad to attach second client")
	// attach 第二个客户端（用来接收广播）
	calls2 := make(chan ReceivedCall, 100)
	callFunc2 := func(name string, data any, _ *string) error {
		calls2 <- ReceivedCall{Name: name, Data: data}
		return nil
	}
	_, err = SessionLoad(SessionLoadRequest{Cwd: tmpDir, SessionID: sessionID}, callFunc2, 2)
	if err != nil {
		t.Fatalf("SessionLoad failed: %v", err)
	}
	fmt.Printf("[TEST] calls2 buffered after SessionLoad: %d\n", len(calls2))

	fmt.Println("[TEST] calling SessionPrompt to send prompt")
	// 发送 prompt（来自 connID 1）
	prompt := []u.H{{"type": "text", "text": "Hello mock"}}
	_, err = SessionPrompt(SessionPromptRequest{SessionID: sessionID, Prompt: prompt}, nil, 1)
	if err != nil {
		t.Fatalf("SessionPrompt failed: %v", err)
	}
	fmt.Printf("[TEST] calls2 buffered after SessionPrompt: %d\n", len(calls2))

	// 等待第二个客户端收到 user_message_chunk（立即广播）或 session_stop
	matchUser := func(rc ReceivedCall) bool {
		if rc.Name != "session/update" {
			return false
		}
		if su, ok := rc.Data.(SessionUpdate); ok {
			if upd, ok2 := su.Update.(SessionUpdateUpdate); ok2 {
				return upd.SessionUpdate == "user_message_chunk" || upd.SessionUpdate == "alk.cxykevin.top/session_stop"
			}
		}
		return false
	}

	_, ok := waitForUpdate(calls2, matchUser, 5*time.Second)
	if !ok {
		t.Fatal("did not receive user_message_chunk on second client")
	}

	// 关闭会话
	closeSession(sessionID)
}

// TestPromptIntegration_MultiClient 多客户端场景验证广播
func TestPromptIntegration_MultiClient(t *testing.T) {
	if os.Getenv("ALKAID0_DEBUG_MOCKSERVER") != "true" {
		t.Skip("ALKAID0_DEBUG_MOCKSERVER not set, skipping test")
		return
	}

	// 启动 mock server（serverOnce.Do 保证只启动一次）
	openai.StartServerTask()

	setupConfigForTest()

	// 重置全局状态
	sessions = map[string]*sessionObj{}
	sessLock = &sync.Mutex{}
	connCallMap = map[uint64]func(string, any, *string) error{}
	connCallLock = &sync.Mutex{}
	sessionConnMap = map[string][]uint64{}
	sessionConnLock = &sync.Mutex{}
	bindedSessionOnConn = map[uint64][]string{}

	tmpDir, err := os.MkdirTemp("", "alkaid0_test_")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// 三个客户端（buffer 需足够容纳所有流式 broadcast）
	chs := make([]chan ReceivedCall, 3)
	for i := range chs {
		chs[i] = make(chan ReceivedCall, 100)
	}

	callFor := func(i int) func(string, any, *string) error {
		return func(name string, data any, _ *string) error {
			chs[i] <- ReceivedCall{Name: name, Data: data}
			return nil
		}
	}

	// 第一个客户端创建会话
	_, err = SessionNew(SessionNewRequest{Cwd: tmpDir}, callFor(0), 101)
	if err != nil {
		t.Fatalf("SessionNew failed: %v", err)
	}

	// 获取 session id from bindedSessionOnConn
	sessIDs := bindedSessionOnConn[101]
	if len(sessIDs) == 0 {
		t.Fatalf("no session bound to conn 101")
	}
	sessionID := sessIDs[0]

	// 其余客户端 attach
	for i := 1; i <= 2; i++ {
		_, err := SessionLoad(SessionLoadRequest{Cwd: tmpDir, SessionID: sessionID}, callFor(i), uint64(102+i))
		if err != nil {
			t.Fatalf("SessionLoad failed for client %d: %v", i, err)
		}
	}

	// 发送 prompt 来自 client 0
	_, err = SessionPrompt(SessionPromptRequest{SessionID: sessionID, Prompt: []u.H{{"type": "text", "text": "Broadcast test"}}}, nil, 101)
	if err != nil {
		t.Fatalf("SessionPrompt failed: %v", err)
	}

	// 等待其它两个客户端均能收到 user_message_chunk（立即广播）或 session_stop
	matchUser := func(rc ReceivedCall) bool {
		if rc.Name != "session/update" {
			return false
		}
		if su, ok := rc.Data.(SessionUpdate); ok {
			if upd, ok2 := su.Update.(SessionUpdateUpdate); ok2 {
				return upd.SessionUpdate == "user_message_chunk" || upd.SessionUpdate == "alk.cxykevin.top/session_stop"
			}
		}
		return false
	}

	for i := 1; i <= 2; i++ {
		_, ok := waitForUpdate(chs[i], matchUser, 5*time.Second)
		if !ok {
			t.Fatalf("client %d did not receive user_message_chunk", i)
		}
	}

	closeSession(sessionID)
}
