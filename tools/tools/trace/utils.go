package trace

import (
	"bytes"
	"io"

	"golang.org/x/net/html/charset"
	"golang.org/x/text/transform"
)

// fileContentToString 将文件内容转换为字符串
func fileContentToString(content []byte) string {
	if len(content) == 0 {
		return ""
	}

	// 使用 golang.org/x/net/html/charset 自动检测编码
	// 它会处理 BOM 并尝试预测编码
	e, _, _ := charset.DetermineEncoding(content, "")
	reader := transform.NewReader(bytes.NewReader(content), e.NewDecoder())

	decoded, err := io.ReadAll(reader)
	if err != nil {
		// 如果转换失败，兜底使用原始 string 转换
		return string(content)
	}

	return string(decoded)
}
