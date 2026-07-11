package actions

import (
	"fmt"
	"strings"

	"github.com/cxykevin/alkaid0/storage/structs"
)

// cmdObj cmd 对象描述
type cmdObj struct {
	Description string
	Hint        string
	Function    func(*sessionObj, string) (bool, error)
}

// commandMaps 存储所有聊天命令及其对应处理函数
var commandMaps = map[string]*cmdObj{
	"/approve": {
		Description: "Approve a request",
		Hint:        "(no args)",
		Function: func(obj *sessionObj, _ string) (bool, error) {
			err := obj.loop.Approve()
			if err != nil {
				return false, err
			}
			return true, nil
		},
	},
	"/compress": {
		Description: "Compress the history",
		Hint:        "(no args)",
		Function: func(obj *sessionObj, _ string) (bool, error) {
			err := obj.loop.Summary()
			if err != nil {
				return false, err
			}
			return true, nil
		},
	},
	"/effort": {
		Description: "Set the reasoning effort (low | medium | high | max | xhigh | unset)",
		Hint:        "reasoning effort",
		Function: func(obj *sessionObj, arg string) (bool, error) {
			effortArg := strings.TrimSpace(strings.ToLower(arg))
			if effortArg == "low" || effortArg == "medium" || effortArg == "high" || effortArg == "max" || effortArg == "xhigh" || effortArg == "unset" {
				obj.session.ReasoningEffort = effortArg
				err := obj.session.DB.Model(&structs.Chats{}).Where("id = ?", obj.session.ID).Update("reasoning_effort", effortArg).Error
				return false, err
			}
			return false, fmt.Errorf("Unknown reasoning effort")
		},
	},
	"/reload": {
		Description: "Reload config file",
		Hint:        "(no args)",
		Function: func(obj *sessionObj, arg string) (bool, error) {
			go updateCfgsToConns()
			return false, nil
		},
	},
	"/background": {
		Description: "Set background mode on/off — keep session alive after all clients disconnect",
		Hint:        "on|off",
		Function: func(obj *sessionObj, arg string) (bool, error) {
			arg = strings.TrimSpace(strings.ToLower(arg))
			switch arg {
			case "on":
				obj.background = true
				return false, nil
			case "off":
				obj.background = false
				return false, nil
			default:
				return false, fmt.Errorf("Usage: /background on|off")
			}
		},
	},
}
