package response_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/provider/response"
	"github.com/cxykevin/alkaid0/storage"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/toolobj"
)

func unwrap[T any](args T, err error) T {
	if err != nil {
		panic(err)
	}
	return args
}

// TestSolver_AddToken 测试 Solver 的 AddToken 方法
func TestSolver_AddToken(t *testing.T) {
	// 初始化内存数据库，便于 DoneToken 写入时不报错
	if err := storage.InitDB(":memory:"); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}

	chatID := uint32(1001)
	s := response.NewSolver(chatID, "")

	// 测试普通文本
	resp, thinking, err := s.AddToken("Hello World", "")
	if err != nil {
		t.Fatalf("AddToken error: %v", err)
	}
	_, r2, t2, err := s.DoneToken()
	if err != nil {
		t.Fatalf("DoneToken error: %v", err)
	}
	resp += r2
	thinking += t2

	if resp != "Hello World" {
		t.Fatalf("expected response 'Hello World', got '%s'", resp)
	}
	if thinking != "" {
		t.Fatalf("expected thinking empty, got '%s'", thinking)
	}

	// 测试 think 标签
	s = response.NewSolver(chatID, "")
	resp, thinking, err = s.AddToken("<think>思考内容</think>", "")
	if err != nil {
		t.Fatalf("AddToken error: %v", err)
	}
	_, r2, t2, err = s.DoneToken()
	if err != nil {
		t.Fatalf("DoneToken error: %v", err)
	}
	resp += r2
	thinking += t2
	if resp != "" {
		t.Fatalf("expected empty response, got '%s'", resp)
	}
	if thinking != "思考内容" {
		t.Fatalf("expected thinking '思考内容', got '%s'", thinking)
	}
}

// // TestSolver_DoneToken_Persist 测试 DoneToken 会将 toolResponses 写入数据库
// func TestSolver_DoneToken_Persist(t *testing.T) {
// 	if err := storage.InitDB(":memory:"); err != nil {
// 		t.Fatalf("InitDB failed: %v", err)
// 	}
// 	chatID := uint32(2002)
// 	s := response.NewSolver(chatID, "")

// 	_, _, _, err := s.DoneToken()
// 	if err != nil {
// 		t.Fatalf("DoneToken error: %v", err)
// 	}

// 	var msg storageStructs.Messages
// 	if err := storage.DB.Where("chat_id = ?", chatID).First(&msg).Error; err != nil {
// 		t.Fatalf("failed to find message record: %v", err)
// 	}
// 	// 空的 toolResponses 可能为 nil 或 空切片，允许为 "null\n" 或 "[]\n"
// 	if msg.Delta != "[]\n" && msg.Delta != "null\n" {
// 		t.Fatalf("expected delta '[]\\n' or 'null\\n', got '%s'", msg.Delta)
// 	}
// 	if msg.Type != storageStructs.MessagesRoleTool {
// 		t.Fatalf("expected message type Tool, got %v", msg.Type)
// 	}
// }

// TestSolver_ToolCalling_SingleTool 测试单个工具调用的完整流程
func TestSolver_ToolCalling_SingleTool(t *testing.T) {
	if err := storage.InitDB(":memory:"); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}

	// 初始化工具系统
	toolobj.ToolsList = make(map[string]*toolobj.Tools)
	toolobj.Scopes = make(map[string]string)
	toolobj.EnableScopes = make(map[string]bool)

	// 添加scope
	toolobj.Scopes["test_scope"] = "Test Scope"
	toolobj.EnableScopes["test_scope"] = true

	// 创建测试工具
	testTool := &toolobj.Tools{
		Name:  "test_calculator",
		ID:    "test_tool_1",
		Scope: "test_scope",
		Parameters: map[string]parser.ToolParameters{
			"expression": {
				Type:        parser.ToolTypeString,
				Required:    true,
				Description: "计算表达式",
			},
		},
		Hooks: []toolobj.Hook{
			{
				Scope: "test_scope",
				PreHook: toolobj.PreHookFunction{
					Func: func() (string, error) {
						return "", nil
					},
				},
				OnHook: toolobj.OnHookFunction{
					Func: func(args map[string]*any, passObjs []*any) (bool, []*any, error) {
						fmt.Printf("Obj: %v\n", string(unwrap(json.Marshal(args))))
						return false, passObjs, nil
					},
				},
				PostHook: toolobj.PostHookFunction{
					Func: func(args map[string]*any, passObjs []*any) (bool, []*any, map[string]*any, error) {
						// 模拟工具执行结果
						result := map[string]*any{
							"result": newAny("2"),
							"status": newAny("success"),
						}
						return false, passObjs, result, nil
					},
				},
			},
		},
	}
	toolobj.ToolsList["test_calculator"] = testTool

	chatID := uint32(3003)
	s := response.NewSolver(chatID, "")

	// 模拟AI返回的工具调用JSON
	toolCallJSON := `[{"name": "test_calculator", "id": "call_123", "parameters": {"expression": "1+1"}}]`

	// 添加工具调用token
	resp, thinking, err := s.AddToken("<tools>"+toolCallJSON+"</tools>", "")
	if err != nil {
		t.Fatalf("AddToken error: %v", err)
	}

	// 完成token处理
	_, r2, t2, err := s.DoneToken()
	if err != nil {
		t.Fatalf("DoneToken error: %v", err)
	}
	resp += r2
	thinking += t2

	// 验证响应内容
	if resp != "" {
		t.Fatalf("expected empty response, got '%s'", resp)
	}
	if thinking != "" {
		t.Fatalf("expected empty thinking, got '%s'", thinking)
	}

	// 验证数据库中保存的toolResponses
	var msg storageStructs.Messages
	if err := storage.DB.Where("chat_id = ?", chatID).First(&msg).Error; err != nil {
		t.Fatalf("failed to find message record: %v", err)
	}

	// 解析保存的toolResponses
	var savedResponses []map[string]interface{}
	if err := json.Unmarshal([]byte(msg.Delta), &savedResponses); err != nil {
		t.Fatalf("failed to unmarshal toolResponses: %v", err)
	}

	// 验证toolResponses内容
	if len(savedResponses) != 1 {
		t.Fatalf("expected 1 tool response, got %d", len(savedResponses))
	}

	response := savedResponses[0]
	if response["name"] != "test_calculator" {
		t.Fatalf("expected tool name 'test_calculator', got '%v'", response["name"])
	}
	if response["id"] != "call_123" {
		t.Fatalf("expected tool id 'call_123', got '%v'", response["id"])
	}

	// 验证返回的数据
	returnData, ok := response["return"].(string)
	if !ok {
		t.Fatalf("expected return to be string, got %T", response["return"])
	}

	var returnObj map[string]interface{}
	if err := json.Unmarshal([]byte(returnData), &returnObj); err != nil {
		t.Fatalf("failed to unmarshal return data: %v", err)
	}

	if returnObj["result"] != "2" {
		t.Fatalf("expected result '2', got '%v'", returnObj["result"])
	}
	if returnObj["status"] != "success" {
		t.Fatalf("expected status 'success', got '%v'", returnObj["status"])
	}
}

// TestSolver_ToolCalling_MultipleTools 测试多个工具调用的完整流程
func TestSolver_ToolCalling_MultipleTools(t *testing.T) {
	if err := storage.InitDB(":memory:"); err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}

	// 初始化工具系统
	toolobj.ToolsList = make(map[string]*toolobj.Tools)
	toolobj.Scopes = make(map[string]string)
	toolobj.EnableScopes = make(map[string]bool)

	// 添加scope
	toolobj.Scopes["test_scope"] = "Test Scope"
	toolobj.EnableScopes["test_scope"] = true

	// 创建第一个测试工具
	testTool1 := &toolobj.Tools{
		Name:  "test_calculator",
		ID:    "test_tool_1",
		Scope: "test_scope",
		Parameters: map[string]parser.ToolParameters{
			"expression": {
				Type:        parser.ToolTypeString,
				Required:    true,
				Description: "计算表达式",
			},
		},
		Hooks: []toolobj.Hook{
			{
				Scope: "test_scope",
				PreHook: toolobj.PreHookFunction{
					Func: func() (string, error) {
						return "", nil
					},
				},
				OnHook: toolobj.OnHookFunction{
					Func: func(args map[string]*any, passObjs []*any) (bool, []*any, error) {
						fmt.Printf("Obj: %v\n", string(unwrap(json.Marshal(args))))
						return false, passObjs, nil
					},
				},
				PostHook: toolobj.PostHookFunction{
					Func: func(args map[string]*any, passObjs []*any) (bool, []*any, map[string]*any, error) {
						result := map[string]*any{
							"result": newAny("2"),
							"status": newAny("success"),
						}
						return false, passObjs, result, nil
					},
				},
			},
		},
	}
	toolobj.ToolsList["test_calculator"] = testTool1

	// 创建第二个测试工具
	testTool2 := &toolobj.Tools{
		Name:  "test_echo",
		ID:    "test_tool_2",
		Scope: "test_scope",
		Parameters: map[string]parser.ToolParameters{
			"message": {
				Type:        parser.ToolTypeString,
				Required:    true,
				Description: "消息内容",
			},
		},
		Hooks: []toolobj.Hook{
			{
				Scope: "test_scope",
				PreHook: toolobj.PreHookFunction{
					Func: func() (string, error) {
						return "", nil
					},
				},
				OnHook: toolobj.OnHookFunction{
					Func: func(args map[string]*any, passObjs []*any) (bool, []*any, error) {
						fmt.Printf("Obj: %v\n", string(unwrap(json.Marshal(args))))
						return false, passObjs, nil
					},
				},
				PostHook: toolobj.PostHookFunction{
					Func: func(args map[string]*any, passObjs []*any) (bool, []*any, map[string]*any, error) {
						// 获取参数
						msg := ""
						if msgPtr, ok := args["message"]; ok && msgPtr != nil {
							if msgStr, ok2 := (*msgPtr).(string); ok2 {
								msg = msgStr
							}
						}
						result := map[string]*any{
							"echo":   newAny(msg),
							"length": newAny(float64(len(msg))),
						}
						return false, passObjs, result, nil
					},
				},
			},
		},
	}
	toolobj.ToolsList["test_echo"] = testTool2

	chatID := uint32(4004)
	s := response.NewSolver(chatID, "")

	// 模拟AI返回的多个工具调用JSON
	toolCallJSON := `[` +
		`{"name": "test_calculator", "id": "call_123", "parameters": {"expression": "1+1"}},` +
		`{"name": "test_echo", "id": "call_456", "parameters": {"message": "Hello World"}}` +
		`]`

	// 添加工具调用token
	_, _, err := s.AddToken("<tools>"+toolCallJSON+"</tools>", "")
	if err != nil {
		t.Fatalf("AddToken error: %v", err)
	}

	// 完成token处理
	_, _, _, err = s.DoneToken()
	if err != nil {
		t.Fatalf("DoneToken error: %v", err)
	}

	// 验证数据库中保存的toolResponses
	var msg storageStructs.Messages
	if err := storage.DB.Where("chat_id = ?", chatID).First(&msg).Error; err != nil {
		t.Fatalf("failed to find message record: %v", err)
	}

	// 解析保存的toolResponses
	var savedResponses []map[string]interface{}
	if err := json.Unmarshal([]byte(msg.Delta), &savedResponses); err != nil {
		t.Fatalf("failed to unmarshal toolResponses: %v", err)
	}

	// 验证toolResponses内容
	if len(savedResponses) != 2 {
		t.Fatalf("expected 2 tool responses, got %d", len(savedResponses))
	}

	// 验证第一个工具响应
	resp1 := savedResponses[0]
	if resp1["name"] != "test_calculator" || resp1["id"] != "call_123" {
		t.Fatalf("first tool response mismatch: %+v", resp1)
	}

	// 验证第二个工具响应
	resp2 := savedResponses[1]
	if resp2["name"] != "test_echo" || resp2["id"] != "call_456" {
		t.Fatalf("second tool response mismatch: %+v", resp2)
	}
}

// Helper function to create *any from values
func newAny(v interface{}) *any {
	var a any = v
	return &a
}
