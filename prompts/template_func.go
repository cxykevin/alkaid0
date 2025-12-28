package prompts

import (
	"fmt"
	"strconv"
)

// toInt 将字符串转换为整数
func toInt(s any) int {
	switch v := s.(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case string:
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	case float64:
		return int(v)
	}
	return 0
}

// sub 执行减法运算
func sub(a, b any) int {
	aInt := toInt(a)
	bInt := toInt(b)
	return aInt - bInt
}

// toString 将任意类型转换为字符串
func toString(s any) string {
	switch v := s.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	case int, int32, int64:
		return fmt.Sprintf("%d", v)
	case float32, float64:
		return fmt.Sprintf("%f", v)
	case bool:
		return fmt.Sprintf("%t", v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// le 小于等于比较
func le(a, b any) bool {
	aInt := toInt(a)
	bInt := toInt(b)
	return aInt <= bInt
}

// gt 大于比较
func gt(a, b any) bool {
	aInt := toInt(a)
	bInt := toInt(b)
	return aInt > bInt
}
