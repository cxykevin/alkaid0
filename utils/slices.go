// Package u 极常用的公共短类型和函数
package u

// Map map(slice -> slice)
func Map[T any, R any](v []T, f func(T) R) []R {
	res := make([]R, len(v))
	for i, item := range v {
		res[i] = f(item)
	}
	return res
}

// MapFilter map + filter
func MapFilter[T any, R any](v []T, f func(T) (R, bool)) []R {
	res := make([]R, 0, len(v))
	for _, item := range v {
		if r, ok := f(item); ok {
			res = append(res, r)
		}
	}
	return res
}
