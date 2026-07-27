package codebase

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/provider/request"
	reqstructs "github.com/cxykevin/alkaid0/provider/request/structs"
)

// BM25Result BM25 搜索结果条目
type BM25Result struct {
	ID          int64   `json:"id"`
	FilePath    string  `json:"file_path"`
	Symbol      string  `json:"symbol"`
	Tags        string  `json:"tags"`
	FullContent string  `json:"full_content"`
	EmbedText   string  `json:"embed_text"`
	Score       float64 `json:"score"`
}

// VectorSearchResult 向量相似度搜索结果条目
type VectorSearchResult struct {
	ID          int64   `json:"id"`
	FilePath    string  `json:"file_path"`
	Symbol      string  `json:"symbol"`
	Tags        string  `json:"tags"`
	FullContent string  `json:"full_content"`
	EmbedText   string  `json:"embed_text"`
	Distance    float64 `json:"distance"`
}

// SearchResult 统一的搜索结果条目，同时携带 BM25 得分和向量距离
type SearchResult struct {
	ID          int64   `json:"id"`
	FilePath    string  `json:"file_path"`
	Symbol      string  `json:"symbol"`
	Tags        string  `json:"tags"`
	FullContent string  `json:"full_content"`
	EmbedText   string  `json:"embed_text"`
	Score       float64 `json:"score,omitempty"`   // BM25 score（越低越相关），仅 BM25 搜索时有效
	Distance    float64 `json:"distance,omitempty"` // 向量距离（越低越相似），仅向量搜索时有效
}

// ---------------------------------------------------------------------------
// BM25 关键词检索（已有）
// ---------------------------------------------------------------------------

// BM25Search 使用 BM25 算法对 codebase 进行关键词检索
//
// query 是用户的搜索文本，内部会自动从中提取关键词构建 FTS5 查询。
// limit 控制最大返回条数（默认 10）。
// 返回按 BM25 相关性得分排序的结果（得分越低越相关）。
// ctx 取消时，查询会立即中止并返回 context.Canceled。
func (cdb *CodebaseDB) BM25Search(ctx context.Context, query string, limit int) ([]BM25Result, error) {
	cdb.mu.RLock()
	defer cdb.mu.RUnlock()

	if err := cdb.ensureDBOpen(); err != nil {
		return nil, err
	}

	if limit <= 0 {
		limit = 10
	}

	// 从查询文本中提取关键词
	keywords := extractKeywords(query)
	if len(keywords) == 0 {
		return nil, nil
	}

	// 构建 FTS5 MATCH 查询字符串
	ftsQuery := buildFTSQuery(keywords)

	// 搜索：使用 bm25() 排序，weights: symbol=3, tags=1, embed_text=1
	// k1=1.2, b=0.75 是 BM25 标准参数
	sqlQuery := `SELECT
		c.id, c.file_path, c.symbol, c.tags, c.full_content, c.embed_text,
		bm25(codebase_fts, 1.2, 0.75, 3.0, 1.0, 1.0) AS score
	FROM codebase_fts
	JOIN codebase_items c ON c.id = codebase_fts.rowid
	WHERE codebase_fts MATCH ?
	ORDER BY score
	LIMIT ?`

	rows, err := cdb.db.QueryContext(ctx, sqlQuery, ftsQuery, limit)
	if err != nil {
		return nil, fmt.Errorf("bm25 search: %w", err)
	}
	defer rows.Close()

	var results []BM25Result
	for rows.Next() {
		var r BM25Result
		if err := rows.Scan(
			&r.ID, &r.FilePath, &r.Symbol, &r.Tags,
			&r.FullContent, &r.EmbedText, &r.Score,
		); err != nil {
			return nil, fmt.Errorf("scan bm25 result: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return results, nil
}

// ---------------------------------------------------------------------------
// 向量相似度搜索（语义搜索）
// ---------------------------------------------------------------------------

// VectorSearch 使用向量相似度进行语义搜索。
//
// 先将 query 转换为嵌入向量，再在 vec0 表中查找最近的 K 个邻居。
// limit 控制最大返回条数（默认 10）。
// 返回按向量距离升序排列的结果（距离越小越相似）。
// ctx 取消时，搜索会立即中止并返回 context.Canceled。
func (cdb *CodebaseDB) VectorSearch(ctx context.Context, query string, limit int) ([]VectorSearchResult, error) {
	if limit <= 0 {
		limit = 10
	}

	// 将查询文本转为嵌入向量
	vec, err := cdb.embedQuery(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embed query: %w", err)
	}

	// vec0 距离查询
	cdb.mu.RLock()
	if err := cdb.ensureDBOpen(); err != nil {
		cdb.mu.RUnlock()
		return nil, err
	}
	cdb.mu.RUnlock()

	// vec0 使用 l2 距离，MATCH 语法：WHERE embedding MATCH ? AND k = ?
	vecBytes := float32SliceToBytes(vec)
	sqlQuery := `SELECT
		v.id, v.distance, c.file_path, c.symbol, c.tags, c.full_content, c.embed_text
	FROM codebase_vec v
	JOIN codebase_items c ON c.id = v.id
	WHERE v.embedding MATCH ?
		AND v.k = ?`

	rows, err := cdb.db.QueryContext(ctx, sqlQuery, vecBytes, limit)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}
	defer rows.Close()

	var results []VectorSearchResult
	for rows.Next() {
		var r VectorSearchResult
		if err := rows.Scan(
			&r.ID, &r.Distance, &r.FilePath, &r.Symbol,
			&r.Tags, &r.FullContent, &r.EmbedText,
		); err != nil {
			return nil, fmt.Errorf("scan vector result: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	return results, nil
}

// ---------------------------------------------------------------------------
// 统一搜索入口
// ---------------------------------------------------------------------------

// SearchType 搜索类型
type SearchType int

const (
	// SearchAuto 自动混合模式：始终混合 BM25 和向量搜索，加权融合排序
	SearchAuto SearchType = iota
	// SearchBM25 仅 BM25 关键词检索
	SearchBM25
	// SearchVector 仅向量相似度搜索
	SearchVector
)

// Search 在指定目录执行搜索。
//
// searchType 控制搜索方式：
//   - SearchAuto: BM25 + 向量混合搜索，始终尝试两者后加权融合
//   - SearchBM25: 仅 BM25 关键词检索
//   - SearchVector: 仅向量相似度搜索
//
// limit 控制最大返回条数（默认 10）。
// ctx 取消时搜索立即中止。
func (cdb *CodebaseDB) Search(ctx context.Context, searchType SearchType, query string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 10
	}

	switch searchType {
	case SearchBM25:
		return cdb.searchBM25Only(ctx, query, limit)
	case SearchVector:
		return cdb.searchVectorOnly(ctx, query, limit)
	case SearchAuto:
		fallthrough
	default:
		return cdb.searchHybrid(ctx, query, limit)
	}
}

// Search 包级便捷函数，自动获取指定目录的 CodebaseDB 并执行搜索。
//
//	results, err := codebase.Search(ctx, dir, codebase.SearchAuto, "find function", 10)
func Search(ctx context.Context, directory string, searchType SearchType, query string, limit int) ([]SearchResult, error) {
	cdb, err := getOrCreateDB(directory)
	if err != nil {
		return nil, fmt.Errorf("get db for %s: %w", directory, err)
	}
	return cdb.Search(ctx, searchType, query, limit)
}

// searchBM25Only 仅 BM25 检索
func (cdb *CodebaseDB) searchBM25Only(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	results, err := cdb.BM25Search(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, nil
	}
	out := make([]SearchResult, len(results))
	for i, r := range results {
		out[i] = SearchResult{
			ID:          r.ID,
			FilePath:    r.FilePath,
			Symbol:      r.Symbol,
			Tags:        r.Tags,
			FullContent: r.FullContent,
			EmbedText:   r.EmbedText,
			Score:       r.Score,
		}
	}
	return out, nil
}

// searchVectorOnly 仅向量相似度搜索
func (cdb *CodebaseDB) searchVectorOnly(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	results, err := cdb.VectorSearch(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, nil
	}
	out := make([]SearchResult, len(results))
	for i, r := range results {
		out[i] = SearchResult{
			ID:          r.ID,
			FilePath:    r.FilePath,
			Symbol:      r.Symbol,
			Tags:        r.Tags,
			FullContent: r.FullContent,
			EmbedText:   r.EmbedText,
			Distance:    r.Distance,
		}
	}
	return out, nil
}

// searchHybrid 混合搜索：始终并行发起 BM25 和向量搜索，加权融合后返回。
//
// - 排序：BM25 匹配的项在前（BM25 权重更高），仅向量匹配的在阈值内保留在后
// - BM25 或向量搜索各自失败时，仅用另一方的结果
// - 配置通过 config.Agents.ContextEngine 控制：
//   - BM25Weight: BM25 权重（默认 0.7），向量权重 = 1-BM25Weight
//   - VectorMinSimilarity: 向量最小余弦相似度保留阈值（默认 0.5）
//   - BM25RetentionScore: BM25 保留阈值（0 不限制）
func (cdb *CodebaseDB) searchHybrid(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	// 加载搜索引擎配置
	cfg := config.GlobalConfigSafe()
	ec := cfg.Agent.ContextEngine
	bm25Weight := clampWeight(ec.BM25Weight, 0.7)
	vecWeight := 1.0 - bm25Weight

	// cosine → L2 距离阈值：L2 <= sqrt(2 * (1 - cos))
	var vecL2Threshold float64
	vecMinSim := clampWeight(ec.VectorMinSimilarity, 0.5)
	if vecMinSim < 1.0 {
		vecL2Threshold = math.Sqrt(2.0 * (1.0 - vecMinSim))
	} else {
		vecL2Threshold = 0
	}

	bm25RetScore := ec.BM25RetentionScore

	// 并行发起两种搜索
	type bm25Res struct {
		results []BM25Result
		err     error
	}
	type vecRes struct {
		results []VectorSearchResult
		err     error
	}

	bm25Ch := make(chan bm25Res, 1)
	vecCh := make(chan vecRes, 1)

	go func() {
		r, err := cdb.BM25Search(ctx, query, limit*2)
		bm25Ch <- bm25Res{r, err}
	}()
	go func() {
		r, err := cdb.VectorSearch(ctx, query, limit*2)
		vecCh <- vecRes{r, err}
	}()

	bm25 := <-bm25Ch
	vec := <-vecCh

	// 两者都失败才返回错误
	if bm25.err != nil && vec.err != nil {
		return nil, fmt.Errorf("both searches failed: bm25=%v, vec=%v", bm25.err, vec.err)
	}

	// 处理一方失败的情形
	if bm25.err != nil {
		return cdb.asHybridResult(vec.results)
	}
	if vec.err != nil {
		return cdb.bm25ResultsToHybrid(bm25.results)
	}

	// 分两组：
	//   groupA — BM25 命中（+ 可能也有向量命中）
	//   groupB — 仅向量命中且余弦相似度通过阈值
	type scored struct {
		result     SearchResult
		bm25Score  float64
		vecDist    float64
		hasBM25    bool
		hasVec     bool
	}
	merged := make(map[int64]*scored)

	// 收集 BM25 结果
	for _, r := range bm25.results {
		if bm25RetScore > 0 && r.Score > bm25RetScore {
			continue
		}
		merged[r.ID] = &scored{
			result: SearchResult{
				ID:          r.ID,
				FilePath:    r.FilePath,
				Symbol:      r.Symbol,
				Tags:        r.Tags,
				FullContent: r.FullContent,
				EmbedText:   r.EmbedText,
				Score:       r.Score,
			},
			bm25Score: r.Score,
			hasBM25:   true,
		}
	}

	// 收集向量结果，同时计算最大距离用于归一化
	var maxDist float64
	for _, r := range vec.results {
		if vecL2Threshold > 0 && r.Distance > vecL2Threshold {
			continue
		}
		if r.Distance > maxDist {
			maxDist = r.Distance
		}
	}

	for _, r := range vec.results {
		if vecL2Threshold > 0 && r.Distance > vecL2Threshold {
			continue
		}
		if s, ok := merged[r.ID]; ok {
			s.vecDist = r.Distance
			s.hasVec = true
			s.result.Distance = r.Distance
		} else {
			merged[r.ID] = &scored{
				result: SearchResult{
					ID:          r.ID,
					FilePath:    r.FilePath,
					Symbol:      r.Symbol,
					Tags:        r.Tags,
					FullContent: r.FullContent,
					EmbedText:   r.EmbedText,
					Distance:    r.Distance,
				},
				vecDist: r.Distance,
				hasVec:  true,
			}
		}
	}

	// 无任何结果
	if len(merged) == 0 {
		return nil, nil
	}

	// 计算 BM25 归一化最大值
	var maxBM25Score float64
	for _, s := range merged {
		if s.bm25Score > maxBM25Score {
			maxBM25Score = s.bm25Score
		}
	}

	// 计算融合得分
	for _, s := range merged {
		normBM25 := 0.0
		if maxBM25Score > 0 && s.hasBM25 {
			normBM25 = s.bm25Score / maxBM25Score
		} else if !s.hasBM25 {
			normBM25 = 1.0 // 仅向量命中：BM25 给最大惩罚
		}
		normVec := 0.0
		if maxDist > 0 && s.hasVec {
			normVec = s.vecDist / maxDist
		} else if !s.hasVec {
			normVec = 1.0 // 仅 BM25 命中：向量给最大惩罚
		}
		s.result.Score = bm25Weight*normBM25 + vecWeight*normVec
	}

	// 排序：BM25 命中项（groupA）在前，仅向量命中（groupB）在后
	// 各组内按融合得分升序
	sorted := make([]SearchResult, 0, len(merged))
	var groupA, groupB []*scored
	for _, s := range merged {
		if s.hasBM25 {
			groupA = append(groupA, s)
		} else {
			groupB = append(groupB, s)
		}
	}

	// 组内排序
	sortScored := func(list []*scored) {
		for i := 0; i < len(list); i++ {
			for j := i + 1; j < len(list); j++ {
				if list[j].result.Score < list[i].result.Score {
					list[i], list[j] = list[j], list[i]
				}
			}
		}
	}
	sortScored(groupA)
	sortScored(groupB)

	for _, s := range groupA {
		sorted = append(sorted, s.result)
	}
	for _, s := range groupB {
		sorted = append(sorted, s.result)
	}

	if len(sorted) > limit {
		sorted = sorted[:limit]
	}
	return sorted, nil
}

// asHybridResult 将向量结果直接包装为 SearchResult
func (cdb *CodebaseDB) asHybridResult(results []VectorSearchResult) ([]SearchResult, error) {
	if len(results) == 0 {
		return nil, nil
	}
	out := make([]SearchResult, len(results))
	for i, r := range results {
		out[i] = SearchResult{
			ID:          r.ID,
			FilePath:    r.FilePath,
			Symbol:      r.Symbol,
			Tags:        r.Tags,
			FullContent: r.FullContent,
			EmbedText:   r.EmbedText,
			Distance:    r.Distance,
		}
	}
	return out, nil
}

// bm25ResultsToHybrid 将 BM25 结果直接包装为 SearchResult
func (cdb *CodebaseDB) bm25ResultsToHybrid(results []BM25Result) ([]SearchResult, error) {
	if len(results) == 0 {
		return nil, nil
	}
	out := make([]SearchResult, len(results))
	for i, r := range results {
		out[i] = SearchResult{
			ID:          r.ID,
			FilePath:    r.FilePath,
			Symbol:      r.Symbol,
			Tags:        r.Tags,
			FullContent: r.FullContent,
			EmbedText:   r.EmbedText,
			Score:       r.Score,
		}
	}
	return out, nil
}

// clampWeight 将权重限制在 [0,1] 范围，无效值（0）使用默认值
func clampWeight(v, def float64) float64 {
	if v < 0 || v > 1 {
		return def
	}
	if v == 0 {
		return def
	}
	return v
}

// ---------------------------------------------------------------------------
// 内部辅助
// ---------------------------------------------------------------------------

// embedQuery 调用嵌入 API 将查询文本转为向量
func (cdb *CodebaseDB) embedQuery(ctx context.Context, query string) ([]float32, error) {
	req := reqstructs.EmbeddingRequest{
		Input: []string{query},
		Model: cdb.modelID,
	}
	embeddings, err := request.SimpleOpenAIEmbedding(ctx, cdb.providerURL, cdb.providerKey, cdb.modelID, req)
	if err != nil {
		return nil, fmt.Errorf("embedding api: %w", err)
	}
	if len(embeddings) == 0 {
		return nil, fmt.Errorf("embedding api returned empty result")
	}
	return embeddings[0], nil
}

// ---------------------------------------------------------------------------
// 关键词提取
// ---------------------------------------------------------------------------

// extractKeywords 从查询文本中提取关键词
// 步骤：分词 → 转小写 → 过滤停用词和短词（<2 字符）
func extractKeywords(query string) []string {
	// 按非字母数字/下划线分割
	tokens := strings.FieldsFunc(query, func(r rune) bool {
		return !isWordRune(r)
	})

	seen := make(map[string]bool, len(tokens))
	keywords := make([]string, 0, len(tokens))

	for _, t := range tokens {
		t = strings.ToLower(strings.TrimSpace(t))
		if len(t) < 2 {
			continue
		}
		// 过滤纯数字（通常不是有效关键词）
		if isNumeric(t) {
			continue
		}
		// 过滤停用词
		if isStopWord(t) {
			continue
		}
		// 过滤 FTS5 运算符名
		if t == "and" || t == "or" || t == "not" {
			continue
		}
		if seen[t] {
			continue
		}
		seen[t] = true
		keywords = append(keywords, t)
	}

	return keywords
}

// buildFTSQuery 将关键词列表拼接为 FTS5 MATCH 查询字符串
//
// 使用 AND + 前缀匹配（keyword*），实现对代码标识符的自然查询：
// 搜索 "read" 可匹配 "ReadFile"、"reads"、"reader" 等。
func buildFTSQuery(keywords []string) string {
	quoted := make([]string, len(keywords))
	for i, kw := range keywords {
		// 使用前缀匹配以便能匹配到代码中的复合标识符（如 ReadFile）
		quoted[i] = fmt.Sprintf(`"%s"*`, kw)
	}
	return strings.Join(quoted, " AND ")
}

// ---------------------------------------------------------------------------
// 辅助函数
// ---------------------------------------------------------------------------

// isWordRune 判断 rune 是否属于"词"字符（字母、数字、下划线）
func isWordRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') || r == '_'
}

// isNumeric 判断字符串是否全由数字组成
func isNumeric(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

// stopWords 常见英文停用词
var stopWords = map[string]bool{
	"the": true, "is": true, "at": true, "which": true, "on": true,
	"and": true, "or": true, "a": true, "an": true, "in": true,
	"to": true, "for": true, "of": true, "by": true, "with": true,
	"this": true, "that": true, "it": true, "from": true, "as": true,
	"be": true, "are": true, "was": true, "were": true, "been": true,
	"has": true, "have": true, "had": true, "do": true, "does": true,
	"did": true, "will": true, "would": true, "could": true, "should": true,
	"may": true, "might": true, "can": true, "shall": true, "not": true,
	"no": true, "nor": true, "but": true, "if": true, "so": true,
	"about": true, "into": true, "over": true, "after": true, "before": true,
	"between": true, "under": true, "above": true, "below": true,
	"up": true, "down": true, "out": true, "off": true, "then": true,
	"than": true, "when": true, "where": true, "why": true, "how": true,
	"all": true, "each": true, "every": true, "both": true, "few": true,
	"more": true, "most": true, "other": true, "some": true, "such": true,
	"only": true, "own": true, "same": true, "here": true, "there": true,
	"its": true, "just": true, "also": true, "very": true, "too": true,
	"any": true, "get": true, "got": true, "use": true, "used": true,
	"using": true, "like": true, "make": true, "made": true, "see": true,
	"put": true, "take": true, "know": true, "let": true, "tell": true,
	"doctype": true, "html": true,
}

// isStopWord 判断单词是否为停用词
func isStopWord(word string) bool {
	return stopWords[word]
}
