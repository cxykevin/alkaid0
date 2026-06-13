// Package json 流式解析 json
package json

import (
	"github.com/cxykevin/alkaid0/library/stack"
)

// Parser JSON 流式解析器，支持增量解析（逐 token 输入）。
// 设计用于 LLM 流式响应场景，可在 JSON 尚未完全到达时完成部分解析。
// 使用栈结构跟踪嵌套的 object/array 层级，支持四种 JSON 值的增量重建。
type Parser struct {
	FullCallingObject    *any         // 根解析结果指针，解析完成后指向完整 JSON 值
	mode                 jsonMode     // 当前状态机状态
	Stop                 bool         // 遇到致命错误时设为 true，停止后续解析
	typeStack            *stack.Stack // 容器类型栈（Object/Array），跟踪嵌套层级
	StructStack          *stack.Stack // 容器值栈，跟踪解析过程中的部分值
	stringTmp            string       // 正在构建中的字符串值缓存
	stringHexTmp         string       // Unicode 转义序列的十六进制数字缓存（\uXXXX）
	pendingHighSurrogate int          // 未完成的高代理对（U+D800-U+DBFF），用于代理对拼接
	// stringIsKey 标识当前进入的字符串是作为对象的 key 还是 value
	stringIsKey     bool
	objectKeyTmp    *string         // 当前正在处理的对象键名
	numberMinus     bool            // 数字是否以负号开头
	keywordTmp      jsonKeywordType // 正在构建中的关键字（null/true/false）
	numTmp          string          // 正在构建中的数字字符串
	currentValuePtr *any            // 当前正在构建的值指针，用于实时更新
}

// New 创建一个新的 JSON 流式解析器，初始化空的对象/数组栈。
// 每次解析新的 JSON 输入前都应创建一个新实例。
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
