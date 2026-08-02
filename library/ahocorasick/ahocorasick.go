// Package ahocorasick 提供基于 Aho-Corasick 自动机的流式多关键字替换器。
//
// 适用场景：在 LLM 流式响应到达时，逐字符扫描其中的关键字（假值），
// 并还原为对应的原文（原始敏感值）。自动机在构造时把所有关键字预加载进内存。
//
// 匹配按字节进行。所有关键字要求为 ASCII（假值均由 ASCII 字符构成）；
// UTF-8 多字节字符的每个字节值均 >= 0x80，不会与 ASCII 关键字重叠，
// 也不会被关键字切分，因此按字节匹配是安全的。
package ahocorasick

// Item 表示一个待替换的关键字及其替换值。
type Item struct {
	// Keyword 被查找的关键字（如脱敏后的假值）。
	Keyword string
	// Replace 命中关键字时输出的替换值（如原始敏感值）。
	Replace string
}

// node 是 AC 自动机的 trie 节点。
// next 在构建完成后被完全填充（包含 goto 与 fail 转移，合一为 delta 函数），
// 因此单次字符转移是 O(1)。
type node struct {
	next    [256]*node
	fail    *node
	output  *node  // fail 链上最深的终止节点（用于最长匹配）
	depth   int    // 从根到本节点的关键字前缀长度
	replace []byte // 非 nil 表示该节点是某个关键字的终点
}

// Replacer 是一个流式多关键字替换器。
// state 与 buf 跨 Stream 调用保持，因此被切分到多个 chunk 的关键字也能被完整匹配。
type Replacer struct {
	root   *node
	maxLen int // 最长关键字的字节数（有界缓冲的上界）
	state  *node
	buf    []byte // 未决缓冲：可能成为某关键字前缀的尾部
}

// NewReplacer 从 items 构建 AC 自动机。
func NewReplacer(items []Item) *Replacer {
	root := &node{}
	maxLen := 0

	// 1. 插入所有关键字构建 trie
	for _, it := range items {
		if it.Keyword == "" {
			continue
		}
		cur := root
		for i := 0; i < len(it.Keyword); i++ {
			b := it.Keyword[i]
			if cur.next[b] == nil {
				cur.next[b] = &node{depth: i + 1}
			}
			cur = cur.next[b]
		}
		cur.replace = []byte(it.Replace)
		if len(it.Keyword) > maxLen {
			maxLen = len(it.Keyword)
		}
	}

	// 2. BFS 求 fail 链接，并完全填充 next 转移表
	//    - root 缺失转移自环；
	//    - 非根节点缺失转移指向 fail 节点的对应转移（delta 完全化）；
	//    - 每个节点的 output 指向 fail 链上最深的终止节点。
	q := make([]*node, 0, 64)
	for b := range 256 {
		if root.next[b] != nil {
			root.next[b].fail = root
			q = append(q, root.next[b])
		} else {
			root.next[b] = root // root 自环
		}
	}
	for len(q) > 0 {
		v := q[0]
		q = q[1:]
		for b := range 256 {
			u := v.next[b]
			if u == nil {
				v.next[b] = v.fail.next[b]
				continue
			}
			u.fail = v.fail.next[b]
			switch {
			case u.fail.replace != nil:
				u.output = u.fail
			default:
				u.output = u.fail.output
			}
			q = append(q, u)
		}
	}

	return &Replacer{root: root, maxLen: maxLen, state: root}
}

// Stream 处理一段输入，返回可立即输出的字节。
// 状态跨调用保持：某关键字被切分到多次 Stream 调用时仍能完整匹配。
// 流结束时必须调用 Finish 刷出剩余缓冲。
func (r *Replacer) Stream(in []byte) []byte {
	if r == nil || r.root == nil || len(in) == 0 {
		return nil
	}
	out := make([]byte, 0, len(in)+8)
	for _, c := range in {
		r.state = r.state.next[c]
		r.buf = append(r.buf, c)

		// 取当前状态下「最长终止匹配」：优先自身，否则取 fail 链上最深的终止节点。
		m := r.state
		if m.replace == nil {
			m = m.output
		}
		if m != nil && m.replace != nil {
			// 命中关键字：刷出匹配段之前的明文，再输出替换值。
			if L := m.depth; L < len(r.buf) {
				out = append(out, r.buf[:len(r.buf)-L]...)
			}
			out = append(out, m.replace...)
			r.buf = r.buf[:0]
			r.state = r.root
			continue
		}

		if r.state == r.root {
			// 当前字符没有在 trie 中匹配任何关键字前缀：立即刷出全部缓冲（立刻返回）。
			out = append(out, r.buf...)
			r.buf = r.buf[:0]
			continue
		}

		// 处于某个关键字前缀的跟踪中：有界缓冲，超过最长关键字长度则刷出最旧字节。
		// （任何匹配长度不超过 maxLen，缓冲超过 maxLen 的最旧字节必不可能属于未来匹配）
		for len(r.buf) > r.maxLen {
			out = append(out, r.buf[0])
			r.buf = r.buf[1:]
		}
	}
	return out
}

// Finish 刷出流结束时剩余的缓冲（未完成的关键字前缀按明文输出），并重置状态。
func (r *Replacer) Finish() []byte {
	if r == nil || r.root == nil {
		return nil
	}
	out := r.buf
	r.buf = r.buf[:0]
	r.state = r.root
	return out
}
