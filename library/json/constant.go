package json

// 状态机
type jsonMode int

const (
	jsonModeDefault jsonMode = iota
	jsonModeInObjectWaitingKey
	jsonModeInObjectWaitingValue
	jsonModeInObjectWaitingSep
	jsonModeInArray
	jsonModeInString
	jsonModeInStringSpecialChar
	jsonModeInStringSpecialCharHex
	jsonModeInNumber
	jsonModeInKeyword
)

// 关键字
type jsonKeywordType string

const (
	jsonKeywordNull  jsonKeywordType = "null"
	jsonKeywordTrue  jsonKeywordType = "true"
	jsonKeywordFalse jsonKeywordType = "false"
)

// 标记

// StringSlot 未完成标记
type StringSlot string

// ObjectSlot 未完成标记
type ObjectSlot map[string]*any

// ArraySlot 未完成标记
type ArraySlot []*any
