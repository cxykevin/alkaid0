package tree

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cxykevin/alkaid0/tools/tools/tree/ios"
)

// MaxDepth 最大递归深度
const MaxDepth = 6

// MaxChildrenNum 最大子节点数
const MaxChildrenNum = 100

// Node 树节点
type Node struct {
	Name        string
	Children    []*Node
	Path        string
	IsDir       bool
	ChildrenNum int32
	Error       error
	ID          int32
	IDStart     int32
	IDEnd       int32
	RemoveFlag  bool
}

var dirBlacklistsOrigin = map[string]bool{
	// 明确 skip
	".alkaid0_skip": true,
	// git 目录
	".git": true,
	// alkaid 自身聊天记录
	".alkaid0": true,
	".alkaid":  true,
	// Agents 文件（应通过 memory 读取）
	".cursor":   true,
	".github":   true,
	"CLAUDE.md": true,
	"GEMINI.md": true,
	"AGENTS.md": true,
	"IFLOW.md":  true,
	// macos DS_Store
	"DS_Store":  true,
	".DS_Store": true,
	".Trash":    true,
	// windows Thumbs.db
	"Thumbs.db":   true,
	"desktop.ini": true,
}

var dirBlacklists = map[string]bool{}

func init() {
	// 复制
	maps.Copy(dirBlacklists, dirBlacklistsOrigin)
}

// BuildTree 构建树
func BuildTree(dir string, ID *int32) (*Node, []error) {
	errorsTable := []error{}
	info, err := os.Stat(dir)
	if err != nil {
		errorsTable = append(errorsTable, err)
		return &Node{
			Name:  filepath.Base(dir),
			Path:  dir,
			Error: err,
		}, errorsTable
	}
	if !info.IsDir() {
		(*ID)++
		return &Node{
			Name:  info.Name(),
			Path:  dir,
			IsDir: false,
			ID:    *ID,
		}, errorsTable
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		errorsTable = append(errorsTable, err)
		return &Node{
			Name:  info.Name(),
			Path:  dir,
			Error: err,
		}, errorsTable
	}
	node := &Node{
		Name:  info.Name(),
		Path:  dir,
		IsDir: true,
	}
	node.IDStart = *ID
	node.ChildrenNum = int32(len(entries))
	if node.ChildrenNum <= MaxChildrenNum {
		node.Children = []*Node{}
		for _, entry := range entries {
			entryName := entry.Name()
			if _, ok := dirBlacklists[entryName]; ok {
				continue
			}
			subnode, errorsSubTable := BuildTree(filepath.Join(dir, entryName), ID)
			if len(errorsSubTable) > 0 {
				errorsTable = append(errorsTable, errorsSubTable...)
			}
			node.Children = append(node.Children, subnode)
		}
		// 排序
		slices.SortFunc(node.Children, func(i, j *Node) int {
			return cmp.Compare(i.Name, j.Name)
		})
	}
	node.IDEnd = *ID
	return node, errorsTable
}

// BuildString 构建字符串
func BuildString(node *Node) string {
	// 格式:
	/*
		foo
		  - bar `1`
		  !ERROR: some_err_result
		hello
		  home
		  - world `2`
		  - world2 `3`
		test
		  ... (100 files)
	*/
	var builder strings.Builder
	buildStringRecursive(node, "", &builder)
	return builder.String()
}

const indentString = "    "

func buildStringRecursive(node *Node, prefix string, builder *strings.Builder) {

	// 写入当前节点名称
	builder.WriteString(prefix)
	if !node.IsDir && node.ID > 0 {
		builder.WriteString("- ")
	}
	builder.WriteString(node.Name)

	// 如果有错误，显示错误信息
	if node.Error != nil {
		builder.WriteString("\n")
		builder.WriteString(prefix)
		builder.WriteString("!ERROR: ")
		builder.WriteString(node.Error.Error())
		return
	}

	// 如果是文件，显示ID
	if !node.IsDir && node.ID > 0 {
		builder.WriteString(" '")
		builder.WriteString(strconv.Itoa(int(node.ID)))
		builder.WriteString("'")
	}

	// 处理子节点
	if node.IsDir {
		// 如果有大量子节点，显示省略号
		if node.ChildrenNum > MaxChildrenNum {
			builder.WriteString("\n")
			builder.WriteString(prefix)
			builder.WriteString("... (")
			builder.WriteString(strconv.Itoa(int(node.ChildrenNum)))
			builder.WriteString(" files)")
			return
		}

		// 递归处理子节点
		for i, child := range node.Children {
			builder.WriteString("\n")

			// 判断是否是最后一个子节点
			isLast := i == len(node.Children)-1

			// 构建子节点的前缀
			var childPrefix string
			if isLast {
				childPrefix = prefix + indentString
			} else {
				childPrefix = prefix + indentString
			}

			// 递归构建子节点字符串
			buildStringRecursive(child, childPrefix, builder)
		}
	}
}

func sortNodes(nodes []*Node) {
	slices.SortFunc(nodes, func(i, j *Node) int {
		return cmp.Compare(i.Name, j.Name)
	})
	// 递归排序子节点
	for _, node := range nodes {
		sortNodes(node.Children)
	}
}

// BuildNodeFromString 从字符串构建节点
func BuildNodeFromString(str string) (*Node, error) {
	str = strings.ReplaceAll(str, "\t", indentString)
	lines := strings.Split(str, "\n")
	nodeTreeList := []*Node{}
	nodeTreeList = append(nodeTreeList, &Node{
		Name:     "", // FakeRoot
		Path:     "FakeRoot",
		Children: []*Node{},
		IsDir:    true,
	})
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		// 计算Indent
		indentLn := 0
		for _, char := range ln {
			if char != ' ' {
				break
			}
			indentLn++
		}
		if (indentLn % len(indentString)) != 0 {
			return nil, errors.New("invalid indent")
		}
		indent := indentLn / len(indentString)
		nameTemp := strings.TrimPrefix(strings.TrimSpace(ln), "- ")
		if strings.HasPrefix(nameTemp, "!ERROR") || strings.HasPrefix(nameTemp, "...") {
			continue
		}
		name := nameTemp
		id := int32(0)
		if strings.Contains(nameTemp, "'") {
			nameTemps := strings.Split(nameTemp, "'")
			if (len(nameTemps)) != 3 || nameTemps[2] != "" {
				return nil, errors.New("invalid name")
			}
			name = strings.TrimSpace(nameTemps[0])
			idTmp, err := strconv.Atoi(nameTemps[1])
			if err != nil {
				return nil, err
			}
			id = int32(idTmp)
		}
		if strings.Contains(name, "/") ||
			strings.Contains(name, "\\") ||
			strings.Contains(name, ":") ||
			strings.Contains(name, "*") ||
			strings.Contains(name, "?") ||
			strings.Contains(name, "\"") ||
			strings.Contains(name, "<") ||
			strings.Contains(name, ">") ||
			strings.Contains(name, "|") ||
			strings.Contains(name, "\n") ||
			strings.Contains(name, "\r") ||
			strings.Contains(name, "\t") ||
			strings.Contains(name, "..") {
			return nil, errors.New("path must be a correct and relative path")
		}
		if len(nodeTreeList) <= indent {
			return nil, errors.New("indent too deep")
		}

		if _, ok := dirBlacklists[name]; ok {
			return nil, fmt.Errorf("the '%s' file is not allowed to operate", name)
		}

		nodeTreeList = nodeTreeList[:indent+1]
		// 插入
		// 构造路径
		path := ""
		for idx, node := range nodeTreeList {
			if idx <= 1 {
				continue
			}
			path = filepath.Join(path, node.Name)
		}
		path = filepath.Join(path, name)
		newNode := &Node{
			Name:     name,
			ID:       id,
			IsDir:    (id == 0),
			Children: []*Node{},
			Path:     path,
		}
		nodeTreeList[indent].Children = append(nodeTreeList[indent].Children, newNode)
		nodeTreeList = append(nodeTreeList, newNode)
	}
	if len(nodeTreeList[0].Children) == 0 {
		return nil, errors.New("empty tree")
	}
	if len(nodeTreeList[0].Children) != 1 {
		return nil, errors.New("invalid tree (multiple root nodes)")
	}
	// 递归排序
	sortNodes(nodeTreeList[0].Children[0].Children)
	return nodeTreeList[0].Children[0], nil
}

func solveNodesTreeDiff(result *Node, origin *Node, target *Node) error {
	// 将 origin 和 target 的差异合并到 result

	// 收集 target 中所有文件 ID 到目标相对路径的映射（用于检测移动）
	targetIDMap := map[int32]string{}
	var collectTargetIDs func(n *Node)
	collectTargetIDs = func(n *Node) {
		if n == nil {
			return
		}
		if !n.IsDir {
			if n.ID > 0 {
				targetIDMap[n.ID] = n.Path
			}
			return
		}
		for _, c := range n.Children {
			collectTargetIDs(c)
		}
	}
	collectTargetIDs(target)

	// 递归函数，闭包捕获 targetIDMap
	var walk func(res *Node, o *Node, t *Node) error
	walk = func(res *Node, o *Node, t *Node) error {
		// 准备子节点映射
		originMap := map[string]*Node{}
		targetMap := map[string]*Node{}
		if o != nil {
			for _, c := range o.Children {
				originMap[c.Name] = c
			}
		}
		if t != nil {
			for _, c := range t.Children {
				targetMap[c.Name] = c
			}
		}

		// 收集所有名字，排序以保证稳定性
		nameSet := map[string]bool{}
		for k := range originMap {
			nameSet[k] = true
		}
		for k := range targetMap {
			nameSet[k] = true
		}
		names := []string{}
		for k := range nameSet {
			names = append(names, k)
		}
		sort.Strings(names)

		for _, name := range names {
			oc := originMap[name]
			tc := targetMap[name]

			// 跳过空节点
			if oc == nil && tc == nil {
				continue
			}

			// 如果 origin 节点有错误或是省略节点（ChildrenNum 太大），则跳过删除/覆盖，保留 origin
			if oc != nil && (oc.Error != nil || oc.ChildrenNum > MaxChildrenNum) {
				// 直接拷贝 origin 到结果
				clone := &Node{
					Name:        oc.Name,
					Path:        oc.Path,
					IsDir:       oc.IsDir,
					Children:    []*Node{},
					ID:          oc.ID,
					ChildrenNum: oc.ChildrenNum,
					Error:       oc.Error,
				}
				for _, c := range oc.Children {
					clone.Children = append(clone.Children, &Node{Name: c.Name, Path: c.Path, IsDir: c.IsDir, ID: c.ID})
				}
				res.Children = append(res.Children, clone)
				continue
			}

			// origin 存在但 target 不存在 -> 标记删除（文件或目录）
			if oc != nil && tc == nil {
				// 如果是文件且该文件的 ID 在 target 中被引用到其它路径，则也应删除原位置（移动场景）
				del := &Node{
					Name:       oc.Name,
					Path:       oc.Path,
					IsDir:      oc.IsDir,
					Children:   []*Node{},
					ID:         oc.ID,
					RemoveFlag: true,
				}
				res.Children = append(res.Children, del)
				continue
			}

			// origin 不存在但 target 存在 -> 添加目标节点（可能是新文件或目录）
			if oc == nil && tc != nil {
				if tc.IsDir {
					newDir := &Node{Name: tc.Name, Path: tc.Path, IsDir: true, Children: []*Node{}}
					if err := walk(newDir, &Node{IsDir: true, Children: []*Node{}}, tc); err != nil {
						return err
					}
					res.Children = append(res.Children, newDir)
				} else {
					newFile := &Node{Name: tc.Name, Path: tc.Path, IsDir: false, ID: tc.ID}
					res.Children = append(res.Children, newFile)
				}
				continue
			}

			// 两边都存在的情况
			if oc != nil && tc != nil {
				// 都是目录 -> 递归
				if oc.IsDir && tc.IsDir {
					newDir := &Node{Name: tc.Name, Path: tc.Path, IsDir: true, Children: []*Node{}}
					if err := walk(newDir, oc, tc); err != nil {
						return err
					}
					res.Children = append(res.Children, newDir)
					continue
				}

				// 都是文件 -> 处理 ID 变化和移动
				if !oc.IsDir && !tc.IsDir {
					// 若 ID 相同或目标未指定 ID，则视为保留（若目标未指定且 origin 有 ID，则保留 origin ID）
					if tc.ID == oc.ID || (tc.ID == 0 && oc.ID > 0) {
						keep := &Node{Name: tc.Name, Path: tc.Path, IsDir: false, ID: oc.ID}
						res.Children = append(res.Children, keep)
						continue
					}

					// ID 不同或目标指定了新的 ID -> 需要删除原位置并创建目标位置
					// 删除原文件
					del := &Node{Name: oc.Name, Path: oc.Path, IsDir: false, ID: oc.ID, RemoveFlag: true}
					res.Children = append(res.Children, del)
					// 在目标位置创建新文件（可能引用某个 origin ID 用于复制/移动）
					newFile := &Node{Name: tc.Name, Path: tc.Path, IsDir: false, ID: tc.ID}
					res.Children = append(res.Children, newFile)
					continue
				}

				// 类型不一致：目录->文件 或 文件->目录，需要同时删除原项并创建目标项
				if oc.IsDir && !tc.IsDir {
					// 删除目录
					del := &Node{Name: oc.Name, Path: oc.Path, IsDir: true, RemoveFlag: true}
					res.Children = append(res.Children, del)
					// 创建文件
					newFile := &Node{Name: tc.Name, Path: tc.Path, IsDir: false, ID: tc.ID}
					res.Children = append(res.Children, newFile)
					continue
				}
				if !oc.IsDir && tc.IsDir {
					del := &Node{Name: oc.Name, Path: oc.Path, IsDir: false, ID: oc.ID, RemoveFlag: true}
					res.Children = append(res.Children, del)
					newDir := &Node{Name: tc.Name, Path: tc.Path, IsDir: true, Children: []*Node{}}
					if err := walk(newDir, &Node{IsDir: true, Children: []*Node{}}, tc); err != nil {
						return err
					}
					res.Children = append(res.Children, newDir)
					continue
				}
			}
		}
		return nil
	}

	// 启动递归
	if err := walk(result, origin, target); err != nil {
		return err
	}

	// 对结果排序并返回
	sortNodes(result.Children)
	return nil
}

// DiffStatus 差异表状态
type DiffStatus int8

// 差异类型
const (
	DiffStatusCreateDir DiffStatus = iota
	DiffStatusCreateFile
	DiffStatusMove
	DiffStatusCopy
	DiffStatusDelete
)

// Diff 执行差异表
type Diff struct {
	Origin string
	Target string
	Type   DiffStatus
}

// diffOriginStatus 原始格式枚举
type diffOriginStatus int8

// 枚举
const (
	// 枚举同样是origin优先级
	diffOriginStatusCreateDir  diffOriginStatus = 0
	diffOriginStatusCreateFile diffOriginStatus = 1
	diffOriginStatusDeleteDir  diffOriginStatus = 2
	diffOriginStatusDeleteFile diffOriginStatus = 3
)

type diffOrigin struct {
	ID     int32
	Target string
	Type   diffOriginStatus
}

type idMapOrigin map[int32]string

func generateDiffNodes(diffObj *[]diffOrigin, node *Node) {
	if node.IsDir {
		if node.RemoveFlag {
			*diffObj = append(*diffObj, diffOrigin{
				Target: node.Path,
				Type:   diffOriginStatusDeleteDir,
			})
			// 直接短路，不用再递归
			return
		}
		for _, child := range node.Children {
			generateDiffNodes(diffObj, child)
		}
		if len(node.Children) == 0 { // 叶子节点
			*diffObj = append(*diffObj, diffOrigin{
				Target: node.Path,
				Type:   diffOriginStatusCreateDir,
			})
		}
		return
	}
	if node.RemoveFlag {
		*diffObj = append(*diffObj, diffOrigin{
			ID:     node.ID,
			Target: node.Path,
			Type:   diffOriginStatusDeleteFile,
		})
		return
	}
	*diffObj = append(*diffObj, diffOrigin{
		ID:     node.ID,
		Target: node.Path,
		Type:   diffOriginStatusCreateFile,
	})
}
func generateIDMap(mapOrigin *idMapOrigin, node *Node) {
	if node.IsDir {
		for _, child := range node.Children {
			generateIDMap(mapOrigin, child)
		}
		return
	}
	(*mapOrigin)[node.ID] = node.Path
}

func checkNodeFileNameErr(node *Node) error {
	if !node.IsDir {
		return nil
	}
	mp := map[string]bool{}
	for _, child := range node.Children {
		if _, ok := mp[child.Name]; ok {
			return fmt.Errorf("duplicate file name: %s", child.Name)
		}
		mp[child.Name] = true
		err := checkNodeFileNameErr(child)
		if err != nil {
			return err
		}
	}
	return nil
}

var rander = rand.New(rand.NewSource(time.Now().UnixNano()))

func generateDiff(origin *Node, node *Node) ([]Diff, error) {
	diffOrigins := []diffOrigin{}
	mapOrigin := idMapOrigin{}

	generateIDMap(&mapOrigin, origin)
	generateDiffNodes(&diffOrigins, node)

	obj := []Diff{}
	// origin优化
	// 排序
	sort.Slice(diffOrigins, func(i, j int) bool {
		return diffOrigins[i].Type < diffOrigins[j].Type
	})

	// 将 origin 转化为合理的文件操作
	// 要求：操作之间不冲突，如文件改ID（即同一文件先删后加），先删后从别的地方复制

	// 收集不同类型的操作
	createDirs := []string{}
	createFiles := []diffOrigin{}
	deleteFiles := []diffOrigin{}
	deleteDirs := []string{}

	for _, d := range diffOrigins {
		switch d.Type {
		case diffOriginStatusCreateDir:
			createDirs = append(createDirs, d.Target)
		case diffOriginStatusCreateFile:
			createFiles = append(createFiles, d)
		case diffOriginStatusDeleteFile:
			deleteFiles = append(deleteFiles, d)
		case diffOriginStatusDeleteDir:
			deleteDirs = append(deleteDirs, d.Target)
		}
	}

	// 先创建目录
	for _, p := range createDirs {
		obj = append(obj, Diff{Target: p, Type: DiffStatusCreateDir})
	}

	// 临时文件名表
	tmpFilePathTable := map[string]string{}

	// 复制文件
	for _, d := range createFiles {
		originPath, ok := mapOrigin[d.ID]
		if !ok {
			obj = append(obj, Diff{Target: d.Target, Type: DiffStatusCreateFile})
			continue
		}
		// 生成随机文件名
		randNum := rander.Intn(8192)
		tmpFileName := fmt.Sprintf(".alk_%d.%s", randNum, filepath.Base(d.Target))
		tmpFilePath := filepath.Join(filepath.Dir(d.Target), tmpFileName)
		tmpFilePathTable[d.Target] = tmpFilePath
		obj = append(obj, Diff{Origin: originPath, Target: tmpFilePath, Type: DiffStatusCopy})
	}

	// 删除文件
	for _, d := range deleteFiles {
		obj = append(obj, Diff{Target: d.Target, Type: DiffStatusDelete})
	}

	// 删除路径
	for _, p := range deleteDirs {
		obj = append(obj, Diff{Target: p, Type: DiffStatusDelete})
	}

	// 移动文件
	for real, tmpdist := range tmpFilePathTable {
		obj = append(obj, Diff{Origin: tmpdist, Target: real, Type: DiffStatusMove})
	}

	return obj, nil
}

const permission = 0755

func solveDiffTask(path string, diff []Diff) error {
	for _, d := range diff {
		switch d.Type {
		case DiffStatusCreateDir:
			err := os.MkdirAll(filepath.Join(d.Target, path), permission)
			if err != nil {
				return err
			}
		case DiffStatusCreateFile:
			// 写空文件
			err := os.WriteFile(filepath.Join(d.Target, path), []byte{}, permission)
			if err != nil {
				return err
			}
		case DiffStatusCopy:
			err := ios.Copy(filepath.Join(d.Origin, path), filepath.Join(d.Target, path))
			if err != nil {
				return err
			}
		case DiffStatusDelete:
			err := os.RemoveAll(filepath.Join(d.Target, path))
			if err != nil {
				return err
			}
		case DiffStatusMove:
			err := os.Rename(filepath.Join(d.Origin, path), filepath.Join(d.Target, path))
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// SolveCall 解决调用
func SolveCall(path string, node *Node, dist string) ([]Diff, error) {
	distNode, err := BuildNodeFromString(dist)
	if err != nil {
		return nil, fmt.Errorf("error in parse string (%v)", err)
	}

	err = checkNodeFileNameErr(distNode)
	if err != nil {
		return nil, fmt.Errorf("error in check file name (%v)", err)
	}

	diffNode := &Node{
		IsDir: true,
	}

	solveNodesTreeDiff(diffNode, node, distNode)
	diff, err := generateDiff(node, diffNode)
	if err != nil {
		return nil, fmt.Errorf("error in calculate diff (%v)", err)
	}
	err = solveDiffTask(path, diff)
	if err != nil {
		return nil, fmt.Errorf("error in act (%v)", err)
	}
	return diff, nil
}
