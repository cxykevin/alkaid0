package actions

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/storage"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/ui/funcs"
	"github.com/cxykevin/alkaid0/ui/loop"
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

// ConfigOption 配置选项
type ConfigOption struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	Description  string              `json:"description,omitempty"`
	Category     string              `json:"category,omitempty"`
	Type         string              `json:"type"`
	CurrentValue string              `json:"currentValue"`
	Options      []ConfigOptionValue `json:"options"`
}

// SessionNewResponse 创建新会话的响应
type SessionNewResponse struct {
	SessionID     string         `json:"sessionId"`
	Models        ModelConfig    `json:"models"`
	ConfigOptions []ConfigOption `json:"configOptions"`
}

// SessionLoadRequest 加载会话的请求
type SessionLoadRequest struct {
	Cwd       string `json:"cwd"`
	SessionID string `json:"sessionId"`
}

// AvailableModel 可用的模型
type AvailableModel struct {
	ModelID string `json:"modelId"`
	Name    string `json:"name"`
	RealID  int32  `json:"alk.cxykevin.top/model_real_id"`
}

// ModelConfig 模型配置
type ModelConfig struct {
	CurrentModelID  string           `json:"currentModelId"`
	AvailableModels []AvailableModel `json:"availableModels"`
}

// SessionLoadResponse 加载会话的响应
type SessionLoadResponse struct {
	Models        ModelConfig    `json:"models"`
	ConfigOptions []ConfigOption `json:"configOptions"`
}

// StopMsg 停止会话的消息
type StopMsg struct {
	StopReason string
	ErrorMsg   *string
}

// sessionObj 会话对象，包含会话的核心信息和生命周期管理
type sessionObj struct {
	cwd          string
	id           uint32
	session      *structs.Chats
	loop         *loop.Object
	ctx          context.Context
	referCnt     int
	waitStopChan chan (*chan StopMsg)
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

// 连接ID到call函数的映射，用于发送跨conn通知
var connCallMap = map[uint64]func(string, any) error{}
var connCallLock = &sync.Mutex{}

// 会话ID到连接ID列表的反向映射，用于广播更新
var sessionConnMap = map[string][]uint64{}
var sessionConnLock = &sync.Mutex{}

// 进行中的prompt请求的上下文管理，用于支持cancellation
// 结构：session ID -> {cancel context func, isActive flag}
type promptCtx struct {
	cancel   context.CancelFunc
	isActive bool
}

var activePrompts = map[string]*promptCtx{}
var activePromptsLock = &sync.Mutex{}

// cwd2SessionID 将工作目录和会话ID转换为规范化的会话ID格式
func cwd2SessionID(cwd string, id uint32) string {
	return fmt.Sprintf("sess_%d:%s", id, cwd)
}

func buildModelList() []AvailableModel {
	cfg := config.GlobalConfig.Model.Models
	models := make([]AvailableModel, len(cfg))
	idx := 0
	for i, model := range cfg {
		models[idx] = AvailableModel{
			ModelID: fmt.Sprintf("%d/%s", i, model.ModelID),
			Name:    model.ModelName,
			RealID:  i,
		}
		idx++
	}
	slices.SortFunc(models, func(a, b AvailableModel) int {
		return int(a.RealID - b.RealID)
	})
	return models
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

// buildConfigOptions 生成配置选项列表
func buildConfigOptions(currentModelID uint32) []ConfigOption {
	cfg := config.GlobalConfig.Model.Models
	options := make([]ConfigOptionValue, 0, len(cfg))

	for i, model := range cfg {
		options = append(options, ConfigOptionValue{
			Value:       fmt.Sprintf("%d/%s", i, model.ModelID),
			Name:        model.ModelName,
			Description: "",
		})
	}

	// 确保当前模型值格式正确
	currentValue := fmt.Sprintf("%d/%s", currentModelID, u.Default(cfg, int32(currentModelID), cfgStructs.ModelConfig{
		ModelID: fmt.Sprintf("UnknownModel(%d)", currentModelID),
	}).ModelID)

	return []ConfigOption{
		{
			ID:           "model",
			Name:         "Model",
			Category:     "model",
			Type:         "select",
			CurrentValue: currentValue,
			Options:      options,
		},
	}
}

// sessionID2Cwd 解析会话ID，返回工作目录和会话ID
func sessionID2Cwd(sessionID string) (string, uint32, error) {
	if len(sessionID) < 6 {
		return "", 0, fmt.Errorf("session id too short")
	}
	s := strings.SplitN(sessionID, ":", 2)
	if len(s) != 2 {
		return "", 0, fmt.Errorf("invalid session id")
	}
	num, err := strconv.ParseUint(s[0][5:], 10, 32)
	if err != nil {
		return "", 0, err
	}
	return s[1], uint32(num), nil
}

// registerConnCall 注册连接的call函数和会话绑定
func registerConnCall(connID uint64, sessionID string, callFunc func(string, any) error) {
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
	callFuncs := make(map[uint64]func(string, any) error)
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
		err := fn("session/update", update)
		if err != nil {
			lastErr = err
		}
	}
	return lastErr
}

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
			cwd:      cwd,
			id:       0,
			ctx:      context.Background(),
			referCnt: 1,
		}

		db, err := loadDB(cwd)
		if err != nil {
			return nil, err
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
		sess, err := funcs.InitChat(db, &chTemp)
		if err != nil {
			closeDB(obj.cwd)
			return nil, err
		}
		sess.Root = cwd

		obj.loop = loop.New(sess)
		obj.waitStopChan = make(chan *chan StopMsg, 20)
		// 设置回调接收流式响应
		obj.loop.SetCallback(func(resp loop.AIResponse) {
			logger.Debug("callback respose ID=%d", resp.MsgID)
			// 处理thinking内容
			if resp.ThinkingContext != "" {
				err = broadcastSessionUpdate(sessID, SessionUpdate{
					SessionID: sessID,
					Update: SessionUpdateUpdate{
						SessionUpdate: "agent_thought_chunk",
						Content: u.H{
							"type": "text",
							"text": resp.ThinkingContext,
						},
					},
				}, 0)
				if err != nil {
					logger.Warn("failed to broadcast session update: %v", err)
				}
			}

			// 处理内容delta
			if resp.Content != "" {
				err = broadcastSessionUpdate(sessID, SessionUpdate{
					SessionID: sessID,
					Update: SessionUpdateUpdate{
						SessionUpdate: "agent_message_chunk",
						Content: u.H{
							"type": "text",
							"text": resp.Content,
						},
					},
				}, 0)
				if err != nil {
					logger.Warn("failed to broadcast session update: %v", err)
				}
			}

			toolStatus := "pending"
			if sess.ToolState == 1 {
				toolStatus = "completed"
				sess.LatestToolCallingContext = make(map[string]any)
				sess.LatestToolCallingType = make(map[string]string)
			}

			if sess.LatestToolCallingContext == nil {
				sess.LatestToolCallingContext = make(map[string]any)
				sess.LatestToolCallingType = make(map[string]string)
			}

			if len(sess.ToolCallingContext) != 0 {
				for id, val := range sess.ToolCallingContext {
					stx := strings.SplitN(id, "_", 4)
					s := ""
					if len(stx) == 4 {
						s = stx[3]
					}
					err = broadcastSessionUpdate(sessID, SessionUpdate{
						SessionID: sessID,
						Update: SessionUpdateUpdate{
							SessionUpdate: "tool_call",
							ToolCallID:    id,
							Kind:          ToolNameToTypeMap[sess.ToolCallingType[id]],
							Status:        toolStatus,
							Title:         fmt.Sprintf("[Call %s]%s", sess.ToolCallingType[id], s),
							Content:       val,
						},
					}, 0)
					if err != nil {
						logger.Warn("failed to broadcast session update: %v", err)
					}
				}
				maps.Copy(sess.LatestToolCallingContext, sess.ToolCallingContext)
				maps.Copy(sess.LatestToolCallingType, sess.ToolCallingType)
				sess.ToolCallingContext = make(map[string]any)
				sess.ToolCallingType = make(map[string]string)
			}

			// 处理错误
			if resp.Error != nil {
				logger.Warn("callback respose error in Session=%d, ID=%d error=%s", sess.ID, resp.MsgID, resp.Error.Error())
				for {
					select {
					case i := <-obj.waitStopChan:
						*i <- StopMsg{StopReason: ReasonMap[resp.StopReason], ErrorMsg: new(resp.Error.Error())}
					default:
						return
					}
				}
			}

			if resp.StopReason != loop.StopReasonNone && resp.StopReason != loop.StopReasonPendingTool {
				for {
					select {
					case i := <-obj.waitStopChan:
						*i <- StopMsg{StopReason: ReasonMap[resp.StopReason]}
					default:
						return
					}
				}
			} else if resp.StopReason == loop.StopReasonPendingTool {
				logger.Info("request pending tool call to Session=%d, ID=%d", sess.ID, resp.MsgID)
				// for _, tool := range *resp.PendingTool {
				// 	_ = broadcastSessionUpdate(sessID, SessionUpdate{
				// 		SessionID: sessID,
				// 		Update: SessionUpdateUpdate{
				// 			SessionUpdate: "tool_call",
				// 			ToolCallID:    fmt.Sprintf("call_%d_%d_%s", sess.ID, resp.MsgID, tool.ID),
				// 			Title:         fmt.Sprintf("[Call %s]%s", tool.Name, tool.ID),
				// 			Kind:          u.Default(ToolNameToType, tool.Name, "other"),
				// 			Status:        "pending",
				// 		},
				// 	}, 0)
				// }

				// TODO: 使用 request permission API
			}

		})
		go obj.loop.Start(context.Background())

		obj.session = sess
		sess.ReferCount = 1
		sessions[sessID] = obj
		return sess, nil
	}
	sessions[sessID].referCnt++
	return sessions[sessID].session, nil
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
			closeDB(obj.cwd)
			delete(sessions, sessionID)
		}
	}
}

// SessionNew 创建新会话
func SessionNew(req SessionNewRequest, call func(string, any) error, connID uint64) (SessionNewResponse, error) {
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
	bindedSessionOnConn[connID] = append(u.Default(bindedSessionOnConn, connID, []string{}), sessionID)
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
	modelRealID := "Unconfigured"
	modelRealCfg, ok := config.GlobalConfig.Model.Models[int32(currentModelID)]
	if ok {
		modelRealID = modelRealCfg.ModelID
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
		SessionID: sessionID,
		Models: ModelConfig{
			AvailableModels: buildModelList(),
			CurrentModelID:  fmt.Sprintf("%d/%s", currentModelID, modelRealID),
		},
		ConfigOptions: buildConfigOptions(currentModelID),
	}, nil
}

// SessionUpdateUpdate 更新会话的参数
type SessionUpdateUpdate struct {
	SessionUpdate  string `json:"sessionUpdate"`
	Content        any    `json:"content,omitempty"`
	ToolCallID     string `json:"toolCallId,omitempty"`
	Title          string `json:"title,omitempty"`
	Kind           string `json:"kind,omitempty"`
	Status         string `json:"status,omitempty"`
	ExpandErrorMsg string `json:"alk.cxykevin.top/error_msg,omitempty"`
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

// SessionLoad 加载会话并发送历史回放
func SessionLoad(req SessionLoadRequest, call func(string, any) error, connID uint64) (SessionLoadResponse, error) {
	req.Cwd = path.Clean(req.Cwd)
	cwd, sid, err := sessionID2Cwd(req.SessionID)
	if err != nil {
		return SessionLoadResponse{}, err
	}
	if cwd != req.Cwd {
		return SessionLoadResponse{}, fmt.Errorf("cwd not match")
	}
	sess, err := loadSession(cwd, &sid, true)
	if err != nil {
		return SessionLoadResponse{}, err
	}
	bindedSessionOnConn[connID] = append(u.Default(bindedSessionOnConn, connID, []string{}), req.SessionID)
	// 注册连接的call函数用于后续广播
	registerConnCall(connID, req.SessionID, call)
	msgs, err := funcs.GetHistory(sess)
	previousToolJSON := ""
	prevMsgID := uint64(0)
	logger.Info("replay session: %s", req.SessionID)
	for _, val := range msgs {
		switch val.Type {
		case structs.MessagesRoleUser:
			err := call("session/update", SessionUpdate{
				SessionID: req.SessionID,
				Update: SessionUpdateUpdate{
					SessionUpdate: "user_message_chunk",
					Content: u.H{
						"type": "text",
						"text": val.Delta,
					},
				},
			})
			if err != nil {
				return SessionLoadResponse{}, err
			}
		case structs.MessagesRoleAgent:
			if val.ThinkingDelta != "" {
				err := call("session/update", SessionUpdate{
					SessionID: req.SessionID,
					Update: SessionUpdateUpdate{
						SessionUpdate: "agent_thought_chunk",
						Content: []u.H{{
							"type": "text",
							"text": val.ThinkingDelta,
						}},
					},
				})
				if err != nil {
					return SessionLoadResponse{}, err
				}
			}
			err := call("session/update", SessionUpdate{
				SessionID: req.SessionID,
				Update: SessionUpdateUpdate{
					SessionUpdate: "agent_message_chunk",
					Content: u.H{
						"type": "text",
						"text": val.Delta,
					},
				},
			})
			previousToolJSON = val.ToolCallingJSONString
			prevMsgID = val.ID
			if err != nil {
				return SessionLoadResponse{}, err
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
							SessionUpdate: "tool_call",
							ToolCallID:    fmt.Sprintf("call_%d_%d_%s", sess.ID, prevMsgID, toolID),
							Title:         fmt.Sprintf("[Call %s]%s", toolName, toolID),
							Kind:          u.Default(ToolNameToTypeMap, toolName, "other"),
							Status:        "completed",
						},
					})
					if err != nil {
						return SessionLoadResponse{}, err
					}
				}
			}
		}
	}

	if sess.LatestToolCallingContext != nil && sess.LatestToolCallingType != nil {
		for id, val := range sess.LatestToolCallingContext {
			stx := strings.SplitN(id, "_", 4)
			s := ""
			if len(stx) == 4 {
				s = stx[3]
			}
			err = broadcastSessionUpdate(req.SessionID, SessionUpdate{
				SessionID: req.SessionID,
				Update: SessionUpdateUpdate{
					SessionUpdate: "tool_call",
					ToolCallID:    id,
					Kind:          ToolNameToTypeMap[sess.LatestToolCallingType[id]],
					Status:        "pending",
					Title:         fmt.Sprintf("[Call %s]%s", sess.LatestToolCallingType[id], s),
					Content:       val,
				},
			}, 0)
			if err != nil {
				logger.Warn("failed to broadcast session update: %v", err)
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
			Content:       buildConfigOptions(uint32(modelID)),
		},
	}, 0)

	return SessionLoadResponse{
		Models: ModelConfig{
			AvailableModels: buildModelList(),
			CurrentModelID: fmt.Sprintf("%d/%s", modelID, u.Default(config.GlobalConfig.Model.Models, int32(modelID), cfgStructs.ModelConfig{
				ModelID: fmt.Sprintf("UnknownModel(%d)", modelID),
			}).ModelID),
		},
		ConfigOptions: buildConfigOptions(modelID),
	}, nil
}

// SessionSetConfigOptionRequest 设置配置选项的请求
type SessionSetConfigOptionRequest struct {
	SessionID string `json:"sessionId"`
	ConfigID  string `json:"configId"`
	Value     string `json:"value"`
}

// SessionSetConfigOptionResponse 设置配置选项的响应
type SessionSetConfigOptionResponse struct {
	ConfigOptions []ConfigOption `json:"configOptions"`
}

// SessionSetConfigOption 设置配置选项（如模型选择）
func SessionSetConfigOption(req SessionSetConfigOptionRequest, call func(string, any) error, connID uint64) (SessionSetConfigOptionResponse, error) {
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

	default:
		return SessionSetConfigOptionResponse{}, fmt.Errorf("unknown config option: %s", req.ConfigID)
	}

	// 生成更新后的配置选项列表
	configOptions := buildConfigOptions(sess.LastModelID)

	// 广播配置更新到所有连接到该会话的客户端
	err = broadcastSessionUpdate(req.SessionID, SessionUpdate{
		SessionID: req.SessionID,
		Update: SessionUpdateUpdate{
			SessionUpdate: "config_option_update",
			Content:       configOptions,
		},
	}, 0) // 不排除任何连接，所有客户端都需要知道配置更新

	if err != nil {
		logger.Warn("failed to broadcast session update: %v", err)
	}

	return SessionSetConfigOptionResponse{
		ConfigOptions: configOptions,
	}, nil
}

// SessionSetModelRequest 设置会话模型的请求（向后兼容的老接口）
type SessionSetModelRequest struct {
	SessionID string `json:"sessionId"`
	ModelID   string `json:"modelId"`
}

// SessionSetModelResponse 设置会话模型的响应
type SessionSetModelResponse struct {
	CurrentModelID  string           `json:"currentModelId"`
	AvailableModels []AvailableModel `json:"availableModels"`
}

// SessionSetModel 设置会话模型
// 这个接口提供与 session/set_config_option 相同的功能
func SessionSetModel(req SessionSetModelRequest, call func(string, any) error, connID uint64) (SessionSetModelResponse, error) {
	if req.SessionID == "" {
		return SessionSetModelResponse{}, fmt.Errorf("sessionId is empty")
	}

	// 解析会话ID
	_, _, err := sessionID2Cwd(req.SessionID)
	if err != nil {
		return SessionSetModelResponse{}, fmt.Errorf("invalid sessionId: %v", err)
	}

	// 获取会话对象
	sessLock.Lock()
	sessObj, ok := sessions[req.SessionID]
	if !ok {
		sessLock.Unlock()
		return SessionSetModelResponse{}, fmt.Errorf("session not found")
	}
	sessLock.Unlock()

	sess := sessObj.session

	parts := strings.SplitN(req.ModelID, "/", 2)
	if len(parts) != 2 {
		return SessionSetModelResponse{}, fmt.Errorf("invalid model value format")
	}

	modelIdx, err := strconv.ParseInt(parts[0], 10, 32)
	if err != nil {
		return SessionSetModelResponse{}, fmt.Errorf("invalid model index: %v", err)
	}

	// 验证模型是否存在
	cfg := config.GlobalConfig.Model.Models
	if _, ok := cfg[int32(modelIdx)]; !ok {
		return SessionSetModelResponse{}, fmt.Errorf("model not found: %d", modelIdx)
	}

	// 使用 SelectModel 函数更新模型
	err = funcs.SelectModel(sess, int32(modelIdx))
	if err != nil {
		return SessionSetModelResponse{}, fmt.Errorf("failed to set model: %v", err)
	}

	// 获取更新后的模型信息
	modelName := u.Default(cfg, int32(modelIdx), cfgStructs.ModelConfig{
		ModelID: fmt.Sprintf("UnknownModel(%s)", req.ModelID),
	}).ModelID
	currentModelID := fmt.Sprintf("%d/%s", modelIdx, modelName)

	// 广播配置更新到所有连接到该会话的客户端
	err = broadcastSessionUpdate(req.SessionID, SessionUpdate{
		SessionID: req.SessionID,
		Update: SessionUpdateUpdate{
			SessionUpdate: "config_option_update",
			Content:       buildConfigOptions(uint32(modelIdx)),
		},
	}, 0)

	if err != nil {
		// 仅记录日志，不返回错误
	}

	return SessionSetModelResponse{
		CurrentModelID:  currentModelID,
		AvailableModels: buildModelList(),
	}, nil
}

// SessionListRequest 列出会话的请求
type SessionListRequest struct {
	Cwd    string `json:"cwd"`
	Cursor string `json:"cursor,omitempty"`
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
func SessionList(req SessionListRequest, call func(string, any) error, connID uint64) (SessionListResponse, error) {
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
		tit := chat.Title
		if tit == "" {
			tit = fmt.Sprintf("Untitled(%d)", chat.ID)
		}
		sess[idx] = SessionInfo{
			SessionID: cwd2SessionID(req.Cwd, chat.ID),
			Cwd:       req.Cwd,
			Title:     chat.Title,
		}
	}

	return SessionListResponse{
		Sessions: sess,
	}, nil
}
