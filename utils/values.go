// Package u 极常用的公共短类型和函数
package u

// ValDefault 默认值
func ValDefault[T any](v *T, defaults T) T {
	if v == nil {
		return defaults
	}
	return *v
}

// AnyDefault 默认值
func AnyDefault[T any](v any, defaults T) T {
	v2, ok := v.(T)
	if !ok {
		return defaults
	}
	return v2
}
