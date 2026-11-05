package build

import (
	"strings"

	"github.com/cxykevin/alkaid0/storage/structs"
	"gorm.io/gorm"
)

// GoalBlock 把会话 goal 渲染为注入块。空 goal 返回空串（调用方据此跳过）。
// 内容原样保留（用户/AI 可写任意文本），不做转义。
func GoalBlock(goal string) string {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return ""
	}
	return "<goal>\n" + goal + "\n</goal>"
}

// loadGoalForRequest 读取指定会话的 goal，供摘要/标题这类独立请求在 system 末尾注入。
// 会话不存在或字段为空时返回空串。
func loadGoalForRequest(db *gorm.DB, chatID uint32) string {
	if db == nil || chatID == 0 {
		return ""
	}
	var obj structs.Chats
	if err := db.Select("goal").Where("id = ?", chatID).Limit(1).Find(&obj).Error; err != nil {
		return ""
	}
	return obj.Goal
}
