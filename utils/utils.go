// Package u 极常用的公共短类型和函数
package u

import "encoding/json"

// H map[string]any（同 gin.H 风格）
type H map[string]any

// Unwrap 解包
func Unwrap[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// Assert 断言
func Assert(v error) {
	if v != nil {
		panic("assertion failed: " + v.Error())
	}
}

// AssertB 断言(bool)
func AssertB(v bool) {
	if !v {
		panic("assertion failed!")
	}
}

// Default 默认值
func Default[T any, K comparable](v map[K]T, key K, defaults T) T {
	val, ok := v[key]
	if !ok {
		return defaults
	}
	return val
}

// GetH 从 H 里取 type
func GetH[T any](v H, key string) (T, bool) {
	val, ok := v[key]
	var resNull T
	if !ok {
		return resNull, false
	}
	valObj, ok := val.(T)
	if !ok {
		return resNull, false
	}
	return valObj, true
}

// Apply 将 v 应用到 T
func Apply[T any](v H) (T, error) {
	bts, err := json.Marshal(v)
	if err != nil {
		return *new(T), err
	}
	var res T
	err = json.Unmarshal(bts, &res)
	return res, err
}

// ReApply 将 T 应用到 v
func ReApply[T any](v T) (H, error) {
	bts, err := json.Marshal(v)
	if err != nil {
		return H{}, err
	}
	var res H
	err = json.Unmarshal(bts, &res)
	return res, err
}

// Ternary 三元运算
func Ternary[T any](v bool, a, b T) T {
	if v {
		return a
	}
	return b
}
