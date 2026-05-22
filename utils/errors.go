// Package u 极常用的公共短类型和函数
package u

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
