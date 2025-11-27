package structs

// CodeTheme 代码主题
type CodeTheme struct {
	Keyword  Color
	String   Color
	Comment  Color
	Number   Color
	Varible  Color
	Constant Color
	Operator Color
	Errors   Color
	Warnings Color
	Add      Color
	Remove   Color
}

// Theme 主题
type Theme struct {
	Name           string
	PrimaryColor   Color
	SecondaryColor Color
	UserTextColor  Color
	DefaultUIColor Color
	ReplyColor     Color
	CodeTheme      CodeTheme
}

// DefaultThemes 默认主题列表
var DefaultThemes = []Theme{
	{
		Name:           "Default Dark Theme",
		PrimaryColor:   Color{64, 158, 255}, // ElementPlus蓝 #409EFF
		SecondaryColor: Color{96, 165, 250}, // 潜蓝 #60A5FA
		UserTextColor:  Color{96, 98, 102},  // 次要文本色 #606266
		DefaultUIColor: Color{30, 64, 175},  // 深蓝 #1E40AF
		ReplyColor:     Color{48, 49, 51},   // ElementPlus主要文本色 #303133
		CodeTheme: CodeTheme{
			Keyword:  Color{147, 51, 234},  // #9333EA 紫色
			String:   Color{34, 197, 94},   // #22C55E 绿色
			Comment:  Color{156, 163, 175}, // #9CA3AF 灰色
			Number:   Color{251, 146, 60},  // #FB923C 橙色
			Varible:  Color{59, 130, 246},  // #60A5FA 潜蓝
			Constant: Color{239, 68, 68},   // #3B82F6 蓝色
			Operator: Color{168, 85, 247},  // #A855F7 紫色
			Errors:   Color{239, 68, 68},   // #EF4444 红色
			Warnings: Color{245, 158, 11},  // #F59E0B 黄色
			Add:      Color{34, 197, 94},   // #22C55E 绿色
			Remove:   Color{239, 68, 68},   // #EF4444 红色
		},
	},
	{
		Name:           "Claude Code Theme",
		PrimaryColor:   Color{255, 153, 0},   // 标题/提示标题的橙色 #FF9900
		SecondaryColor: Color{77, 144, 254},  // 欢迎语的蓝色 #4D90FE
		UserTextColor:  Color{255, 255, 255}, // 主体文字白色
		DefaultUIColor: Color{30, 30, 30},    // 深灰背景色 #444444ff
		ReplyColor:     Color{160, 160, 160}, // 次要文字浅灰
		CodeTheme: CodeTheme{
			Keyword:  Color{255, 153, 0},   // 主要橙色用于关键字
			String:   Color{77, 144, 254},  // 蓝色用于字符串
			Comment:  Color{120, 120, 120}, // 深灰注释 #787878
			Number:   Color{255, 200, 100}, // 浅橙色数字
			Varible:  Color{59, 130, 246},  // #60A5FA 潜蓝
			Constant: Color{239, 68, 68},   // #3B82F6 蓝色
			Operator: Color{255, 153, 0},   // 橙色运算符
			Errors:   Color{255, 60, 60},   // 错误红色
			Warnings: Color{255, 165, 0},   // 警告橙色
			Add:      Color{34, 197, 94},   // 新增绿色 (沿用惯例)
			Remove:   Color{239, 68, 68},   // 删除红色 (沿用惯例)
		},
	},
}
