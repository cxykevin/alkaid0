package actions

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	feedbacksdk "github.com/cxykevin/feederback/sdk"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/provider/mask"
	"github.com/cxykevin/alkaid0/storage/structs"
	runTool "github.com/cxykevin/alkaid0/tools/tools/run"
	u "github.com/cxykevin/alkaid0/utils"
)

// ---- A1: config/set 落盘失败必须整体回滚 ----

// TestConfigSetSaveFailureRollsBack 回归：落盘失败时调用方收到错误，
// 但内存配置不得留下已被 patch 的半成品（旧实现 commit 在 Save 之前，
// Save 失败后内存里已是新配置，config/get 能看到"失败却生效"的部分修改）。
func TestConfigSetSaveFailureRollsBack(t *testing.T) {
	origConfig := *config.GlobalConfig
	defer config.GlobalConfigSwap(origConfig)

	tmpDir := t.TempDir()
	// 用一个普通文件占住配置文件的父目录，使 config.Save 的 MkdirAll 失败
	blocker := filepath.Join(tmpDir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatalf("prepare blocker: %v", err)
	}
	t.Setenv("ALKAID0_CONFIG_PATH", filepath.Join(blocker, "config.json"))
	config.Load()

	before := config.GlobalConfig.Server.Port
	newPort := uint16(34567)
	if before == newPort {
		t.Fatalf("test setup: unexpected port %d", before)
	}
	reqData, _ := json.Marshal(map[string]any{"Server": map[string]any{"port": newPort}})

	if _, err := ConfigSet(ConfigSetRequest{Config: reqData}, nil, 0); err == nil {
		t.Fatal("配置无法落盘时必须返回错误")
	} else if !strings.Contains(err.Error(), "failed to save config") {
		t.Fatalf("应报告落盘失败，got %v", err)
	}

	if got := config.GlobalConfig.Server.Port; got != before {
		t.Errorf("落盘失败后内存配置必须整体回滚：Server.Port = %d, want %d", got, before)
	}
}

// ---- A2: /mask add 在引擎禁用时不得报成功 ----

func TestMaskAddDisabledEngineFails(t *testing.T) {
	dir, db, ids := newSessionListDB(t, 1)
	// DataMask.Enable=false：NewEngine 返回 nil，AddCustom 写入的值永不生效
	restore := config.GlobalConfigSwap(cfgStructs.Config{})
	defer restore()

	obj := &sessionObj{cwd: dir, id: ids[0], session: &structs.Chats{ID: ids[0], DB: db}}
	_, err := commandMaps["/mask"].Function(obj, "add super-secret-value")
	if err == nil {
		t.Fatal("脱敏引擎禁用时 /mask add 必须报错，而不是回复成功")
	}

	var count int64
	if err := db.Model(&structs.CustomMask{}).Count(&count).Error; err != nil {
		t.Fatalf("count custom masks: %v", err)
	}
	if count != 0 {
		t.Errorf("引擎禁用时不应写入自定义脱敏值，got %d 行", count)
	}
}

// ---- A3: workflow 行/事件按会话隔离 ----

// TestPersistWorkflowEventIsolatedPerChat 回归：run 序号按工作目录从 1 重新计数
// （进程重启后一定会重复），旧实现仅以 run_id 为键：同一工作区第二个会话的
// workflow 事件会更新到第一个会话的行上，events 也因 (workflow_id, sequence)
// 唯一索引冲突而丢失。
func TestPersistWorkflowEventIsolatedPerChat(t *testing.T) {
	dir, db, ids := newSessionListDB(t, 2)
	sidA := registerTestSession(t, dir, ids[0])
	sidB := registerTestSession(t, dir, ids[1])

	runID := runTool.RunIDPrefix + "1" // 进程重启后两个会话都可能拿到 @temp/run/1
	persistWorkflowEvent(sidA, runID, runTool.WorkflowEvent{
		Type: "graph",
		Data: map[string]any{"nodes": []any{"node-A"}},
		Raw:  []byte(`{"nodes":["node-A"]}`),
	})
	persistWorkflowEvent(sidB, runID, runTool.WorkflowEvent{
		Type: "graph",
		Data: map[string]any{"nodes": []any{"node-B"}},
		Raw:  []byte(`{"nodes":["node-B"]}`),
	})

	snapA, err := workflowSnapshot(db, ids[0], runID, "")
	if err != nil {
		t.Fatalf("session A snapshot: %v", err)
	}
	snapB, err := workflowSnapshot(db, ids[1], runID, "")
	if err != nil {
		t.Fatalf("session B snapshot（B 的 workflow 行被 A 的 run_id 覆盖/无法创建）: %v", err)
	}
	graphA, _ := json.Marshal(snapA.Graph)
	graphB, _ := json.Marshal(snapB.Graph)
	if !strings.Contains(string(graphA), "node-A") || strings.Contains(string(graphA), "node-B") {
		t.Errorf("session A 的 workflow 被其他会话覆盖: %s", graphA)
	}
	if !strings.Contains(string(graphB), "node-B") || strings.Contains(string(graphB), "node-A") {
		t.Errorf("session B 的 workflow 图错误: %s", graphB)
	}

	// 每个会话各自一条事件
	for _, tc := range []struct {
		name   string
		chatID uint32
	}{{"A", ids[0]}, {"B", ids[1]}} {
		var count int64
		if err := db.Model(&structs.WorkflowEvents{}).Where("chat_id = ?", tc.chatID).Count(&count).Error; err != nil {
			t.Fatalf("count events: %v", err)
		}
		if count != 1 {
			t.Errorf("session %s 应有 1 条事件，got %d", tc.name, count)
		}
	}

	// 对外暴露的 runId 仍是原始值，不带库内会话前缀
	wfA, ok := snapA.Workflow.(map[string]any)
	if !ok || wfA["runId"] != runID {
		t.Errorf("runId 必须对外保持原值 %q, got %#v", runID, snapA.Workflow)
	}
}

// ---- A4: 第二次 /index 不得覆盖活动索引的 cancel 句柄 ----

func TestIndexCommandRejectsConcurrentRun(t *testing.T) {
	dir := t.TempDir()
	obj := &sessionObj{cwd: dir, id: 1}
	sessID := cwd2SessionID(dir, obj.id)

	oldConnCall, oldSessionConn := connCallMap, sessionConnMap
	connCallMap = map[uint64]func(string, any, *string) error{}
	sessionConnMap = map[string][]uint64{}
	defer func() { connCallMap, sessionConnMap = oldConnCall, oldSessionConn }()

	const connID = 77
	texts := make(chan string, 8)
	sessionConnMap[sessID] = []uint64{connID}
	connCallMap[connID] = func(_ string, data any, _ *string) error {
		su, ok := data.(SessionUpdate)
		if !ok {
			return nil
		}
		inner, ok := su.Update.(SessionUpdateUpdate)
		if !ok {
			return nil
		}
		if c, ok := inner.Content.(u.H); ok {
			if text, ok := c["text"].(string); ok {
				texts <- text
			}
		}
		return nil
	}

	absCwd, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !beginIndexRun(absCwd) {
		t.Fatal("test setup: index guard should be free")
	}
	defer endIndexRun(absCwd)

	if _, err := commandMaps["/index"].Function(obj, ""); err != nil {
		t.Fatalf("/index 应静默拒绝而不是报错: %v", err)
	}
	select {
	case msg := <-texts:
		if !strings.Contains(msg, "already running") {
			t.Errorf("第二次 /index 应提示已有索引在跑，got %q", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("已有索引运行时不得再次启动 RunIndex（否则会覆盖第一次索引的 cancel 句柄）")
	}
}

// ---- A5: /feedback 上传的日志尾部必须擦除自定义脱敏值 ----

func TestFeedbackCommandScrubsCustomMasks(t *testing.T) {
	t.Setenv("ALKAID0_DEBUG", "false")

	const customSecret = "custom-secret-value-123"
	const providerKey = "provider-key-abc-xyz"
	dir, db, ids := newSessionListDB(t, 1)
	if err := mask.AddCustom(db, customSecret); err != nil {
		t.Fatalf("AddCustom: %v", err)
	}
	restoreCfg := config.GlobalConfigSwap(cfgStructs.Config{
		Model: cfgStructs.ModelsConfig{
			Models: map[int32]cfgStructs.ModelConfig{0: {ProviderKey: providerKey}},
		},
	})
	defer restoreCfg()

	// 日志包只有静态正则，不含用户自定义脱敏值；用测试缝注入一段含敏感值的日志尾部
	oldTail := feedbackLogTail
	feedbackLogTail = func(int) string {
		return `INFO run in session=1 with input "key=` + customSecret + `&provider=` + providerKey + `"` + "\n"
	}
	defer func() { feedbackLogTail = oldTail }()

	oldSubmit := feedbackSubmit
	defer func() { feedbackSubmit = oldSubmit }()
	var gotLogs string
	feedbackSubmit = func(_ context.Context, _, logs, _ string) (*feedbacksdk.Result, error) {
		gotLogs = logs
		return &feedbacksdk.Result{FeedbackID: "fid-test"}, nil
	}

	obj := &sessionObj{cwd: dir, id: ids[0], session: &structs.Chats{ID: ids[0], DB: db}}
	collectFeedbackBroadcasts(t, obj, "bug 描述")

	if strings.Contains(gotLogs, customSecret) {
		t.Errorf("自定义脱敏值不得随 /feedback 上传，got %q", gotLogs)
	}
	if strings.Contains(gotLogs, providerKey) {
		t.Errorf("模型 ProviderKey 不得随 /feedback 上传，got %q", gotLogs)
	}
	if !strings.Contains(gotLogs, "***") {
		t.Errorf("敏感值应被替换为 ***，got %q", gotLogs)
	}
}

// ---- A6: 遥测失败也要记录尝试时间，避免无限重试 ----

func TestRunAutoTelemetryFailureRecordsAttempt(t *testing.T) {
	t.Setenv("ALKAID0_DEBUG", "false")

	oldPath := telemetryLastPath
	oldSubmit := feedbackSubmit
	defer func() { telemetryLastPath = oldPath }()
	defer func() { feedbackSubmit = oldSubmit }()

	path := filepath.Join(t.TempDir(), "telemetry_last")
	telemetryLastPath = func() string { return path }
	restore := config.GlobalConfigSwap(cfgStructs.Config{})
	defer restore()

	if err := os.WriteFile(path, []byte(strconv.FormatInt(time.Now().Unix()-31*24*3600, 10)), 0600); err != nil {
		t.Fatalf("prepare timestamp: %v", err)
	}

	calls := 0
	feedbackSubmit = func(context.Context, string, string, string) (*feedbacksdk.Result, error) {
		calls++
		return nil, errors.New("network down")
	}

	runAutoTelemetry()
	if calls != 1 {
		t.Fatalf("超过周期时应尝试上报一次，got %d", calls)
	}
	// 失败后必须记录尝试时间：否则每次启动都会重试，形成无上限上报
	runAutoTelemetry()
	if calls != 1 {
		t.Errorf("上报失败后不得无限重试：第二次调用又提交了，calls=%d", calls)
	}
}
