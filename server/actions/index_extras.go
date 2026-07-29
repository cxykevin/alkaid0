package actions

import (
	"fmt"
	"strings"

	"github.com/cxykevin/alkaid0/context/codebase"
	"github.com/cxykevin/alkaid0/storage/structs"
)

// indexTempfsAndChatHistory 在索引重建后，重新索引 temp 文件和聊天历史
func indexTempfsAndChatHistory(cwd string) {
	db, err := loadDB(cwd)
	if err != nil {
		logger.Warn("index extras: load db failed: %v", err)
		return
	}
	defer closeDB(cwd)

	// 1) 最新的 60 条 ReferFiles（temp 文件）
	var refs []structs.ReferFiles
	if err := db.Order("chat_id DESC").Limit(60).Find(&refs).Error; err != nil {
		logger.Warn("index extras: query refer files failed: %v", err)
	} else {
		for i := range refs {
			_ = codebase.AddToQueue(cwd, codebase.EmbedTask{
				FilePath:    fmt.Sprintf("tempfs/%d/%s", refs[i].ChatID, refs[i].Path),
				FullContent: refs[i].Content,
				EmbedText:   refs[i].Content,
				Tags:        []string{"tempfs"},
			})
		}
		logger.Info("index extras: queued %d tempfs files", len(refs))
	}

	// 2) 最新的 3 个会话，各取末尾 8 对对话
	var chats []structs.Chats
	if err := db.Order("id DESC").Limit(3).Find(&chats).Error; err != nil {
		logger.Warn("index extras: query chats failed: %v", err)
	} else {
		for ci := range chats {
			var messages []structs.Messages
			if err := db.Where("chat_id = ? AND type IN (0, 1)", chats[ci].ID).
				Order("id DESC").Limit(16).Find(&messages).Error; err != nil {
				logger.Warn("index extras: query messages for chat %d failed: %v", chats[ci].ID, err)
				continue
			}
			// 反转：从降序变回正序
			for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
				messages[i], messages[j] = messages[j], messages[i]
			}
			var buf strings.Builder
			for _, msg := range messages {
				role := "User"
				if msg.Type == 1 {
					role = "Assistant"
				}
				_, _ = fmt.Fprintf(&buf, "[%s]\n%s\n\n", role, msg.Delta)
			}
			contentStr := buf.String()
			if contentStr != "" {
				_ = codebase.AddToQueue(cwd, codebase.EmbedTask{
					FilePath:    fmt.Sprintf("chathistory/%d", chats[ci].ID),
					FullContent: contentStr,
					EmbedText:   contentStr,
					Tags:        []string{"chathistory"},
				})
			}
		}
		logger.Info("index extras: queued %d chathistory sessions", len(chats))
	}
}
