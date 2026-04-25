package actions

// cmdObj cmd 对象描述
type cmdObj struct {
	Description string
	Hint        string
	Function    func(*sessionObj, string) error
}

var commandMaps = map[string]*cmdObj{
	"/approve": {
		Description: "Approve a request",
		Hint:        "(no args)",
		Function: func(obj *sessionObj, _ string) error {
			err := obj.loop.Approve()
			return err
		},
	},
	"/compress": {
		Description: "Compress the history",
		Hint:        "(no args)",
		Function: func(obj *sessionObj, _ string) error {
			err := obj.loop.Summary()
			return err
		},
	},
}
