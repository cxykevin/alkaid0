// Package ahocorasick 提供基于 Aho-Corasick 自动机的流式多关键字替换器。
//
// 适用场景：在 LLM 流式响应到达时，逐字节扫描其中的关键字（假值），
// 并还原为对应的原文（原始敏感值）。自动机在构造时把所有关键字预加载进内存。
//
// 匹配按字节进行。所有关键字要求为 ASCII（假值均由 ASCII 字符构成）；
// UTF-8 多字节字符的每个字节值均 >= 0x80，不会与 ASCII 关键字重叠，
// 也不会被关键字切分，因此按字节匹配是安全的。
//
// # 内存布局（重要）
//
// 自动机（Trie）构建完成后**只读、可共享**；流式状态（Replacer）是每条流各自的
// 轻量对象。同一次请求里的多条流（正文 / 思考 / 每条工具参数）应共用一份自动机：
// 先 `trie := NewTrie(items)` 一次，再对每条流 `trie.NewMatcher()`。
//
// 节点采用扁平 CSR 布局：每个节点只存「出边区间 + fail/output/替换值下标」，
// 出边按字节升序连续存放。早期实现每个节点内联 `[256]*node`（约 2 KiB/节点），
// 对随机假值这类几乎不共享前缀的关键字集合会膨胀到数百 MiB（实测 2255 条映射
// = 28 万节点 ≈ 620 MiB，而且每条流各建一份）；扁平布局压到 10 MiB 量级，
// 占用只与关键字总长度成正比。
package ahocorasick

import "slices"

// Item 表示一个待替换的关键字及其替换值。
type Item struct {
	// Keyword 被查找的关键字（如脱敏后的假值）。
	Keyword string
	// Replace 命中关键字时输出的替换值（如原始敏感值）。
	Replace string
}

// node 是扁平化后的 trie 节点。
//
// 出边存在 edges[edgeStart : edgeStart+edgeCount]（按字节升序），因此子节点查找
// 只与节点出度有关，与字母表大小无关。fail/output/replace 都是节点或替换值的
// 下标，-1 表示「无」。
type node struct {
	edgeStart int32
	edgeCount int32
	fail      int32 // 失配转移
	output    int32 // fail 链上最深的终止节点下标，-1 表示没有
	replace   int32 // 本节点是关键字终点时替换值下标，-1 表示不是终点
	depth     int32 // 从根到本节点的关键字前缀长度
}

// edge 是一条出边：字节值 → 目标节点下标。
type edge struct {
	ch   byte
	next int32
}

// Trie 是不可变的 AC 自动机（构建完成后只读，可被多条流并发共享）。
type Trie struct {
	nodes  []node
	edges  []edge
	repl   []string
	maxLen int // 最长关键字的字节数（未决缓冲的上界）
	// rootNext 是根状态的转移表（1 KiB）：流式文本的绝大多数字节都落在根上，
	// 直接查表可以省掉每次的边二分查找，匹配吞吐提升约一倍。
	rootNext [256]int32
}

// Replacer 是一条流的匹配状态：引用共享的 Trie，只保存自己的状态与未决缓冲。
// state 与 buf 跨 Stream 调用保持，因此被切分到多个 chunk 的关键字也能被完整匹配。
type Replacer struct {
	trie  *Trie
	state int32
	buf   []byte // 未决缓冲：可能成为某关键字前缀的尾部
}

// buildNode 是构建期的可变节点（构建完成后压平成 node/edge，随后丢弃）。
//
// 子节点用「首子 + 兄弟链」表示，避免每个节点各自持有一个切片：后者对十万级
// 节点会产生同样数量的独立小分配，构建期的瞬时峰值因此高出一个数量级。
type buildNode struct {
	firstChild  int32 // -1 表示无子节点
	nextSibling int32 // -1 表示没有下一个兄弟
	ch          byte  // 从父节点到本节点的出边字节
	depth       int32
	replace     int32
}

// NewTrie 从 items 构建 AC 自动机。
//
// 复杂度与内存都只与「关键字总长度」成正比。调用方应构建一次、多处共享：
// 每条流用 trie.NewMatcher() 取独立的匹配状态。
func NewTrie(items []Item) *Trie {
	build := []buildNode{{firstChild: -1, nextSibling: -1, replace: -1}}
	repl := make([]string, 0, len(items))
	maxLen := 0

	// 1. 插入所有关键字构建 trie
	for _, it := range items {
		if it.Keyword == "" {
			continue
		}
		cur := int32(0)
		for i := 0; i < len(it.Keyword); i++ {
			ch := it.Keyword[i]
			next := int32(-1)
			for c := build[cur].firstChild; c >= 0; c = build[c].nextSibling {
				if build[c].ch == ch {
					next = c
					break
				}
			}
			if next < 0 {
				next = int32(len(build))
				build = append(build, buildNode{
					firstChild:  -1,
					nextSibling: build[cur].firstChild, // 头插，O(1)
					ch:          ch,
					depth:       int32(i + 1),
					replace:     -1,
				})
				build[cur].firstChild = next
			}
			cur = next
		}
		if idx := build[cur].replace; idx >= 0 {
			// 同一关键字重复出现：后者覆盖前者（与原实现一致）
			repl[idx] = it.Replace
		} else {
			build[cur].replace = int32(len(repl))
			repl = append(repl, it.Replace)
		}
		if n := len(it.Keyword); n > maxLen {
			maxLen = n
		}
	}

	// 2. 压平成 CSR：节点数组 + 按字节升序的边数组
	t := &Trie{
		nodes:  make([]node, len(build)),
		edges:  make([]edge, 0, len(build)),
		repl:   repl,
		maxLen: maxLen,
	}
	for i := range build {
		b := &build[i]
		start := len(t.edges)
		for c := b.firstChild; c >= 0; c = build[c].nextSibling {
			t.edges = append(t.edges, edge{ch: build[c].ch, next: c})
		}
		seg := t.edges[start:]
		// 子节点查找用二分，因此同节点出边按字节升序（原地排序，不额外分配）。
		// 出度 0/1 的节点天然有序（长链上的绝大多数节点都属此类），不必进排序器。
		if len(seg) > 1 {
			slices.SortFunc(seg, func(x, y edge) int { return int(x.ch) - int(y.ch) })
		}
		t.nodes[i] = node{
			edgeStart: int32(start),
			edgeCount: int32(len(seg)),
			fail:      0,
			output:    -1,
			replace:   b.replace,
			depth:     b.depth,
		}
	}

	// 3. 根状态转移表（0 = 无转移，根自身下标也是 0，因此不会冲突）。
	//    必须在 BFS 之前填好：BFS 求 fail 时会用 step(..., 0) 做根状态查询。
	for b := 0; b < 256; b++ {
		if next := t.child(0, byte(b)); next >= 0 {
			t.rootNext[b] = next
		}
	}

	// 4. BFS 求 fail 链接与 output（fail 链上最深的终止节点）
	//    队列按节点下标顺序入队；父节点一定先于子节点出队。
	queue := make([]int32, 0, len(t.nodes))
	for i := 0; i < int(t.nodes[0].edgeCount); i++ {
		child := t.edges[int(t.nodes[0].edgeStart)+i].next
		t.nodes[child].fail = 0
		t.nodes[child].output = -1
		queue = append(queue, child)
	}
	for head := 0; head < len(queue); head++ {
		v := queue[head]
		vn := &t.nodes[v]
		for i := int32(0); i < vn.edgeCount; i++ {
			e := t.edges[int(vn.edgeStart+i)]
			f := t.step(vn.fail, e.ch)
			t.nodes[e.next].fail = f
			if t.nodes[f].replace >= 0 {
				t.nodes[e.next].output = f
			} else {
				t.nodes[e.next].output = t.nodes[f].output
			}
			queue = append(queue, e.next)
		}
	}
	return t
}

// child 返回节点 n 经字节 ch 的直接转移（没有则返回 -1）。
//
// 绝大多数节点只有一条出边（关键字是长链），因此先处理 0/1 条边的情形，
// 只有分叉节点才二分查找。
func (t *Trie) child(n int32, ch byte) int32 {
	nd := &t.nodes[n]
	switch nd.edgeCount {
	case 0:
		return -1
	case 1:
		if e := t.edges[int(nd.edgeStart)]; e.ch == ch {
			return e.next
		}
		return -1
	}
	lo, hi := int32(0), nd.edgeCount
	for lo < hi {
		mid := (lo + hi) / 2
		e := t.edges[int(nd.edgeStart+mid)]
		switch {
		case e.ch == ch:
			return e.next
		case e.ch < ch:
			lo = mid + 1
		default:
			hi = mid
		}
	}
	return -1
}

// step 返回从状态 n 读入字节 ch 之后的状态（标准 AC 失配回溯，摊还 O(1)）。
func (t *Trie) step(n int32, ch byte) int32 {
	if n == 0 {
		return t.rootNext[ch]
	}
	for {
		if next := t.child(n, ch); next >= 0 {
			return next
		}
		f := t.nodes[n].fail
		if f == n {
			return t.rootNext[ch]
		}
		n = f
	}
}

// NumNodes 返回自动机节点数（用于容量评估/测试断言）。
func (t *Trie) NumNodes() int {
	if t == nil {
		return 0
	}
	return len(t.nodes)
}

// NewMatcher 为这个自动机创建一条独立的流式匹配状态。
// Trie 只读共享，因此可以（并且应该）为多条流各建一个 Matcher。
func (t *Trie) NewMatcher() *Replacer {
	if t == nil {
		return nil
	}
	return &Replacer{trie: t}
}

// NewReplacer 构建单条流的替换器（等价于 NewTrie(items).NewMatcher()）。
// 多条流请改用 NewTrie + NewMatcher，避免重复构建自动机。
func NewReplacer(items []Item) *Replacer {
	return NewTrie(items).NewMatcher()
}

// Stream 处理一段输入，返回可立即输出的字节。
// 状态跨调用保持：某关键字被切分到多次 Stream 调用时仍能完整匹配。
// 流结束时必须调用 Finish 刷出剩余缓冲。
func (r *Replacer) Stream(in []byte) []byte {
	if r == nil || r.trie == nil || len(in) == 0 {
		return nil
	}
	t := r.trie
	out := make([]byte, 0, len(in)+8)
	for _, c := range in {
		r.state = t.step(r.state, c)
		r.buf = append(r.buf, c)

		// 取当前状态下「最长终止匹配」：优先自身，否则取 fail 链上最深的终止节点。
		m := r.state
		if t.nodes[m].replace < 0 {
			m = t.nodes[m].output
		}
		if m >= 0 && t.nodes[m].replace >= 0 {
			// 命中关键字：刷出匹配段之前的明文，再输出替换值。
			if L := int(t.nodes[m].depth); L < len(r.buf) {
				out = append(out, r.buf[:len(r.buf)-L]...)
			}
			out = append(out, t.repl[t.nodes[m].replace]...)
			r.buf = r.buf[:0]
			r.state = 0
			continue
		}

		if r.state == 0 {
			// 当前字节没有在 trie 中匹配任何关键字前缀：立即刷出全部缓冲。
			out = append(out, r.buf...)
			r.buf = r.buf[:0]
			continue
		}

		// 处于某个关键字前缀的跟踪中：有界缓冲，超过最长关键字长度则刷出最旧字节。
		// （任何匹配长度不超过 maxLen，缓冲超过 maxLen 的最旧字节必不可能属于未来匹配）
		for len(r.buf) > t.maxLen {
			out = append(out, r.buf[0])
			r.buf = r.buf[1:]
		}
	}
	return out
}

// Finish 刷出流结束时剩余的缓冲（未完成的关键字前缀按明文输出），并重置状态。
func (r *Replacer) Finish() []byte {
	if r == nil || r.trie == nil {
		return nil
	}
	out := r.buf
	r.buf = r.buf[:0]
	r.state = 0
	return out
}

// TrieNodeCount 返回该匹配状态所用的自动机节点数（只用于容量评估/测试）。
func (r *Replacer) TrieNodeCount() int {
	if r == nil {
		return 0
	}
	return r.trie.NumNodes()
}
