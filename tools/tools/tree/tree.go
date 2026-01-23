package tree

import (
	_ "embed" // embed
	"fmt"
	"path/filepath"
	"text/template"

	"github.com/cxykevin/alkaid0/prompts"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/index"
	"github.com/cxykevin/alkaid0/tools/toolobj"
	"github.com/cxykevin/alkaid0/tools/tools/edit"
)

const toolName = "edit"

//go:embed prompt.md
var prompt string

//go:embed trace.md
var treePrompt string

var treeTempate *template.Template

func init() {
	treeTempate = prompts.Load("tools:tree:tree", treePrompt)
}

type cacheStruct struct {
	TreeObj    *Node
	TreeString string
}

func buildGlobalPrompt(session *structs.Chats) (string, error) {
	if session.TemporyDataOfRequest == nil {
		session.TemporyDataOfRequest = make(map[string]any)
	}
	treeID := int32(0)
	nowpath := session.CurrentActivatePath
	if nowpath == "" {
		nowpath = "."
	}
	nowpath, err := filepath.Abs(nowpath)
	if err != nil {
		return "", err
	}
	tree := BuildTree(nowpath, &treeID)
	tree.Name = "(root)"
	str := BuildString(tree)
	session.TemporyDataOfRequest["tools:tree"] = &cacheStruct{
		TreeObj:    tree,
		TreeString: str,
	}
	return prompts.Render(treeTempate, str), nil
}

func buildPrompt(session *structs.Chats) (string, error) {
	return prompt, nil
}

func updateInfo(session *structs.Chats, mp map[string]*any, cross []*any) (bool, []*any, error) {
	ret := any(edit.PassInfo{
		From:        "tree",
		Description: "File Tree Manager",
		Parameters:  map[string]any{},
	})
	cross = append(cross, &ret)

	return true, cross, nil
}

func writeTree(session *structs.Chats, mp map[string]*any, cross []*any) (bool, []*any, map[string]*any, error) {
	path, err := edit.CheckPath(mp)
	if err != nil {
		return true, cross, nil, nil
	}

	if path != "@tree" {
		return true, cross, nil, nil
	}

	target, text, err := edit.CheckTargetText(mp)
	if err != nil {
		boolx := false
		success := any(boolx)
		errMsg := any(err.Error())
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	ret, ok := session.TemporyDataOfRequest["tools:tree"]
	if !ok {
		boolx := false
		success := any(boolx)
		errMsg := any("No cache object found")
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}
	rets, ok := ret.(*cacheStruct)
	if !ok {
		boolx := false
		success := any(boolx)
		errMsg := any("Struct type error")
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	str, err := edit.ProcessString(rets.TreeString, target, text, true)
	if err != nil {
		boolx := false
		success := any(boolx)
		errMsg := any(err.Error())
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	diff, err := SolveCall(rets.TreeObj, str)
	fmt.Printf("%v\n", diff)
	if err != nil {
		boolx := false
		success := any(boolx)
		errMsg := any(err.Error())
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	boolx := true
	success := any(boolx)
	return false, cross, map[string]*any{
		"success": &success,
	}, nil
}

func load() string {
	actions.HookTool("", &toolobj.Hook{
		Scope: "",
		PreHook: toolobj.PreHookFunction{
			Priority: 100,
			Func:     buildGlobalPrompt,
		},
		OnHook: toolobj.OnHookFunction{
			Priority: 100,
			Func:     nil,
		},
		PostHook: toolobj.PostHookFunction{
			Priority: 100,
			Func:     nil,
		},
	})
	actions.HookTool(toolName, &toolobj.Hook{
		Scope: "",
		PreHook: toolobj.PreHookFunction{
			Priority: 90,
			Func:     buildPrompt,
		},
		OnHook: toolobj.OnHookFunction{
			Priority: 110,
			Func:     updateInfo,
		},
		PostHook: toolobj.PostHookFunction{
			Priority: 110,
			Func:     writeTree,
		},
	})
	return toolName
}

func init() {
	index.AddIndex(load)
}
