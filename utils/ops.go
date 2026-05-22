// Package u 极常用的公共短类型和函数
package u

// Ternary 三元运算
func Ternary[T any](v bool, a, b T) T {
	if v {
		return a
	}
	return b
}
