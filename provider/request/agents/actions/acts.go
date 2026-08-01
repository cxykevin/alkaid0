package actions

import (
	"fmt"

	"github.com/cxykevin/alkaid0/library/chancall"
	"github.com/cxykevin/alkaid0/storage/structs"
)

// ConsumerName 消费者名称
const ConsumerName = "agents"

// Call 调用函数
var Call chancall.CallFunc

// AddAgent 添加
func AddAgent(session *structs.Chats, agentCode string, agentID string, path string) error {
	_, err := Call(Add{
		Session:   session,
		AgentCode: agentCode,
		AgentID:   agentID,
		Path:      path,
	})
	return err
}

// UpdateAgent 更新
func UpdateAgent(session *structs.Chats, agentCode string, agentID string, path string) error {
	_, err := Call(Update{
		Session:   session,
		AgentCode: agentCode,
		AgentID:   agentID,
		Path:      path,
	})
	return err
}

// DeleteAgent 删除
func DeleteAgent(session *structs.Chats, agentCode string) error {
	_, err := Call(Del{
		Session:   session,
		AgentCode: agentCode,
	})
	return err
}

// ListAgent 列表
func ListAgent(session *structs.Chats) ([]structs.SubAgents, error) {
	res, err := Call(List{
		Session: session,
	})
	if err != nil {
		return nil, err
	}
	ret, ok := res.([]structs.SubAgents)
	if !ok {
		// 类型断言失败时返回明确错误，而不是静默的 nil, err（err 在此路径必为 nil）
		return nil, fmt.Errorf("ListAgent: unexpected result type %T", res)
	}
	return ret, nil
}

// ActivateAgent 激活
func ActivateAgent(session *structs.Chats, agentCode string, prompt string) error {
	_, err := Call(Activate{
		Session:   session,
		AgentCode: agentCode,
		Prompt:    prompt,
	})
	return err
}

// DeactivateAgent 取消激活
func DeactivateAgent(session *structs.Chats, prompt string) error {
	_, err := Call(Deactivate{
		Session: session,
		Prompt:  prompt,
	})
	return err
}
