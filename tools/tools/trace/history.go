package trace

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
)

const (
	// HistoryMessageLimit 历史会话虚拟对象保留的消息条数（只算用户消息与 AI 回复）。
	HistoryMessageLimit = 16
	// historyContentMaxBytes 单次注入的历史会话内容上限。
	// 必须低于 MaxFileSize：renderTraceFileFragment 会丢弃超限的内容块。
	historyContentMaxBytes = 200 * 1024
	// historyContentMaxLines 行数上限，低于 MaxFileLine。
	historyContentMaxLines = 8000
)

// historySessionContent 读取 @history/{session_id} 的实时内容。
//
// 内容 = 该会话活跃分支上最后 HistoryMessageLimit 条「用户消息 + AI 回复」，按时间正序渲染。
// 每轮构建上下文时重新计算，因此其它会话继续对话时这里会实时跟进；插入 trace 后由差分
// 机制只追加新消息（见 decideHistoryDiffPlan）。
func historySessionContent(session *structs.Chats, path string) (string, error) {
	id, ok := structs.ParseHistoryPath(path)
	if !ok {
		return "", fmt.Errorf("invalid history path %q: expected @history/{session_id}", path)
	}
	if session == nil || session.DB == nil {
		return "", errors.New("session database is not available")
	}

	var chat structs.Chats
	if err := session.DB.Select("id", "title", "ai_title").First(&chat, id).Error; err != nil {
		return "", fmt.Errorf("history session %d not found in this workspace", id)
	}

	var messages []structs.Messages
	if err := structs.OnActiveBranch(session.DB, id).
		Where("type IN (?, ?)", structs.MessagesRoleUser, structs.MessagesRoleAgent).
		Order("id DESC").
		Limit(HistoryMessageLimit).
		Find(&messages).Error; err != nil {
		return "", fmt.Errorf("load history session %d: %w", id, err)
	}
	if len(messages) == 0 {
		return "", fmt.Errorf("history session %d has no user/assistant messages", id)
	}
	// id DESC → 时间正序
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	title := strings.TrimSpace(chat.Title)
	if title == "" {
		title = strings.TrimSpace(chat.AITitle)
	}
	header := fmt.Sprintf("[session %d]", chat.ID)
	if title != "" {
		header += " " + title
	}
	header += "\n\n"

	entries := make([]string, 0, len(messages))
	for _, msg := range messages {
		role := "User"
		if msg.Type == structs.MessagesRoleAgent {
			role = "Assistant"
		}
		body := strings.TrimRight(msg.Delta, "\n")
		if body == "" {
			continue
		}
		entries = append(entries, fmt.Sprintf("[%s #%d]\n%s\n", role, msg.ID, body))
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("history session %d has no user/assistant messages", id)
	}
	return trimHistoryContent(header, entries), nil
}

// trimHistoryContent 从最旧的消息开始丢弃，保证内容不超过字节/行数上限。
// 单条消息本身超限时再做尾部截断（保留开头，标记 truncated）。
func trimHistoryContent(header string, entries []string) string {
	for {
		content := header + strings.Join(entries, "\n")
		if len(entries) <= 1 {
			return truncateHistoryContent(content)
		}
		if len(content) <= historyContentMaxBytes && strings.Count(content, "\n")+1 <= historyContentMaxLines {
			return content
		}
		entries = entries[1:]
	}
}

const historyTruncatedMarker = "\n... (truncated)"

// truncateHistoryContent 兜底截断（按行、按 rune 边界），保持 UTF-8 完整。
func truncateHistoryContent(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) > historyContentMaxLines {
		lines = lines[:historyContentMaxLines]
		content = strings.Join(lines, "\n") + historyTruncatedMarker
	}
	if len(content) <= historyContentMaxBytes {
		return content
	}
	var b strings.Builder
	size := 0
	for _, r := range content {
		n := len(string(r))
		if size+n+len(historyTruncatedMarker) > historyContentMaxBytes {
			break
		}
		b.WriteRune(r)
		size += n
	}
	return b.String() + historyTruncatedMarker
}

// decideHistoryDiffPlan 历史会话的差分方案：增量只包含新增的消息。
//
// 历史会话是「最后 N 条消息」的滚动窗口——新消息进来会把最旧的消息挤出窗口。
// 那部分删除不需要告诉模型（旧块里已经有这些消息），把它们写成 unified diff 的
// 删除行只会浪费 token；因此这里只截取新内容相对于旧块的追加部分。
// 找不到重叠（窗口被完全换掉）时返回 keep=false，退回完整内容块。
func decideHistoryDiffPlan(path, oldContent, newContent string, timeout bool, mult float32) (DiffPlan, bool) {
	if timeout || oldContent == "" || oldContent == newContent {
		return DiffPlan{}, false
	}
	inc, ok := historyIncrement(oldContent, newContent)
	if !ok {
		return DiffPlan{}, false
	}
	dTok := u.EstimateTokens(inc)
	oTok := u.EstimateTokens(oldContent)
	nTok := u.EstimateTokens(newContent)
	// 与普通文件一致：增量过大时放弃保留（上下文膨胀安全阀）。
	if float64(dTok) > float64(oTok)*diffMaxTOverOldRatio {
		return DiffPlan{}, false
	}
	oldBlock, ok := renderTraceFileFragment(path, oldContent)
	if !ok {
		return DiffPlan{}, false
	}
	return DiffPlan{
		Keep:     true,
		OldBlock: oldBlock,
		DiffBlock: FileBlock{
			Name:   path,
			Size:   strconv.Itoa(len(inc)),
			Length: uint32(len(inc)),
			Text:   inc,
			Type:   "update",
		},
		oTok: oTok,
		nTok: nTok,
		dTok: dTok,
		mult: mult,
	}, true
}

// historyIncrement 计算滚动窗口新增的文本（不包含滚出窗口的旧消息）。
//
// 内容结构固定为「头部 + 消息列表」，头部（会话 id/标题）保持不变，不能参与行对齐，
// 因此先定位消息列表的起始行。旧窗口 = A，新窗口 = B：B 的头部是 A 尾部保留下来的消息，
// 所以找最大的 k 使 oldBody 的最后 k 行 == newBody 的前 k 行，之后的即新增部分。
// 最后一条消息在流式生成时只是变长，同样满足该关系（旧行是 new 的前缀）。
func historyIncrement(oldContent, newContent string) (string, bool) {
	if oldContent == "" || newContent == "" {
		return "", false
	}
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")
	o := historyBodyStart(oldLines)
	n := historyBodyStart(newLines)
	if o < 0 || n < 0 {
		return "", false
	}
	oldBody := oldLines[o:]
	newBody := newLines[n:]
	if len(newBody) == 0 {
		return "", false
	}

	// 重叠区必须从 newBody 的第 0 行开始，而这一行是带消息 id 的角色头（唯一），
	// 用它作为锚点在 oldBody 中定位旧窗口的对应起点，避免逐 k 全量比较。
	target := newBody[0]
	for p := 0; p < len(oldBody); p++ {
		if oldBody[p] != target {
			continue
		}
		k := len(oldBody) - p
		if k >= len(newBody) {
			// 没有新增行（窗口只收缩 / 内容完全相同），交回完整内容块
			if k == len(newBody) && slices.Equal(oldBody[p:], newBody) {
				return "", false
			}
			continue
		}
		if slices.Equal(oldBody[p:], newBody[:k]) {
			inc := strings.Join(newBody[k:], "\n")
			if strings.TrimSpace(inc) == "" {
				return "", false
			}
			return inc, true
		}
	}
	return "", false
}

// historyBodyStart 返回消息列表的起始行号（第一个 "[User #id]" / "[Assistant #id]" 头），
// 头部（"[session N] 标题" 与空行）不参与滚动窗口的行对齐。
func historyBodyStart(lines []string) int {
	for i, line := range lines {
		if isHistoryEntryHeader(line) {
			return i
		}
	}
	return -1
}

func isHistoryEntryHeader(line string) bool {
	if !strings.HasSuffix(line, "]") {
		return false
	}
	return strings.HasPrefix(line, "[User #") || strings.HasPrefix(line, "[Assistant #")
}
