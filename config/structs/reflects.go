package structs

import (
	"reflect"
	"strconv"
)

// BuildDefault 构造默认值
func BuildDefault[T any](obj T) T {
	// 校验必须是非空指针并且指向结构体
	v := reflect.ValueOf(&obj)
	if v.Kind() != reflect.Pointer {
		panic("BuildDefault: obj must be a non-nil pointer to a struct")
	}
	elem := v.Elem()
	if elem.Kind() != reflect.Struct {
		panic("BuildDefault: obj must be a pointer to a struct")
	}

	t := elem.Type()

	// 遍历所有字段
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		fv := elem.Field(i)

		// // 取字段名
		// fieldName := field.Name
		// fmt.Printf("fieldName: %s\n", fieldName)

		// 取 default 标签
		defaultTag := field.Tag.Get("default")
		kind := fv.Kind()
		if defaultTag != "" && fv.CanSet() {
			// if fv.Kind() == reflect.String {
			// 	fv.SetString(defaultTag)
			// }
			switch kind {
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				// 从 tag 中解析默认值
				deg, err := strconv.ParseInt(defaultTag, 10, 64)
				if err != nil {
					panic(err)
				}
				fv.SetInt(deg)
			case reflect.String:
				fv.SetString(defaultTag)
			case reflect.Float32, reflect.Float64:
				deg, err := strconv.ParseFloat(defaultTag, 64)
				if err != nil {
					panic(err)
				}
				fv.SetFloat(deg)
			case reflect.Bool:
				deg, err := strconv.ParseBool(defaultTag)
				if err != nil {
					panic(err)
				}
				fv.SetBool(deg)
			}
		}

		// 如果是值类型结构体，递归调用
		if kind == reflect.Struct {
			if fv.CanAddr() {
				BuildDefault(fv.Addr().Interface())
			}
			continue
		}

		// 如果是指向结构体的指针，确保已分配并递归调用
		if kind == reflect.Pointer && fv.Type().Elem().Kind() == reflect.Struct {
			if fv.IsNil() && fv.CanSet() {
				// 为指针字段分配一个新结构体实例
				fv.Set(reflect.New(fv.Type().Elem()))
			}
			if !fv.IsNil() {
				BuildDefault(fv.Interface())
			}
		}
	}
	return obj
}
