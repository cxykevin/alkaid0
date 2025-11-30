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

// StringNotFinishSlot 文本未完成标记
type StringNotFinishSlot string

const (
	jsonKeywordNull  jsonKeywordType = "null"
	jsonKeywordTrue  jsonKeywordType = "true"
	jsonKeywordFalse jsonKeywordType = "false"
)

// JSONParser json 流式解析器
type JSONParser struct {
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

// helper: push a container (map or slice) into structure stack and typestack
func (p *JSONParser) pushContainer(val any, contMode jsonMode) (*any, error) {
	ptr := new(any)
	*ptr = val

	if p.StructStack.Size() == 0 {
		p.StructStack.Push(ptr)
		if p.FullCallingObject == nil {
			p.FullCallingObject = ptr
		}
		p.typeStack.Push(contMode)
		return ptr, nil
	}

	// attach to parent
	topType, ok := p.typeStack.Top()
	if !ok {
		return nil, errors.New("invalid json structure")
	}
	switch topType.(jsonMode) {
	case jsonModeInArray:
		// append to array
		topPtrAny, ok := p.StructStack.Top()
		if !ok {
			return nil, errors.New("invalid json structure")
		}
		topPtr := topPtrAny.(*any)
		arr := (*topPtr).([]*any)
		arr = append(arr, ptr)
		*topPtr = arr
		p.StructStack.Push(ptr)
		p.typeStack.Push(contMode)
		return ptr, nil
	case jsonModeInObjectWaitingValue:
		topPtrAny, ok := p.StructStack.Top()
		if !ok {
			return nil, errors.New("invalid json structure")
		}
		topPtr := topPtrAny.(*any)
		obj := (*topPtr).(map[string]*any)
		if p.objectKeyTmp == nil {
			return nil, errors.New("missing object key")
		}
		obj[*p.objectKeyTmp] = ptr
		p.objectKeyTmp = nil
		p.StructStack.Push(ptr)
		p.typeStack.Push(contMode)
		return ptr, nil
	default:
		return nil, errors.New("unexpected parent container type")
	}
}

// helper: push a primitive value (string, number, bool, nil) into current container
func (p *JSONParser) pushValue(val any) error {
	var vptr *any
	if val != nil {
		vptr = new(any)
		*vptr = val
	} else {
		vptr = nil
	}

	if p.StructStack.Size() == 0 {
		// root value
		rootPtr := new(any)
		if vptr != nil {
			*rootPtr = *vptr
		} else {
			// nil value: keep interface nil
			var a any = nil
			*rootPtr = a
		}
		p.FullCallingObject = rootPtr
		p.currentValuePtr = rootPtr
		p.StructStack.Push(rootPtr)
		// 已经是最终值，不再需要实时更新
		p.currentValuePtr = nil
		return nil
	}
	topType, ok := p.typeStack.Top()
	if !ok {
		return errors.New("invalid json structure")
	}
	switch topType.(jsonMode) {
	case jsonModeInArray:
		arrPtrAny, ok := p.StructStack.Top()
		if !ok {
			return errors.New("invalid json structure")
		}
		arrPtr := arrPtrAny.(*any)
		arr := (*arrPtr).([]*any)
		arr = append(arr, vptr)
		*arrPtr = arr
		// 已经是最终值，不再需要实时更新
		p.currentValuePtr = nil
		return nil
	case jsonModeInObjectWaitingValue:
		objPtrAny, ok := p.StructStack.Top()
		if !ok {
			return errors.New("invalid json structure")
		}
		objPtr := objPtrAny.(*any)
		obj := (*objPtr).(map[string]*any)
		if p.objectKeyTmp == nil {
			return errors.New("missing object key")
		}
		obj[*p.objectKeyTmp] = vptr
		p.objectKeyTmp = nil
		// 已经是最终值，不再需要实时更新
		p.currentValuePtr = nil
		// after value assigned, expect next key
		// switch mode to waiting key
		_, _ = p.typeStack.Pop()
		p.typeStack.Push(jsonModeInObjectWaitingKey)
		return nil
	default:
		return errors.New("unexpected parent type for value")
	}
}

// beginValueSlot 创建一个值的占位符，并将它插入到当前容器（或作为根）中，返回指针
func (p *JSONParser) beginValueSlot(initial any) (*any, error) {
	vptr := new(any)
	*vptr = initial

	if p.StructStack.Size() == 0 {
		p.StructStack.Push(vptr)
		if p.FullCallingObject == nil {
			p.FullCallingObject = vptr
		}
		p.currentValuePtr = vptr
		return vptr, nil
	}

	topType, ok := p.typeStack.Top()
	if !ok {
		return nil, errors.New("invalid json structure")
	}
	switch topType.(jsonMode) {
	case jsonModeInArray:
		arrPtrAny, ok := p.StructStack.Top()
		if !ok {
			return nil, errors.New("invalid json structure")
		}
		arrPtr := arrPtrAny.(*any)
		arr := (*arrPtr).([]*any)
		arr = append(arr, vptr)
		*arrPtr = arr
		p.currentValuePtr = vptr
		return vptr, nil
	case jsonModeInObjectWaitingValue:
		objPtrAny, ok := p.StructStack.Top()
		if !ok {
			return nil, errors.New("invalid json structure")
		}
		objPtr := objPtrAny.(*any)
		obj := (*objPtr).(map[string]*any)
		if p.objectKeyTmp == nil {
			return nil, errors.New("missing object key")
		}
		obj[*p.objectKeyTmp] = vptr
		p.objectKeyTmp = nil
		p.currentValuePtr = vptr
		// after value assigned, switch back to waiting key
		_, _ = p.typeStack.Pop()
		p.typeStack.Push(jsonModeInObjectWaitingKey)
		return vptr, nil
	default:
		return nil, errors.New("unexpected parent container type")
	}
}

// AddToken 流式传入 token
func (p *JSONParser) AddToken(token string) error {
	if p.Stop {
		return errors.New("parser stopped but received token")
	}
	// 简化实现：以 mode 为主判断，TypeStack 仅用于跟踪容器 (object/array)
	isNumChar := func(r rune) bool {
		if r >= '0' && r <= '9' {
			return true
		}
		switch r {
		case '-', '+', '.', 'e', 'E':
			return true
		default:
			return false
		}
	}

	for _, rv := range token {
		switch p.mode {
		case jsonModeInString:
			// 如果存在未完成的高代理对，当前字符必须以 '\\' 开始以开始低代理的转义序列
			if p.pendingHighSurrogate != 0 && rv != '\\' {
				p.Stop = true
				return errors.New("expecting low surrogate escape sequence")
			}
			if rv == '\\' {
				p.mode = jsonModeInStringSpecialChar
				continue
			}
			if rv == '"' {
				// 字符串结束
				strVal := p.stringTmp
				p.stringTmp = ""
				p.mode = jsonModeDefault

				// 根据当前容器上下文决定是 key 还是 value
				if p.StructStack.Size() == 0 {
					// 根字符串
					if err := p.pushValue(strVal); err != nil {
						p.Stop = true
						return err
					}
					continue
				}
				// 根据进入字符串时的标记判断是 key 还是 value
				if p.stringIsKey {
					// 字符串作为 key
					k := new(string)
					*k = strVal
					p.objectKeyTmp = k
					p.stringIsKey = false
					continue
				}
				// 常规字符串值
				if p.currentValuePtr != nil {
					*p.currentValuePtr = strVal
					p.currentValuePtr = nil
				} else if err := p.pushValue(strVal); err != nil {
					p.Stop = true
					return err
				}
				continue
			}
			// 追加字符串内容
			p.stringTmp += string(rv)
			if p.currentValuePtr != nil {
				*p.currentValuePtr = StringNotFinishSlot(p.stringTmp)
			}
			continue
		case jsonModeInStringSpecialChar:
			// 支持常见转义
			switch rv {
			case 'u':
				// 开始 unicode 十六进制转义 \uXXXX
				p.stringHexTmp = ""
				p.mode = jsonModeInStringSpecialCharHex
				continue
			case 'n':
				p.stringTmp += "\n"
			case 'r':
				p.stringTmp += "\r"
			case 't':
				p.stringTmp += "\t"
			case 'b':
				p.stringTmp += "\b"
			case 'f':
				p.stringTmp += "\f"
			default:
				p.stringTmp += string(rv)
			}
			p.mode = jsonModeInString
			if p.currentValuePtr != nil {
				*p.currentValuePtr = StringNotFinishSlot(p.stringTmp)
			}
			continue
		case jsonModeInStringSpecialCharHex:
			// 处理 unicode 十六进制转义 \uXXXX
			// 接收 4 个 hex 字符
			if (rv >= '0' && rv <= '9') || (rv >= 'a' && rv <= 'f') || (rv >= 'A' && rv <= 'F') {
				p.stringHexTmp += string(rv)
				if len(p.stringHexTmp) == 4 {
					// 解析 hex
					v, err := strconv.ParseInt(p.stringHexTmp, 16, 32)
					if err != nil {
						p.Stop = true
						return errors.New("invalid unicode escape hex")
					}
					code := int(v)
					// 处理代理对
					if p.pendingHighSurrogate != 0 {
						// 期望低代理项
						if code >= 0xDC00 && code <= 0xDFFF {
							// 将两个代理对合并为一个 Unicode 码点
							high := p.pendingHighSurrogate
							low := code
							r := 0x10000 + ((high - 0xD800) << 10) + (low - 0xDC00)
							p.stringTmp += string(rune(r))
							p.pendingHighSurrogate = 0
						} else {
							p.Stop = true
							return errors.New("invalid low surrogate in unicode escape")
						}
					} else {
						// 判断是否为高代理项
						if code >= 0xD800 && code <= 0xDBFF {
							// 缓存高代理，等待低代理
							p.pendingHighSurrogate = code
						} else {
							p.stringTmp += string(rune(code))
						}
					}
					p.stringHexTmp = ""
					p.mode = jsonModeInString
					if p.currentValuePtr != nil {
						*p.currentValuePtr = StringNotFinishSlot(p.stringTmp)
					}
				}
				continue
			}
			// 非 hex 字符为非法
			p.Stop = true
			return errors.New("invalid unicode escape char")
		case jsonModeInNumber:
			if isNumChar(rv) {
				p.numTmp += string(rv)
				if p.currentValuePtr != nil {
					*p.currentValuePtr = p.numTmp
				}
				continue
			}
			// 结束数字，尝试解析
			num, err := strconv.ParseFloat(p.numTmp, 64)
			if err != nil {
				p.Stop = true
				return errors.New("invalid number format")
			}
			p.numTmp = ""
			p.mode = jsonModeDefault
			// push number value
			if p.currentValuePtr != nil {
				*p.currentValuePtr = num
				p.currentValuePtr = nil
			} else if err := p.pushValue(num); err != nil {
				p.Stop = true
				return err
			}
			// 继续处理当前字符（不跳过）
		case jsonModeInKeyword:
			if rv >= 'a' && rv <= 'z' {
				p.keywordTmp += jsonKeywordType(string(rv))
				if p.currentValuePtr != nil {
					*p.currentValuePtr = string(p.keywordTmp)
				}
				if p.keywordTmp == jsonKeywordNull || p.keywordTmp == jsonKeywordTrue || p.keywordTmp == jsonKeywordFalse {
					// 完成关键字
					var val any
					switch p.keywordTmp {
					case jsonKeywordNull:
						val = nil
					case jsonKeywordTrue:
						val = true
					case jsonKeywordFalse:
						val = false
					}
					p.keywordTmp = ""
					p.mode = jsonModeDefault
					if p.currentValuePtr != nil {
						*p.currentValuePtr = val
						p.currentValuePtr = nil
					} else if err := p.pushValue(val); err != nil {
						p.Stop = true
						return err
					}
				}
				continue
			}
			// 未被字母扩展的字符，认为关键字结束或非法
			if p.keywordTmp != jsonKeywordNull && p.keywordTmp != jsonKeywordTrue && p.keywordTmp != jsonKeywordFalse {
				p.Stop = true
				return errors.New("invalid keyword")
			}
			var val any
			switch p.keywordTmp {
			case jsonKeywordNull:
				val = nil
			case jsonKeywordTrue:
				val = true
			case jsonKeywordFalse:
				val = false
			}
			p.keywordTmp = ""
			p.mode = jsonModeDefault
			if p.currentValuePtr != nil {
				*p.currentValuePtr = val
				p.currentValuePtr = nil
			} else if err := p.pushValue(val); err != nil {
				p.Stop = true
				return err
			}
			// 继续处理当前字符
		}

		// 默认模式：处理容器、字符串起始、数字/关键字起始
		switch rv {
		case ' ', '\n', '\r', '\t':
			continue
		case '{':
			// 创建对象容器并进入对象解析模式
			obj := make(map[string]*any)
			_, err := p.pushContainer(obj, jsonModeInObjectWaitingKey)
			if err != nil {
				p.Stop = true
				return err
			}
			continue
		case '[':
			// 创建数组容器并进入数组解析模式
			arr := make([]*any, 0)
			_, err := p.pushContainer(arr, jsonModeInArray)
			if err != nil {
				p.Stop = true
				return err
			}
			continue
		case '}':
			top, ok := p.typeStack.Pop()
			if !ok || top.(jsonMode) != jsonModeInObjectWaitingKey {
				p.Stop = true
				return errors.New("unexpected '}'")
			}
			// pop struct stack
			structTop, _ := p.StructStack.Pop()
			if p.currentValuePtr != nil {
				if ptr, ok := structTop.(*any); ok && ptr == p.currentValuePtr {
					p.currentValuePtr = nil
				}
			}
			// 如果括号是对象被当作父对象的值时，需要将父对象状态从 WaitingValue 切换回 WaitingKey
			parentTop, okParent := p.typeStack.Top()
			if okParent && parentTop.(jsonMode) == jsonModeInObjectWaitingValue {
				_, _ = p.typeStack.Pop()
				p.typeStack.Push(jsonModeInObjectWaitingKey)
			}
			continue
		case ']':
			top, ok := p.typeStack.Pop()
			if !ok || top.(jsonMode) != jsonModeInArray {
				p.Stop = true
				return errors.New("unexpected ']'")
			}
			structTop, _ := p.StructStack.Pop()
			if p.currentValuePtr != nil {
				if ptr, ok := structTop.(*any); ok && ptr == p.currentValuePtr {
					p.currentValuePtr = nil
				}
			}
			// 如果数组是对象的一个值，需要将父对象状态从 WaitingValue 切换回 WaitingKey
			parentTop, okParent := p.typeStack.Top()
			if okParent && parentTop.(jsonMode) == jsonModeInObjectWaitingValue {
				_, _ = p.typeStack.Pop()
				p.typeStack.Push(jsonModeInObjectWaitingKey)
			}
			continue
		case '"':
			// Determine whether this is a key or a value
			topType, ok := p.typeStack.Top()
			if ok && topType.(jsonMode) == jsonModeInObjectWaitingKey {
				// It's a key string
				p.mode = jsonModeInString
				p.stringTmp = ""
				p.stringIsKey = true
				continue
			}
			// It's a value string (root, array element, or object value)
			p.mode = jsonModeInString
			p.stringTmp = ""
			p.stringIsKey = false
			// create placeholder in parent
			if _, err := p.beginValueSlot(""); err != nil {
				p.Stop = true
				return err
			}
			if p.currentValuePtr != nil {
				*p.currentValuePtr = StringNotFinishSlot("")
			}
			continue
		case 'n', 't', 'f':
			p.mode = jsonModeInKeyword
			p.keywordTmp = jsonKeywordType(string(rv))
			// create placeholder for keyword value
			if _, err := p.beginValueSlot(string(rv)); err != nil {
				p.Stop = true
				return err
			}
			continue
		case ':':
			// 切换对象模式到等待值
			top, ok := p.typeStack.Pop()
			if !ok {
				p.Stop = true
				return errors.New("unexpected ':'")
			}
			if top.(jsonMode) != jsonModeInObjectWaitingKey {
				p.Stop = true
				return errors.New("unexpected ':' context")
			}
			p.typeStack.Push(jsonModeInObjectWaitingValue)
			continue
		case ',':
			// 在对象中，切换回等待键；在数组中继续等待值
			top, ok := p.typeStack.Top()
			if !ok {
				p.Stop = true
				return errors.New("unexpected ','")
			}
			switch top.(jsonMode) {
			case jsonModeInArray:
				// nothing to do
			case jsonModeInObjectWaitingValue:
				_, _ = p.typeStack.Pop()
				p.typeStack.Push(jsonModeInObjectWaitingKey)
			default:
				// ignore commas in other contexts
			}
			continue
		default:
			if isNumChar(rv) {
				p.mode = jsonModeInNumber
				p.numTmp = string(rv)
				// create placeholder for number
				if _, err := p.beginValueSlot(p.numTmp); err != nil {
					p.Stop = true
					return err
				}
				continue
			}
			p.Stop = true
			return errors.New("invalid json char")
		}
	}

	return nil
}

// DoneToken 传入结束 token
func (p *JSONParser) DoneToken() error {
	if p.Stop {
		return errors.New("parser already stopped")
	}

	// 如果处于字符串模式，说明 JSON 不完整
	if p.mode == jsonModeInString || p.mode == jsonModeInStringSpecialChar || p.mode == jsonModeInStringSpecialCharHex {
		return errors.New("incomplete JSON (in string)")
	}

	// 尝试接受以数字/关键字结尾的输入，并在 EOF 时将值填充进占位符或容器
	if p.mode == jsonModeInNumber {
		num, err := strconv.ParseFloat(p.numTmp, 64)
		if err != nil {
			return errors.New("invalid number format at EOF")
		}
		p.numTmp = ""
		p.mode = jsonModeDefault
		if p.currentValuePtr != nil {
			*p.currentValuePtr = num
			p.currentValuePtr = nil
		} else if err := p.pushValue(num); err != nil {
			p.Stop = true
			return err
		}
	}
	if p.mode == jsonModeInKeyword {
		if p.keywordTmp != jsonKeywordNull && p.keywordTmp != jsonKeywordTrue && p.keywordTmp != jsonKeywordFalse {
			return errors.New("invalid keyword at EOF")
		}
		var val any
		switch p.keywordTmp {
		case jsonKeywordNull:
			val = nil
		case jsonKeywordTrue:
			val = true
		case jsonKeywordFalse:
			val = false
		}
		p.keywordTmp = ""
		p.mode = jsonModeDefault
		if p.currentValuePtr != nil {
			*p.currentValuePtr = val
			p.currentValuePtr = nil
		} else if err := p.pushValue(val); err != nil {
			p.Stop = true
			return err
		}
	}

	// 检查容器栈是否完全关闭
	if p.typeStack.Size() != 0 {
		return errors.New("incomplete JSON structure")
	}
	if p.pendingHighSurrogate != 0 {
		return errors.New("incomplete unicode surrogate pair at EOF")
	}

	return nil
}

// NewJSONParser 创建解析器
func NewJSONParser() *JSONParser {

	stk := stack.New()
	stkStruct := stack.New()
	parser := &JSONParser{
		typeStack:            stk,
		StructStack:          stkStruct,
		stringHexTmp:         "",
		pendingHighSurrogate: 0,
	}
	return parser
}
