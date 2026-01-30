// Package json 流式解析 json
package json

import (
	"github.com/cxykevin/alkaid0/library/stack"
)

// Parser json 流式解析器
type Parser struct {
	FullCallingObject    *any
	mode                 jsonMode
	Stop                 bool
	typeStack            *stack.Stack
	StructStack          *stack.Stack
	stringTmp            string
	stringHexTmp         string
	pendingHighSurrogate int
	// stringIsKey 标识当前进入的字符串是作为对象的 key 还是 value
	stringIsKey     bool
	objectKeyTmp    *string
	numberMinus     bool
	keywordTmp      jsonKeywordType
	numTmp          string
	currentValuePtr *any
}

// New 创建解析器
func New() *Parser {
	stk := stack.New()
	stkStruct := stack.New()
	parser := &Parser{
		typeStack:            stk,
		StructStack:          stkStruct,
		stringHexTmp:         "",
		pendingHighSurrogate: 0,
	}
	return parser
}
