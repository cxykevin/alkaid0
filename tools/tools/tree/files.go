package tree

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
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
}

var dirBlacklists = map[string]bool{
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

// BuildTree 构建树
func BuildTree(dir string, ID *int32) *Node {
	info, err := os.Stat(dir)
	if err != nil {
		return &Node{
			Name:  filepath.Base(dir),
			Path:  dir,
			Error: err,
		}
	}
	if !info.IsDir() {
		(*ID)++
		return &Node{
			Name:  info.Name(),
			Path:  dir,
			IsDir: false,
			ID:    *ID,
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return &Node{
			Name:  info.Name(),
			Path:  dir,
			Error: err,
		}
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
			subnode := BuildTree(filepath.Join(dir, entryName), ID)
			node.Children = append(node.Children, subnode)
		}
		// 排序
		slices.SortFunc(node.Children, func(i, j *Node) int {
			return cmp.Compare(i.Name, j.Name)
		})
	}
	node.IDEnd = *ID
	return node
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

// DiffObj 差分对象
type DiffObj struct {
	OriginID    int32
	Origin      string
	Target      string
	Mode        string // "add" or "delete"
	HasOmission bool   // 标记目录是否包含省略节点
}

// parseDistString 解析 dist 字符串，返回路径-ID 映射（不包含根节点）
func parseDistString(dist string) map[string]int32 {
	result := make(map[string]int32)
	if strings.TrimSpace(dist) == "" {
		return result
	}

	lines := strings.Split(dist, "\n")
	if len(lines) == 0 {
		return result
	}

	// 跳过第一行（根节点）
	if len(lines) > 0 {
		lines = lines[1:]
	}

	// 使用栈来跟踪当前路径
	type pathInfo struct {
		path   string
		indent int
	}
	var pathStack []pathInfo
	currentPath := ""

	// 动态检测缩进大小（找到第一个非空行的缩进）
	indentSize := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			for i := 0; i < len(line); i++ {
				if line[i] == ' ' {
					indentSize++
				} else {
					break
				}
			}
			break
		}
	}
	if indentSize == 0 {
		indentSize = len(indentString) // 默认使用4个空格
	}

	for _, line := range lines {
		trimmed := strings.TrimRight(line, " ")
		if trimmed == "" {
			continue
		}

		// 计算缩进级别
		indent := 0
		for i := 0; i < len(line); i++ {
			if line[i] == ' ' {
				indent++
			} else {
				break
			}
		}
		indentLevel := indent / indentSize

		// 调整路径栈
		for len(pathStack) > 0 && pathStack[len(pathStack)-1].indent >= indentLevel {
			pathStack = pathStack[:len(pathStack)-1]
		}

		if len(pathStack) > 0 {
			currentPath = pathStack[len(pathStack)-1].path
		} else {
			currentPath = ""
		}

		// 提取名称和ID
		content := strings.TrimSpace(trimmed)

		// 检查是否是省略节点
		if strings.Contains(content, "... (") {
			continue
		}

		// 检查是否是错误节点
		if strings.Contains(content, "!ERROR: ") {
			continue
		}

		// 检查是否是文件行（包含 'ID' 格式）
		if strings.Contains(content, " '") {
			// 文件行，可能格式: "- filename 'ID'" 或 "filename 'ID'"
			if after, ok := strings.CutPrefix(content, "- "); ok {
				content = after
			}
			// 查找最后一个空格，分割文件名和ID
			lastSpace := strings.LastIndex(content, " '")
			if lastSpace != -1 {
				name := content[:lastSpace]
				idStr := strings.TrimSuffix(content[lastSpace+2:], "'")
				id, err := strconv.ParseInt(idStr, 10, 32)
				if err == nil {
					path := name
					if currentPath != "" {
						path = currentPath + "/" + name
					}
					result[path] = int32(id)
				}
			}
		} else {
			// 目录行
			dirName := content
			path := dirName
			if currentPath != "" {
				path = currentPath + "/" + dirName
			}
			pathStack = append(pathStack, pathInfo{path: path, indent: indentLevel})
		}
	}

	return result
}

// buildPathMap 将 node 树转换为路径-ID 映射（不包含根节点）
func buildPathMap(node *Node) map[string]int32 {
	result := make(map[string]int32)
	if node == nil {
		return result
	}

	// 递归遍历函数
	var traverse func(n *Node, currentPath string)
	traverse = func(n *Node, currentPath string) {
		for _, child := range n.Children {
			path := child.Name
			if currentPath != "" {
				path = currentPath + "/" + child.Name
			}

			if !child.IsDir && child.ID > 0 {
				// 只包含文件节点且ID有效的
				result[path] = child.ID
			} else if child.IsDir && len(child.Children) > 0 {
				// 递归遍历目录
				traverse(child, path)
			}
		}
	}

	traverse(node, "")
	return result
}

// SolveCall 解决调用
func SolveCall(node *Node, dist string) ([]DiffObj, error) {
	// 根据AI编辑完的字符串string进行更改差分

	// 构建原始路径映射
	originalMap := buildPathMap(node)

	// 解析目标字符串
	distMap := parseDistString(dist)

	// 生成差分对象
	var diffObjs []DiffObj

	// 检查删除和修改（在原始中存在，但在目标中不存在或ID不同）
	for path, originalID := range originalMap {
		distID, exists := distMap[path]
		if !exists {
			// 文件被删除
			diffObjs = append(diffObjs, DiffObj{
				OriginID: originalID,
				Origin:   path,
				Mode:     "delete",
			})
		} else if distID != originalID {
			// ID被修改，需要删除旧ID并添加新ID
			diffObjs = append(diffObjs, DiffObj{
				OriginID: originalID,
				Origin:   path,
				Mode:     "delete",
			})
			diffObjs = append(diffObjs, DiffObj{
				OriginID: distID,
				Target:   path,
				Mode:     "add",
			})
		}
		// 如果ID相同，不需要操作
	}

	// 检查添加（在目标中存在，但在原始中不存在）
	for path, distID := range distMap {
		if _, exists := originalMap[path]; !exists {
			// 文件被添加
			diffObjs = append(diffObjs, DiffObj{
				OriginID: distID,
				Target:   path,
				Mode:     "add",
			})
		}
	}

	return diffObjs, nil
}
