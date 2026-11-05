package actions

import (
	"fmt"
	"strings"
	"time"

	"github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
)

// goalUpdateName 会话 goal 快照事件的 sessionUpdate 名称。
const goalUpdateName = "alk.cxykevin.top/session/goal"

// broadcastGoalUpdate 向会话所有连接广播当前 goal 快照。
// 空 goal 也广播（客户端据此清空展示，避免复用旧缓存）。
func broadcastGoalUpdate(sess *structs.Chats, sessionID string) {
	content := u.H{
		"sessionUpdate": goalUpdateName,
		"goal":          "",
	}
	if sess != nil {
		content["goal"] = sess.Goal
		content["updatedAt"] = formatSessionUpdatedAt(sess)
	}
	if err := broadcastSessionUpdate(sessionID, SessionUpdate{
		SessionID: sessionID,
		Update:    content,
	}, 0); err != nil {
		logger.Warn("failed to broadcast goal update: %v", err)
	}
}

// pushGoalToConn 建连（session/new / session/resume）后向该连接补推一次 goal。
// 与状态补发、队列快照一样：这类快照事件不会随 resume 重放，必须主动补发。
func pushGoalToConn(sessionID string, call func(string, any, *string) error, sess *structs.Chats) {
	if call == nil || sess == nil {
		return
	}
	content := u.H{
		"sessionUpdate": goalUpdateName,
		"goal":          sess.Goal,
		"updatedAt":     formatSessionUpdatedAt(sess),
	}
	_ = callSessionUpdate(sessionID, call, SessionUpdate{SessionID: sessionID, Update: content})
}

// setGoalValue 校验并写入 goal，然后广播。空串表示清除。
// 供 /goal 命令与 goal/set 私有协议共用。
func setGoalValue(sess *structs.Chats, sessionID, goal string) error {
	if sess == nil || sess.DB == nil {
		return fmt.Errorf("session is not available")
	}
	goal = strings.TrimSpace(goal)
	if n := len([]rune(goal)); n > structs.MaxGoalRunes {
		return fmt.Errorf("goal is too long: %d runes (max %d)", n, structs.MaxGoalRunes)
	}
	if err := sess.SaveGoal(goal); err != nil {
		return err
	}
	// 单列 Update 会刷新 DB 的 updated_at，内存同步避免广播时间滞后
	sess.UpdatedAt = time.Now()
	broadcastGoalUpdate(sess, sessionID)
	return nil
}

// goalCommand 处理 /goal：
//   - 无参数：显示当前 goal
//   - clear：清除
//   - 其它：设置/覆盖（用户特权；AI 的 edit @goal 只能写一次）
//
// 只改会话元数据，不调用 loop，因此不影响正在进行的回合。
func goalCommand(obj *sessionObj, arg string) error {
	sessionID := cwd2SessionID(obj.cwd, obj.id)
	arg = strings.TrimSpace(arg)
	if arg == "" {
		if strings.TrimSpace(obj.session.Goal) == "" {
			broadcastCmdText(obj, "No goal is set. Use `/goal <text>` to set one.")
		} else {
			broadcastCmdText(obj, "Current goal:\n\n"+obj.session.Goal)
		}
		return nil
	}
	if strings.EqualFold(arg, "clear") {
		if err := setGoalValue(obj.session, sessionID, ""); err != nil {
			return err
		}
		broadcastCmdText(obj, "Goal cleared.")
		return nil
	}
	if err := setGoalValue(obj.session, sessionID, arg); err != nil {
		return err
	}
	broadcastCmdText(obj, "Goal set.")
	return nil
}
