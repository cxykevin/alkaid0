package structs

// Phrase 单个短语：短键 + 长内容 + 可选描述。
// 用户通过 /s <short> 输入短键，服务端在发送前展开为 Text 长内容。
type Phrase struct {
	// Short 短键，/s <short> 输入的唯一标识
	Short string
	// Text 短语展开后的完整内容，实际作为用户消息发送给 AI
	Text string
	// Desc 可选描述，用于 /s 列表展示与 AI 上下文中解释短语含义
	Desc string
}

// PhraseConfig 短语系统配置：/s <short> 展开为长内容发送
type PhraseConfig struct {
	// Enable 总开关，关闭时 /s 命令不可用
	Enable bool `default:"false"`
	// Phrases 短语表
	Phrases []Phrase
}
