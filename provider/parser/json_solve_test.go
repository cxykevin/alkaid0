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
		valStr, ok := (*valPtr).(string)
		if !ok {
			t.Fatalf("value for key 'a' not string at iteration")
		}
		if valStr != strCmpTmp {
			t.Fatalf("expected '%s', got '%s' at iteration", strCmpTmp, valStr)
		}
	}
	parser.AddToken("\"}")
	err = parser.DoneToken()
	if err != nil {
		t.Errorf("DoneToken error: %v", err)
	}
}
