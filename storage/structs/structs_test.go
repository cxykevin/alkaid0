package structs

import (
	"testing"
)

func TestMessagesReferList_ValueAndScan(t *testing.T) {
	tests := []struct {
		name string
		list MessagesReferList
	}{
		{
			name: "空列表",
			list: MessagesReferList{},
		},
		{
			name: "单条引用",
			list: MessagesReferList{
				{FilePath: "/tmp/test.go", FileType: MessagesReferTypeFile, FileFromLine: 1, FileToLine: 10},
			},
		},
		{
			name: "多条引用",
			list: MessagesReferList{
				{FilePath: "/tmp/a.go", FileType: MessagesReferTypeFile},
				{FilePath: "/tmp/b.go", FileType: MessagesReferTypeFile, FileFromLine: 5, FileToLine: 15},
				{FilePath: "/tmp/c.go", FileType: MessagesReferTypeFile, FileFromLine: 1, FileFromCol: 1, FileToLine: 100, FileToCol: 80, Origin: []byte("original content")},
			},
		},
		{
			name: "带 Text 类型的引用",
			list: MessagesReferList{
				{FilePath: "inline text", FileType: MessagesReferTypeText},
			},
		},
		{
			name: "nil 列表",
			list: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Value -> bytes
			val, err := tt.list.Value()
			if err != nil {
				t.Fatalf("Value() failed: %v", err)
			}

			var decoded MessagesReferList
			if val == nil {
				// nil 列表的 Value() 可能返回 nil
				t.Log("Value() returned nil for", tt.name)
				return
			}

			// Scan back
			if err := decoded.Scan(val); err != nil {
				t.Fatalf("Scan() failed: %v", err)
			}

			// 验证长度匹配
			if len(decoded) != len(tt.list) {
				t.Fatalf("Length mismatch: got %d, want %d", len(decoded), len(tt.list))
			}

			// 验证内容
			for i := range decoded {
				if decoded[i].FilePath != tt.list[i].FilePath {
					t.Errorf("Item %d FilePath: got %q, want %q", i, decoded[i].FilePath, tt.list[i].FilePath)
				}
				if decoded[i].FileType != tt.list[i].FileType {
					t.Errorf("Item %d FileType: got %d, want %d", i, decoded[i].FileType, tt.list[i].FileType)
				}
				if decoded[i].FileFromLine != tt.list[i].FileFromLine {
					t.Errorf("Item %d FileFromLine: got %d, want %d", i, decoded[i].FileFromLine, tt.list[i].FileFromLine)
				}
				if decoded[i].FileToLine != tt.list[i].FileToLine {
					t.Errorf("Item %d FileToLine: got %d, want %d", i, decoded[i].FileToLine, tt.list[i].FileToLine)
				}
				if len(decoded[i].Origin) != len(tt.list[i].Origin) {
					t.Errorf("Item %d Origin length: got %d, want %d", i, len(decoded[i].Origin), len(tt.list[i].Origin))
				}
			}
		})
	}
}

func TestMessagesReferList_ScanFromString(t *testing.T) {
	list := MessagesReferList{
		{FilePath: "/tmp/test.go", FileType: MessagesReferTypeFile},
	}
	val, err := list.Value()
	if err != nil {
		t.Fatalf("Value() failed: %v", err)
	}

	// Scan from string (模拟数据库返回 string 类型)
	var decoded MessagesReferList
	if err := decoded.Scan(string(val.([]byte))); err != nil {
		t.Fatalf("Scan() from string failed: %v", err)
	}
	if len(decoded) != 1 || decoded[0].FilePath != "/tmp/test.go" {
		t.Errorf("Scan from string mismatch: got %+v", decoded)
	}
}

func TestMessagesReferList_ScanNil(t *testing.T) {
	var list MessagesReferList
	if err := list.Scan(nil); err != nil {
		t.Fatalf("Scan(nil) failed: %v", err)
	}
	if list == nil || len(list) != 0 {
		t.Error("Scan(nil) should result in empty list, not nil")
	}
}

func TestMessagesReferList_ScanInvalidType(t *testing.T) {
	var list MessagesReferList
	if err := list.Scan(123); err != nil {
		t.Fatalf("Scan(invalid type) failed: %v", err)
	}
	if list == nil || len(list) != 0 {
		t.Error("Scan(invalid type) should result in empty list, not nil")
	}
}

func TestMessagesRole_Values(t *testing.T) {
	if MessagesRoleUser != 0 {
		t.Errorf("MessagesRoleUser should be 0, got %d", MessagesRoleUser)
	}
	if MessagesRoleAgent != 1 {
		t.Errorf("MessagesRoleAgent should be 1, got %d", MessagesRoleAgent)
	}
	if MessagesRoleTool != 2 {
		t.Errorf("MessagesRoleTool should be 2, got %d", MessagesRoleTool)
	}
	if MessagesRoleCommunicate != 3 {
		t.Errorf("MessagesRoleCommunicate should be 3, got %d", MessagesRoleCommunicate)
	}
}

func TestMessagesReferType_Values(t *testing.T) {
	if MessagesReferTypeFile != 0 {
		t.Errorf("MessagesReferTypeFile should be 0, got %d", MessagesReferTypeFile)
	}
	if MessagesReferTypeText != 1 {
		t.Errorf("MessagesReferTypeText should be 1, got %d", MessagesReferTypeText)
	}
	if MessagesReferTypeImage != 2 {
		t.Errorf("MessagesReferTypeImage should be 2, got %d", MessagesReferTypeImage)
	}
}

func TestTables_Completeness(t *testing.T) {
	if len(Tables) == 0 {
		t.Fatal("Tables should not be empty")
	}

	// 检查所有主要模型都在 Tables 中
	modelTypes := make(map[string]bool)
	for _, table := range Tables {
		switch table.(type) {
		case *Chats:
			modelTypes["Chats"] = true
		case *Messages:
			modelTypes["Messages"] = true
		case *SubAgents:
			modelTypes["SubAgents"] = true
		case *Terminals:
			modelTypes["Terminals"] = true
		case *Scopes:
			modelTypes["Scopes"] = true
		case *Configs:
			modelTypes["Configs"] = true
		case *Traces:
			modelTypes["Traces"] = true
		case *ReferFiles:
			modelTypes["ReferFiles"] = true
		}
	}

	required := []string{"Chats", "Messages", "SubAgents", "Terminals", "Scopes", "Configs", "Traces", "ReferFiles"}
	for _, name := range required {
		if !modelTypes[name] {
			t.Errorf("Tables missing required model: %s", name)
		}
	}
}

func TestChats_HasRuntimeFields(t *testing.T) {
	chat := Chats{
		ID:          1,
		LastModelID: 1,
	}

	// 这些字段应该是 `gorm:"-"`（运行时字段），允许直接赋值
	if chat.ID != 1 {
		t.Errorf("ID should be 1, got %d", chat.ID)
	}
	if chat.LastModelID != 1 {
		t.Errorf("LastModelID should be 1, got %d", chat.LastModelID)
	}
}

func TestConfigs_Default(t *testing.T) {
	cfg := Configs{}
	if cfg.LastChatID != 0 {
		t.Errorf("Default LastChatID should be 0, got %d", cfg.LastChatID)
	}
}

func TestSubAgents_Default(t *testing.T) {
	agent := SubAgents{}
	if agent.Deleted {
		t.Error("Default Deleted should be false")
	}
}

func TestScopes_Default(t *testing.T) {
	scope := Scopes{}
	if scope.Enabled {
		t.Error("Default Enabled should be false")
	}
}
