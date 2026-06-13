package json

// jsonMode JSON 解析器状态机状态类型
type jsonMode int

const (
	jsonModeDefault                jsonMode = iota // 默认状态
	jsonModeInObjectWaitingKey                     // 对象中等待键名
	jsonModeInObjectWaitingValue                   // 对象中等待值
	jsonModeInObjectWaitingSep                     // 对象中等待分隔符
	jsonModeInArray                                // 数组中
	jsonModeInString                               // 字符串中
	jsonModeInStringSpecialChar                    // 字符串中转义字符
	jsonModeInStringSpecialCharHex                 // 字符串中十六进制转义
	jsonModeInNumber                               // 数字中
	jsonModeInKeyword                              // 关键字中（null/true/false）
)

// jsonKeywordType JSON 关键字类型（null/true/false）
type jsonKeywordType string

const (
	jsonKeywordNull  jsonKeywordType = "null"  // null 关键字
	jsonKeywordTrue  jsonKeywordType = "true"  // true 关键字
	jsonKeywordFalse jsonKeywordType = "false" // false 关键字
)

// StringSlot 流式解析中的未完成字符串占位符
type StringSlot string

// ObjectSlot 流式解析中的未完成对象占位符
type ObjectSlot map[string]*any

// ArraySlot 流式解析中的未完成数组占位符
type ArraySlot []*any
