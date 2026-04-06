package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"

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

// SessionNewResponse 创建新会话的响应
type SessionNewResponse struct {
	SessionID string `json:"sessionId"`
}

// SessionLoadRequest 加载会话的请求
type SessionLoadRequest struct {
	Cwd       string `json:"cwd"`
	SessionID string `json:"sessionId"`
}

// SessionLoadResponse 加载会话的响应
type SessionLoadResponse struct {
}

// sessionObj 会话对象，包含会话的核心信息和生命周期管理
type sessionObj struct {
	cwd      string
	id       uint32
	session  *structs.Chats
	loop     *loop.Object
	ctx      context.Context
	referCnt int
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

// cwd2SessionID 将工作目录和会话ID转换为规范化的会话ID格式
func cwd2SessionID(cwd string, id uint32) string {
	return fmt.Sprintf("sess_%d:%s", id, cwd)
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
	dbLock.Lock()
	defer dbLock.Unlock()
	if obj, ok := dbs[path]; ok {
		obj.referCnt--
		if obj.referCnt == 0 {
			delete(dbs, path)
			db, _ := obj.db.DB()
			db.Close()
		}
	}
}

// loadSession 加载或创建会话，支持引用计数生命周期管理
// knowID为true时表示使用已知的会话ID，否则创建新会话
func loadSession(cwd string, id *uint32, knowID bool) (*structs.Chats, error) {
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
		go obj.loop.Start(context.Background())

		obj.session = sess
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
		if obj.session.ReferCount == 0 {
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
	_, err = loadSession(req.Cwd, &id, false)
	if err != nil {
		return SessionNewResponse{}, fmt.Errorf("new session failed: %v", err)
	}

	bindedSessionOnConn[connID] = append(u.Default(bindedSessionOnConn, connID, []string{}), cwd2SessionID(req.Cwd, id))

	return SessionNewResponse{
		SessionID: cwd2SessionID(req.Cwd, id),
	}, nil
}

// SessionUpdateUpdate 更新会话的参数
type SessionUpdateUpdate struct {
	SessionUpdate string `json:"sessionUpdate"`
	Content       any    `json:"content,omitempty"`
	ToolCallID    string `json:"toolCallId,omitempty"`
	Title         string `json:"title,omitempty"`
	Kind          string `json:"kind,omitempty"`
	Status        string `json:"status,omitempty"`
}

// SessionUpdate 更新会话的请求
type SessionUpdate struct {
	SessionID string              `json:"sessionId"`
	Update    SessionUpdateUpdate `json:"update"`
}

// ToolNameToType 工具名称到类型的映射，用于规范化工具调用类型
var ToolNameToType = map[string]string{
	"agent":            "other",
	"scope":            "other",
	"activate_agent":   "other",
	"deactivate_agent": "other",
	"edit":             "edit",
	"trace":            "read",
	"run":              "execute",
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
	msgs, err := funcs.GetHistory(sess)
	previousToolJSON := ""
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
						SessionUpdate: "tool_call",
						ToolCallID:    fmt.Sprintf("call_think_%d", val.ID),
						Kind:          "think",
						Status:        "completed",
						Title:         "Thinking",
						Content: []u.H{{
							"type": "text",
							"text": val.Delta,
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
			if err != nil {
				return SessionLoadResponse{}, err
			}
		case structs.MessagesRoleTool:
			if previousToolJSON != "" {
				jsonObj := []u.H{}
				err := json.Unmarshal([]byte(strings.TrimSpace(previousToolJSON)), &jsonObj)
				// 【BUG修复】之前是 if err == nil { continue }，这是逻辑反转错误
				// 修正为：JSON解析失败时才应该跳过，不处理无效数据
				if err != nil {
					continue
				}
				for idx, obj := range jsonObj {
					toolName, ok := u.GetH[string](obj, "name")
					if !ok {
						continue
					}
					toolID, ok := u.GetH[string](obj, "id")
					if !ok {
						continue
					}
					err = call("session/update", SessionUpdate{
						SessionID: req.SessionID,
						Update: SessionUpdateUpdate{
							SessionUpdate: "tool_call",
							ToolCallID:    fmt.Sprintf("call_%d_%d", val.ID, idx),
							Title:         fmt.Sprintf("[Call %s]%s", toolName, toolID),
							Kind:          u.Default(ToolNameToType, toolName, "other"),
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
	return SessionLoadResponse{}, nil
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
	// 【BUG修复】添加defer closeDB来平衡loadDB的引用计数
	// 之前缺少这一行导致数据库连接无法正确释放
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
