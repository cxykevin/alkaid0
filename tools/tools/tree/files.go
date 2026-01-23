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

// SolveCall 解决调用
func SolveCall(node *Node, dist string) ([]DiffObj, error) {
	// 根据AI编辑完的字符串string进行更改差分
}
