package config

import (
	"reflect"

	"github.com/cxykevin/alkaid0/config/structs"
)

// EnsureContainerDefaults 把配置树中值为 nil 的 map / slice 就地替换为空容器
// （map → {}、slice → []），递归覆盖嵌套结构体、map 元素与 slice 元素。
//
// 为什么需要：Load() 只在"默认值基底"（structs.BuildDefault）阶段初始化容器，
// 而 encoding/json 把配置里显式写出的 null 解码成 nil map / slice，并**整体替换**
// 基底上的空容器——一个 null 值就足以让该字段在运行时、config/get 和落盘 JSON 里
// 永久变成 null（历史版本写入的配置、客户端 config/set 传入的空值都会这样）。
// 客户端配置树通常把 null 当成"字段不存在"，空列表 / 空映射因此无法编辑。
//
// 只处理 map / slice 本身：不改变标量字段与元素内容。nil 与空容器在读取侧语义一致
// （len == 0），因此运行时行为不变，只是序列化结果从 null 变成 {} / []。
func EnsureContainerDefaults(cfg *structs.Config) {
	if cfg == nil {
		return
	}
	initContainers(reflect.ValueOf(cfg).Elem())
}

// initContainers 深度遍历 v，把 nil 的 map / slice 置为空容器。
func initContainers(v reflect.Value) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return
		}
		initContainers(v.Elem())
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			if t.Field(i).PkgPath != "" {
				continue // 未导出字段
			}
			initContainers(v.Field(i))
		}
	case reflect.Map:
		if v.IsNil() {
			if v.CanSet() {
				v.Set(reflect.MakeMap(v.Type()))
			}
			return
		}
		initMapElements(v)
	case reflect.Slice:
		if v.IsNil() {
			if v.CanSet() {
				v.Set(reflect.MakeSlice(v.Type(), 0, 0))
			}
			return
		}
		// slice 元素本身可寻址（即便 slice 值不可寻址），可直接递归。
		for i := 0; i < v.Len(); i++ {
			initContainers(v.Index(i))
		}
	}
}

// initMapElements 处理 map 元素内部的容器：map 元素不可寻址，先复制成可写值、
// 补齐后再写回。元素类型不可能包含 map / slice 时直接跳过，避免无谓的 map 重建。
func initMapElements(m reflect.Value) {
	elemType := m.Type().Elem()
	switch elemType.Kind() {
	case reflect.Struct, reflect.Pointer, reflect.Map, reflect.Slice:
	default:
		return
	}
	keys := m.MapKeys()
	for _, key := range keys {
		elem := reflect.New(elemType).Elem()
		elem.Set(m.MapIndex(key))
		initContainers(elem)
		m.SetMapIndex(key, elem)
	}
}
