package search

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cxykevin/alkaid0/storage/structs"
	"gorm.io/gorm"
)

// 搜索模式（search 工具的 type 参数）。
const (
	searchModeLocal   = "local"
	searchModeOnline  = "online"
	searchModeHistory = "history"
)

// getSearchType 读取并校验 type 参数。
func getSearchType(mp map[string]*any) (string, error) {
	v, err := getStringParam(mp, "type")
	if err != nil {
		return "", err
	}
	mode := strings.ToLower(strings.TrimSpace(v))
	switch mode {
	case searchModeLocal, searchModeOnline, searchModeHistory:
		return mode, nil
	}
	return "", fmt.Errorf("invalid type %q: must be %s, %s or %s", v, searchModeLocal, searchModeOnline, searchModeHistory)
}

const (
	historySnippetRadius    = 80  // 命中位置两侧保留的字节数
	historySnippetsPerChat  = 3   // 每个会话最多展示的命中片段
	historySnippetMaxRunes  = 240 // 无命中位置时截取的最大字符数
	historyMaxMessageScan   = 200 // 内容检索最多扫描的消息条数
	historyMaxResultsLimit  = 50
	historyDefaultMaxResult = 10
)

// historySnippet 一条命中消息的摘要。
type historySnippet struct {
	MsgID uint64
	Role  string
	Text  string
}

// historyMatch 一个命中的会话。
type historyMatch struct {
	ChatID       uint32
	Title        string
	UpdatedAt    time.Time
	MatchedID    bool
	MatchedTitle bool
	Snippets     []historySnippet
}

// runHistorySearch 执行 history 模式搜索：
//   - 按标题（Title / AITitle）与 session id 匹配会话；
//   - 按消息内容（只算用户消息与 AI 回复）匹配，并给出命中片段；
//   - path=@history/{session_id} 时只在该会话内检索。
func runHistorySearch(session *structs.Chats, mp map[string]*any, cross []*any, query string) (bool, []*any, map[string]*any, error) {
	if session == nil || session.DB == nil {
		return errResult("session database is not available", cross)
	}

	maxResults, _ := getIntParamDefault(mp, "max_results", historyDefaultMaxResult)
	if maxResults <= 0 {
		maxResults = historyDefaultMaxResult
	}
	if maxResults > historyMaxResultsLimit {
		maxResults = historyMaxResultsLimit
	}

	var scopeID uint32
	if searchPath, _ := getStringParamDefault(mp, "path", ""); searchPath != "" {
		id, ok := structs.ParseHistoryPath(searchPath)
		if !ok {
			return errResult("in history mode, path must be @history/{session_id}", cross)
		}
		scopeID = id
	}

	matches, err := searchHistorySessions(session.DB, query, scopeID, session.ID, maxResults)
	if err != nil {
		return errResult("history search failed: "+err.Error(), cross)
	}

	outAny := any(formatHistoryMatches(matches, scopeID))
	successAny := any(true)
	return false, cross, map[string]*any{
		"success": &successAny,
		"output":  &outAny,
	}, nil
}

// searchHistorySessions 在同一个工作目录的数据库中检索会话历史。
// currentID 是当前会话，普通搜索时排除（"其它会话"）；scopeID 非 0 时只搜该会话。
func searchHistorySessions(db *gorm.DB, query string, scopeID, currentID uint32, maxResults int) ([]historyMatch, error) {
	raw := strings.TrimSpace(query)
	if raw == "" {
		return nil, errors.New("empty query")
	}
	like := "%" + escapeLikePattern(raw) + "%"

	matches := make(map[uint32]*historyMatch)
	order := make([]uint32, 0, maxResults+1)
	// 参数用指针：Chats 内含 sync.Mutex，按值传递会触发 go vet 的 copylocks。
	add := func(chat *structs.Chats) *historyMatch {
		if m, ok := matches[chat.ID]; ok {
			return m
		}
		m := &historyMatch{
			ChatID:    chat.ID,
			Title:     historyChatTitle(chat),
			UpdatedAt: chat.UpdatedAt,
		}
		matches[chat.ID] = m
		order = append(order, chat.ID)
		return m
	}

	// 1) 标题 / AI 标题
	tq := db.Select("id", "title", "ai_title", "updated_at").
		Where("title LIKE ? ESCAPE '\\' OR ai_title LIKE ? ESCAPE '\\'", like, like)
	if scopeID != 0 {
		tq = tq.Where("id = ?", scopeID)
	} else {
		tq = tq.Where("id <> ?", currentID)
	}
	var titleRows []structs.Chats
	if err := tq.Order("updated_at DESC").Limit(maxResults).Find(&titleRows).Error; err != nil {
		return nil, err
	}
	for i := range titleRows {
		add(&titleRows[i]).MatchedTitle = true
	}

	// 2) session id（query 是纯数字时）
	if id, ok := parseSessionID(raw); ok && (scopeID == 0 || scopeID == id) && (scopeID != 0 || id != currentID) {
		var chat structs.Chats
		err := db.Select("id", "title", "ai_title", "updated_at").First(&chat, id).Error
		if err == nil {
			add(&chat).MatchedID = true
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}

	// 3) 消息内容
	mq := db.Model(&structs.Messages{}).
		Select("id", "chat_id", "type", "delta").
		Where("type IN (?, ?)", structs.MessagesRoleUser, structs.MessagesRoleAgent).
		Where("delta LIKE ? ESCAPE '\\'", like)
	if scopeID != 0 {
		mq = mq.Where("chat_id = ?", scopeID)
	} else {
		mq = mq.Where("chat_id <> ?", currentID)
	}
	scanLimit := maxResults * 8
	if scanLimit > historyMaxMessageScan {
		scanLimit = historyMaxMessageScan
	}
	var hits []structs.Messages
	if err := mq.Order("id DESC").Limit(scanLimit).Find(&hits).Error; err != nil {
		return nil, err
	}

	chatCache := make(map[uint32]*structs.Chats, len(order))
	for _, hit := range hits {
		m, ok := matches[hit.ChatID]
		if !ok {
			chat, cached := chatCache[hit.ChatID]
			if !cached {
				var row structs.Chats
				err := db.Select("id", "title", "ai_title", "updated_at").First(&row, hit.ChatID).Error
				if err != nil {
					continue
				}
				chatCache[hit.ChatID] = &row
				chat = &row
			}
			m = add(chat)
		}
		if len(m.Snippets) >= historySnippetsPerChat {
			continue
		}
		role := "User"
		if hit.Type == structs.MessagesRoleAgent {
			role = "Assistant"
		}
		m.Snippets = append(m.Snippets, historySnippet{
			MsgID: hit.ID,
			Role:  role,
			Text:  historySnippetText(hit.Delta, raw),
		})
	}

	// 片段按消息时间正序（命中扫描是倒序）
	for _, id := range order {
		m := matches[id]
		for i, j := 0, len(m.Snippets)-1; i < j; i, j = i+1, j-1 {
			m.Snippets[i], m.Snippets[j] = m.Snippets[j], m.Snippets[i]
		}
	}

	list := make([]historyMatch, 0, len(order))
	for _, id := range order {
		m := matches[id]
		// 普通搜索只保留真正命中的会话；scope 模式同理，没有命中就返回 "No history
		// matches found in session N"。
		if len(m.Snippets) == 0 && !m.MatchedID && !m.MatchedTitle {
			continue
		}
		list = append(list, *m)
	}

	// id 命中优先，其次标题命中，最后内容命中；同组内按最近活跃排序。
	rank := func(m historyMatch) int {
		switch {
		case m.MatchedID:
			return 0
		case m.MatchedTitle:
			return 1
		default:
			return 2
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		ri, rj := rank(list[i]), rank(list[j])
		if ri != rj {
			return ri < rj
		}
		return list[i].UpdatedAt.After(list[j].UpdatedAt)
	})
	if len(list) > maxResults {
		list = list[:maxResults]
	}
	return list, nil
}

func historyChatTitle(chat *structs.Chats) string {
	if t := strings.TrimSpace(chat.Title); t != "" {
		return t
	}
	return strings.TrimSpace(chat.AITitle)
}

func parseSessionID(s string) (uint32, bool) {
	id, err := strconv.ParseUint(s, 10, 32)
	if err != nil || id == 0 {
		return 0, false
	}
	return uint32(id), true
}

var likeEscaper = strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_")

// escapeLikePattern 转义 SQL LIKE 的通配符，让用户输入按字面匹配。
func escapeLikePattern(s string) string {
	return likeEscaper.Replace(s)
}

// historySnippetText 把命中位置附近的文本压成一行摘要。
func historySnippetText(delta, query string) string {
	flat := strings.Join(strings.Fields(delta), " ")
	lowerFlat := strings.ToLower(flat)
	idx := strings.Index(lowerFlat, strings.ToLower(query))
	if idx < 0 {
		return truncateRunes(flat, historySnippetMaxRunes)
	}
	start := idx - historySnippetRadius
	if start < 0 {
		start = 0
	}
	end := idx + len(query) + historySnippetRadius
	if end > len(flat) {
		end = len(flat)
	}
	// 对齐到 UTF-8 字符边界
	for start > 0 && start < len(flat) && !utf8.RuneStart(flat[start]) {
		start--
	}
	for end > 0 && end < len(flat) && !utf8.RuneStart(flat[end]) {
		end--
	}
	out := flat[start:end]
	if start > 0 {
		out = "..." + out
	}
	if end < len(flat) {
		out += "..."
	}
	return out
}

func truncateRunes(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes]) + "..."
}

// formatHistoryMatches 格式化 history 模式结果。
func formatHistoryMatches(matches []historyMatch, scopeID uint32) string {
	if len(matches) == 0 {
		if scopeID != 0 {
			return fmt.Sprintf("No history matches found in session %d.", scopeID)
		}
		return "No history sessions found."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Found %d session(s) with history matches:\n\n", len(matches))
	for _, m := range matches {
		title := m.Title
		if title == "" {
			title = "(untitled)"
		}
		fmt.Fprintf(&b, "[session %d] %s", m.ChatID, title)
		if !m.UpdatedAt.IsZero() {
			fmt.Fprintf(&b, " (updated %s)", m.UpdatedAt.Local().Format("2006-01-02 15:04"))
		}
		b.WriteString("\n")
		if m.MatchedID {
			b.WriteString("  matched: session id\n")
		}
		if m.MatchedTitle {
			b.WriteString("  matched: title\n")
		}
		for _, s := range m.Snippets {
			fmt.Fprintf(&b, "  [%s #%d] %s\n", s.Role, s.MsgID, s.Text)
		}
		b.WriteString("\n")
	}
	b.WriteString("---\n")
	b.WriteString("Use read with path \"@history/<session_id>\" to load that session's last 16 user/assistant messages.\n")
	return b.String()
}
