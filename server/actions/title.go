package actions

import (
	"strings"
	"time"

	"github.com/cxykevin/alkaid0/provider/request"
	"github.com/cxykevin/alkaid0/storage/structs"
)

// generateTitle 异步生成并持久化会话标题。
// full=false：首次生成（第一条用户请求 + 第一条 AI 响应）；full=true：compress 后重生成（完整对话）。
// 仅写入 AI 标题字段（ai_title），用户手动标题（title）不受影响。
func generateTitle(sess *structs.Chats, sessionID string, full bool) {
	go func() {
		var (
			title string
			err   error
		)
		if full {
			title, err = request.TitleSummaryFull(sess.GetContext(), sess.DB, sess.ID)
		} else {
			title, err = request.TitleSummary(sess.GetContext(), sess.DB, sess.ID)
		}
		if err != nil {
			logger.Warn("auto title failed for chatID=%d: %v", sess.ID, err)
			return
		}
		title = strings.TrimSpace(title)
		if title == "" {
			return
		}
		// 先更新内存，再单列写库，避免主循环全字段 Save 覆盖回空串
		sess.AITitle = title
		if err := sess.DB.Model(&structs.Chats{}).
			Where("id = ?", sess.ID).
			Update("ai_title", title).Error; err != nil {
			logger.Error("failed to save title for chatID=%d: %v", sess.ID, err)
			return
		}
		// 单列 Update 会触发 GORM autoUpdateTime 刷新 DB 的 updated_at，内存同步避免广播时间滞后
		sess.UpdatedAt = time.Now()
		logger.Info("auto title generated for chatID=%d: %s", sess.ID, title)
		broadcastSessionInfoUpdate(sess, sessionID, title)
	}()
}

// titleCommand 处理 /title 命令：
// 带参数设置用户标题；无参数还原（清除用户标题，展示回退到 AI 标题，没有 AI 标题时不另外生成）。
// 命令永远空响应/报错：不进入 AI 对话，不触发 AI 标题生成。
func titleCommand(obj *sessionObj, arg string) error {
	sessionID := cwd2SessionID(obj.cwd, obj.id)
	if arg != "" {
		obj.session.Title = arg
		if err := obj.session.DB.Model(&structs.Chats{}).
			Where("id = ?", obj.session.ID).
			Update("title", arg).Error; err != nil {
			return err
		}
		// 单列 Update 触发 GORM autoUpdateTime 刷新 DB 的 updated_at，内存同步避免广播时间滞后
		obj.session.UpdatedAt = time.Now()
		broadcastSessionInfoUpdate(obj.session, sessionID, arg)
		return nil
	}
	// 无参数：还原——清除用户标题，展示回退到 AI 标题；没有 AI 标题时也不另外生成
	if obj.session.Title == "" {
		return nil // 未设置用户标题，无事可做
	}
	obj.session.Title = ""
	if err := obj.session.DB.Model(&structs.Chats{}).
		Where("id = ?", obj.session.ID).
		Update("title", "").Error; err != nil {
		return err
	}
	obj.session.UpdatedAt = time.Now()
	broadcastSessionInfoUpdate(obj.session, sessionID, obj.session.AITitle)
	return nil
}

// broadcastSessionInfoUpdate 广播 ACP v2 标准 session_info_update 通知，
// 同步会话元数据（标题、最后活动时间）到所有已连接客户端（含发起者）。
// 供标题变更（AI 自动生成 / /title 命令 / compress 重生成）复用，客户端据此刷新会话列表。
func broadcastSessionInfoUpdate(sess *structs.Chats, sessionID, title string) {
	if err := broadcastSessionUpdate(sessionID, SessionUpdate{
		SessionID: sessionID,
		Update: SessionUpdateUpdate{
			SessionUpdate: "session_info_update",
			Title:         title,
			UpdatedAt:     formatSessionUpdatedAt(sess),
		},
	}, 0); err != nil {
		logger.Warn("failed to broadcast session_info_update: %v", err)
	}
}
