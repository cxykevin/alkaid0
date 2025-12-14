package json

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

type jsonKeywordType string

// StringSlot 未完成标记
type StringSlot string

// ObjectSlot 未完成标记
type ObjectSlot map[string]*any

// ArraySlot 未完成标记
type ArraySlot []*any

const (
	jsonKeywordNull  jsonKeywordType = "null"
	jsonKeywordTrue  jsonKeywordType = "true"
	jsonKeywordFalse jsonKeywordType = "false"
)
