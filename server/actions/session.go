package actions

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/context/codebase"
	"github.com/cxykevin/alkaid0/provider/request"
	"github.com/cxykevin/alkaid0/storage"
	"github.com/cxykevin/alkaid0/storage/structs"
	task "github.com/cxykevin/alkaid0/tools/tools/task"
	"github.com/cxykevin/alkaid0/ui/funcs"
	"github.com/cxykevin/alkaid0/ui/loop"
	"github.com/cxykevin/alkaid0/ui/state"
	u "github.com/cxykevin/alkaid0/utils"
	"gorm.io/gorm"
)

// SessionNewRequest 创建新会话的请求
type SessionNewRequest struct {
	Cwd string `json:"cwd"`
}

// ConfigOptionValue 配置选项值
type ConfigOptionValue struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ConfigOption 配置选项（ACP v2：configId 命名）
type ConfigOption struct {
	ConfigID     string              `json:"configId"`
	Name         string              `json:"name"`
	Description  string              `json:"description,omitempty"`
	Category     string              `json:"category,omitempty"`
	Type         string              `json:"type"`
	CurrentValue string              `json:"currentValue"`
	Options      []ConfigOptionValue `json:"options"`
}

// SessionNewResponse 创建新会话的响应（ACP v2：无 models，模型经 configOptions 暴露）
type SessionNewResponse struct {
	SessionID     string         `json:"sessionId"`
	ConfigOptions []ConfigOption `json:"configOptions"`
}

// ReplayFrom session/resume 的历史回放参数（ACP v2）
type ReplayFrom struct {
	Type string `json:"type"` // "start"
}

// SessionResumeRequest 恢复会话的请求（ACP v2）
type SessionResumeRequest struct {
	SessionID  string      `json:"sessionId"`
	Cwd        string      `json:"cwd"`
	ReplayFrom *ReplayFrom `json:"replayFrom,omitempty"`
}

// SessionResumeResponse 恢复会话的响应（ACP v2：无 models）
type SessionResumeResponse struct {
	ConfigOptions []ConfigOption `json:"configOptions"`
}

// sessionObj 会话对象，包含会话的核心信息和生命周期管理
// toolStreamInterval 工具调用增量流式广播的限流间隔。
// 每 0.1s 最多向前端推送一次完整快照，避免每 token 一次广播导致前端渲染抖动。
// 内网/127 回环部署，100ms 粒度足够实时且开销极小。
const toolStreamInterval = 100 * time.Millisecond

type sessionObj struct {
	cwd      string
	id       uint32
	session  *structs.Chats
	loop     *loop.Object
	ctx      context.Context
	referCnt int
	// permMu 保护 permChan 的并发读写
	permMu sync.Mutex
	// permChan 等待中的 request_permission 响应通道（nil 表示无等待）
	permChan chan bool
	// permDone 会话释放信号；释放时 close，解除权限 goroutine 的无限等待
	permDone     chan struct{}
	permDoneOnce sync.Once
	// releaseTimer 连接断开后延迟释放的定时器
	// 当连接断开时启动，超时后若仍无连接则释放会话资源
	releaseTimer *time.Timer
	// background 后台运行模式
	// 为 true 时，断连后若 loop 正在活跃处理则保持运行，空闲时才释放
	// 通过 /background on 启用，重启 loop 后自动重置为 false
	background bool
	// lastToolStreamTime 工具调用增量流式广播的上次推送时间（限流用）
	lastToolStreamTime time.Time
	// indexDone 异步索引 goroutine（loadSession 启动）的完成信号。
	// goroutine 完成后 close 该 channel；测试等它完成后再清理 TempDir，
	// 避免异步索引在目录清理期间重新打开 codebase.sqlite 导致 Windows 删除失败。
	indexDone chan struct{}
}

// dbObj 数据库对象，包含引用计数用于生命周期管理
type dbObj struct {
	db       *gorm.DB
	referCnt int
}

var sessions = map[string]*sessionObj{}
var sessLock = &sync.Mutex{}
var dbs = map[string]*dbObj{}
var dbLock = &sync.Mutex{}

// 连接ID到会话ID列表的映射
var bindedSessionOnConn = map[uint64][]string{}
var bindedSessionOnConnMu = &sync.Mutex{}

// 连接ID到call函数的映射，用于发送跨conn通知
var connCallMap = map[uint64]func(string, any, *string) error{}
var connCallLock = &sync.Mutex{}

// 会话ID到连接ID列表的反向映射，用于广播更新
var sessionConnMap = map[string][]uint64{}
var sessionConnLock = &sync.Mutex{}

var agentCallList = map[string]map[string]func(){}

// cwd2SessionID 将工作目录和会话ID转换为规范化的会话ID格式
func cwd2SessionID(cwd string, id uint32) string {
	return fmt.Sprintf("sess_%d:%s", id, cwd)
}

func getMinValueByKey[K cmp.Ordered, T any](m map[K]T) (K, *T, bool) {
	if len(m) == 0 {
		return *new(K), new(T), false // map 为空
	}

	// 初始化最小键（假设键可以比较）
	var minKey K
	var minValue *T
	first := true

	for k, v := range m {
		if first || k < minKey {
			minKey = k
			minValue = &v
			first = false
		}
	}

	return minKey, minValue, true
}

func getDefaultModel() string {
	cfg := config.GlobalConfig.Model.Models
	defaultID := config.GlobalConfig.Model.DefaultModelID
	if obj, ok := cfg[defaultID]; ok {
		return fmt.Sprintf("%d/%s", defaultID, obj.ModelID)
	}
	if len(cfg) == 0 {
		return "0/UnconfiguredAnyModel"
	}
	id, obj, _ := getMinValueByKey(cfg)
	logger.Debug("default model: %s", fmt.Sprintf("%d/%s", id, obj.ModelID))
	return fmt.Sprintf("%d/%s", id, obj.ModelID)
}

// buildConfigOptions 生成配置选项列表（ACP v2：model 经 configOptions 暴露，含 thought_level 选项）
func buildConfigOptions(currentModelID uint32, reasoningEffort string) []ConfigOption {
	cfg := config.GlobalConfig.Model.Models
	options := make([]ConfigOptionValue, 0, len(cfg))

	for i, model := range cfg {
		if model.Hide {
			continue
		}
		options = append(options, ConfigOptionValue{
			Value:       fmt.Sprintf("%d/%s", i, model.ModelID),
			Name:        model.ModelName,
			Description: "",
		})
	}

	slices.SortFunc(options, func(a, b ConfigOptionValue) int {
		// 值形如 "<index>/<modelID>"，按 index 排序
		ia, _ := strconv.ParseInt(strings.SplitN(a.Value, "/", 2)[0], 10, 32)
		ib, _ := strconv.ParseInt(strings.SplitN(b.Value, "/", 2)[0], 10, 32)
		return int(ia - ib)
	})

	// 确保当前模型值格式正确
	currentValue := fmt.Sprintf("%d/%s", currentModelID, u.Default(cfg, int32(currentModelID), cfgStructs.ModelConfig{
		ModelID: fmt.Sprintf("UnknownModel(%d)", currentModelID),
	}).ModelID)

	// 推理强度（thought_level）选项
	effort := reasoningEffort
	if effort == "" {
		effort = "unset"
	}

	return []ConfigOption{
		{
			ConfigID:     "model",
			Name:         "Model",
			Category:     "model",
			Type:         "select",
			CurrentValue: currentValue,
			Options:      options,
		},
		{
			ConfigID:     "thought_level",
			Name:         "Thought Level",
			Category:     "thought_level",
			Type:         "select",
			CurrentValue: effort,
			Options: []ConfigOptionValue{
				{Value: "unset", Name: "Unset"},
				{Value: "low", Name: "Low"},
				{Value: "medium", Name: "Medium"},
				{Value: "high", Name: "High"},
				{Value: "max", Name: "Max"},
				{Value: "xhigh", Name: "XHigh"},
			},
		},
	}
}

// sessionID2Cwd 解析会话ID，返回工作目录和会话ID。
// 以服务器会话注册表为准返回会话的真实工作目录：
//   - 防止客户端伪造 sessionId 中的 cwd 部分绕过沙箱（任意文件读写/递归删除）；
//   - 防止畸形 sessionId（如 "abcd:x"）触发 s[0][5:] 越界 panic（远程 DoS）。
func sessionID2Cwd(sessionID string) (string, uint32, error) {
	if sessionID == "" {
		return "", 0, fmt.Errorf("session id is empty")
	}
	sessLock.Lock()
	defer sessLock.Unlock()
	obj, ok := sessions[sessionID]
	if !ok {
		return "", 0, fmt.Errorf("session not found")
	}
	return obj.cwd, obj.id, nil
}

// parseSessionID 从会话ID字符串中安全解析出工作目录和会话ID（纯字符串解析，不查内存注册表）。
// 仅供 session/resume 冷还原使用：服务器重启后内存注册表为空，需从客户端提供的 sessionId 恢复
// cwd+id，再交由 loadSession 打开对应数据库并 QueryChat 验证会话真实存在，验证通过后才注册进
// 内存注册表。与 sessionID2Cwd 的区别：后者以内存注册表为准（防止伪造 cwd 绕过沙箱），
// parseSessionID 本身不授权任何操作，其解析结果必须经过数据库校验后方可使用。
func parseSessionID(sessionID string) (string, uint32, error) {
	if sessionID == "" {
		return "", 0, fmt.Errorf("session id is empty")
	}
	s := strings.SplitN(sessionID, ":", 2)
	if len(s) != 2 || !strings.HasPrefix(s[0], "sess_") {
		return "", 0, fmt.Errorf("invalid session id")
	}
	idStr := strings.TrimPrefix(s[0], "sess_")
	if idStr == "" {
		return "", 0, fmt.Errorf("invalid session id")
	}
	num, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		return "", 0, fmt.Errorf("invalid session id")
	}
	cwd := s[1]
	if cwd == "" {
		return "", 0, fmt.Errorf("invalid session id")
	}
	return cwd, uint32(num), nil
}

// registerConnCall 注册连接的call函数和会话绑定
// 新连接绑定到会话时，会自动取消任何待处理的延迟释放定时器
func registerConnCall(connID uint64, sessionID string, callFunc func(string, any, *string) error) {
	connCallLock.Lock()
	defer connCallLock.Unlock()
	connCallMap[connID] = callFunc

	sessionConnLock.Lock()
	defer sessionConnLock.Unlock()
	// 检查是否已存在，防止重复添加
	if slices.Contains(sessionConnMap[sessionID], connID) {
		return
	}
	sessionConnMap[sessionID] = append(sessionConnMap[sessionID], connID)

	// 新连接绑定到此会话，取消任何待处理的延迟释放定时器
	cancelSessionRelease(sessionID)
}

// unregisterConnCall 注销连接和会话的绑定
func unregisterConnCall(connID uint64, sessionID string) {
	connCallLock.Lock()
	defer connCallLock.Unlock()
	delete(connCallMap, connID)

	sessionConnLock.Lock()
	defer sessionConnLock.Unlock()
	conns := sessionConnMap[sessionID]
	for i, cid := range conns {
		if cid == connID {
			sessionConnMap[sessionID] = append(conns[:i], conns[i+1:]...)
			break
		}
	}
	if len(sessionConnMap[sessionID]) == 0 {
		delete(sessionConnMap, sessionID)
	}
}

// broadcastSessionUpdate 向所有连接到该会话的客户端广播更新
// 如果broadcastConnID != 0，则排除该连接（不向自己发送）
func broadcastSessionUpdate(sessionID string, update any, excludeConnID uint64) error {

	logger.Debug("broadcast \"%#v\" in session %s exclude %d", update, sessionID, excludeConnID)

	sessionConnLock.Lock()
	connIDs := make([]uint64, len(sessionConnMap[sessionID]))
	copy(connIDs, sessionConnMap[sessionID])
	sessionConnLock.Unlock()

	connCallLock.Lock()
	callFuncs := make(map[uint64]func(string, any, *string) error)
	for _, cid := range connIDs {
		if cid != excludeConnID {
			if fn, ok := connCallMap[cid]; ok {
				callFuncs[cid] = fn
			}
		}
	}
	connCallLock.Unlock()

	var lastErr error
	for _, fn := range callFuncs {
		err := fn("session/update", update, nil)
		if err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// broadcastStateUpdate 广播 ACP v2 state_update（state/stopReason 置于 update 顶层）
func broadcastStateUpdate(sessionID, state, stopReason, errMsg string) {
	if err := broadcastSessionUpdate(sessionID, SessionUpdate{
		SessionID: sessionID,
		Update: SessionUpdateUpdate{
			SessionUpdate:  "state_update",
			State:          state,
			StopReason:     stopReason,
			ExpandErrorMsg: errMsg,
		},
	}, 0); err != nil {
		logger.Warn("failed to broadcast state_update: %v", err)
	}
}

// broadcastToolCallCancelled 拒绝时把待审批工具标记为 cancelled
func broadcastToolCallCancelled(sessionID string, pending *[]funcs.ToolCall) {
	if pending == nil {
		return
	}
	obj, ok := sessions[sessionID]
	if !ok {
		return
	}
	for _, tool := range *pending {
		toolCallID := fmt.Sprintf("call_%d_%d_%s", obj.session.ID, obj.session.CurrentMessageID, tool.ID)
		_ = broadcastSessionUpdate(sessionID, SessionUpdate{
			SessionID: sessionID,
			Update: SessionUpdateUpdate{
				SessionUpdate: "tool_call_update",
				ToolCallID:    toolCallID,
				Title:         fmt.Sprintf("[Call %s]%s", tool.Name, tool.ID),
				Kind:          u.Default(ToolNameToTypeMap, tool.Name, "other"),
				Status:        "cancelled",
				Content:       []u.H{{"type": "alk.cxykevin.top/calling_info", "name": tool.Name, "args": tool.Parameters}},
			},
		}, 0)
	}
}

// connCallFuncsFor 快照会话所有连接的 call 函数（用于向客户端发起请求）
func connCallFuncsFor(obj *sessionObj) []func(string, any, *string) error {
	sessionID := cwd2SessionID(obj.cwd, obj.id)
	sessionConnLock.Lock()
	connIDs := make([]uint64, len(sessionConnMap[sessionID]))
	copy(connIDs, sessionConnMap[sessionID])
	sessionConnLock.Unlock()

	connCallLock.Lock()
	callFuncs := make([]func(string, any, *string) error, 0, len(connIDs))
	for _, cid := range connIDs {
		if fn, ok := connCallMap[cid]; ok {
			callFuncs = append(callFuncs, fn)
		}
	}
	connCallLock.Unlock()
	return callFuncs
}

// signalPermission 非阻塞唤醒等待中的权限 goroutine（session/cancel 时使用）
func signalPermission(obj *sessionObj, approved bool) {
	if obj == nil {
		return
	}
	obj.permMu.Lock()
	ch := obj.permChan
	obj.permMu.Unlock()
	if ch != nil {
		select {
		case ch <- approved:
		default:
		}
	}
}

// requestPermission 发送 session/request_permission 并等待首个响应。
// 无超时：仅在回包到达或会话释放（permDone 关闭）时解除。
func requestPermission(obj *sessionObj, pending *[]funcs.ToolCall) (bool, error) {
	if obj == nil || obj.session == nil || pending == nil || len(*pending) == 0 {
		return false, fmt.Errorf("invalid permission request")
	}
	tool := (*pending)[0]
	toolCallID := fmt.Sprintf("call_%d_%d_%s", obj.session.ID, obj.session.CurrentMessageID, tool.ID)
	params := RequestPermissionParams{
		SessionID: cwd2SessionID(obj.cwd, obj.id),
		Title:     fmt.Sprintf("Approve tool call: %s", tool.Name),
		Subject: &PermissionSubject{
			Type: "tool_call",
			ToolCall: &ToolCallInfo{
				ToolCallID: toolCallID,
				Title:      fmt.Sprintf("[Call %s]%s", tool.Name, tool.ID),
				Kind:       u.Default(ToolNameToTypeMap, tool.Name, "other"),
				Status:     "pending",
				Content:    []u.H{{"type": "alk.cxykevin.top/calling_info", "name": tool.Name, "args": tool.Parameters}},
			},
		},
		Options: []PermissionOption{
			{OptionID: "allow_once", Name: "Allow once", Kind: "allow_once"},
			{OptionID: "reject_once", Name: "Reject once", Kind: "reject_once"},
		},
	}

	id := fmt.Sprintf("perm_%d", rpcSrv.NextReqSeq())
	ch := make(chan bool, 1)
	rpcSrv.AddPending(id, func(resp u.H) {
		if err, ok := resp["error"]; ok && err != nil {
			ch <- false
			return
		}
		// 回包形状：{outcome: selected|cancelled, optionId}。结果可能为 map，序列化再解析。
		raw, merr := json.Marshal(resp["result"])
		if merr != nil {
			ch <- false
			return
		}
		var result RequestPermissionResult
		if json.Unmarshal(raw, &result) != nil {
			ch <- false
			return
		}
		ch <- (result.Outcome == "selected" && result.OptionID == "allow_once")
	})
	defer rpcSrv.RemovePending(id)

	obj.permMu.Lock()
	obj.permChan = ch
	obj.permMu.Unlock()
	defer func() {
		obj.permMu.Lock()
		obj.permChan = nil
		obj.permMu.Unlock()
	}()

	// 向会话所有连接发起请求（首个响应生效）
	for _, fn := range connCallFuncsFor(obj) {
		_ = fn("session/request_permission", params, &id)
	}

	select {
	case approved := <-ch:
		return approved, nil
	case <-obj.permDone:
		return false, fmt.Errorf("session released")
	}
}

// // broadcastCallRequest 向所有连接到该会话的客户端广播更新
// func broadcastCallRequest(sessionID string, funcName string, update any) error {
// 	logger.Debug("broadcast call \"%s\" in session %s", funcName, sessionID)

// 	sessionConnLock.Lock()
// 	connIDs := make([]uint64, len(sessionConnMap[sessionID]))
// 	copy(connIDs, sessionConnMap[sessionID])
// 	sessionConnLock.Unlock()

// 	connCallLock.Lock()
// 	callFuncs := make(map[uint64]func(string, any, *string) error)
// 	for _, cid := range connIDs {
// 		if fn, ok := connCallMap[cid]; ok {
// 			callFuncs[cid] = fn
// 		}
// 	}
// 	connCallLock.Unlock()

// 	var lastErr error
// 	for _, fn := range callFuncs {
// 		err := fn(funcName, update, nil)
// 		if err != nil {
// 			lastErr = err
// 		}
// 	}
// 	return lastErr
// }

// loadDB 加载数据库连接，支持连接复用和引用计数
func loadDB(pathx string) (*gorm.DB, error) {
	dbLock.Lock()
	defer dbLock.Unlock()
	if obj, ok := dbs[pathx]; ok {
		obj.referCnt++
	} else {
		if pathx == "" {
			return nil, fmt.Errorf("cwd is empty")
		}
		pathx = path.Clean(pathx)
		info, err := os.Stat(pathx)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("cwd not found or not a directory")
		}
		db, err := storage.InitStorage(path.Join(pathx, ".alkaid0"), "")
		if err != nil {
			return nil, err
		}
		dbs[pathx] = &dbObj{
			db:       db,
			referCnt: 1,
		}
	}
	return dbs[pathx].db, nil
}

// closeDB 关闭数据库连接，引用计数递减，处理资源清理
func closeDB(path string) {
	logger.Debug("close db %s", path)
	dbLock.Lock()
	defer dbLock.Unlock()
	if obj, ok := dbs[path]; ok {
		obj.referCnt--
		if obj.referCnt <= 0 {
			logger.Info("release db %s", path)
			delete(dbs, path)
			db, _ := obj.db.DB()
			db.Close()
		}
	}
}

// loadSession 加载或创建会话，支持引用计数生命周期管理
// knowID为true时表示使用已知的会话ID，否则创建新会话
func loadSession(cwd string, id *uint32, knowID bool) (*structs.Chats, error) {
	logger.Info("load session cwd=%s id=%d knowID=%t", cwd, *id, knowID)
	sessID := ""
	if knowID {
		sessID = cwd2SessionID(cwd, *id)
	}
	sessLock.Lock()
	defer sessLock.Unlock()
	if _, ok := sessions[sessID]; !ok {
		obj := &sessionObj{
			cwd:       cwd,
			id:        0,
			ctx:       context.Background(),
			referCnt:  1,
			indexDone: make(chan struct{}),
		}

		db, err := loadDB(cwd)
		if err != nil {
			return nil, err
		}

		// Codebase 初始化（懒初始化，首次使用时自动连接）
		if err := codebase.Initialize(); err != nil {
			logger.Warn("codebase init: %v (continuing without codebase)", err)
		}

		if !knowID {
			idv, err := funcs.CreateChat(db)
			*id = idv
			if err != nil {
				closeDB(cwd)
				return nil, err
			}
			obj.id = idv
			sessID = cwd2SessionID(cwd, idv)
		} else {
			obj.id = *id
		}

		chTemp, err := funcs.QueryChat(db, obj.id)
		if err != nil {
			closeDB(obj.cwd)
			return nil, err
		}

		chTemp.Root = cwd
		sess, err := funcs.InitChat(db, chTemp)
		if err != nil {
			closeDB(obj.cwd)
			return nil, err
		}
		sess.Root = cwd

		// 注册 ACP plan 推送回调：task 工具修改 @task 后触发，向会话所有客户端广播完整 plan。
		sess.SetPlanPushFn(func(entries []structs.PlanEntry) {
			err := broadcastSessionUpdate(sessID, SessionUpdate{
				SessionID: sessID,
				Update: SessionUpdateUpdate{
					SessionUpdate: "plan_update",
					Plan: &PlanItems{
						Type:    "items",
						PlanID:  fmt.Sprintf("plan_%d", sess.ID),
						Entries: entries,
					},
				},
			}, 0)
			if err != nil {
				logger.Warn("failed to broadcast plan update: %v", err)
			}
		})

		// 后台启动 codebase 索引。
		// 必须在 QueryChat/InitChat 验证会话真实存在并成功之后再启动：
		// 若过早启动，会话无效（QueryChat/InitChat 失败）时 goroutine 仍会 loadDB
		// 打开 db.sqlite，且无人负责关闭，导致连接泄漏——Windows 上 TempDir 清理
		// 无法删除被占用文件（TestSessionLoadColdRestore 子测试2 曾触发）。
		// 无 embedding 模型时 RunIndex 自动降级为 BM25-only（仅建立全文索引，不嵌向量），
		// 因此这里不再以 IsEmbeddingConfigured 守卫。
		// indexDone 在 goroutine 完成后关闭，供测试等待异步索引结束（见 TestSessionLoadColdRestore）。
		go func() {
			defer close(obj.indexDone)
			if err := codebase.RunIndex(context.Background(), cwd, nil); err != nil {
				logger.Debug("auto index: %v", err)
			}
			indexTempfsAndChatHistory(cwd)
		}()

		obj.loop = loop.New(sess)
		obj.permDone = make(chan struct{})
		// 设置回调接收流式响应
		obj.loop.SetCallback(func(resp loop.AIResponse) {
			logger.Debug("callback respose ID=%d", resp.MsgID)
			// 处理thinking内容（ACP v2：messageId 必填）
			if resp.ThinkingContext != "" {
				err = broadcastSessionUpdate(sessID, SessionUpdate{
					SessionID: sessID,
					Update: SessionUpdateUpdate{
						SessionUpdate: "agent_thought_chunk",
						MessageID:     msgID(resp.MsgID),
						Content: u.H{
							"type": "text",
							"text": resp.ThinkingContext,
						},
						AgentStatus: new(u.ValDefault(resp.AgentID, "")),
					},
				}, 0)
				if err != nil {
					logger.Warn("failed to broadcast session update: %v", err)
				}
			}

			// 处理内容delta（ACP v2：messageId 必填）
			if resp.Content != "" {
				err = broadcastSessionUpdate(sessID, SessionUpdate{
					SessionID: sessID,
					Update: SessionUpdateUpdate{
						SessionUpdate: "agent_message_chunk",
						MessageID:     msgID(resp.MsgID),
						Content: u.H{
							"type": "text",
							"text": resp.Content,
						},
						AgentStatus: new(u.ValDefault(resp.AgentID, "")),
					},
				}, 0)
				if err != nil {
					logger.Warn("failed to broadcast session update: %v", err)
				}
			}

			if resp.SummaryFlag {
				err = broadcastSessionUpdate(sessID, SessionUpdate{
					SessionID: sessID,
					Update: SessionUpdateUpdate{
						SessionUpdate: "alk.cxykevin.top/summary",
						Content: u.H{
							"type": "text",
							"text": resp.SummaryText,
						},
					},
				}, 0)
				if err != nil {
					logger.Warn("failed to broadcast session update: %v", err)
				}
				// compress 完成后重生成 AI 标题（用户已设置手动标题则跳过）。
				// 自动 compress（doAutoSummary）与 /compress 命令都汇聚于此分支。
				if resp.SummaryText != "" && sess.Title == "" {
					generateTitle(sess, sessID, true)
				}
			}

			// 最终工具调用状态（ACP v2 tool_call_update）：streaming=false 标记的条目
			// （审批后 ExecuteToolCalls 阶段 OnHook 写入，session.State=StateToolCalling）。
			// 任意回调到达时立即广播——不能依赖 session.State 判断（审批后空 AIResponse 与
			// 新一轮流式存在 State 竞态），按标记最可靠。
			if finalCtx, finalTyp := sess.TakeFinalToolCalling(); len(finalCtx) != 0 {
				toolStatus := "pending"
				if sess.ToolState == 1 {
					toolStatus = "completed"
				} else if sess.ToolState == 2 {
					toolStatus = "cancelled"
				}
				for id, val := range finalCtx {
					stx := strings.SplitN(id, "_", 4)
					s := ""
					if len(stx) == 4 {
						s = stx[3]
					}
					err = broadcastSessionUpdate(sessID, SessionUpdate{
						SessionID: sessID,
						Update: SessionUpdateUpdate{
							SessionUpdate: "tool_call_update",
							ToolCallID:    id,
							Kind:          ToolNameToTypeMap[finalTyp[id]],
							Status:        toolStatus,
							Title:         fmt.Sprintf("[Call %s]%s", finalTyp[id], s),
							Content:       val,
						},
					}, 0)
					if err != nil {
						logger.Warn("failed to broadcast session update: %v", err)
					}
				}
				sess.SetLatest(finalCtx, finalTyp)
			}

			// 流式增量工具调用预览（ACP v2 tool_call_update status=streaming，0.1s 限流）：
			// streaming=true 标记的条目（AI 正在生成工具调用，session.State=StateReciving 时
			// OnHook 写入）。限流推送完整快照，客户端按 toolCallId patch（覆盖）渲染；
			// 限流未通过时跳过广播但不清空，保留供下个 chunk。
			if sess.HasToolCalling() {
				now := time.Now()
				if obj.lastToolStreamTime.IsZero() || now.Sub(obj.lastToolStreamTime) >= toolStreamInterval {
					obj.lastToolStreamTime = now
					ctx, typ := sess.TakeStreamingToolCalling()
					for id, val := range ctx {
						stx := strings.SplitN(id, "_", 4)
						s := ""
						if len(stx) == 4 {
							s = stx[3]
						}
						err = broadcastSessionUpdate(sessID, SessionUpdate{
							SessionID: sessID,
							Update: SessionUpdateUpdate{
								SessionUpdate: "tool_call_update",
								ToolCallID:    id,
								Kind:          ToolNameToTypeMap[typ[id]],
								Status:        "streaming",
								Title:         fmt.Sprintf("[Call %s]%s", typ[id], s),
								Content:       val,
							},
						}, 0)
						if err != nil {
							logger.Warn("failed to broadcast session update: %v", err)
						}
					}
				}
			}

			// usage → ACP v2 usage_update（used/size）
			if resp.Usage != nil {
				if resp.Usage.TotalTokens != 0 || resp.Usage.PromptTokens != 0 || resp.Usage.CompletionTokens != 0 || resp.Usage.CachedTokens != 0 {
					err = broadcastSessionUpdate(sessID, SessionUpdate{
						SessionID: sessID,
						Update: SessionUpdateUpdate{
							SessionUpdate: "usage_update",
							Used:          uint64(resp.Usage.TotalTokens),
							Size:          currentTokenLimit(sess),
						},
					}, 0)
				}
			}

			// 停止分支：广播 idle state_update（stopReason）并结束本轮。
			// 错误也并入 idle（stopReason=refusal + 私有 error_msg）。
			if resp.StopReason != loop.StopReasonNone && resp.StopReason != loop.StopReasonPendingTool {
				var errMsg string
				if resp.Error != nil {
					errMsg = resp.Error.Error()
					logger.Warn("callback respose error in Session=%d, ID=%d error=%s", sess.ID, resp.MsgID, errMsg)
				}
				broadcastStateUpdate(sessID, "idle", ReasonMap[resp.StopReason], errMsg)
				// 自动标题：首次正常请求 end_turn 且无错误（原 SessionPrompt 触发逻辑迁到此处）
				if resp.StopReason == loop.StopReasonModel && resp.Error == nil &&
					sess.Title == "" && sess.AITitle == "" {
					generateTitle(sess, sessID, false)
				}
				return
			}

			// 待审批分支：requires_action + 发起 session/request_permission
			if resp.StopReason == loop.StopReasonPendingTool {
				logger.Info("request pending tool call to Session=%d, ID=%d", sess.ID, resp.MsgID)
				broadcastStateUpdate(sessID, "requires_action", "", "")
				pending := resp.PendingTool
				pendingMsgID := resp.MsgID
				go func() {
					approved, perr := requestPermission(obj, pending)
					if perr != nil {
						return // 会话释放
					}
					// 守卫：会话须仍在 WaitApprove 且当前待审批消息未被新的工具调用替换
					// （权限未决时第二个 prompt 到达会拒绝旧的工具并可能产生新的 WaitApprove）
					if obj.session.State != state.StateWaitApprove || obj.session.CurrentMessageID != pendingMsgID {
						return
					}
					if approved {
						broadcastStateUpdate(sessID, "running", "", "")
						if err := obj.loop.Approve(); err != nil {
							logger.Warn("approve error: %v", err)
						}
					} else {
						// reject_once == cancel：结束本轮，不执行、不踢 loop
						broadcastToolCallCancelled(sessID, pending)
						broadcastStateUpdate(sessID, "idle", "cancelled", "")
						if err := request.RejectToolCallsNoDeactivate(obj.session, "rejected by user", nil); err != nil {
							logger.Warn("reject error: %v", err)
						}
					}
				}()
			}

		})
		go obj.loop.Start(context.Background())

		obj.session = sess
		sess.ReferCount = 1
		sessions[sessID] = obj
		agentCallList[sessID] = make(map[string]func())
		return sess, nil
	}
	sessions[sessID].referCnt++
	return sessions[sessID].session, nil
}

// indexChatHistory 将会话聊天历史打包索引到 codebase（打 chathistory 标签）
func indexChatHistory(session *structs.Chats, cwd string) {
	if session == nil || session.DB == nil {
		return
	}

	var messages []structs.Messages
	if err := session.DB.Where("chat_id = ? AND type IN (0, 1)", session.ID).
		Order("id ASC").
		Find(&messages).Error; err != nil {
		logger.Warn("index chat history: query messages failed: %v", err)
		return
	}
	if len(messages) == 0 {
		logger.Debug("index chat history: no user/agent messages for session %d", session.ID)
		return
	}

	var buf strings.Builder
	for _, msg := range messages {
		role := "User"
		if msg.Type == 1 { // MessagesRoleAgent
			role = "Assistant"
		}
		_, _ = fmt.Fprintf(&buf, "[%s]\n%s\n\n", role, msg.Delta)
	}

	contentStr := buf.String()
	if contentStr == "" {
		return
	}

	// 后台静默索引
	go func() {
		filePath := fmt.Sprintf("chathistory/%d", session.ID)
		if same, _ := codebase.CheckContentHash(cwd, filePath, "", contentStr); !same {
			_ = codebase.AddToQueue(cwd, codebase.EmbedTask{
				FilePath:    filePath,
				FullContent: contentStr,
				EmbedText:   contentStr,
				Tags:        []string{"chathistory"},
			})
		}
	}()
}

// closePermDone 关闭会话的权限等待信号（防重复关闭；permDone 未初始化时为 no-op）
func (obj *sessionObj) closePermDone() {
	if obj == nil || obj.permDone == nil {
		return
	}
	obj.permDoneOnce.Do(func() { close(obj.permDone) })
}

// closeSession 关闭会话，引用计数递减，处理资源清理
func closeSession(sessionID string) {
	sessLock.Lock()
	defer sessLock.Unlock()
	if obj, ok := sessions[sessionID]; ok {
		obj.session.ReferCount--
		logger.Debug("close session ID=%s count=%d", sessionID, obj.session.ReferCount)
		if obj.session.ReferCount <= int32(0) {
			logger.Info("release session ID=%s", sessionID)
			obj.loop.Cancel()
			obj.closePermDone()
			indexChatHistory(obj.session, obj.cwd)
			closeDB(obj.cwd)
			delete(sessions, sessionID)
			delete(agentCallList, sessionID)
		}
	}
}

// cancelSessionRelease 取消会话的延迟释放定时器
// 在客户端重新连接时调用，防止会话被错误释放
func cancelSessionRelease(sessionID string) {
	sessLock.Lock()
	defer sessLock.Unlock()
	if obj, ok := sessions[sessionID]; ok && obj.releaseTimer != nil {
		obj.releaseTimer.Stop()
		obj.releaseTimer = nil
	}
}

// SessionGetBackgroundRequest 获取后台模式状态的请求
type SessionGetBackgroundRequest struct {
	SessionID string `json:"sessionId"`
}

// SessionGetBackgroundResponse 获取后台模式状态的响应
type SessionGetBackgroundResponse struct {
	Background bool `json:"background"`
}

// SessionGetBackground 获取会话的后台运行模式状态
func SessionGetBackground(req SessionGetBackgroundRequest, call func(string, any, *string) error, connID uint64) (SessionGetBackgroundResponse, error) {
	if req.SessionID == "" {
		return SessionGetBackgroundResponse{}, fmt.Errorf("sessionId is empty")
	}
	_, _, err := sessionID2Cwd(req.SessionID)
	if err != nil {
		return SessionGetBackgroundResponse{}, fmt.Errorf("invalid sessionId: %v", err)
	}

	sessLock.Lock()
	obj, ok := sessions[req.SessionID]
	sessLock.Unlock()
	if !ok {
		return SessionGetBackgroundResponse{}, fmt.Errorf("session not found")
	}
	return SessionGetBackgroundResponse{Background: obj.background}, nil
}

// SessionGetEffortRequest 获取推理强度设置的请求
type SessionGetEffortRequest struct {
	SessionID string `json:"sessionId"`
}

// SessionGetEffortResponse 获取推理强度设置的响应
type SessionGetEffortResponse struct {
	Effort string `json:"effort"`
}

// SessionGetEffort 获取会话当前的 reasoning effort 设置
func SessionGetEffort(req SessionGetEffortRequest, call func(string, any, *string) error, connID uint64) (SessionGetEffortResponse, error) {
	if req.SessionID == "" {
		return SessionGetEffortResponse{}, fmt.Errorf("sessionId is empty")
	}
	_, _, err := sessionID2Cwd(req.SessionID)
	if err != nil {
		return SessionGetEffortResponse{}, fmt.Errorf("invalid sessionId: %v", err)
	}

	sessLock.Lock()
	obj, ok := sessions[req.SessionID]
	sessLock.Unlock()
	if !ok {
		return SessionGetEffortResponse{}, fmt.Errorf("session not found")
	}
	effort := obj.session.ReasoningEffort
	if effort == "" {
		effort = "unset"
	}
	return SessionGetEffortResponse{Effort: effort}, nil
}

// scheduleSessionRelease 启动会话的延迟释放定时器
// 连接断开后根据配置的超时时间启动定时器，超时后若仍无连接则释放会话资源
// 当 background 开启时，仅在 loop 空闲或等待审批时才释放，活跃处理中会重新调度
func scheduleSessionRelease(sessionID string) {
	sessLock.Lock()
	obj, ok := sessions[sessionID]
	if !ok {
		sessLock.Unlock()
		return
	}

	// 停止已有的定时器
	if obj.releaseTimer != nil {
		obj.releaseTimer.Stop()
	}

	timeout := config.GlobalConfig.Server.SessionTimeout
	if timeout <= 0 {
		timeout = 60 // 默认 60 秒
	}

	var releaseFunc func()
	releaseFunc = func() {
		// 先检查连接状态（不持 sessLock，避免死锁）
		sessionConnLock.Lock()
		conns := sessionConnMap[sessionID]
		sessionConnLock.Unlock()
		if len(conns) > 0 {
			return // 已有新连接重连，不释放
		}

		// 获取 sessLock 后二次确认并清理
		sessLock.Lock()
		defer sessLock.Unlock()
		obj2, ok2 := sessions[sessionID]
		if !ok2 || obj2.releaseTimer == nil {
			return // 已被其他路径处理（如 SessionDelete）
		}
		obj2.releaseTimer = nil

		// 后台模式开启且 loop 正在活跃处理时，重新调度，不释放
		if obj2.background && obj2.session != nil {
			switch obj2.session.State {
			case state.StateRequesting, state.StateReciving, state.StateToolCalling:
				logger.Debug("session %s background mode: still processing (state=%d), reschedule release",
					sessionID, obj2.session.State)
				obj2.releaseTimer = time.AfterFunc(
					time.Duration(timeout)*time.Second, releaseFunc)
				return
			}
			// 其他状态（Idle、WaitApprove、Waiting、GeneratingPrompt）→ 执行释放
		}

		logger.Info("release session %s after %ds timeout", sessionID, timeout)
		obj2.loop.Cancel()
		obj2.closePermDone()
		indexChatHistory(obj2.session, obj2.cwd)
		closeDB(obj2.cwd)
		delete(sessions, sessionID)
		delete(agentCallList, sessionID)
	}

	obj.releaseTimer = time.AfterFunc(time.Duration(timeout)*time.Second, releaseFunc)
	sessLock.Unlock()
}

// SessionNew 创建新会话
func SessionNew(req SessionNewRequest, call func(string, any, *string) error, connID uint64) (SessionNewResponse, error) {
	if req.Cwd == "" {
		return SessionNewResponse{}, fmt.Errorf("cwd is empty")
	}
	req.Cwd = path.Clean(req.Cwd)
	info, err := os.Stat(req.Cwd)
	if err != nil || !info.IsDir() {
		return SessionNewResponse{}, fmt.Errorf("cwd not found or not a directory")
	}

	var id uint32
	sess, err := loadSession(req.Cwd, &id, false)
	if err != nil {
		return SessionNewResponse{}, fmt.Errorf("new session failed: %v", err)
	}

	sessionID := cwd2SessionID(req.Cwd, id)
	bindedSessionOnConnMu.Lock()
	bindedSessionOnConn[connID] = append(bindedSessionOnConn[connID], sessionID)
	bindedSessionOnConnMu.Unlock()
	// 注册连接的call函数用于后续广播
	registerConnCall(connID, sessionID, call)

	// 获取当前模型ID（新会话使用默认模型）
	currentModelID := sess.LastModelID
	if currentModelID == 0 {
		// 如果未设置，使用配置的默认模型
		cfg := config.GlobalConfig.Model.Models
		currentModelID = uint32(config.GlobalConfig.Model.DefaultModelID)
		if _, ok := cfg[int32(currentModelID)]; !ok && len(cfg) > 0 {
			currentModelIDTmp, _, ok := getMinValueByKey(cfg)
			if ok {
				currentModelID = uint32(currentModelIDTmp)
			}
		}
	}
	getDefaultModel()

	// 手动切换一遍模型，确保新会话的模型被正确初始化
	err = funcs.SelectModel(sess, int32(currentModelID))

	availableCommands := make([]any, len(commandMaps))
	idx := 0
	for i, v := range commandMaps {
		availableCommands[idx] = u.H{
			"name":        strings.TrimLeft(i, "/"),
			"description": v.Description,
			"input": u.H{
				"type": "text",
				"hint": v.Hint,
			},
		}
		idx++
	}
	slices.SortFunc(availableCommands, func(a, b any) int {
		nameA := a.(u.H)["name"].(string)
		nameB := b.(u.H)["name"].(string)
		return strings.Compare(nameA, nameB)
	})

	err = broadcastSessionUpdate(sessionID, u.H{
		"sessionId": sessionID,
		"update": u.H{
			"sessionUpdate":     "available_commands_update",
			"availableCommands": availableCommands,
		}}, 0)
	if err != nil {
		logger.Warn("failed to broadcast session update: %v", err)
	}

	return SessionNewResponse{
		SessionID:     sessionID,
		ConfigOptions: buildConfigOptions(currentModelID, sess.ReasoningEffort),
	}, nil
}

// SessionUpdateUpdate 更新会话的参数（ACP v2：messageId 必填、标准事件字段置于顶层）
type SessionUpdateUpdate struct {
	SessionUpdate     string         `json:"sessionUpdate"`
	MessageID         string         `json:"messageId,omitempty"`         // 消息 chunk 与整消息 upsert
	Content           any            `json:"content,omitempty"`           // 消息 chunk 的 ContentBlock、tool_call_update 的 content 数组
	ToolCallID        string         `json:"toolCallId,omitempty"`        // tool_call_update
	Title             string         `json:"title,omitempty"`             // tool_call_update
	Kind              string         `json:"kind,omitempty"`              // tool_call_update
	Status            string         `json:"status,omitempty"`            // tool_call_update
	State             string         `json:"state,omitempty"`             // state_update
	StopReason        string         `json:"stopReason,omitempty"`        // state_update idle
	ConfigOptions     []ConfigOption `json:"configOptions,omitempty"`     // config_option_update
	AvailableCommands any            `json:"availableCommands,omitempty"` // available_commands_update
	Used              uint64         `json:"used,omitempty"`              // usage_update
	Size              uint64         `json:"size,omitempty"`              // usage_update
	Plan              *PlanItems     `json:"plan,omitempty"`              // plan_update（嵌套在 plan 下）
	ExpandErrorMsg    string         `json:"alk.cxykevin.top/error_msg,omitempty"`
	AgentStatus       *string        `json:"alk.cxykevin.top/agent_status,omitempty"`
}

// PlanItems plan_update 内容（ACP v2：{plan: {type:"items", planId, entries}}）
type PlanItems struct {
	Type    string              `json:"type"` // "items"
	PlanID  string              `json:"planId"`
	Entries []structs.PlanEntry `json:"entries"`
}

// PermissionOption request_permission 选项（ACP v2）
type PermissionOption struct {
	OptionID string `json:"optionId"`
	Name     string `json:"name"`
	Kind     string `json:"kind"` // allow_once | reject_once
}

// ToolCallInfo request_permission subject 中的工具调用信息（ACP v2 ToolCallUpdate 子集）
type ToolCallInfo struct {
	ToolCallID string `json:"toolCallId"`
	Title      string `json:"title,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Status     string `json:"status,omitempty"`
	Content    any    `json:"content,omitempty"`
}

// PermissionSubject request_permission 的 subject（ACP v2：type + toolCall 嵌套）
type PermissionSubject struct {
	Type     string        `json:"type"` // "tool_call"
	ToolCall *ToolCallInfo `json:"toolCall,omitempty"`
}

// RequestPermissionParams session/request_permission 服务端→客户端请求参数（ACP v2）
type RequestPermissionParams struct {
	SessionID   string             `json:"sessionId"`
	Title       string             `json:"title"`
	Description string             `json:"description,omitempty"`
	Subject     *PermissionSubject `json:"subject,omitempty"`
	Options     []PermissionOption `json:"options"`
}

// RequestPermissionResult 客户端对 request_permission 的响应（ACP v2）
type RequestPermissionResult struct {
	Outcome  string `json:"outcome"` // selected | cancelled
	OptionID string `json:"optionId,omitempty"`
}

// SessionUpdate 更新会话的请求
type SessionUpdate struct {
	SessionID string `json:"sessionId"`
	Update    any    `json:"update"`
}

// SessionRequestPermission 批准工具调用
type SessionRequestPermission struct {
	SessionID string `json:"sessionId"`
	Update    any    `json:"update"`
}

// SessionResume 恢复会话（ACP v2：replayFrom={type:"start"} 回放历史；省略则不回放）
func SessionResume(req SessionResumeRequest, call func(string, any, *string) error, connID uint64) (SessionResumeResponse, error) {
	cwd, sid, err := sessionID2Cwd(req.SessionID)
	if err != nil {
		// 冷还原：服务器重启后内存注册表为空（sessionID2Cwd 报 session not found），
		// 回退到从 sessionId 字符串解析 cwd+id，由 loadSession 打开对应数据库并
		// QueryChat 验证会话真实存在，验证通过后注册进内存。解析失败则返回原始错误。
		var perr error
		cwd, sid, perr = parseSessionID(req.SessionID)
		if perr != nil {
			return SessionResumeResponse{}, err
		}
	}
	if cwd != path.Clean(req.Cwd) {
		return SessionResumeResponse{}, fmt.Errorf("cwd not match")
	}
	sess, err := loadSession(cwd, &sid, true)
	if err != nil {
		return SessionResumeResponse{}, err
	}
	bindedSessionOnConnMu.Lock()
	bindedSessionOnConn[connID] = append(bindedSessionOnConn[connID], req.SessionID)
	bindedSessionOnConnMu.Unlock()
	// 注册连接的call函数用于后续广播
	registerConnCall(connID, req.SessionID, call)
	// 冷/热加载时把当前任务计划推给新连接（客户端整体替换）。
	if sess.Task != "" {
		entries, perr := task.BuildPlanEntries(sess.Task)
		if perr != nil {
			logger.Warn("session resume: parse task failed: %v", perr)
		} else if len(entries) > 0 {
			err = broadcastSessionUpdate(req.SessionID, SessionUpdate{
				SessionID: req.SessionID,
				Update: SessionUpdateUpdate{
					SessionUpdate: "plan_update",
					Plan: &PlanItems{
						Type:    "items",
						PlanID:  fmt.Sprintf("plan_%d", sess.ID),
						Entries: entries,
					},
				},
			}, 0)
			if err != nil {
				logger.Warn("failed to broadcast plan on session resume: %v", err)
			}
		}
	}
	msgs, err := funcs.GetHistory(sess)
	previousToolJSON := ""
	prevMsgID := uint64(0)
	if req.ReplayFrom != nil && req.ReplayFrom.Type == "start" {
		logger.Info("replay session: %s", req.SessionID)
		for _, val := range msgs {
			switch val.Type {
			case structs.MessagesRoleUser:
				err := call("session/update", SessionUpdate{
					SessionID: req.SessionID,
					Update: SessionUpdateUpdate{
						SessionUpdate: "user_message",
						MessageID:     msgID(val.ID),
						Content:       []u.H{{"type": "text", "text": val.Delta}},
						AgentStatus:   new(u.ValDefault(val.AgentID, "")),
					},
				}, nil)
				if err != nil {
					return SessionResumeResponse{}, err
				}
			case structs.MessagesRoleAgent:
				if val.ThinkingDelta != "" {
					err := call("session/update", SessionUpdate{
						SessionID: req.SessionID,
						Update: SessionUpdateUpdate{
							SessionUpdate: "agent_thought",
							MessageID:     msgID(val.ID),
							Content:       []u.H{{"type": "text", "text": val.ThinkingDelta}},
							AgentStatus:   new(u.ValDefault(val.AgentID, "")),
						},
					}, nil)
					if err != nil {
						return SessionResumeResponse{}, err
					}
				}
				err := call("session/update", SessionUpdate{
					SessionID: req.SessionID,
					Update: SessionUpdateUpdate{
						SessionUpdate: "agent_message",
						MessageID:     msgID(val.ID),
						Content:       []u.H{{"type": "text", "text": val.Delta}},
						AgentStatus:   new(u.ValDefault(val.AgentID, "")),
					},
				}, nil)
				previousToolJSON = val.ToolCallingJSONString
				prevMsgID = val.ID
				if err != nil {
					return SessionResumeResponse{}, err
				}
				if val.TotalTokens != 0 || val.CachedTokens != 0 || val.PromptTokens != 0 || val.CompletionTokens != 0 {
					err = broadcastSessionUpdate(req.SessionID, SessionUpdate{
						SessionID: req.SessionID,
						Update: SessionUpdateUpdate{
							SessionUpdate: "usage_update",
							Used:          uint64(val.TotalTokens),
							Size:          currentTokenLimit(sess),
						},
					}, 0)
				}
			case structs.MessagesRoleTool:
				if previousToolJSON != "" {
					jsonObj := []u.H{}
					err := json.Unmarshal([]byte(strings.TrimSpace(previousToolJSON)), &jsonObj)
					if err != nil {
						logger.Warn("error when replay session marshal json: %v", err)
						continue
					}
					for _, obj := range jsonObj {
						toolName, ok := u.GetH[string](obj, "name")
						if !ok {
							logger.Warn("error when replay session without tool name: %v", err)
							continue
						}
						toolID, ok := u.GetH[string](obj, "id")
						if !ok {
							logger.Warn("error when replay session without tool id: %v", err)
							continue
						}
						err = call("session/update", SessionUpdate{
							SessionID: req.SessionID,
							Update: SessionUpdateUpdate{
								SessionUpdate: "tool_call_update",
								ToolCallID:    fmt.Sprintf("call_%d_%d_%s", sess.ID, prevMsgID, toolID),
								Title:         fmt.Sprintf("[Call %s]%s", toolName, toolID),
								Kind:          u.Default(ToolNameToTypeMap, toolName, "other"),
								Status:        "completed",
							},
						}, nil)
						if err != nil {
							return SessionResumeResponse{}, err
						}
					}
				}
			}
		}
		latestCtx, latestTyp := sess.SnapshotLatest()
		if len(latestCtx) != 0 {
			for id, val := range latestCtx {
				stx := strings.SplitN(id, "_", 4)
				s := ""
				if len(stx) == 4 {
					s = stx[3]
				}
				err = broadcastSessionUpdate(req.SessionID, SessionUpdate{
					SessionID: req.SessionID,
					Update: SessionUpdateUpdate{
						SessionUpdate: "tool_call_update",
						ToolCallID:    id,
						Kind:          ToolNameToTypeMap[latestTyp[id]],
						Status:        "pending",
						Title:         fmt.Sprintf("[Call %s]%s", latestTyp[id], s),
						Content:       val,
					},
				}, 0)
				if err != nil {
					logger.Warn("failed to broadcast session update: %v", err)
				}
			}
		}
	}
	modelID := sess.LastModelID

	availableCommands := make([]any, len(commandMaps))
	idx := 0
	for i, v := range commandMaps {
		availableCommands[idx] = u.H{
			"name":        strings.TrimLeft(i, "/"),
			"description": v.Description,
			"input": u.H{
				"type": "text",
				"hint": v.Hint,
			},
		}
		idx++
	}
	slices.SortFunc(availableCommands, func(a, b any) int {
		nameA := a.(u.H)["name"].(string)
		nameB := b.(u.H)["name"].(string)
		return strings.Compare(nameA, nameB)
	})

	err = broadcastSessionUpdate(req.SessionID, u.H{
		"sessionId": req.SessionID,
		"update": u.H{
			"sessionUpdate":     "available_commands_update",
			"availableCommands": availableCommands,
		}}, 0)
	if err != nil {
		logger.Warn("failed to broadcast session update: %v", err)
	}

	err = broadcastSessionUpdate(req.SessionID, SessionUpdate{
		SessionID: req.SessionID,
		Update: SessionUpdateUpdate{
			SessionUpdate: "config_option_update",
			ConfigOptions: buildConfigOptions(modelID, sess.ReasoningEffort),
		},
	}, 0)

	return SessionResumeResponse{
		ConfigOptions: buildConfigOptions(modelID, sess.ReasoningEffort),
	}, nil
}

// SessionCloseRequest 关闭会话的请求（ACP v2 session/close）
type SessionCloseRequest struct {
	SessionID string `json:"sessionId"`
}

// SessionClose 关闭会话：释放会话资源但不删除聊天记录。
// 无其他连接时才真正释放（scheduleSessionRelease 内部处理）；返回空对象 {}。
func SessionClose(req SessionCloseRequest, call func(string, any, *string) error, connID uint64) (u.H, error) {
	if req.SessionID == "" {
		return u.H{}, fmt.Errorf("sessionId is empty")
	}
	if _, _, err := sessionID2Cwd(req.SessionID); err != nil {
		return u.H{}, err
	}
	// 解绑当前连接并调度释放
	unregisterConnCall(connID, req.SessionID)
	bindedSessionOnConnMu.Lock()
	bindedSessionOnConn[connID] = slices.DeleteFunc(bindedSessionOnConn[connID], func(s string) bool { return s == req.SessionID })
	bindedSessionOnConnMu.Unlock()
	scheduleSessionRelease(req.SessionID)
	return u.H{}, nil
}

// msgID 生成 ACP messageId（基于 DB 消息 ID，直播与回放一致）
func msgID(id uint64) string {
	return "msg_" + strconv.FormatUint(id, 10)
}

// currentTokenLimit 返回当前模型上下文窗口大小（usage_update 的 size）
func currentTokenLimit(sess *structs.Chats) uint64 {
	if sess == nil {
		return 8192
	}
	modelID := sess.LastModelID
	if sess.CurrentAgentID != "" {
		if id := uint32(sess.CurrentAgentConfig.AgentModel); id != 0 {
			modelID = id
		}
	}
	if cfg, ok := config.GlobalConfig.Model.Models[int32(modelID)]; ok {
		return uint64(cfg.TokenLimit)
	}
	return 8192
}

// SessionSetConfigOptionRequest 设置配置选项的请求
type SessionSetConfigOptionRequest struct {
	SessionID string `json:"sessionId"`
	ConfigID  string `json:"configId"`
	Type      string `json:"type"` // "id"（select）| "boolean"（ACP v2 必填）
	Value     string `json:"value"`
}

// SessionSetConfigOptionResponse 设置配置选项的响应
type SessionSetConfigOptionResponse struct {
	ConfigOptions []ConfigOption `json:"configOptions"`
}

// SessionSetConfigOption 设置配置选项（如模型选择）
func SessionSetConfigOption(req SessionSetConfigOptionRequest, call func(string, any, *string) error, connID uint64) (SessionSetConfigOptionResponse, error) {
	if req.SessionID == "" {
		return SessionSetConfigOptionResponse{}, fmt.Errorf("sessionId is empty")
	}
	if req.ConfigID == "" {
		return SessionSetConfigOptionResponse{}, fmt.Errorf("configId is empty")
	}
	if req.Value == "" {
		return SessionSetConfigOptionResponse{}, fmt.Errorf("value is empty")
	}

	// 解析会话ID（用于验证格式）
	_, _, err := sessionID2Cwd(req.SessionID)
	if err != nil {
		return SessionSetConfigOptionResponse{}, fmt.Errorf("invalid sessionId: %v", err)
	}

	// 获取会话对象
	sessLock.Lock()
	sessObj, ok := sessions[req.SessionID]
	if !ok {
		sessLock.Unlock()
		return SessionSetConfigOptionResponse{}, fmt.Errorf("session not found")
	}
	sessLock.Unlock()

	sess := sessObj.session

	// 根据 configId 处理相应的配置更新
	switch req.ConfigID {
	case "model":
		logger.Info("set model %s in session=%s", req.Value, req.SessionID)
		// 解析模型值格式："index/modelId"
		parts := strings.SplitN(req.Value, "/", 2)
		if len(parts) != 2 {
			return SessionSetConfigOptionResponse{}, fmt.Errorf("invalid model value format")
		}

		modelIdx, err := strconv.ParseInt(parts[0], 10, 32)
		if err != nil {
			return SessionSetConfigOptionResponse{}, fmt.Errorf("invalid model index: %v", err)
		}

		// 验证模型是否存在
		cfg := config.GlobalConfig.Model.Models
		if _, ok := cfg[int32(modelIdx)]; !ok {
			return SessionSetConfigOptionResponse{}, fmt.Errorf("model not found: %s", req.Value)
		}

		// 使用现有的 SelectModel 函数来更新模型
		err = funcs.SelectModel(sess, int32(modelIdx))
		if err != nil {
			return SessionSetConfigOptionResponse{}, fmt.Errorf("failed to set model: %v", err)
		}

	case "thought_level":
		logger.Info("set thought level %s in session=%s", req.Value, req.SessionID)
		switch req.Value {
		case "unset", "low", "medium", "high", "max", "xhigh":
			sess.ReasoningEffort = req.Value
			if err := sess.DB.Model(&structs.Chats{}).Where("id = ?", sess.ID).
				Update("reasoning_effort", req.Value).Error; err != nil {
				return SessionSetConfigOptionResponse{}, fmt.Errorf("failed to set thought level: %v", err)
			}
		default:
			return SessionSetConfigOptionResponse{}, fmt.Errorf("invalid thought level: %s", req.Value)
		}

	default:
		return SessionSetConfigOptionResponse{}, fmt.Errorf("unknown config option: %s", req.ConfigID)
	}

	// 生成更新后的配置选项列表
	configOptions := buildConfigOptions(sess.LastModelID, sess.ReasoningEffort)

	// 广播配置更新到所有连接到该会话的客户端
	err = broadcastSessionUpdate(req.SessionID, SessionUpdate{
		SessionID: req.SessionID,
		Update: SessionUpdateUpdate{
			SessionUpdate: "config_option_update",
			ConfigOptions: configOptions,
		},
	}, 0) // 不排除任何连接，所有客户端都需要知道配置更新

	if err != nil {
		logger.Warn("failed to broadcast session update: %v", err)
	}

	return SessionSetConfigOptionResponse{
		ConfigOptions: configOptions,
	}, nil
}

// SessionDeleteRequest 删除会话的请求
type SessionDeleteRequest struct {
	SessionID string `json:"sessionId"`
}

// SessionListRequest 列出会话的请求
type SessionListRequest struct {
	Cwd    string `json:"cwd"`
	Cursor string `json:"cursor,omitempty"`
}

// SubAgentListRequest 列出 subagent 的请求
type SubAgentListRequest struct {
	SessionID string `json:"sessionId"`
}

// SubAgentListResponse 列出 subagent 的响应
type SubAgentListResponse struct {
	Subagents []AgentsInfo    `json:"agents"`
	Tags      []AgentTagsInfo `json:"tags"`
}

// AgentsInfo subagent 信息
type AgentsInfo struct {
	Name string `json:"name"`
	Tag  string `json:"tag"`
	Path string `json:"path"`
}

// AgentTagsInfo subagent tag 信息
type AgentTagsInfo struct {
	Name        string `json:"name"`
	ID          string `json:"id"`
	ModelID     int32  `json:"modelId"`
	Color       string `json:"color"`
	AutoApprove string `json:"autoApproveExpr"`
	AutoReject  string `json:"autoRejectExpr"`
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
	ShortPrompt string `json:"shortPrompt"`
}

// SessionInfo 会话信息
type SessionInfo struct {
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Title     string `json:"title"`
}

// SessionListResponse 列出会话的响应
type SessionListResponse struct {
	Sessions []SessionInfo `json:"sessions"`
}

// SessionList 列出工作目录中的所有会话
func SessionList(req SessionListRequest, call func(string, any, *string) error, connID uint64) (SessionListResponse, error) {
	req.Cwd = path.Clean(req.Cwd)
	info, err := os.Stat(req.Cwd)
	if err != nil || !info.IsDir() {
		return SessionListResponse{}, fmt.Errorf("cwd not found or not a directory")
	}
	info, err = os.Stat(path.Join(req.Cwd, ".alkaid0"))
	if err != nil || !info.IsDir() {
		return SessionListResponse{}, fmt.Errorf("cwd not inited")
	}

	db, err := loadDB(req.Cwd)
	if err != nil {
		return SessionListResponse{}, err
	}
	// 平衡引用计数
	defer closeDB(req.Cwd)

	chats, err := funcs.GetChats(db)
	if err != nil {
		return SessionListResponse{}, err
	}

	sess := make([]SessionInfo, len(chats))
	for idx, chat := range chats {
		// 展示回退链：用户设置的标题 → AI 生成的标题 → Untitled(N)
		tit := chat.Title
		if tit == "" {
			tit = chat.AITitle
		}
		if tit == "" {
			tit = fmt.Sprintf("Untitled(%d)", chat.ID)
		}
		sess[idx] = SessionInfo{
			SessionID: cwd2SessionID(req.Cwd, chat.ID),
			Cwd:       req.Cwd,
			Title:     tit,
		}
	}

	return SessionListResponse{
		Sessions: sess,
	}, nil
}

// SessionDelete 删除会话（ACP session/delete）
// 根据 ACP 规范：
//   - 删除的会话不再出现在 session/list 结果中
//   - 删除已删除或不存在的会话应静默成功
//   - 返回空对象 {}
func SessionDelete(req SessionDeleteRequest, call func(string, any, *string) error, connID uint64) (u.H, error) {
	if req.SessionID == "" {
		return u.H{}, fmt.Errorf("sessionId is empty")
	}

	cwd, id, err := sessionID2Cwd(req.SessionID)
	if err != nil {
		// 无效的会话ID，按规范静默成功
		return u.H{}, nil
	}

	// 如果会话当前活跃，先关闭它
	sessLock.Lock()
	if obj, ok := sessions[req.SessionID]; ok && obj.session.ID == id {
		// 取消任何待处理的延迟释放定时器
		if obj.releaseTimer != nil {
			obj.releaseTimer.Stop()
			obj.releaseTimer = nil
		}
		sessLock.Unlock()
		closeSession(req.SessionID)
	} else {
		sessLock.Unlock()
	}

	db, err := loadDB(cwd)
	if err != nil {
		return u.H{}, nil
	}
	defer closeDB(cwd)

	// 删除聊天记录（无论是否存在，按规范静默成功）
	_ = funcs.DeleteChat(db, &structs.Chats{ID: id})

	return u.H{}, nil
}

// SubAgentList 列出工作目录中所有 subagent
func SubAgentList(req SubAgentListRequest, call func(string, any, *string) error, connID uint64) (SubAgentListResponse, error) {
	if req.SessionID == "" {
		return SubAgentListResponse{}, fmt.Errorf("sessionId is empty")
	}

	// 解析会话ID
	_, _, err := sessionID2Cwd(req.SessionID)
	if err != nil {
		return SubAgentListResponse{}, fmt.Errorf("invalid sessionId: %v", err)
	}

	// 获取会话对象
	sessLock.Lock()
	sessObj, ok := sessions[req.SessionID]
	if !ok {
		sessLock.Unlock()
		return SubAgentListResponse{}, fmt.Errorf("session not found")
	}
	sessLock.Unlock()

	sess := sessObj.session

	db := sess.DB

	if db == nil {
		return SubAgentListResponse{}, fmt.Errorf("session not found")
	}

	agents, err := funcs.GetAgents(sess)
	if err != nil {
		return SubAgentListResponse{}, fmt.Errorf("failed to get agents: %v", err)
	}
	tags := funcs.GetAgentTags()

	return SubAgentListResponse{
		Subagents: u.MapFilter(agents, func(v structs.SubAgents) (AgentsInfo, bool) {
			return AgentsInfo{
				Name: v.ID,
				Tag:  v.AgentID,
				Path: v.BindPath,
			}, !v.Deleted
		}),
		Tags: u.Map(tags, func(v funcs.AgentTagsList) AgentTagsInfo {
			return AgentTagsInfo{
				Name:    v.Agent.AgentName,
				ID:      v.ID,
				ModelID: v.Agent.AgentModel,
				Color: fmt.Sprintf("#%02X%02X%02X",
					v.Agent.Color.Red,
					v.Agent.Color.Green,
					v.Agent.Color.Blue,
				),
				AutoApprove: v.Agent.AutoApprove,
				AutoReject:  v.Agent.AutoReject,
				Description: v.Agent.AgentDescription,
				Prompt:      v.Agent.AgentPrompt,
				ShortPrompt: v.Agent.AgentShortDescription,
			}
		}),
	}, nil
}
