package funcs

import (
	"os"
	"testing"

	"github.com/cxykevin/alkaid0/storage"
	"github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *gorm.DB {
	os.Setenv("ALKAID_DEBUG_SQLITEFILE", ":memory:")
	db, err := storage.InitStorage("", "")
	if err != nil {
		t.Fatalf("Failed to init storage: %v", err)
	}
	return db
}

func TestGetChats(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()
	chats, err := GetChats(db)
	if err != nil {
		t.Fatalf("GetChats failed: %v", err)
	}
	oldchats := len(chats)

	// Create some chats
	chat1 := &structs.Chats{Title: "Chat 1"}
	chat2 := &structs.Chats{Title: "Chat 2"}
	db.Create(chat1)
	db.Create(chat2)

	chats, err = GetChats(db)
	if err != nil {
		t.Fatalf("GetChats failed: %v", err)
	}
	if len(chats)-oldchats != 2 {
		t.Errorf("Expected 2 chats, got %d", len(chats)-oldchats)
	}
}

func TestQueryChat(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	chat := &structs.Chats{Title: "Test Chat"}
	db.Create(chat)

	found, err := QueryChat(db, chat.ID)
	if err != nil {
		t.Fatalf("QueryChat failed: %v", err)
	}
	if found.Title != "Test Chat" {
		t.Errorf("Expected title 'Test Chat', got '%s'", found.Title)
	}
}

func TestCreateChat(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	id, err := CreateChat(db)
	if err != nil {
		t.Fatalf("CreateChat failed: %v", err)
	}
	if id == 0 {
		t.Error("Expected non-zero ID")
	}
}

func TestDeleteChat(t *testing.T) {
	db := setupTestDB(t)
	defer u.Unwrap(db.DB()).Close()

	chat := &structs.Chats{Title: "To Delete"}
	db.Create(chat)

	err := DeleteChat(db, chat)
	if err != nil {
		t.Fatalf("DeleteChat failed: %v", err)
	}

	// Verify deleted
	_, err = QueryChat(db, chat.ID)
	if err == nil {
		t.Error("Expected error after delete")
	}
}
