package parser

import (
	"testing"
)

func TestJSONParser_SimpleObject(t *testing.T) {
	parser := NewJSONParser()
	jsonStr := "{\"a\":1}"
	for _, c := range jsonStr {
		err := parser.AddToken(string(c))
		if err != nil {
			t.Fatalf("AddToken error: %v", err)
		}
	}
	err := parser.DoneToken()
	if err != nil {
		t.Errorf("DoneToken error: %v", err)
	}
	// 检查 FullCallingObject 内容
	if parser.FullCallingObject == nil {
		t.Fatalf("FullCallingObject is nil")
	}
	rootAny := *parser.FullCallingObject
	rootMap, ok := rootAny.(map[string]*any)
	if !ok {
		t.Fatalf("FullCallingObject not an object")
	}
	valPtr, exists := rootMap["a"]
	if !exists || valPtr == nil {
		t.Fatalf("key 'a' missing or nil")
	}
	num, ok := (*valPtr).(float64)
	if !ok {
		t.Fatalf("value for 'a' not a number")
	}
	if num != 1 {
		t.Fatalf("expected 1 for 'a', got %v", num)
	}
}

func TestJSONParser_Array(t *testing.T) {
	parser := NewJSONParser()
	jsonStr := "[1,2,3]"
	for _, c := range jsonStr {
		err := parser.AddToken(string(c))
		if err != nil {
			t.Fatalf("AddToken error: %v", err)
		}
	}
	err := parser.DoneToken()
	if err != nil {
		t.Errorf("DoneToken error: %v", err)
	}
	// 检查 FullCallingObject 内容
	if parser.FullCallingObject == nil {
		t.Fatalf("FullCallingObject is nil")
	}
	rootAny := *parser.FullCallingObject
	rootArr, ok := rootAny.([]*any)
	if !ok {
		t.Fatalf("FullCallingObject not an array")
	}
	if len(rootArr) != 3 {
		t.Fatalf("expected array length 3, got %d", len(rootArr))
	}
	for i, expected := range []float64{1, 2, 3} {
		if rootArr[i] == nil {
			t.Fatalf("element %d is nil", i)
		}
		v, ok := (*rootArr[i]).(float64)
		if !ok {
			t.Fatalf("element %d not a number", i)
		}
		if v != expected {
			t.Fatalf("element %d expected %v got %v", i, expected, v)
		}
	}
}

func TestJSONParser_Incomplete(t *testing.T) {
	parser := NewJSONParser()
	jsonStr := "{\"a\":1"
	for _, c := range jsonStr {
		err := parser.AddToken(string(c))
		if err != nil {
			t.Fatalf("AddToken error: %v", err)
		}
	}
	err := parser.DoneToken()
	if err == nil {
		t.Errorf("DoneToken should error for incomplete JSON")
	}
}

func TestJSONParser_EmptyObject(t *testing.T) {
	parser := NewJSONParser()
	jsonStr := "{}"
	for _, c := range jsonStr {
		err := parser.AddToken(string(c))
		if err != nil {
			t.Fatalf("AddToken error: %v", err)
		}
	}
	err := parser.DoneToken()
	if err != nil {
		t.Errorf("DoneToken error: %v", err)
	}
	// 检查 FullCallingObject 内容
	if parser.FullCallingObject == nil {
		t.Fatalf("FullCallingObject is nil")
	}
	rootAny := *parser.FullCallingObject
	rootMap, ok := rootAny.(map[string]*any)
	if !ok {
		t.Fatalf("FullCallingObject not an object")
	}
	if len(rootMap) != 0 {
		t.Fatalf("expected empty object, got %d keys", len(rootMap))
	}
}

func TestJSONParser_Keywords(t *testing.T) {
	parser := NewJSONParser()
	jsonStr := "[true,false,null]"
	for _, c := range jsonStr {
		err := parser.AddToken(string(c))
		if err != nil {
			t.Fatalf("AddToken error: %v", err)
		}
	}
	err := parser.DoneToken()
	if err != nil {
		t.Errorf("DoneToken error: %v", err)
	}
	// 检查 FullCallingObject 内容
	if parser.FullCallingObject == nil {
		t.Fatalf("FullCallingObject is nil")
	}
	rootAny := *parser.FullCallingObject
	rootArr, ok := rootAny.([]*any)
	if !ok {
		t.Fatalf("FullCallingObject not an array")
	}
	if len(rootArr) != 3 {
		t.Fatalf("expected array length 3, got %d", len(rootArr))
	}
	// true, false, null
	if rootArr[0] == nil {
		t.Fatalf("first element nil")
	}
	if v, ok := (*rootArr[0]).(bool); !ok || v != true {
		t.Fatalf("first element not true")
	}
	if rootArr[1] == nil {
		t.Fatalf("second element nil")
	}
	if v, ok := (*rootArr[1]).(bool); !ok || v != false {
		t.Fatalf("second element not false")
	}
	// third should be nil (either nil pointer or pointer whose value is nil)
	if rootArr[2] == nil {
		// ok
	} else if (*rootArr[2]) == nil {
		// ok
	} else {
		t.Fatalf("third element expected nil, got %v", rootArr[2])
	}
}
func TestJSONParser_ErrorKeywords(t *testing.T) {
	parser := NewJSONParser()
	jsonStr := "[true,falxe]"
	flag := false
	for _, c := range jsonStr {
		err := parser.AddToken(string(c))
		if err != nil {
			flag = true
			break
		}
	}
	if !flag {
		t.Errorf("AddToken should error for invalid keywords")
	}
	jsonStr = "[true,fal]"
	flag = false
	for _, c := range jsonStr {
		err := parser.AddToken(string(c))
		if err != nil {
			flag = true
			break
		}
	}
	if !flag {
		t.Errorf("AddToken should error for invalid keywords")
	}
	jsonStr = "[true,fal"
	flag = false
	for _, c := range jsonStr {
		err := parser.AddToken(string(c))
		if err != nil {
			flag = true
			break
		}
	}
	err := parser.DoneToken()
	if err != nil {
		flag = true
	}
	if !flag {
		t.Errorf("AddToken should error for invalid keywords")
	}
}

func TestJSONParser_DynamicString(t *testing.T) {
	parser := NewJSONParser()
	jsonStr := "{\"a\":\""
	err := parser.AddToken(string(jsonStr))
	strCmpTmp := ""
	// 随机字符串
	dynamicTestStr := ""
	for i := range 100 {
		dynamicTestStr += string(rune(97 + i%26))
	}
	for _, c := range dynamicTestStr {
		strCmpTmp += string(c)
		err = parser.AddToken(string(c))
		if err != nil {
			t.Fatalf("AddToken error: %v", err)
		}
		// 检查 FullCallingObject 中的值实时更新
		if parser.FullCallingObject == nil {
			t.Fatalf("FullCallingObject is nil at iteration")
		}
		rootAny := *parser.FullCallingObject
		rootMap, ok := rootAny.(map[string]*any)
		if !ok {
			t.Fatalf("FullCallingObject not a map at iteration")
		}
		valPtr, exists := rootMap["a"]
		if !exists || valPtr == nil {
			t.Fatalf("value for key 'a' missing at iteration, exists=%v", exists)
		}
		// 值可以是 string 或 StringNotFinishSlot（未完成的字符串占位）
		if valStr, ok := (*valPtr).(string); ok {
			if valStr != strCmpTmp {
				t.Fatalf("expected '%s', got '%s' at iteration", strCmpTmp, valStr)
			}
		} else if valTmp, ok := (*valPtr).(StringNotFinishSlot); ok {
			if string(valTmp) != strCmpTmp {
				t.Fatalf("expected '%s', got '%s' at iteration (StringNotFinishSlot)", strCmpTmp, string(valTmp))
			}
		} else {
			t.Fatalf("value for key 'a' not string or StringNotFinishSlot at iteration")
		}
	}
	parser.AddToken("\"}")
	err = parser.DoneToken()
	if err != nil {
		t.Errorf("DoneToken error: %v", err)
	}
}

func TestJSONParser_NestedStructures(t *testing.T) {
	parser := NewJSONParser()
	jsonStr := "{\"a\":[1,{\"b\":[2,3]},4],\"c\":{\"d\":5}}"
	for _, c := range jsonStr {
		if err := parser.AddToken(string(c)); err != nil {
			t.Fatalf("AddToken error: %v", err)
		}
	}
	if err := parser.DoneToken(); err != nil {
		t.Fatalf("DoneToken error: %v", err)
	}
	if parser.FullCallingObject == nil {
		t.Fatalf("FullCallingObject is nil")
	}
	rootAny := *parser.FullCallingObject
	rootMap, ok := rootAny.(map[string]*any)
	if !ok {
		t.Fatalf("FullCallingObject not an object")
	}
	// a
	aPtr, exists := rootMap["a"]
	if !exists || aPtr == nil {
		t.Fatalf("missing key a")
	}
	aArr, ok := (*aPtr).([]*any)
	if !ok {
		t.Fatalf("a is not an array")
	}
	if len(aArr) != 3 {
		t.Fatalf("expected a length 3, got %d", len(aArr))
	}
	if v, ok := (*aArr[0]).(float64); !ok || v != 1 {
		t.Fatalf("a[0] expected 1, got %v", (*aArr[0]))
	}
	// nested object b
	nestedObj, ok := (*aArr[1]).(map[string]*any)
	if !ok {
		t.Fatalf("a[1] expected object")
	}
	bPtr, exists := nestedObj["b"]
	if !exists || bPtr == nil {
		t.Fatalf("missing b in nested object")
	}
	bArr, ok := (*bPtr).([]*any)
	if !ok || len(bArr) != 2 {
		t.Fatalf("b not an array of length 2")
	}
	if (*bArr[0]).(float64) != 2 || (*bArr[1]).(float64) != 3 {
		t.Fatalf("b array mismatch")
	}
	// c.d == 5
	cPtr, exists := rootMap["c"]
	if !exists || cPtr == nil {
		t.Fatalf("missing key c")
	}
	cObj, ok := (*cPtr).(map[string]*any)
	if !ok {
		t.Fatalf("c is not an object")
	}
	if (*cObj["d"]).(float64) != 5 {
		t.Fatalf("c.d expected 5")
	}
}

func TestJSONParser_EscapedCharacters(t *testing.T) {
	parser := NewJSONParser()
	// 使用 raw string 保持 JSON 中的反斜杠序列
	jsonStr := `{"s":"Line\nTab\tQuote\"Backslash\\"}`
	for _, c := range jsonStr {
		if err := parser.AddToken(string(c)); err != nil {
			t.Fatalf("AddToken error: %v", err)
		}
	}
	if err := parser.DoneToken(); err != nil {
		t.Fatalf("DoneToken error: %v", err)
	}
	if parser.FullCallingObject == nil {
		t.Fatalf("FullCallingObject is nil")
	}
	rootAny := *parser.FullCallingObject
	rootMap, ok := rootAny.(map[string]*any)
	if !ok {
		t.Fatalf("FullCallingObject not an object")
	}
	sPtr := rootMap["s"]
	if sPtr == nil {
		t.Fatalf("key 's' missing")
	}
	sVal, ok := (*sPtr).(string)
	if !ok {
		t.Fatalf("s not a string")
	}
	expected := "Line\nTab\tQuote\"Backslash\\"
	if sVal != expected {
		t.Fatalf("expected %q got %q", expected, sVal)
	}
}

func TestJSONParser_NumbersAndExponents(t *testing.T) {
	parser := NewJSONParser()
	jsonStr := "[-1,3.14,1e10,-2E-3]"
	for _, c := range jsonStr {
		if err := parser.AddToken(string(c)); err != nil {
			t.Fatalf("AddToken error: %v", err)
		}
	}
	if err := parser.DoneToken(); err != nil {
		t.Fatalf("DoneToken error: %v", err)
	}
	if parser.FullCallingObject == nil {
		t.Fatalf("FullCallingObject is nil")
	}
	rootAny := *parser.FullCallingObject
	arr, ok := rootAny.([]*any)
	if !ok {
		t.Fatalf("not array")
	}
	expected := []float64{-1, 3.14, 1e10, -2e-3}
	if len(arr) != len(expected) {
		t.Fatalf("expected len %d got %d", len(expected), len(arr))
	}
	for i := range expected {
		if (*arr[i]).(float64) != expected[i] {
			t.Fatalf("element %d mismatch, expected %v got %v", i, expected[i], (*arr[i]))
		}
	}
}

func TestJSONParser_RootPrimitives(t *testing.T) {
	// number root
	parser := NewJSONParser()
	if err := parser.AddToken("123"); err != nil {
		t.Fatalf("AddToken error: %v", err)
	}
	if err := parser.DoneToken(); err != nil {
		t.Fatalf("DoneToken error: %v", err)
	}
	if parser.FullCallingObject == nil {
		t.Fatalf("FullCallingObject is nil")
	}
	if v, ok := (*parser.FullCallingObject).(float64); !ok || v != 123 {
		t.Fatalf("expected number 123 root, got %v", *parser.FullCallingObject)
	}

	// string root
	parser = NewJSONParser()
	if err := parser.AddToken("\"hello\""); err != nil {
		t.Fatalf("AddToken error: %v", err)
	}
	if err := parser.DoneToken(); err != nil {
		t.Fatalf("DoneToken error: %v", err)
	}
	if s, ok := (*parser.FullCallingObject).(string); !ok || s != "hello" {
		t.Fatalf("expected string root 'hello', got %v", *parser.FullCallingObject)
	}

	// true root
	parser = NewJSONParser()
	if err := parser.AddToken("true"); err != nil {
		t.Fatalf("AddToken error: %v", err)
	}
	if err := parser.DoneToken(); err != nil {
		t.Fatalf("DoneToken error: %v", err)
	}
	if b, ok := (*parser.FullCallingObject).(bool); !ok || b != true {
		t.Fatalf("expected bool true root, got %v", *parser.FullCallingObject)
	}

	// null root
	parser = NewJSONParser()
	if err := parser.AddToken("null"); err != nil {
		t.Fatalf("AddToken error: %v", err)
	}
	if err := parser.DoneToken(); err != nil {
		t.Fatalf("DoneToken error: %v", err)
	}
	if (*parser.FullCallingObject) != nil {
		t.Fatalf("expected nil root, got %v", *parser.FullCallingObject)
	}
}

func TestJSONParser_TrailingGarbageAndIncompleteNumber(t *testing.T) {
	// trailing garbage after object
	parser := NewJSONParser()
	jsonStr := "{\"a\":1}x"
	err := error(nil)
	for _, c := range jsonStr {
		err = parser.AddToken(string(c))
		if err != nil {
			break
		}
	}
	if err == nil {
		t.Fatalf("expected error for trailing garbage, got nil")
	}

	// incomplete number at EOF
	parser = NewJSONParser()
	if err := parser.AddToken("1e"); err != nil {
		// AddToken itself might not error until DoneToken
	}
	if err := parser.DoneToken(); err == nil {
		t.Fatalf("expected DoneToken error for incomplete number, got nil")
	}
}

func TestJSONParser_TrailingCommaArray(t *testing.T) {
	parser := NewJSONParser()
	jsonStr := "[1,]"
	for _, c := range jsonStr {
		if err := parser.AddToken(string(c)); err != nil {
			t.Fatalf("AddToken error: %v", err)
		}
	}
	if err := parser.DoneToken(); err != nil {
		t.Fatalf("DoneToken error: %v", err)
	}
	rootAny := *parser.FullCallingObject
	arr, ok := rootAny.([]*any)
	if !ok {
		t.Fatalf("expected array")
	}
	if len(arr) != 1 {
		t.Fatalf("expected length 1 for [1,], got %d", len(arr))
	}
}

func TestJSONParser_MultiTokenAdd(t *testing.T) {
	parser := NewJSONParser()
	// 在一次 AddToken 中传入完整 JSON
	if err := parser.AddToken("[1,2,3]"); err != nil {
		t.Fatalf("AddToken error: %v", err)
	}
	if err := parser.DoneToken(); err != nil {
		t.Fatalf("DoneToken error: %v", err)
	}
	rootAny := *parser.FullCallingObject
	arr, ok := rootAny.([]*any)
	if !ok || len(arr) != 3 {
		t.Fatalf("expected array of len 3, got %v", rootAny)
	}
}

func TestJSONParser_UnicodeEscapeBasic(t *testing.T) {
	parser := NewJSONParser()
	jsonStr := "{\"s\":\"\\u0041\"}"
	for _, c := range jsonStr {
		if err := parser.AddToken(string(c)); err != nil {
			t.Fatalf("AddToken error: %v", err)
		}
	}
	if err := parser.DoneToken(); err != nil {
		t.Fatalf("DoneToken error: %v", err)
	}
	if parser.FullCallingObject == nil {
		t.Fatalf("FullCallingObject is nil")
	}
	rootAny := *parser.FullCallingObject
	rootMap, ok := rootAny.(map[string]*any)
	if !ok {
		t.Fatalf("FullCallingObject not an object")
	}
	sPtr, ok := rootMap["s"]
	if !ok || sPtr == nil {
		t.Fatalf("missing s")
	}
	if s, _ := (*sPtr).(string); s != "A" {
		t.Fatalf("expected 'A', got %v", *sPtr)
	}
}

func TestJSONParser_UnicodeSurrogatePair(t *testing.T) {
	parser := NewJSONParser()
	// 😀 U+1F600 github sequence: \uD83D\uDE00
	jsonStr := "{\"e\":\"\\uD83D\\uDE00\"}"
	for _, c := range jsonStr {
		if err := parser.AddToken(string(c)); err != nil {
			t.Fatalf("AddToken error: %v", err)
		}
	}
	if err := parser.DoneToken(); err != nil {
		t.Fatalf("DoneToken error: %v", err)
	}
	if parser.FullCallingObject == nil {
		t.Fatalf("FullCallingObject is nil")
	}
	rootAny := *parser.FullCallingObject
	rootMap, ok := rootAny.(map[string]*any)
	if !ok {
		t.Fatalf("FullCallingObject not an object")
	}
	ePtr, ok := rootMap["e"]
	if !ok || ePtr == nil {
		t.Fatalf("missing e")
	}
	if s, _ := (*ePtr).(string); s != "😀" {
		t.Fatalf("expected '😀', got %v", *ePtr)
	}
}

func TestJSONParser_UnicodeEscapeIncomplete(t *testing.T) {
	parser := NewJSONParser()
	// incomplete hex digits
	jsonStr := "{\"s\":\"\\u00" // incomplete
	for _, c := range jsonStr {
		if err := parser.AddToken(string(c)); err != nil {
			t.Fatalf("AddToken error: %v", err)
		}
	}
	if err := parser.DoneToken(); err == nil {
		t.Fatalf("DoneToken should return error for incomplete unicode escape")
	}
}

func TestJSONParser_StringEndFlag(t *testing.T) {
	parser := NewJSONParser()
	// 流式传入字符串，检查字符串未完成的占位符 (StringNotFinishSlot)
	arr := []string{"{", "\"a\":\""}
	for _, s := range arr {
		if err := parser.AddToken(s); err != nil {
			t.Fatalf("AddToken error: %v", err)
		}
		// 如果存在 currentValuePtr，则它应该被标记为 StringNotFinishSlot
		if parser.currentValuePtr != nil {
			if _, ok := (*parser.currentValuePtr).(StringNotFinishSlot); !ok {
				t.Fatalf("current value should be a StringNotFinishSlot while parsing")
			}
		}
	}
	// 添加中间内容
	if err := parser.AddToken("hello"); err != nil {
		t.Fatalf("AddToken error: %v", err)
	}
	// 仍然处于字符串解析中，待填写的值应为 StringNotFinishSlot
	if parser.currentValuePtr == nil {
		t.Fatalf("currentValuePtr should not be nil while parsing string")
	}
	if _, ok := (*parser.currentValuePtr).(StringNotFinishSlot); !ok {
		t.Fatalf("current value should be a StringNotFinishSlot while parsing")
	}
	// 结束字符串
	if err := parser.AddToken("\"}"); err != nil {
		t.Fatalf("AddToken error: %v", err)
	}
	// 字符串结束后，currentValuePtr 应置为 nil，FullCallingObject 中的值应为普通 string
	if parser.currentValuePtr != nil {
		t.Fatalf("currentValuePtr should be nil after string closed but is %T with value %v", *parser.currentValuePtr, *parser.currentValuePtr)
	}
	// 检查 FullCallingObject 中值
	rootAny := *parser.FullCallingObject
	rootMap, ok := rootAny.(map[string]*any)
	if !ok {
		t.Fatalf("FullCallingObject not an object")
	}
	valPtr, exists := rootMap["a"]
	if !exists || valPtr == nil {
		t.Fatalf("key 'a' missing or nil")
	}
	s, ok := (*valPtr).(string)
	if !ok {
		t.Fatalf("value for 'a' not a string")
	}
	if s != "hello" {
		t.Fatalf("expected 'hello' for 'a', got %v", s)
	}
}
