package trace

// fileContentToString 将文件内容转换为字符串（编码检测见 DecodeText）。
// 判定为二进制时返回空串，调用方据此给出"可能是二进制文件"的提示，
// 而不是把乱码喂给模型。
func fileContentToString(content []byte) string {
	text, _ := DecodeText(content)
	return text
}
