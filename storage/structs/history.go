package structs

import (
	"strconv"
	"strings"
)

// HistoryPathPrefix 历史会话虚拟对象路径前缀。
// `@history/{session_id}` 由 read 工具读取同一工作目录下另一个会话的最近消息，
// search 工具在 history 模式下也用它把搜索限定在单个会话内。
const HistoryPathPrefix = "@history/"

// ParseHistoryPath 解析历史会话虚拟对象路径（@history/{session_id}）。
// 只接受纯数字 id：`@history/1/2`、`@history/abc` 都视为非法路径。
func ParseHistoryPath(path string) (uint32, bool) {
	rest, ok := strings.CutPrefix(path, HistoryPathPrefix)
	if !ok || rest == "" || strings.ContainsAny(rest, "/\\") {
		return 0, false
	}
	id, err := strconv.ParseUint(rest, 10, 32)
	if err != nil || id == 0 {
		return 0, false
	}
	return uint32(id), true
}

// IsHistoryPath 判断 path 是否为合法的历史会话虚拟对象路径。
func IsHistoryPath(path string) bool {
	_, ok := ParseHistoryPath(path)
	return ok
}

// HistoryPath 构造历史会话虚拟对象路径。
func HistoryPath(sessionID uint32) string {
	return HistoryPathPrefix + strconv.FormatUint(uint64(sessionID), 10)
}
