package trace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/storage/structs"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
)

// 各编码的样本文本（源码里常见的内容形态）。
const (
	encSampleZh  = "// 中文注释：你好，世界。简体中文测试。\nfunc main() {\n\tprintln(\"你好\")\n}\n"
	encSampleTW  = "// 繁體中文註解：你好，世界。\nfunc main() {}\n"
	encSampleJP  = "// 日本語のコメント：こんにちは世界\nfunc main() {}\n"
	encSampleKR  = "// 한국어 주석: 안녕하세요 세계\nfunc main() {}\n"
	encSampleFR  = "// café déjà vu — naïve résumé\n"
	encSampleGB4 = "// 中文注释：你好，𠀋（GB18030 四字节）\n"
)

// TestDecodeTextEncodings 覆盖 read 工具实际会遇到的文件编码。
// 以前用 golang.org/x/net/html/charset（HTML 用）猜编码，非 UTF-8 的 GBK 中文会被
// 当成 windows-1252 解成 "ÖÐÎÄ" 乱码；这里逐个编码做回归。
func TestDecodeTextEncodings(t *testing.T) {
	cases := []struct {
		name     string
		enc      *encoding.Encoder
		body     string
		wantName string
	}{
		{"ascii", nil, "package main\n\nfunc main() {}\n", EncodingUTF8},
		{"utf-8", nil, encSampleZh, EncodingUTF8},
		{"gbk", simplifiedchinese.GBK.NewEncoder(), encSampleZh, EncodingGBK},
		{"gb18030", simplifiedchinese.GB18030.NewEncoder(), encSampleZh, EncodingGBK},
		{"gb18030-4byte", simplifiedchinese.GB18030.NewEncoder(), encSampleGB4, EncodingGB18030},
		{"big5", traditionalchinese.Big5.NewEncoder(), encSampleTW, EncodingBig5},
		{"shift_jis", japanese.ShiftJIS.NewEncoder(), encSampleJP, EncodingShiftJIS},

		{"windows-1252", charmap.Windows1252.NewEncoder(), encSampleFR, "windows-1252"},
		{"utf-16le-bom", unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder(), encSampleZh, EncodingUTF16LE},
		{"utf-16be-bom", unicode.UTF16(unicode.BigEndian, unicode.UseBOM).NewEncoder(), encSampleZh, EncodingUTF16BE},
		{"utf-16le-nobom", unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewEncoder(), encSampleZh, EncodingUTF16LE},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var raw []byte
			if tc.enc == nil {
				raw = []byte(tc.body)
			} else {
				b, err := tc.enc.Bytes([]byte(tc.body))
				if err != nil {
					t.Fatalf("encode: %v", err)
				}
				raw = b
			}
			got, name := DecodeText(raw)
			if got != tc.body {
				t.Errorf("解码结果不一致（判定为 %s）\n  got =%q\n  want=%q", name, got, tc.body)
			}
			if name != tc.wantName {
				t.Errorf("编码判定 = %q, want %q", name, tc.wantName)
			}
			// fileContentToString 是 read 工具的入口，必须与 DecodeText 一致
			if s := fileContentToString(raw); s != tc.body {
				t.Errorf("fileContentToString = %q, want %q", s, tc.body)
			}
		})
	}
}

// TestDecodeTextEUCJPAmbiguity EUC-JP 与 GBK 的得分几乎相同（同一段字节两者都能解成
// 合法 CJK，"假名"部分甚至解出来一模一样），冷门候选达不到领先幅度 → 不会判成 EUC-JP。
// 已知限制：该字节流同时也是合法 GBK 流，最终按简体中文先验落到 GBK。日文里更常见的
// Shift-JIS 不受影响（它与 GBK 的分差足够大，见 TestDecodeTextRealWorldSamples）。
func TestDecodeTextEUCJPAmbiguity(t *testing.T) {
	raw, err := japanese.EUCJP.NewEncoder().Bytes([]byte(encSampleJP))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, name := DecodeText(raw); name == EncodingEUCJP {
		t.Fatalf("领先幅度不够时不应认冷门编码 EUC-JP")
	}
}

// TestDecodeTextKoreanAmbiguity 验证冷门编码不会硬认：EUC-KR 与 GBK/Big5/EUC-JP 的合法
// 字节范围完全重叠（同一段字节都能解出合法 CJK、得分相同），冷门候选必须明显领先
// 常用编码才认，这里达不到 → 不会被判成 EUC-KR。
//
// 已知限制：该字节流同时是合法的 GBK 流，没有词频模型无法与简体中文区分，
// 因此最终会落到 GBK（简体中文先验），而不是 EUC-KR。
func TestDecodeTextKoreanAmbiguity(t *testing.T) {
	raw, err := korean.EUCKR.NewEncoder().Bytes([]byte(encSampleKR))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, name := DecodeText(raw)
	if name == EncodingEUCKR {
		t.Fatalf("领先幅度不够时不应认冷门编码 EUC-KR")
	}
	t.Logf("韩文样本最终落到 %s（已知限制，见注释），解出 %d 字节", name, len(got))
}

// TestDecodeTextUnrecognizableFallsBackToUTF8 验证认不出来的字节一律按 UTF-8 原样输出：
// 既不会硬套一个冷门编码，也不会用 ISO-8859-1 之类"能映射所有字节"的编码兜底。
func TestDecodeTextUnrecognizableFallsBackToUTF8(t *testing.T) {
	cases := map[string][]byte{
		// 高位字节密集、结构上能凑出 GBK 序列，但不像任何真实文本
		"dense-high": {0xA3, 0xC7, 0x91, 0xE5, 0x88, 0xB2, 0xD4, 0x9F, 0xF0, 0xA1, 0xBC, 0xDE,
			0xAB, 0xCD, 0xEF, 0x90, 0xB1, 0xC2, 0xD3, 0xE4, 0xF5, 0xA6, 0xB7, 0xC8},
		// 随机字节（无 NUL，躲过二进制判定）
		"random": {0x9E, 0x37, 0x8B, 0xFD, 0x22, 0xA4, 0x17, 0xC9, 0x5E, 0x80, 0x3F, 0xD1, 0x6A, 0xB8, 0x04, 0xE7},
	}
	for name, raw := range cases {
		got, enc := DecodeText(raw)
		if enc != EncodingUTF8 || got != string(raw) {
			t.Errorf("%s 应回落 UTF-8 原样输出: enc=%q got=%q", name, enc, got)
		}
	}
}

// TestDecodeTextUTF8BOMAndBinary BOM 要去掉；二进制不能解成乱码文本。
func TestDecodeTextUTF8BOMAndBinary(t *testing.T) {
	withBOM := append([]byte{0xEF, 0xBB, 0xBF}, []byte(encSampleZh)...)
	if got, name := DecodeText(withBOM); got != encSampleZh || name != EncodingUTF8BOM {
		t.Errorf("UTF-8 BOM: enc=%q got=%q", name, got)
	}

	// 基本是 UTF-8、只有个别坏字节：仍按 UTF-8 处理（保留原文，不整篇当 GBK 解）
	broken := []byte("package main\n// 中文注释：你好\n")
	broken = append(broken[:len(broken)-1], 0xFF, '\n')
	if got, name := DecodeText(broken); name != EncodingUTF8 || got != string(broken) {
		t.Errorf("含个别坏字节的 UTF-8 应判为 utf-8 且原样返回: enc=%q got=%q", name, got)
	}

	binaries := map[string][]byte{
		"png": {0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x08, 0x06, 0x00, 0x00, 0x00},
		"elf": {0x7F, 0x45, 0x4C, 0x46, 0x02, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x03, 0x00, 0x3E, 0x00},
		"nul": {0x41, 0x42, 0x00, 0x43, 0x44, 0x00, 0x45, 0x46},
	}
	for name, raw := range binaries {
		if got, enc := DecodeText(raw); got != "" || enc != EncodingBinary {
			t.Errorf("%s 应判为二进制: enc=%q got=%q", name, enc, got)
		}
	}
}

// TestReadToolDecodesNonUTF8File 走 read 工具真正的读盘路径（renderTraceFile）：
// GBK 源码文件注入给模型的内容必须是正确中文 + 行号，而不是 "ÖÐÎÄ" 这类乱码。
func TestReadToolDecodesNonUTF8File(t *testing.T) {
	tmpDir := t.TempDir()
	body := "package main\n\n// 中文注释：你好，世界\nfunc main() {}\n"
	gbk, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(body))
	if err != nil {
		t.Fatalf("encode gbk: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "main.go"), gbk, 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	block, ok := renderTraceFile(nil, tmpDir, structs.Traces{Path: "main.go"})
	if !ok {
		t.Fatal("renderTraceFile 应成功")
	}
	for _, want := range []string{"1|package main", "3|// 中文注释：你好，世界", "4|func main() {}"} {
		if !strings.Contains(block.Text, want) {
			t.Errorf("渲染内容缺少 %q\n实际：%s", want, block.Text)
		}
	}
	if strings.Contains(block.Text, "ÖÐÎÄ") {
		t.Errorf("不应把 GBK 当 windows-1252 解：%s", block.Text)
	}
}

// TestDecodeTextRealWorldSamples 用多组真实形态的样本回归：中文/繁体/日文/西里尔/西欧
// 文本各自都要判对，不能互相抢判（这是"编码自动推测不准确"的直接回归测试）。
func TestDecodeTextRealWorldSamples(t *testing.T) {
	zhSamples := []string{
		"// 中文注释：你好，世界。简体中文测试。\nfunc main() {}\n",
		"// 读取配置文件并校验必填项，失败时返回错误\n",
		"// 这个函数负责把日志按天切分，超过七天就删除旧文件\n",
		"// TODO: 这里需要处理并发写入导致的数据竞争问题\n",
		"// 参数说明：path 是文件路径，text 是要写入的内容\n",
		"/* 多行注释\n * 第一行：初始化数据库连接\n * 第二行：注册所有工具\n */\n",
		"// 中文与English混排：调用 SendRequest 发送请求\n",
		"type 用户 struct {\n\t名字 string\n\t年龄 int\n}\n",
	}
	for i, body := range zhSamples {
		raw, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(body))
		if err != nil {
			t.Fatalf("encode gbk: %v", err)
		}
		if got, name := DecodeText(raw); got != body || name != EncodingGBK {
			t.Errorf("GBK 样本 %d: enc=%s got=%q", i, name, got)
		}
	}

	twSamples := []string{
		"// 繁體中文註解：讀取設定檔並檢查必填欄位\n",
		"// 這個函式負責把日誌按天切分，超過七天就刪除舊檔案\n",
	}
	for i, body := range twSamples {
		raw, err := traditionalchinese.Big5.NewEncoder().Bytes([]byte(body))
		if err != nil {
			t.Fatalf("encode big5: %v", err)
		}
		if got, name := DecodeText(raw); got != body || name != EncodingBig5 {
			t.Errorf("Big5 样本 %d: enc=%s got=%q", i, name, got)
		}
	}

	jpSamples := []string{
		"// 日本語のコメント：設定ファイルを読み込んで検証する\n",
		"// この関数はログを日付ごとに分割します\n",
	}
	for i, body := range jpSamples {
		raw, err := japanese.ShiftJIS.NewEncoder().Bytes([]byte(body))
		if err != nil {
			t.Fatalf("encode shift_jis: %v", err)
		}
		if got, name := DecodeText(raw); got != body || name != EncodingShiftJIS {
			t.Errorf("Shift-JIS 样本 %d: enc=%s got=%q", i, name, got)
		}
	}

	// 单字节脚本：西里尔（windows-1251）与西欧（windows-1252）都必须判对——
	// 它们的重音/西里尔字母容易被误当成 GBK/Big5 的汉字。
	west := []struct {
		name     string
		body     string
		enc      *charmap.Charmap
		wantName string
	}{
		{"russian", "// Комментарий: привет мир, это тестовый файл\n", charmap.Windows1251, "windows-1251"},
		{"french", "// café déjà vu — naïve résumé\n", charmap.Windows1252, "windows-1252"},
		{"german", "// Datei ist groesser als erwartet: Größe 12\n", charmap.Windows1252, "windows-1252"},
		{"spanish", "// Comentario: año, niño, señor, corazón\n", charmap.Windows1252, "windows-1252"},
		{"portuguese", "// Comentário: ação, coração, não\n", charmap.Windows1252, "windows-1252"},
	}
	for _, c := range west {
		raw, err := c.enc.NewEncoder().Bytes([]byte(c.body))
		if err != nil {
			t.Fatalf("encode %s: %v", c.name, err)
		}
		got, name := DecodeText(raw)
		if got != c.body || name != c.wantName {
			t.Errorf("%s: enc=%q (want %q) got=%q", c.name, name, c.wantName, got)
		}
	}
}
