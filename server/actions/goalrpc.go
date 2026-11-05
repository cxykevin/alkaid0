package actions

import (
	"fmt"
	"strings"
	"time"

	"github.com/cxykevin/alkaid0/storage/structs"
)

// 私有 ACP 方法：alk.cxykevin.top/session/goal/get | .../goal/set。
//
// goal 归属于「工作目录 + 会话」（chats.goal），与 /goal 命令、edit @goal 同一份数据。
// 客户端只能读写已绑定到本连接的会话；会话允许冷状态（服务端重启后内存注册表为空，
// 但客户端 resume 时已重新绑定）。
//
// 推送：写入后广播 alk.cxykevin.top/session/goal（见 goal.go）；session/new 与
// session/resume 建连时也会补推一次快照。

// GoalGetRequest 读取会话 goal。
type GoalGetRequest struct {
	SessionID string `json:"sessionId"`
}

// GoalGetResponse 当前 goal（空串表示未设置）。
type GoalGetResponse struct {
	Goal      string `json:"goal"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// GoalSetRequest 设置/清除会话 goal。空字符串 = 清除。
type GoalSetRequest struct {
	SessionID string `json:"sessionId"`
	Goal      string `json:"goal"`
}

// GoalSetResponse 写入后的 goal。
type GoalSetResponse struct {
	Goal      string `json:"goal"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// goalTargetForConn 解析 sessionId 并校验连接绑定。
func goalTargetForConn(sessionID string, connID uint64) (cwd string, chatID uint32, canonical string, err error) {
	if sessionID == "" {
		return "", 0, "", fmt.Errorf("sessionId is empty")
	}
	cwd, chatID, err = resolveSessionTarget(sessionID)
	if err != nil {
		return "", 0, "", fmt.Errorf("invalid sessionId: %v", err)
	}
	canonical = cwd2SessionID(cwd, chatID)
	if !sessionBoundToConn(connID, canonical) {
		return "", 0, "", fmt.Errorf("session %s is not bound to this connection", canonical)
	}
	return cwd, chatID, canonical, nil
}

// GoalGet 读取当前会话的 goal。
func GoalGet(req GoalGetRequest, _ func(string, any, *string) error, connID uint64) (GoalGetResponse, error) {
	cwd, chatID, canonical, err := goalTargetForConn(req.SessionID, connID)
	if err != nil {
		return GoalGetResponse{}, err
	}
	if obj, ok := lookupSession(canonical); ok && obj.session != nil {
		return GoalGetResponse{
			Goal:      obj.session.Goal,
			UpdatedAt: formatSessionUpdatedAt(obj.session),
		}, nil
	}

	db, err := loadDB(cwd)
	if err != nil {
		return GoalGetResponse{}, err
	}
	defer closeDB(cwd)
	var sess structs.Chats
	if err := db.Select("id", "goal", "updated_at").Where("id = ?", chatID).Limit(1).Find(&sess).Error; err != nil {
		return GoalGetResponse{}, err
	}
	if sess.ID == 0 {
		return GoalGetResponse{}, fmt.Errorf("session not found")
	}
	return GoalGetResponse{Goal: sess.Goal, UpdatedAt: formatSessionUpdatedAt(&sess)}, nil
}

// GoalSet 设置或清除（goal=""）当前会话的 goal。
// 会话忙碌时也允许：写入立即落库，后续请求（含同一回合的下一轮）立即生效。
func GoalSet(req GoalSetRequest, _ func(string, any, *string) error, connID uint64) (GoalSetResponse, error) {
	cwd, chatID, canonical, err := goalTargetForConn(req.SessionID, connID)
	if err != nil {
		return GoalSetResponse{}, err
	}
	goal := strings.TrimSpace(req.Goal)
	if n := len([]rune(goal)); n > structs.MaxGoalRunes {
		return GoalSetResponse{}, fmt.Errorf("goal is too long: %d runes (max %d)", n, structs.MaxGoalRunes)
	}

	// 热会话：直接改内存并走统一广播。
	if obj, ok := lookupSession(canonical); ok && obj.session != nil {
		if err := setGoalValue(obj.session, canonical, goal); err != nil {
			return GoalSetResponse{}, err
		}
		return GoalSetResponse{Goal: obj.session.Goal, UpdatedAt: formatSessionUpdatedAt(obj.session)}, nil
	}

	// 冷会话：不为了写一个字段调 loadSession（会创建 loop/msgQueue/index）。
	db, err := loadDB(cwd)
	if err != nil {
		return GoalSetResponse{}, err
	}
	defer closeDB(cwd)
	var sess structs.Chats
	if err := db.Select("id").Where("id = ?", chatID).Limit(1).Find(&sess).Error; err != nil {
		return GoalSetResponse{}, err
	}
	if sess.ID == 0 {
		return GoalSetResponse{}, fmt.Errorf("session not found")
	}
	res := db.Model(&structs.Chats{}).Where("id = ?", chatID).Update("goal", goal)
	if res.Error != nil {
		return GoalSetResponse{}, res.Error
	}
	if res.RowsAffected == 0 {
		return GoalSetResponse{}, fmt.Errorf("session not found")
	}
	sess.Goal = goal
	sess.UpdatedAt = time.Now()
	broadcastGoalUpdate(&sess, canonical)
	return GoalSetResponse{Goal: goal, UpdatedAt: formatSessionUpdatedAt(&sess)}, nil
}
