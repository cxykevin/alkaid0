package parser

import (
	"errors"
	"strconv"

	"github.com/cxykevin/alkaid0/provider/utils/stack"
)

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

const (
	jsonKeywordNull  jsonKeywordType = "null"
	jsonKeywordTrue  jsonKeywordType = "true"
	jsonKeywordFalse jsonKeywordType = "false"
)

// JSONParser json 流式解析器
type JSONParser struct {
	FullCallingObject *any
	mode              jsonMode
	Stop              bool
	TypeStack         *stack.Stack
	StructStack       *stack.Stack
	stringTmp         string
	objectKeyTmp      *string
	numberMinus       bool
	keywordTmp        jsonKeywordType
	numTmp            string
}

// AddToken 流式传入 token
func (p *JSONParser) AddToken(token string) error {
	if p.Stop {
		return errors.New("parser stopped but received token")
	}

	for _, v := range token {
		breakFlag := true
		for breakFlag {
			top, ok := p.TypeStack.Top()
			if !ok {
				return errors.New("invalid json format")
			}
			switch top.(jsonMode) {
			case jsonModeInObjectWaitingKey:
				switch v {
				case '"':
					p.mode = jsonModeInString
				case ':':

				default:
					return errors.New("invalid json format")
				}
			case jsonModeInObjectWaitingValue, jsonModeInArray:
				switch v {
				case ' ':
					breakFlag = false
				case '{':
					p.TypeStack.Push(jsonModeInObjectWaitingKey)
					obj := make(map[string]*any)
					p.StructStack.Push(&obj)
					if !ok {
						return errors.New("invalid json format")
					}
					breakFlag = false
				case '[':
					p.TypeStack.Push(jsonModeInArray)
					obj := make(map[string]*any)
					p.StructStack.Push(&obj)
					breakFlag = false
				case '"':
					p.TypeStack.Push(jsonModeInString)
					obj := ""
					p.StructStack.Push(&obj)
					breakFlag = false
				case 'n', 't', 'f':
					p.TypeStack.Push(jsonModeInKeyword)
					p.StructStack.Push(nil)
				case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9', '-', '.':
					p.TypeStack.Push(jsonModeInNumber)

					// if v == '-' {
					// 	p.numberMinus = true
					// } else {
					// 	p.numberMinus = false
					// }
					// if v == '.' {
					// 	obj := float64(0)
					// 	p.StructStack.Push(&obj)
					// } else {
					// 	obj := int64(0)
					// 	p.StructStack.Push(&obj)
					// }
				default:
					p.Stop = true
					return errors.New("invalid json")
				}
			case jsonModeInString:
				switch v {
				case '"':
					p.TypeStack.Pop()
					p.StructStack.Pop()
					breakFlag = false
				case '\\':
					p.TypeStack.Push(jsonModeInStringSpecialChar)
					breakFlag = false
				default:
					stkTop, ok := p.StructStack.Top()
					if !ok {
						p.Stop = true
						return errors.New("invalid json")
					}
					obj := stkTop.(*string)
					*obj = *obj + string(v)
					breakFlag = false
				}
			case jsonModeInStringSpecialChar:
				switch v {
				case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
					charTmp := ""
					switch v {
					case 'n':
						charTmp = "\n"
					case 'r':
						charTmp = "\r"
					case 't':
						charTmp = "\t"
					case 'b':
						charTmp = "\b"
					case 'f':
						charTmp = "\f"
					default:
						charTmp = string(v)
					}
					stkTop, ok := p.StructStack.Top()
					if !ok {
						p.Stop = true
						return errors.New("invalid json")
					}
					obj := stkTop.(*string)
					*obj = *obj + charTmp
					p.TypeStack.Pop()
					breakFlag = false
				default:
					p.Stop = true
					return errors.New("invalid json")
				}
			case jsonModeInNumber:
				switch v {
				case '0', '1', '2', '3', '4', '5', '6', '7', '8', '9', '.', '-', '+':
					p.numTmp += string(v)
				default:
					// 转数字
					num, err := strconv.ParseFloat(p.numTmp, 64)

				}
			}

		}
	}

	return nil
}

// DoneToken 传入结束 token
func (p *JSONParser) DoneToken() error {
	return nil
}

// NewJSONParser 创建解析器
func NewJSONParser() *JSONParser {

	stk := stack.New()
	stk.Push(jsonModeInArray)
	stkStruct := stack.New()
	stkSlice := make([]*any, 0)
	stkStruct.Push(&stkSlice)
	parser := &JSONParser{
		TypeStack:   stk,
		StructStack: stkStruct,
	}
	stkStruct.Push(parser.FullCallingObject)
	return parser
}
