package tree

import (
	_ "embed" // embed
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/prompts"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/index"
	"github.com/cxykevin/alkaid0/tools/toolobj"
	"github.com/cxykevin/alkaid0/tools/tools/edit"
)

const toolName = "tree"

//go:embed prompt.md
var prompt string

//go:embed trace.md
var treePrompt string

var treeTempate *template.Template

var logger = log.New("tools:tree")

func init() {
	treeTempate = prompts.Load("tools:tree:tree", treePrompt)
}

type treeEntryState struct {
	Path     string
	IsDir    bool
	Size     int64
	ModTime  int64
	Target   string
	Scanned  bool
	Children int
}

type cacheStruct struct {
	TreeObj     *Node
	TreeString  string
	WorkPath    string
	Fingerprint string
	Entries     map[string]treeEntryState
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Generation  uint64
}

const (
	treeCacheKey       = "tools:tree"
	treeCacheRetention = 180 * time.Second
)

func treeWorkPath(session *structs.Chats) (string, error) {
	if session == nil {
		return "", fmt.Errorf("session is nil")
	}
	root := session.Root
	if root == "" {
		root = "."
	}
	activatePath := session.CurrentActivatePath
	if activatePath == "" {
		activatePath = "."
	}
	return filepath.Abs(filepath.Join(root, activatePath))
}

// directoryFingerprint tracks the directory entries visible to BuildTree without
// stat-ing every file. It invalidates the snapshot when the tree shape changes.
func stateIdentity(info os.FileInfo) string {
	if info == nil || info.Sys() == nil {
		return ""
	}
	return fmt.Sprintf("%T:%v", info.Sys(), info.Sys())
}

// scanTreeState records the visible directory shape used by BuildTree. A
// truncated directory is deliberately marked incomplete so it can never be
// updated from an incomplete baseline.
func scanTreeState(path string, depth int, ancestors map[string]bool, states map[string]treeEntryState) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	state := treeEntryState{
		Path:    path,
		IsDir:   info.IsDir(),
		Size:    info.Size(),
		ModTime: info.ModTime().UnixNano(),
		Target:  stateIdentity(info),
		Scanned: true,
	}
	if target, targetErr := filepath.EvalSymlinks(path); targetErr == nil {
		state.Target = target
	}
	states[path] = state
	if !info.IsDir() {
		return nil
	}
	realPath, evalErr := filepath.EvalSymlinks(path)
	if evalErr == nil {
		realPath, _ = filepath.Abs(realPath)
		if ancestors[realPath] {
			states[path] = treeEntryState{Path: path, IsDir: true, Target: realPath, Scanned: false}
			return fmt.Errorf("directory symlink cycle at %s", path)
		}
		ancestors[realPath] = true
		defer delete(ancestors, realPath)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		states[path] = treeEntryState{Path: path, IsDir: true, Target: state.Target, Scanned: false}
		return err
	}
	visible := make([]os.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if dirBlacklists[entry.Name()] {
			continue
		}
		visible = append(visible, entry)
	}
	state.Children = len(visible)
	if depth >= MaxDepth || len(visible) > MaxChildrenNum {
		state.Scanned = false
		states[path] = state
		return nil
	}
	states[path] = state
	for _, entry := range visible {
		if childErr := scanTreeState(filepath.Join(path, entry.Name()), depth+1, ancestors, states); childErr != nil {
			return childErr
		}
	}
	return nil
}

func treeStates(path string) (map[string]treeEntryState, error) {
	states := make(map[string]treeEntryState)
	err := scanTreeState(path, 0, make(map[string]bool), states)
	return states, err
}

func treeStatesComplete(states map[string]treeEntryState) bool {
	if len(states) == 0 {
		return false
	}
	for _, state := range states {
		if !state.Scanned {
			return false
		}
	}
	return true
}

func treeStateFingerprint(states map[string]treeEntryState) string {
	paths := make([]string, 0, len(states))
	for path := range states {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var builder strings.Builder
	for _, path := range paths {
		state := states[path]
		fmt.Fprintf(&builder, "%s|%t|%d|%d|%s|%t|%d\\n", path, state.IsDir, state.Size, state.ModTime, state.Target, state.Scanned, state.Children)
	}
	return builder.String()
}

func directoryFingerprint(path string, depth int) (string, error) {
	states, err := treeStates(path)
	if err != nil {
		return treeStateFingerprint(states), err
	}
	return treeStateFingerprint(states), nil
}

func treeCache(session *structs.Chats, workPath, fingerprint string) (*cacheStruct, bool) {
	if session == nil || session.TemporyDataOfSession == nil {
		return nil, false
	}
	cache, ok := session.TemporyDataOfSession[treeCacheKey].(map[string]*cacheStruct)
	if !ok {
		return nil, false
	}
	ret, ok := cache[workPath]
	if !ok || ret == nil || ret.WorkPath != workPath || ret.TreeObj == nil {
		return nil, false
	}
	if time.Since(ret.UpdatedAt) > treeCacheRetention {
		return nil, false
	}
	if fingerprint != "" && ret.Fingerprint == fingerprint {
		return ret, true
	}
	return ret, false
}

func maxTreeID(node *Node) int32 {
	if node == nil {
		return 0
	}
	maxID := node.ID
	for _, child := range node.Children {
		if id := maxTreeID(child); id > maxID {
			maxID = id
		}
	}
	return maxID
}

func preserveTreeIDs(oldNode, newNode *Node, oldByPath map[string]*Node, nextID *int32) {
	if newNode == nil {
		return
	}
	if old := oldByPath[newNode.Path]; old != nil && old.IsDir == newNode.IsDir {
		newNode.ID = old.ID
	} else if !newNode.IsDir {
		*nextID++
		newNode.ID = *nextID
	}
	for _, child := range newNode.Children {
		preserveTreeIDs(oldNode, child, oldByPath, nextID)
	}
}

func indexTreeNodes(node *Node, result map[string]*Node) {
	if node == nil {
		return
	}
	result[node.Path] = node
	for _, child := range node.Children {
		indexTreeNodes(child, result)
	}
}

// refreshChangedSubtrees rebuilds only directories whose observed state changed.
// A failure is intentionally surfaced so callers can fall back to BuildTree.
func refreshChangedSubtrees(node *Node, oldStates, newStates map[string]treeEntryState, depth int, nextID *int32) error {
	if node == nil || !node.IsDir {
		return nil
	}
	oldState, oldOK := oldStates[node.Path]
	newState, newOK := newStates[node.Path]
	if !newOK || !newState.Scanned {
		return fmt.Errorf("tree state is incomplete at %s", node.Path)
	}
	if !oldOK || oldState != newState {
		oldByPath := make(map[string]*Node)
		indexTreeNodes(node, oldByPath)
		fresh, errs := BuildTree(node.Path, nextID, depth)
		if len(errs) > 0 || fresh == nil || fresh.Error != nil {
			if len(errs) > 0 {
				return errs[0]
			}
			return fmt.Errorf("failed to rebuild changed subtree %s", node.Path)
		}
		preserveTreeIDs(node, fresh, oldByPath, nextID)
		*node = *fresh
		return nil
	}
	for _, child := range node.Children {
		if err := refreshChangedSubtrees(child, oldStates, newStates, depth+1, nextID); err != nil {
			return err
		}
	}
	return nil
}

func subtreeChanged(path string, oldStates, newStates map[string]treeEntryState) bool {
	prefix := filepath.Clean(path) + string(filepath.Separator)
	for candidate, oldState := range oldStates {
		if candidate != path && !strings.HasPrefix(candidate, prefix) {
			continue
		}
		newState, ok := newStates[candidate]
		if !ok || newState != oldState {
			return true
		}
	}
	for candidate, newState := range newStates {
		if candidate != path && !strings.HasPrefix(candidate, prefix) {
			continue
		}
		oldState, ok := oldStates[candidate]
		if !ok || oldState != newState {
			return true
		}
	}
	return false
}

func visibleTreeEntries(path string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	visible := entries[:0]
	for _, entry := range entries {
		if dirBlacklists[entry.Name()] {
			continue
		}
		visible = append(visible, entry)
	}
	return visible, nil
}

func refreshDirectory(node *Node, oldStates, newStates map[string]treeEntryState, depth int, nextID *int32) error {
	if node == nil || !node.IsDir {
		return fmt.Errorf("cannot refresh non-directory node")
	}
	state, ok := newStates[node.Path]
	if !ok || !state.Scanned {
		return fmt.Errorf("tree state is incomplete at %s", node.Path)
	}
	if depth >= MaxDepth || state.Children > MaxChildrenNum {
		return fmt.Errorf("tree state is truncated at %s", node.Path)
	}
	entries, err := visibleTreeEntries(node.Path)
	if err != nil {
		return err
	}
	oldChildren := make(map[string]*Node, len(node.Children))
	for _, child := range node.Children {
		oldChildren[child.Path] = child
	}
	children := make([]*Node, 0, len(entries))
	for _, entry := range entries {
		childPath := filepath.Join(node.Path, entry.Name())
		oldChild := oldChildren[childPath]
		if oldChild != nil && oldChild.IsDir == newStates[childPath].IsDir && !subtreeChanged(childPath, oldStates, newStates) {
			children = append(children, oldChild)
			continue
		}
		fresh, errs := BuildTree(childPath, nextID, depth+1)
		if fresh == nil {
			return fmt.Errorf("failed to rebuild %s", childPath)
		}
		if len(errs) > 0 || fresh.Error != nil {
			return fmt.Errorf("failed to rebuild %s", childPath)
		}
		if oldChild != nil {
			oldByPath := make(map[string]*Node)
			indexTreeNodes(oldChild, oldByPath)
			preserveTreeIDs(oldChild, fresh, oldByPath, nextID)
		}
		children = append(children, fresh)
	}
	sort.SliceStable(children, func(i, j int) bool { return sortNodeCmp(children[i], children[j]) < 0 })
	node.Children = children
	node.ChildrenNum = int32(len(entries))
	node.IDStart, node.IDEnd = treeIDRange(node)
	return nil
}

func treeIDRange(node *Node) (int32, int32) {
	if node == nil {
		return 0, 0
	}
	start, end := node.ID, node.ID
	for _, child := range node.Children {
		childStart, childEnd := treeIDRange(child)
		if childStart != 0 && (start == 0 || childStart < start) {
			start = childStart
		}
		if childEnd > end {
			end = childEnd
		}
	}
	return start, end
}

func cloneTreeNode(node *Node) *Node {
	if node == nil {
		return nil
	}
	clone := *node
	clone.Children = make([]*Node, 0, len(node.Children))
	for _, child := range node.Children {
		clone.Children = append(clone.Children, cloneTreeNode(child))
	}
	return &clone
}

func cloneTreeCache(cache *cacheStruct) *cacheStruct {
	if cache == nil {
		return nil
	}
	clone := *cache
	clone.TreeObj = cloneTreeNode(cache.TreeObj)
	clone.Entries = make(map[string]treeEntryState, len(cache.Entries))
	for path, state := range cache.Entries {
		clone.Entries[path] = state
	}
	return &clone
}

func incrementalTree(cache *cacheStruct, states map[string]treeEntryState, fingerprint string) (*cacheStruct, bool) {
	if cache == nil || cache.TreeObj == nil || len(cache.Entries) == 0 {
		return nil, false
	}
	for _, state := range states {
		if !state.Scanned {
			return nil, false
		}
	}
	for _, state := range cache.Entries {
		if !state.Scanned {
			return nil, false
		}
	}
	updated := cloneTreeCache(cache)
	nextID := maxTreeID(updated.TreeObj)
	if err := refreshDirectory(updated.TreeObj, cache.Entries, states, 0, &nextID); err != nil {
		return nil, false
	}
	updated.TreeString = BuildString(updated.TreeObj)
	updated.Entries = states
	updated.Fingerprint = fingerprint
	updated.Generation++
	updated.UpdatedAt = time.Now()
	return updated, true
}

func storeTreeCache(session *structs.Chats, cache *cacheStruct) {
	if session.TemporyDataOfSession == nil {
		session.TemporyDataOfSession = make(map[string]any)
	}
	entries, ok := session.TemporyDataOfSession[treeCacheKey].(map[string]*cacheStruct)
	if !ok {
		entries = make(map[string]*cacheStruct)
		session.TemporyDataOfSession[treeCacheKey] = entries
	}
	entries[cache.WorkPath] = cache
	if session.TemporyDataOfRequest == nil {
		session.TemporyDataOfRequest = make(map[string]any)
	}
	session.TemporyDataOfRequest[treeCacheKey] = cache
}

// InvalidateTreeCache 清理会话级 tree 快照。summary 完成或外部状态无法确认时调用。
func InvalidateTreeCache(session *structs.Chats) {
	if session == nil {
		return
	}
	delete(session.TemporyDataOfSession, treeCacheKey)
	delete(session.TemporyDataOfRequest, treeCacheKey)
}

func buildGlobalPrompt(session *structs.Chats) (string, error) {
	workPath, err := treeWorkPath(session)
	if err != nil {
		logger.Warn("tree get abs error: %v", err)
		return "", err
	}
	states, stateErr := treeStates(workPath)
	fingerprint := ""
	if stateErr == nil && treeStatesComplete(states) {
		fingerprint = treeStateFingerprint(states)
	}
	cache, exact := treeCache(session, workPath, fingerprint)
	if exact {
		storeTreeCache(session, cache)
	} else if cache != nil && fingerprint != "" {
		if updated, ok := incrementalTree(cache, states, fingerprint); ok {
			cache = updated
			storeTreeCache(session, cache)
		} else {
			cache = nil
		}
	}
	if cache == nil {
		treeID := int32(0)
		tree, errs := BuildTree(workPath, &treeID, 0)
		for idx, buildErr := range errs {
			logger.Warn("tree build error (%d/%d): %v", idx+1, len(errs), buildErr)
		}
		if tree == nil {
			if stateErr != nil {
				return "", stateErr
			}
			return "", fmt.Errorf("tree build returned nil")
		}
		tree.Name = "(root)"
		cache = &cacheStruct{
			TreeObj:     tree,
			TreeString:  BuildString(tree),
			WorkPath:    workPath,
			Fingerprint: fingerprint,
			Entries:     states,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
			Generation:  1,
		}
		storeTreeCache(session, cache)
	}

	str := cache.TreeString
	allLenStrLen := len(fmt.Sprintf("%d", len(str)))
	builder := strings.Builder{}
	for lineno, line := range strings.Split(str, "\n") {
		fmt.Fprintf(&builder, "%*d|%s\n", allLenStrLen, lineno+1, line)
	}

	rendered, err := prompts.Render(treeTempate, builder.String())
	if err != nil {
		return "", err
	}
	return rendered, nil
}

func buildPrompt(session *structs.Chats) (string, error) {
	return prompt, nil
}

func updateInfo(session *structs.Chats, mp map[string]*any, cross []*any, _ string) (bool, []*any, error) {
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

	workPath, pathErr := treeWorkPath(session)
	if pathErr != nil {
		boolx := false
		success := any(boolx)
		errMsg := any("Failed to resolve tree path: " + pathErr.Error())
		return false, cross, map[string]*any{"success": &success, "error": &errMsg}, nil
	}
	fingerprint, fingerprintErr := directoryFingerprint(workPath, 0)
	var ret any
	requestTypeError := false
	if fingerprintErr == nil {
		if cached, cacheOK := treeCache(session, workPath, fingerprint); cacheOK {
			ret = cached
		}
	}
	if ret == nil && session.TemporyDataOfRequest != nil {
		if requestCache, requestOK := session.TemporyDataOfRequest[treeCacheKey]; requestOK {
			cached, cacheOK := requestCache.(*cacheStruct)
			if !cacheOK {
				requestTypeError = true
			} else if cached != nil && cached.TreeObj != nil && cached.WorkPath == workPath && cached.Fingerprint == fingerprint {
				ret = cached
			}
		}
	}
	if requestTypeError {
		logger.Warn("struct type error (mustn't appear)")
		boolx := false
		success := any(boolx)
		errMsg := any("Struct type error")
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}
	if ret == nil {
		_, err := buildGlobalPrompt(session)
		if err != nil {
			boolx := false
			success := any(boolx)
			errMsg := any("Failed to rebuild tree cache: " + err.Error())
			return false, cross, map[string]*any{
				"success": &success,
				"error":   &errMsg,
			}, nil
		}
		ret = session.TemporyDataOfRequest[treeCacheKey]
	}
	rets, ok := ret.(*cacheStruct)
	if !ok {
		logger.Warn("struct type error (mustn't appear)")
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
		logger.Warn("text process error: %v", err)
		boolx := false
		success := any(boolx)
		errMsg := any(err.Error())
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	// 模型生成编辑内容期间目录可能被外部修改，旧快照不能继续执行。
	latestFingerprint, latestFingerprintErr := directoryFingerprint(workPath, 0)
	if latestFingerprintErr != nil {
		InvalidateTreeCache(session)
		boolx := false
		success := any(boolx)
		errMsg := any("Failed to verify tree state: " + latestFingerprintErr.Error())
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}
	if rets.Fingerprint == "" || latestFingerprint != rets.Fingerprint {
		InvalidateTreeCache(session)
		boolx := false
		success := any(boolx)
		errMsg := any("Tree changed during edit; please regenerate the tree and retry")
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	// 执行文件操作前检查取消信号（大量文件操作可能阻塞）
	if session.GetContext().Err() != nil {
		boolx := false
		success := any(boolx)
		errMsg := any("tree cancelled: " + session.GetContext().Err().Error())
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	_, err = SolveCall(workPath, rets.TreeObj, str)
	// fmt.Printf("\nTree diff: %v\n", diff)
	if err != nil {
		logger.Warn("act diff error: %v", err)
		boolx := false
		success := any(boolx)
		errMsg := any(err.Error())
		return false, cross, map[string]*any{
			"success": &success,
			"error":   &errMsg,
		}, nil
	}

	// SolveCall 已改变目录结构，旧快照中的节点 ID 和路径可能已失效。
	// 立即重建当前作用域，失败时删除旧缓存，避免后续请求复用过期数据。
	InvalidateTreeCache(session)
	if _, rebuildErr := buildGlobalPrompt(session); rebuildErr != nil {
		logger.Warn("tree cache rebuild after edit error: %v", rebuildErr)
		InvalidateTreeCache(session)
	}

	boolx := true
	success := any(boolx)
	return false, cross, map[string]*any{
		"success": &success,
	}, nil
}

func load() string {
	if err := actions.HookTool("", &toolobj.Hook{
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
	}); err != nil {
		panic(err)
	}
	if err := actions.HookTool("edit", &toolobj.Hook{
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
	}); err != nil {
		panic(err)
	}
	return toolName
}

func init() {
	index.AddIndex(load)
}
