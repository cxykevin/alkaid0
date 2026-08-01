package agents

import (
	"fmt"

	"github.com/cxykevin/alkaid0/library/chancall"
	"github.com/cxykevin/alkaid0/provider/request/agents/actions"
	"github.com/cxykevin/alkaid0/storage/structs"
)

// act 分发器：入口校验 Session/DB 非 nil，避免 nil 指针 panic 导致调用方永久阻塞
func act(obj any) (any, error) {
	switch objs := obj.(type) {
	case actions.Add:
		if err := checkSessionDB(objs.Session); err != nil {
			return nil, err
		}
		return nil, AddAgent(objs.Session, objs.AgentCode, objs.AgentID, objs.Path)
	case actions.Update:
		if err := checkSessionDB(objs.Session); err != nil {
			return nil, err
		}
		return nil, UpdateAgent(objs.Session, objs.AgentCode, objs.AgentID, objs.Path)
	case actions.Del:
		if err := checkSessionDB(objs.Session); err != nil {
			return nil, err
		}
		return nil, DeleteAgent(objs.Session, objs.AgentCode)
	case actions.List:
		if err := checkSessionDB(objs.Session); err != nil {
			return nil, err
		}
		return ListAgents(objs.Session.DB)
	case actions.Activate:
		if err := checkSessionDB(objs.Session); err != nil {
			return nil, err
		}
		return nil, ActivateAgent(objs.Session, objs.AgentCode, objs.Prompt)
	case actions.Deactivate:
		if err := checkSessionDB(objs.Session); err != nil {
			return nil, err
		}
		return nil, DeactivateAgent(objs.Session, objs.Prompt)
	}
	return nil, fmt.Errorf("act not found")
}

func checkSessionDB(s *structs.Chats) error {
	if s == nil || s.DB == nil {
		return fmt.Errorf("act: nil session or db")
	}
	return nil
}

func init() {
	actions.Call = chancall.Register(actions.ConsumerName, act)
}
