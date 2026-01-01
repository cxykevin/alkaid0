package request

import (
	"github.com/cxykevin/alkaid0/storage/structs"
	"gorm.io/gorm"
)

// UserAddMsg 用户发送消息
func UserAddMsg(db *gorm.DB, chatID uint32, msg string, refer structs.MessagesReferList) error {
	var chat structs.Chats
	db.First(&chat, 1)
	chat.NowAgent = ""
	err := db.Select("NowAgent").Save(&chat).Error
	if err != nil {
		return err
	}
	// 插入
	err = db.Create(&structs.Messages{
		ChatID: chatID,
		Delta:  msg,
		Refers: refer,
		Type:   structs.MessagesRoleUser,
	}).Error
	if err != nil {
		return err
	}
	return nil
}
