package trace

import (
	"bytes"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	xunicode "golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// 文本编码名（DecodeText 返回）。
const (
	EncodingUTF8     = "utf-8"
	EncodingUTF8BOM  = "utf-8-bom"
	EncodingUTF16LE  = "utf-16le"
	EncodingUTF16BE  = "utf-16be"
	EncodingGBK      = "gbk"
	EncodingGB18030  = "gb18030"
	EncodingBig5     = "big5"
	EncodingShiftJIS = "shift_jis"
	EncodingEUCJP    = "euc-jp"
	EncodingEUCKR    = "euc-kr"
	// EncodingBinary 表示内容不像文本（含 NUL 且 UTF-16 解释也不成立）：
	// 调用方应按"二进制文件"处理，而不是把乱码喂给模型。
	EncodingBinary = "binary"
)

// decodeCandidates 单/多字节候选编码，按常见程度排序——同分时靠前者优先
// （简中 > 繁中 > 日 > 韩 > 西欧）。UTF-16 不在这里：它由 BOM 或 NUL 分布单独判定。
var decodeCandidates = []struct {
	name string
	enc  encoding.Encoding
	// singleByte 标记单字节编码。它们对任意字节都"合法"、没有结构可言，
	// 只在通过闸门（高位字节孤立 / 能解成单一文字的纯文本）时才参与竞争。
	singleByte bool
}{
	{EncodingGBK, simplifiedchinese.GBK, false},
	{EncodingGB18030, simplifiedchinese.GB18030, false},
	{EncodingBig5, traditionalchinese.Big5, false},
	{EncodingShiftJIS, japanese.ShiftJIS, false},
	{EncodingEUCJP, japanese.EUCJP, false},
	{EncodingEUCKR, korean.EUCKR, false},
	{"windows-1252", charmap.Windows1252, true},
	{"windows-1251", charmap.Windows1251, true},
	{"iso-8859-1", charmap.ISO8859_1, true},
}

// singleByteFavoured 判断内容是否更像"单字节编码的西欧/西里尔文本"：
// 高位字节从不成串（连续高位字节不超过 2 个，即最多一个两字节字符的宽度）。
// 真实的 CJK 文本里汉字/假名总是成串出现（"中文"就是 4 个连续高位字节），
// 因此这个条件能把 "Größe"、"año"、"coração" 这类文本从 GBK/Big5 手里救回来。
func singleByteFavoured(content []byte) bool {
	high, run, maxRun := 0, 0, 0
	for _, b := range content {
		if b < 0x80 {
			run = 0
			continue
		}
		high++
		run++
		if run > maxRun {
			maxRun = run
		}
	}
	return high > 0 && maxRun <= 2
}

// singleByteScriptCoherent 判断内容能否被某个单字节编码解释成"纯某一种文字的文本"：
// 所有高位字节都解成同一种文字的字母（拉丁/西里尔/希腊），没有 £ º ¿ 这类乱码符号。
// 俄语（windows-1251）、希腊语源码就是这种形态；而 GBK 中文被当成 windows-1252 时
// 会混入大量符号字符，不会通过。
func singleByteScriptCoherent(content []byte) bool {
	for _, cand := range decodeCandidates {
		if !cand.singleByte {
			continue
		}
		text, err := decodeWith(content, cand.enc)
		if err != nil && text == "" {
			continue
		}
		if hasCoherentScript(text) {
			return true
		}
	}
	return false
}

// hasCoherentScript 判断文本里的非 ASCII 字符是否都是同一种文字的字母
// （至少 3 个，避免短样本误判）。
func hasCoherentScript(text string) bool {
	letters := 0
	var script *unicode.RangeTable
	for _, r := range text {
		if r < 0x80 {
			continue
		}
		if !unicode.IsLetter(r) {
			if unicode.IsSpace(r) || isCommonLatin1Punct(r) {
				continue
			}
			return false // £ º ½ ¿ ÷ « 这类：单字节候选解错了
		}
		letters++
		var cur *unicode.RangeTable
		switch {
		case unicode.Is(unicode.Latin, r):
			cur = unicode.Latin
		case unicode.Is(unicode.Cyrillic, r):
			cur = unicode.Cyrillic
		case unicode.Is(unicode.Greek, r):
			cur = unicode.Greek
		default:
			return false
		}
		if script == nil {
			script = cur
		} else if script != cur {
			return false
		}
	}
	return letters >= 3
}

// DecodeText 把文件字节解码为 UTF-8 文本，并返回检测到的编码名。
//
// 总原则：**认不准就按 UTF-8 原样输出**（返回原始字节的字符串），绝不硬套一个
// 冷门编码——宁可让模型看到替换符，也不要给出"看着像真的、其实是错的"解码。
//
// 判定顺序（针对源代码等纯文本，而不是 HTML）：
//  1. BOM：UTF-8 / UTF-16LE / UTF-16BE；
//  2. 含 NUL 的内容：按 UTF-16LE/BE 尝试，解出可信文本就用它，否则判为二进制；
//  3. 合法 UTF-8：直接用（绝大多数文件到这里结束）；只有个别坏字节时也按 UTF-8；
//  4. 其余逐个候选解码打分（文本分 + 字节结构分），并按下面的规则**收紧**：
//     - "高位字节不成串"或"能解成单一文字的纯文本"时只在单字节候选里选，
//     避免把西欧/西里尔文本当成 GBK/Big5；
//     - 冷门编码（Shift-JIS/EUC-JP/EUC-KR）必须比常用编码（简中/繁中）领先
//     obscureLeadPercent 才认；繁中必须比简中领先 big5LeadPercent 才认；
//     - 得分密度（得分 / 高位字节数）达不到该编码的置信下限就不认；
//     - 任何一条不满足 → 回到 UTF-8 原样输出。
//
// 判定为二进制时返回 ("", EncodingBinary)。
func DecodeText(content []byte) (string, string) {
	text, name, _ := DecodeTextEx(content)
	return text, name
}

// DecodeTextEx 与 DecodeText 判定完全一致，只是额外返回原始字节是否带 BOM
// （UTF-8 / UTF-16）。edit 工具要按原编码写回，必须知道 BOM 是否存在：
// 无 BOM 的 UTF-16 与带 BOM 的 UTF-16 在 DecodeText 里是同一个编码名。
func DecodeTextEx(content []byte) (text string, name string, hasBOM bool) {
	if len(content) == 0 {
		return "", EncodingUTF8, false
	}

	// 1) BOM
	switch {
	case bytes.HasPrefix(content, []byte{0xEF, 0xBB, 0xBF}):
		return string(content[3:]), EncodingUTF8BOM, true
	case bytes.HasPrefix(content, []byte{0xFF, 0xFE}):
		if s, err := decodeWith(content[2:], xunicode.UTF16(xunicode.LittleEndian, xunicode.IgnoreBOM)); err == nil {
			return s, EncodingUTF16LE, true
		}
	case bytes.HasPrefix(content, []byte{0xFE, 0xFF}):
		if s, err := decodeWith(content[2:], xunicode.UTF16(xunicode.BigEndian, xunicode.IgnoreBOM)); err == nil {
			return s, EncodingUTF16BE, true
		}
	}

	// 2) 含 NUL：UTF-16（无 BOM）或二进制
	if bytes.IndexByte(content, 0) >= 0 {
		if s, name, ok := decodeUTF16WithoutBOM(content); ok {
			return s, name, false
		}
		return "", EncodingBinary, false
	}

	// 3) 合法 UTF-8：直接用（含 ASCII）
	if utf8.Valid(content) {
		return string(content), EncodingUTF8, false
	}
	// 3b) 只有极个别坏字节（例如历史仓库里混了一个坏字节的 UTF-8 文件）：仍按 UTF-8 处理，
	//     避免整篇被当成 GBK/Big5 解成乱码。阈值卡得很紧——放宽会把"少量重音字母的
	//     西欧文本"误判成 UTF-8，那同样是把内容解错。
	if justFewInvalidBytes(content) {
		return string(content), EncodingUTF8, false
	}

	// 4) 候选编码打分。分两条路走，都不成立就按 UTF-8 原样输出：
	//    - 单字节路：高位字节孤立（"Größe"）或能解成单一文字的纯文本（俄语）时，
	//      只在单字节候选里选——单字节编码对任意字节都"合法"，无证据时让它参与
	//      竞争只会把别的东西解成西欧乱码；
	//    - 多字节路：其余情况只在多字节候选里选，且要求**得分密度**达到该编码的
	//      置信下限（冷门编码要求更高）。达不到就不认，回落到 UTF-8。
	singleByteOnly := singleByteFavoured(content) || singleByteScriptCoherent(content)
	high := highByteCount(content)
	bestName, bestText, bestScore := "", "", 0
	familyScores := map[string]int{}
	// commonBest 是"常用编码"（简中/繁中）目前的最好成绩，冷门编码必须明显超过它才认。
	commonBest := func() int { return max(familyScores["gb"], familyScores["big5"]) }
	for _, cand := range decodeCandidates {
		if cand.singleByte != singleByteOnly {
			continue
		}
		text, err := decodeWith(content, cand.enc)
		if err != nil && text == "" {
			continue
		}
		// 文本分（是否解出可读文本）× 结构分（字节序列是否符合该编码的习惯用法）：
		// 前者把 GBK 从 windows-1252 里救回来，后者把 GBK 从 Shift-JIS/Big5 里分出来。
		score := scoreDecodedText(text)*2 + scoreByStructure(content, cand.name)*3
		// 假名是日文的强证据：日文候选解出成串假名时给一次性加成，
		// 免得 EUC-JP/Shift-JIS 与被解成一堆汉字的 GBK 拉不开差距。
		if kanaCount(text) >= minKanaEvidence {
			score += kanaEvidenceBonus
		}
		family := candidateFamily(cand.name)
		if !singleByteOnly {
			// 繁中必须明显领先简中才认：同分或接近时按简中（更常见的先验）。
			if family == "big5" && score*100 < familyScores["gb"]*big5LeadPercent {
				continue
			}
			// 冷门编码（日/韩）必须明显领先常用编码，否则说明只是"恰好也能解"，
			// 按用户要求回落到 UTF-8，不硬猜。
			if isObscureEncodingFamily(family) && score*100 < commonBest()*obscureLeadPercent {
				continue
			}
		}
		if score > familyScores[family] {
			familyScores[family] = score
		}
		if score > bestScore {
			bestScore, bestName, bestText = score, cand.name, text
		}
	}
	if bestText == "" {
		return string(content), EncodingUTF8, false // 一个候选都没解出来：按 UTF-8 原样输出
	}
	if singleByteOnly {
		if bestScore <= 0 {
			return string(content), EncodingUTF8, false
		}
		return bestText, bestName, false
	}
	// 多字节路：还要过置信度（得分 / 高位字节数）下限，冷门编码下限更高——
	// 认错不如不认，认不出就按 UTF-8 原样输出，让模型看到替换符而不是"看着像真的"的错误解码。
	if density := float64(bestScore) / float64(max(high, 1)); density < candidateMinDensity(bestName) {
		return string(content), EncodingUTF8, false
	}
	return bestText, bestName, false
}

// 冷门编码（日/韩）要与常用编码拉开的最小领先幅度（百分比）。
const obscureLeadPercent = 110

// 繁中相对简中的最小领先幅度：达不到就按简中处理（简体中文是更常见的先验）。
// 真正的繁中文件其 GBK 解码通常是一堆非法序列、分差极大，所以这个"偏向简中"的先验
// 不会影响繁中文件，却能挡住"短样本里 Big5 恰好多解出几个字"的误判。
const big5LeadPercent = 120

// 日文候选的假名证据：解出的平假名/片假名达到 minKanaEvidence 个时给 kanaEvidenceBonus。
const (
	minKanaEvidence   = 3
	kanaEvidenceBonus = 60
)

// isObscureEncodingFamily 判断是否冷门编码族：它们只在明显领先时才会被采纳。
func isObscureEncodingFamily(family string) bool {
	switch family {
	case EncodingShiftJIS, EncodingEUCJP, EncodingEUCKR:
		return true
	}
	return false
}

// kanaCount 统计平假名/片假名数量（半角片假名不算：那是 GBK 中文被误判成 Shift-JIS
// 时的典型产物）。
func kanaCount(text string) int {
	n := 0
	for _, r := range text {
		if r >= 0x3041 && r <= 0x30FA {
			n++
		}
	}
	return n
}

// candidateMinDensity 多字节候选的置信度下限（得分 / 高位字节数）。
// 越冷门的编码要求越高：认错不如不认，认不出就按 UTF-8 原样输出。
func candidateMinDensity(name string) float64 {
	switch name {
	case EncodingGBK, EncodingGB18030:
		return 8.5 // 简体中文：最常见的非 UTF-8 场景，阈值最低
	case EncodingBig5:
		return 9.5
	case EncodingShiftJIS:
		return 9.5
	case EncodingEUCJP:
		return 11
	case EncodingEUCKR:
		return 12
	default:
		return 12
	}
}

// candidateFamily 同一族编码的得分往往完全相同（GBK 与 GB18030 互为子集），
// 判定"是否无法区分"时按族比较。
func candidateFamily(name string) string {
	switch name {
	case EncodingGBK, EncodingGB18030:
		return "gb"
	default:
		return name
	}
}

// highByteCount 统计高位字节（>= 0x80）数量。
func highByteCount(content []byte) int {
	n := 0
	for _, b := range content {
		if b >= 0x80 {
			n++
		}
	}
	return n
}

// justFewInvalidBytes 判断内容是否只有极个别非法字节（其余都是合法 UTF-8）。
// 只容忍 1 个坏字节：多到 2 个以上时，更可能是"整篇就是单字节编码 + 少量重音字母"，
// 交给候选打分处理才正确。
func justFewInvalidBytes(content []byte) bool {
	total, invalid := 0, 0
	for i := 0; i < len(content); {
		r, size := utf8.DecodeRune(content[i:])
		if r == utf8.RuneError && size <= 1 {
			invalid++
			if invalid > 1 {
				return false
			}
			i++
			continue
		}
		total++
		i += size
	}
	return invalid == 1 && total >= 8
}

// decodeWith 用给定编码解码，返回文本与错误。
func decodeWith(content []byte, enc encoding.Encoding) (string, error) {
	out, _, err := transform.Bytes(enc.NewDecoder(), content)
	return string(out), err
}

// encodeWith 用给定编码编码，返回字节与错误。
func encodeWith(text string, enc encoding.Encoding) ([]byte, error) {
	out, _, err := transform.Bytes(enc.NewEncoder(), []byte(text))
	if err != nil {
		return nil, err
	}
	return out, nil
}

// EncodeText 把 UTF-8 文本按 name 编码回文件字节，与 DecodeTextEx 成对使用
// （edit 工具写回时保持原文件编码）。
//
// hasBOM 只对 UTF-16 生效：DecodeTextEx 会把 BOM 的存在与否单独返回，因为
// "utf-16le" 这个编码名同时对应带 BOM 与不带 BOM 两种文件。UTF-8 BOM 由
// EncodingUTF8BOM 本身决定。未知编码名（含 EncodingUTF8）按 UTF-8 原样输出；
// 文本里有目标编码无法表示的字符时返回错误，由调用方决定是否放弃写入——
// 宁可报错，也不静默用替换符损坏文件。
func EncodeText(text, name string, hasBOM bool) ([]byte, error) {
	switch name {
	case EncodingUTF8BOM:
		return append([]byte{0xEF, 0xBB, 0xBF}, text...), nil
	case EncodingUTF16LE:
		if hasBOM {
			return encodeWith(text, xunicode.UTF16(xunicode.LittleEndian, xunicode.UseBOM))
		}
		return encodeWith(text, xunicode.UTF16(xunicode.LittleEndian, xunicode.IgnoreBOM))
	case EncodingUTF16BE:
		if hasBOM {
			return encodeWith(text, xunicode.UTF16(xunicode.BigEndian, xunicode.UseBOM))
		}
		return encodeWith(text, xunicode.UTF16(xunicode.BigEndian, xunicode.IgnoreBOM))
	case EncodingGBK:
		return encodeWith(text, simplifiedchinese.GBK)
	case EncodingGB18030:
		return encodeWith(text, simplifiedchinese.GB18030)
	case EncodingBig5:
		return encodeWith(text, traditionalchinese.Big5)
	case EncodingShiftJIS:
		return encodeWith(text, japanese.ShiftJIS)
	case EncodingEUCJP:
		return encodeWith(text, japanese.EUCJP)
	case EncodingEUCKR:
		return encodeWith(text, korean.EUCKR)
	case "windows-1252":
		return encodeWith(text, charmap.Windows1252)
	case "windows-1251":
		return encodeWith(text, charmap.Windows1251)
	case "iso-8859-1":
		return encodeWith(text, charmap.ISO8859_1)
	default:
		return []byte(text), nil
	}
}

// decodeUTF16WithoutBOM 处理没有 BOM 的 UTF-16：Windows 工具与部分编辑器会产出这种文件。
// 先看 NUL 字节的奇偶分布（ASCII 为主的源码里非常明显），再对两种字节序解码确认。
func decodeUTF16WithoutBOM(content []byte) (string, string, bool) {
	if len(content) < 4 {
		return "", "", false
	}
	if len(content)%2 != 0 {
		content = content[:len(content)-1] // UTF-16 码元是 2 字节，末尾多余单字节无法成立
	}
	type candidate struct {
		name string
		enc  encoding.Encoding
	}
	cands := []candidate{
		{EncodingUTF16LE, xunicode.UTF16(xunicode.LittleEndian, xunicode.IgnoreBOM)},
		{EncodingUTF16BE, xunicode.UTF16(xunicode.BigEndian, xunicode.IgnoreBOM)},
	}
	// NUL 奇偶分布：LE 的 ASCII 文本 NUL 多在奇数位，BE 多在偶数位。
	sample := content
	if len(sample) > 4096 {
		sample = sample[:4096]
	}
	even, odd := 0, 0
	for i, b := range sample {
		if b != 0 {
			continue
		}
		if i%2 == 0 {
			even++
		} else {
			odd++
		}
	}
	// UTF-16 文本的 NUL 分布有强特征（ASCII 部分每个字符都带一个 NUL）。
	// NUL 太少、或奇偶分布不偏（真二进制文件的常态），都不当 UTF-16，交二进制判定。
	if (even+odd)*16 < len(sample) {
		return "", "", false
	}
	switch {
	case odd*4 >= (even+odd)*3:
		// 奇数位 NUL 占绝对多数 → 小端
	case even*4 >= (even+odd)*3:
		cands[0], cands[1] = cands[1], cands[0] // 偶数位 NUL 占多数 → 大端
	default:
		return "", "", false
	}
	for _, cand := range cands {
		text, err := decodeWith(content, cand.enc)
		if err != nil && text == "" {
			continue
		}
		if !plausibleUTF16Text(text) {
			continue
		}
		return text, cand.name, true
	}
	return "", "", false
}

// plausibleUTF16Text 判断 UTF-16 解码结果是否像文本：几乎全部由可打印字符组成
// （含换行/制表）。用于把真正的二进制文件挡在外面。
func plausibleUTF16Text(text string) bool {
	if text == "" {
		return false
	}
	total, printable := 0, 0
	for _, r := range text {
		total++
		switch {
		case r == utf8.RuneError:
			return false
		case r == '\n' || r == '\r' || r == '\t':
			printable++
		case r < 0x20 || (r >= 0x7F && r <= 0x9F):
			// 控制符：文本里偶有出现，量大了就判否
		case unicode.IsPrint(r):
			printable++
		}
	}
	return printable*100 >= total*95
}

// scoreDecodedText 给解码结果打分：正确的编码会解出可读文本，错误的编码会解出
// 替换符、控制符或大量生僻符号。汉字/假名/谚文加分——它们几乎只会出现在对应的
// 东亚编码里，是把 GBK/Big5/ShiftJIS 与 windows-1252 区分开的关键。
func scoreDecodedText(text string) int {
	score := 0
	asciiLetters, accented := 0, 0
	for _, r := range text {
		switch {
		case r == utf8.RuneError:
			score -= 30 // 非法字节序列
		case r == '\n' || r == '\r' || r == '\t':
			score += 2
		case r < 0x20 || (r >= 0x7F && r <= 0x9F):
			score -= 8 // 控制符 / C1 区（错误单字节解码的典型产物）
		case r < 0x7F:
			score += 2 // ASCII 可打印
			if unicode.IsLetter(r) {
				asciiLetters++
			}
		case unicode.Is(unicode.Han, r):
			score += 4
		case unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Hangul):
			// 半角片假名（U+FF61–U+FF9F）单独降权：源码文本里几乎不会出现，
			// 但它们是被误判成 Shift-JIS 的 GBK 中文的典型产物。
			if r >= 0xFF61 && r <= 0xFF9F {
				score++
			} else {
				score += 4
			}
		case r >= 0x3000 && r <= 0x303F || r >= 0xFF00 && r <= 0xFF65:
			score += 3 // CJK 标点/全角符号（：，。等）
		case unicode.IsPrint(r):
			score += 1
			if r >= 0xC0 && r <= 0xFF && unicode.IsLetter(r) {
				accented++
			} else if r >= 0x80 && r <= 0xFF && !isCommonLatin1Punct(r) {
				// GBK 中文被当成 windows-1252 时会解出 £ º ½ ¿ ÷ « 这类符号字符：
				// 正常西欧文本里它们极少，出现即说明这个候选多半是错的。
				score -= 3
			}
		default:
			score -= 2 // 私用区 / 未分配码位
		}
	}
	// windows-1252 解西里尔文（Êîììåíòàðèé…）会产出远超正常西欧文本比例的重音字母：
	// 重音字母过半即按"不像西欧文本"扣分，让 windows-1251 之类的候选胜出。
	// 阈值取一半，避免误伤匈牙利语、波兰语这类重音本来就多的西欧文本。
	if accented > 4 && accented*2 > asciiLetters+accented {
		score -= (accented - (asciiLetters+accented)/2) * 2
	}
	return score
}

// isCommonLatin1Punct 判断 Latin-1 区里常见的排版符号（正常西欧文本会出现，
// 不该按"乱码符号"扣分）。
func isCommonLatin1Punct(r rune) bool {
	switch r {
	case 0xA0, 0xA9, 0xAE, 0xB0, 0xB7, // 不换行空格 © ® ° ·
		0xAB, 0xBB, // « »
		0x2013, 0x2014, 0x2018, 0x2019, 0x201C, 0x201D, 0x2026, // – — ‘ ’ “ ” …
		0x20AC, 0x2122: // € ™
		return true
	}
	return false
}

// scoreByStructure 按候选编码的**字节结构**给分：正确的编码会让绝大多数字节落在
// 常见序列上，错误的编码会出现大量"合法但生僻"或非法的序列。它负责把形似而
// 实际不同的东亚编码区分开（GBK vs Big5 vs Shift-JIS vs EUC-JP/KR），
// 单字节编码没有结构可言，固定返回 0，靠文本分决出。
func scoreByStructure(content []byte, name string) int {
	switch name {
	case EncodingGBK:
		return gbkStructure(content, false)
	case EncodingGB18030:
		return gbkStructure(content, true)
	case EncodingBig5:
		return big5Structure(content)
	case EncodingShiftJIS:
		return shiftJISStructure(content)
	case EncodingEUCJP:
		return eucJPStructure(content)
	case EncodingEUCKR:
		return eucKRStructure(content)
	default:
		return 0
	}
}

// gbkStructure GBK/GB18030 结构打分：ASCII 正常；两字节序列里落在 GB2312 区
// （包含最常用的 6763 个汉字与全角符号）得高分，GBK 扩展区（生僻字）得低分，
// 非法序列重罚；allowFourByte 为 true 时接受 GB18030 的四字节序列。
func gbkStructure(content []byte, allowFourByte bool) int {
	score := 0
	for i := 0; i < len(content); {
		b := content[i]
		if b < 0x80 {
			score++
			i++
			continue
		}
		if b == 0x80 || b == 0xFF {
			score -= 4 // 非法首字节
			i++
			continue
		}
		if allowFourByte && i+3 < len(content) && content[i+1] >= 0x30 && content[i+1] <= 0x39 &&
			content[i+2] >= 0x81 && content[i+2] <= 0xFE && content[i+3] >= 0x30 && content[i+3] <= 0x39 {
			score += 1 // GB18030 四字节：合法但罕见
			i += 4
			continue
		}
		if i+1 >= len(content) {
			score -= 4
			break
		}
		t := content[i+1]
		if t == 0x7F || t < 0x40 {
			score -= 4 // 非法尾字节
			i++
			continue
		}
		if b >= 0xA1 && b <= 0xF7 && t >= 0xA1 && t <= 0xFE {
			score += 3 // GB2312 常用区
		} else {
			score += 1 // GBK 扩展区：合法但生僻
		}
		i += 2
	}
	return score
}

// big5Structure Big5 结构打分：常用区（首字节 0xA1–0xF9）得高分，扩展区低分。
func big5Structure(content []byte) int {
	score := 0
	for i := 0; i < len(content); {
		b := content[i]
		if b < 0x80 {
			score++
			i++
			continue
		}
		if i+1 >= len(content) {
			score -= 4
			break
		}
		t := content[i+1]
		trailOK := (t >= 0x40 && t <= 0x7E) || (t >= 0xA1 && t <= 0xFE)
		switch {
		case trailOK && b >= 0xA1 && b <= 0xF9:
			score += 3
			i += 2
		case trailOK && b >= 0x81 && b <= 0xA0:
			score += 1 // Big5 扩展区（罕见）
			i += 2
		default:
			score -= 4
			i++
		}
	}
	return score
}

// shiftJISStructure Shift-JIS 结构打分。半角片假名（0xA1–0xDF 单字节）合法但对
// 源码这类文本很罕见，因此不得分——这正是把"被误判为 Shift-JIS 的 GBK 中文"
// 拉回来的关键。
func shiftJISStructure(content []byte) int {
	score := 0
	for i := 0; i < len(content); {
		b := content[i]
		if b < 0x80 {
			score++
			i++
			continue
		}
		if b >= 0xA1 && b <= 0xDF {
			i++ // 半角片假名：+0
			continue
		}
		if i+1 >= len(content) {
			score -= 4
			break
		}
		t := content[i+1]
		leadOK := (b >= 0x81 && b <= 0x9F) || (b >= 0xE0 && b <= 0xFC)
		trailOK := (t >= 0x40 && t <= 0x7E) || (t >= 0x80 && t <= 0xFC)
		if leadOK && trailOK {
			score += 3
			i += 2
			continue
		}
		score -= 4
		i++
	}
	return score
}

// eucJPStructure EUC-JP 结构打分：0xA1–0xFE 双字节为主，0x8E/0x8F 为前缀。
func eucJPStructure(content []byte) int {
	score := 0
	for i := 0; i < len(content); {
		b := content[i]
		if b < 0x80 {
			score++
			i++
			continue
		}
		switch {
		case b == 0x8E && i+1 < len(content) && content[i+1] >= 0xA1 && content[i+1] <= 0xDF:
			i += 2 // 半角片假名
		case b == 0x8F && i+2 < len(content) && content[i+1] >= 0xA1 && content[i+1] <= 0xFE && content[i+2] >= 0xA1 && content[i+2] <= 0xFE:
			score += 1
			i += 3
		case b >= 0xA1 && b <= 0xFE && i+1 < len(content) && content[i+1] >= 0xA1 && content[i+1] <= 0xFE:
			score += 3
			i += 2
		default:
			score -= 4
			i++
		}
	}
	return score
}

// eucKRStructure EUC-KR 结构打分：0xA1–0xFE 双字节。
func eucKRStructure(content []byte) int {
	score := 0
	for i := 0; i < len(content); {
		b := content[i]
		if b < 0x80 {
			score++
			i++
			continue
		}
		if b >= 0xA1 && b <= 0xFE && i+1 < len(content) && content[i+1] >= 0xA1 && content[i+1] <= 0xFE {
			score += 3
			i += 2
			continue
		}
		score -= 4
		i++
	}
	return score
}
