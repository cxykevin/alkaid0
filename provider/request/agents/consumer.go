package agents

import (
	"fmt"

	"github.com/cxykevin/alkaid0/library/chancall"
	"github.com/cxykevin/alkaid0/provider/request/agents/actions"
)

func act(obj any) (any, error) {
	switch objs := obj.(type) {
	case actions.Add:
		return nil, AddAgent(objs.Session, objs.AgentCode, objs.AgentID, objs.Path)
	case actions.Update:
		return nil, UpdateAgent(objs.Session, objs.AgentCode, objs.AgentID, objs.Path)
	case actions.Del:
		return nil, DeleteAgent(objs.Session, objs.AgentCode)
	case actions.List:
		return ListAgents(objs.Session.DB)
	case actions.Activate:
		return nil, ActivateAgent(objs.Session, objs.AgentCode, objs.Prompt)
	case actions.Deactivate:
		return nil, DeactivateAgent(objs.Session, objs.Prompt)
	}
	panic(fmt.Errorf("act not found"))
}

func init() {
	actions.Call = chancall.Register(actions.ConsumerName, act)
}
