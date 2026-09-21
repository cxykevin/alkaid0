package trace

import (
	"bytes"
	"testing"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
)

// TestEncodeTextRoundTrip 验证 DecodeTextEx/EncodeText 成对往返：edit 按原编码写回，
// 非 UTF-8 文件（含 BOM/UTF-16）字节必须保持一致，否则"能编辑"会变成"编错"。
func TestEncodeTextRoundTrip(t *testing.T) {
	utf8BOM := append([]byte{0xEF, 0xBB, 0xBF}, []byte(encSampleZh)...)
	utf16LEBOM, err := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder().Bytes([]byte(encSampleZh))
	if err != nil {
		t.Fatal(err)
	}
	utf16BEBOM, err := unicode.UTF16(unicode.BigEndian, unicode.UseBOM).NewEncoder().Bytes([]byte(encSampleZh))
	if err != nil {
		t.Fatal(err)
	}
	utf16LENoBOM, err := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewEncoder().Bytes([]byte(encSampleZh))
	if err != nil {
		t.Fatal(err)
	}
	gbkRaw, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(encSampleZh))
	if err != nil {
		t.Fatal(err)
	}
	gb18030Raw, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(encSampleGB4))
	if err != nil {
		t.Fatal(err)
	}
	big5Raw, err := traditionalchinese.Big5.NewEncoder().Bytes([]byte(encSampleTW))
	if err != nil {
		t.Fatal(err)
	}
	sjisRaw, err := japanese.ShiftJIS.NewEncoder().Bytes([]byte(encSampleJP))
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		text    string
		raw     []byte
		wantEnc string
	}{
		{"utf-8", encSampleZh, []byte(encSampleZh), EncodingUTF8},
		{"utf-8-bom", encSampleZh, utf8BOM, EncodingUTF8BOM},
		{"utf-16le-bom", encSampleZh, utf16LEBOM, EncodingUTF16LE},
		{"utf-16be-bom", encSampleZh, utf16BEBOM, EncodingUTF16BE},
		{"utf-16le-nobom", encSampleZh, utf16LENoBOM, EncodingUTF16LE},
		{"gbk", encSampleZh, gbkRaw, EncodingGBK},
		{"gb18030", encSampleGB4, gb18030Raw, EncodingGB18030},
		{"big5", encSampleTW, big5Raw, EncodingBig5},
		{"shift_jis", encSampleJP, sjisRaw, EncodingShiftJIS},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text, name, hasBOM := DecodeTextEx(tc.raw)
			if text != tc.text || name != tc.wantEnc {
				t.Fatalf("DecodeTextEx = (%q, %q, %v), want (%q, %q)", text, name, hasBOM, tc.text, tc.wantEnc)
			}
			out, err := EncodeText(text, name, hasBOM)
			if err != nil {
				t.Fatalf("EncodeText: %v", err)
			}
			if !bytes.Equal(out, tc.raw) {
				t.Fatalf("往返字节不一致:\n got=% x\nwant=% x", out, tc.raw)
			}
			if got, name2 := DecodeText(out); got != tc.text || name2 != tc.wantEnc {
				t.Fatalf("二次解码 = (%q, %q)", got, name2)
			}
		})
	}
}

// TestEncodeTextRejectsUnrepresentable 验证目标编码表示不了的字符会报错而不是
// 静默替换：edit 据此拒绝写盘，不能损坏用户文件。
func TestEncodeTextRejectsUnrepresentable(t *testing.T) {
	if _, err := EncodeText("emoji 😀", EncodingGBK, false); err == nil {
		t.Fatal("GBK 表示不了的字符必须返回错误")
	}
	if _, err := EncodeText("emoji 😀", "iso-8859-1", false); err == nil {
		t.Fatal("iso-8859-1 表示不了的字符必须返回错误")
	}
}
