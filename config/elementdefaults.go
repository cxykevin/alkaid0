package config

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"

	"github.com/cxykevin/alkaid0/config/structs"
)

// EnsureElementDefaults 以 default tag 为基底重建配置里 map / slice 字段的元素。
//
// 为什么需要：Load() 把用户配置叠加到默认配置（structs.BuildDefault）上时，
// struct 字段是**就地更新**——文件中未出现的字段保留默认值；但 map 与 slice 字段
// 的元素会被 encoding/json **整体替换**，元素内未写出的字段降为 Go 零值而非
// default tag 值。例如 Model.Models["0"] 只写了 ModelName/ModelID 时，TokenLimit
// 会变成 0、ModelTopP 会变成 0、ProviderSpecificConfig.EnableReasoningEffort 会
// 丢成 false（0 的上下文预算会让请求直接被供应商拒绝）。
//
// 这里按 raw 中实际出现的 map 键 / slice 下标重建元素：先套用 default tag 默认值，
// 再叠加该元素在 raw 中显式写出的字段——因此用户显式写出的值（含 0 / false / 空串）
// 一律保留，只有真正缺失的字段才回落到默认值。raw 中没有出现的 map / slice 字段
// 保持解析结果不变。
func EnsureElementDefaults(cfg *structs.Config, raw json.RawMessage) {
	if cfg == nil || len(raw) == 0 {
		return
	}
	var tree map[string]any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return
	}
	fillContainerElements(reflect.ValueOf(cfg).Elem(), tree)
}

// fillContainerElements 递归遍历结构体：map / slice 字段按其 raw 子节点重建元素，
// 嵌套结构体继续深入（元素内部的 map / slice 同样会被处理）。
func fillContainerElements(v reflect.Value, patch map[string]any) {
	if v.Kind() != reflect.Struct {
		return
	}
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).PkgPath != "" {
			continue // 未导出字段
		}
		name, ok := elementJSONName(t.Field(i))
		if !ok {
			continue
		}
		sub, present := elementPatchLookup(patch, name)
		if !present {
			continue
		}
		fv := v.Field(i)
		switch fv.Kind() {
		case reflect.Map:
			rebuildMapElements(fv, sub)
		case reflect.Slice:
			rebuildSliceElements(fv, sub)
		case reflect.Struct:
			if m, ok := sub.(map[string]any); ok {
				fillContainerElements(fv, m)
			}
		}
	}
}

// rebuildMapElements 以默认值为基底重建 map 的每个元素，写回原 map 字段。
func rebuildMapElements(fv reflect.Value, patch any) {
	m, ok := patch.(map[string]any)
	if !ok || !fv.CanSet() {
		return
	}
	out := reflect.MakeMap(fv.Type())
	elemType := fv.Type().Elem()
	for k, elemPatch := range m {
		key, ok := parseElementMapKey(fv.Type().Key(), k)
		if !ok {
			continue
		}
		elem, ok := buildDefaultedElement(elemType, elemPatch)
		if !ok {
			// raw 里显式为 null，或元素无法解析（类型不匹配）：保留解析结果。
			// 前者与 encoding/json 的 map 语义一致（键存在、元素为零值），
			// 后者是为了不让一条坏数据毁掉整个 map。
			if existing := fv.MapIndex(key); existing.IsValid() {
				out.SetMapIndex(key, existing)
			}
			continue
		}
		out.SetMapIndex(key, elem)
	}
	fv.Set(out)
}

// rebuildSliceElements 以默认值为基底重建 slice 的每个元素，写回原 slice 字段。
//
// 与 map 同理：encoding/json 对 slice 字段也是**整体替换**，元素内未写出的字段会
// 降为 Go 零值。只有值类型结构体元素需要重建——[]string 之类的元素类型没有
// default tag 可套，解析结果本身就是最终值。
func rebuildSliceElements(fv reflect.Value, patch any) {
	rawElems, ok := patch.([]any)
	if !ok || !fv.CanSet() {
		return
	}
	elemType := fv.Type().Elem()
	if elemType.Kind() != reflect.Struct {
		return
	}
	out := reflect.MakeSlice(fv.Type(), 0, len(rawElems))
	for i, elemPatch := range rawElems {
		elem, ok := buildDefaultedElement(elemType, elemPatch)
		if !ok {
			// raw 元素为 null 或无法解析：保留解析结果。
			out = appendExistingSliceElem(out, fv, i)
			continue
		}
		out = reflect.Append(out, elem)
	}
	fv.Set(out)
}

// buildDefaultedElement 以 default tag 默认值为基底构造一个容器元素：先把默认值写进
// 新元素，再让 raw 中显式写出的字段覆盖它（元素内部的嵌套 map / slice 继续重建）。
// raw 不是 JSON 对象、或与该元素类型不匹配时返回 false，调用方应保留原解析结果。
func buildDefaultedElement(elemType reflect.Type, elemPatch any) (reflect.Value, bool) {
	m, ok := elemPatch.(map[string]any)
	if !ok {
		return reflect.Value{}, false
	}
	elem := reflect.New(elemType).Elem()
	if elem.Kind() == reflect.Struct {
		// 结构体元素先套 default tag 默认值（非结构体元素没有默认值可套）。
		structs.BuildDefault(elem.Addr().Interface())
	}
	b, err := json.Marshal(m)
	if err != nil {
		return reflect.Value{}, false
	}
	if err := json.Unmarshal(b, elem.Addr().Interface()); err != nil {
		return reflect.Value{}, false
	}
	// 元素内部还有嵌套 map / slice（如 FetchConfig.RewriteHeaders）时继续重建。
	fillContainerElements(elem, m)
	return elem, true
}

// appendExistingSliceElem 保留解析结果中下标为 i 的元素（raw 元素不可用时）。
func appendExistingSliceElem(out, src reflect.Value, i int) reflect.Value {
	if i < src.Len() {
		return reflect.Append(out, src.Index(i))
	}
	return out
}

// elementPatchLookup 在 patch 中查找字段对应的 raw 子节点：优先精确匹配，
// 否则大小写不敏感匹配（与 encoding/json 对无 tag 字段名的匹配语义一致）。
func elementPatchLookup(patch map[string]any, name string) (any, bool) {
	if v, ok := patch[name]; ok {
		return v, true
	}
	for k, v := range patch {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return nil, false
}

// elementJSONName 返回 struct 字段在 JSON 中的键名。无 json tag 时与
// encoding/json 一致，使用字段名本身；`json:"-"` 返回 false。
func elementJSONName(f reflect.StructField) (string, bool) {
	tag := f.Tag.Get("json")
	if tag == "" {
		return f.Name, true
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "-" {
		return "", false
	}
	if name == "" {
		return f.Name, true
	}
	return name, true
}

// parseElementMapKey 把 raw 里的字符串键解析成 map 的实际键类型
// （如 "1" → map[int32] 的键 1），不支持的键类型返回 false。
func parseElementMapKey(t reflect.Type, key string) (reflect.Value, bool) {
	k := reflect.New(t).Elem()
	switch t.Kind() {
	case reflect.String:
		k.SetString(key)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(key, 10, t.Bits())
		if err != nil {
			return reflect.Value{}, false
		}
		k.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(key, 10, t.Bits())
		if err != nil {
			return reflect.Value{}, false
		}
		k.SetUint(n)
	default:
		return reflect.Value{}, false
	}
	return k, true
}
