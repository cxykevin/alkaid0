package actions

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
)

// ---- Request/Response types ----

// ConfigGetRequest 获取配置的请求（无需参数）
type ConfigGetRequest struct{}

// ConfigGetResponse 获取配置的响应
type ConfigGetResponse struct {
	Config *cfgStructs.Config `json:"config"`
}

// ConfigSetRequest 设置配置的请求
// Config 字段接受部分配置，未指定的字段保持现有值不变
type ConfigSetRequest struct {
	Config json.RawMessage `json:"config"`
}

// ConfigSetResponse 设置配置的响应
// 显式返回非 nil 的空对象：成功响应为 `{"result":{}}`，与 ConfigGet 的
// ConfigGetResponse 对称。历史上 ConfigSet 曾返回 nil,nil，导致 jsonrpc 框架
// 对带 ID 的请求不发送响应、前端永久等待挂起（框架层现已兜底，见 server.go）。
type ConfigSetResponse struct{}

// ---- Handler functions ----

// ConfigGet 返回完整的当前配置
func ConfigGet(_ ConfigGetRequest, _ func(string, any, *string) error, _ uint64) (ConfigGetResponse, error) {
	return ConfigGetResponse{Config: config.GlobalConfig}, nil
}

// ConfigSet 写入配置并自动重载
// 接受完整的或部分的配置 JSON，通过 json.Unmarshal 合并到现有配置中
// 只有请求中显式指定的字段会被更新，未指定的字段保持现有值
// 保存后自动触发所有注册的重载钩子（配置广播推送等）
func ConfigSet(req ConfigSetRequest, _ func(string, any, *string) error, _ uint64) (ConfigSetResponse, error) {
	if req.Config == nil {
		return ConfigSetResponse{}, fmt.Errorf("config is required")
	}

	// 校验为合法 JSON
	if !json.Valid(req.Config) {
		return ConfigSetResponse{}, fmt.Errorf("invalid JSON config")
	}

	// 合并到 GlobalConfig（真正的局部更新：仅 patch 中显式出现的字段被覆盖，
	// 其余保持现有值）。在写锁下完成合并，避免与并发读者（buildModelList、
	// 各 handler）形成数据竞争。
	//
	// 为什么不用 json.Unmarshal(req.Config, cfg)：encoding/json 对 struct 字段
	// （如 Server.Host）是就地更新、未出现的字段保持原值，但对 map 类型字段
	// （如 Model.Models 是 map[int32]ModelConfig）遇到 patch 中出现的键会用全新
	// 解码的值整体替换，丢失该键下未在 patch 中的字段——表现为"改一个模型字段，
	// 该模型其余字段被清零/重置"。因此改为 applyPatch 做逐字段深度合并。
	cfg, unlock := config.GlobalConfigForWrite()
	var patch map[string]any
	if err := json.Unmarshal(req.Config, &patch); err != nil {
		unlock()
		return ConfigSetResponse{}, fmt.Errorf("failed to apply config: %v", err)
	}
	applyErr := applyPatch(reflect.ValueOf(cfg), patch)
	if applyErr == nil {
		// 补充真正的删除语义：Go 的 encoding/json 对 map 值传 null 只会把该键
		// 置零而不删除。applyPatch 已在 map 字段中直接删除 null 键，这里再
		// 跑一遍 applyNullDeletes 作为防御，覆盖可能的边界情形。
		applyNullDeletes(cfg, req.Config)
	}
	unlock()
	if applyErr != nil {
		return ConfigSetResponse{}, fmt.Errorf("failed to apply config: %v", applyErr)
	}

	// 保存到文件并触发重载钩子
	config.Save()

	return ConfigSetResponse{}, nil
}

// applyPatch 把部分更新 patch（通用 map）递归合并进 dst，实现真正的局部更新。
//
// encoding/json 对 map 类型字段（如 Model.Models 是 map[int32]ModelConfig）的
// 合并是"整体替换"：patch 中出现某个键时，用全新解码的值覆盖该键，该键下未在
// patch 中出现的字段全部归零。因此对 patch 值仍为对象（JSON 对象反序列化后是
// map[string]any）的键做递归合并；map 字段的键先取目标中已存在的值作为基底再
// 合并，未指定的子字段保持原值。标量/数组整体赋值，类型不匹配时返回错误
// （与原 json.Unmarshal 一样导致"保存失败"）。
func applyPatch(dst reflect.Value, patch map[string]any) error {
	for dst.Kind() == reflect.Pointer || dst.Kind() == reflect.Interface {
		if dst.IsNil() {
			return nil // nil 指针无可写目标
		}
		dst = dst.Elem()
	}
	switch dst.Kind() {
	case reflect.Struct:
		t := dst.Type()
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if field.PkgPath != "" {
				continue // 未导出字段
			}
			name, ok := jsonName(field)
			if !ok {
				continue
			}
			v, present := jsonPatchValue(patch, name)
			if !present {
				continue
			}
			if child, ok := v.(map[string]any); ok {
				if err := applyPatch(dst.Field(i), child); err != nil {
					return err
				}
			} else if v == nil {
				// null 对 struct 字段：保持原值（与 encoding/json 语义一致）。
				continue
			} else if err := setJSONValue(dst.Field(i), v); err != nil {
				return err
			}
		}
	case reflect.Map:
		if dst.IsNil() {
			dst.Set(reflect.MakeMap(dst.Type()))
		}
		for key, v := range patch {
			mapKey, ok := parseMapKey(dst.Type().Key(), key)
			if !ok {
				continue
			}
			if v == nil {
				dst.SetMapIndex(mapKey, reflect.Value{}) // 真正删除键
				continue
			}
			// 以已存在值为基底，避免整体替换丢失未 patch 字段。
			elem := reflect.New(dst.Type().Elem()).Elem()
			if existing := dst.MapIndex(mapKey); existing.IsValid() {
				elem.Set(existing)
			}
			if child, ok := v.(map[string]any); ok {
				if err := applyPatch(elem, child); err != nil {
					return err
				}
			} else if err := setJSONValue(elem, v); err != nil {
				return err
			}
			dst.SetMapIndex(mapKey, elem)
		}
	default:
		if err := setJSONValue(dst, patch); err != nil {
			return err
		}
	}
	return nil
}

// setJSONValue 把 JSON 值 v 整体赋值到目标字段（标量/数组/嵌套结构），
// 通过 json.Marshal + json.Unmarshal 完成类型安全的解码与校验（类型不匹配
// 返回错误）。目标为指针时先分配实例。
func setJSONValue(dst reflect.Value, v any) error {
	for dst.Kind() == reflect.Pointer || dst.Kind() == reflect.Interface {
		if dst.IsNil() {
			if !dst.CanSet() {
				return nil
			}
			dst.Set(reflect.New(dst.Type().Elem()))
			dst = dst.Elem()
			continue
		}
		dst = dst.Elem()
	}
	if v == nil {
		dst.Set(reflect.Zero(dst.Type()))
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst.Addr().Interface())
}

// applyNullDeletes 删除配置中 patch 标记为 null 的 map 键。
//
// Go 的 encoding/json 对 map 值传 null 并不会删除该键，而是把值置零
// （例如 map[int32]ModelConfig 解码 {"1":null} 后键 1 仍在，只是内容清空）。
// config/set 依赖 unmarshal 合并语义，因此需要本函数补充真正的删除：
// 遍历 patch（通用 map），凡值为 null 的键，在对应配置 map 中删除该键。
// 只对 map 类型的配置字段生效；struct 字段传 null 保持 unmarshal 的置零行为。
func applyNullDeletes(cfg *cfgStructs.Config, raw json.RawMessage) {
	var patch map[string]any
	if err := json.Unmarshal(raw, &patch); err != nil {
		return
	}
	deleteNullMapKeys(reflect.ValueOf(cfg), patch)
}

// deleteNullMapKeys 递归遍历 patch，删除 dst 中对应 map 字段里值为 null 的键。
func deleteNullMapKeys(dst reflect.Value, patch map[string]any) {
	for dst.Kind() == reflect.Pointer || dst.Kind() == reflect.Interface {
		if dst.IsNil() {
			return
		}
		dst = dst.Elem()
	}
	switch dst.Kind() {
	case reflect.Struct:
		t := dst.Type()
		for i := 0; i < t.NumField(); i++ {
			name, ok := jsonName(t.Field(i))
			if !ok {
				continue
			}
			v, present := patch[name]
			if !present {
				continue
			}
			if child, ok := v.(map[string]any); ok {
				deleteNullMapKeys(dst.Field(i), child)
			}
		}
	case reflect.Map:
		if dst.IsNil() {
			return
		}
		for key, v := range patch {
			if v != nil {
				continue
			}
			mapKey, ok := parseMapKey(dst.Type().Key(), key)
			if !ok {
				continue
			}
			dst.SetMapIndex(mapKey, reflect.Value{})
		}
	}
}

// jsonPatchValue 在 patch 中查找字段 name 对应的值：优先精确匹配（json tag 或
// 字段名本身），否则大小写不敏感匹配——与 encoding/json 对无 tag 字段名的
// 匹配语义一致（如 patch 键 "defaultModelID" 对应字段 DefaultModelID）。
func jsonPatchValue(patch map[string]any, name string) (any, bool) {
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

// jsonName 返回 struct 字段在 JSON 中的键名。无 json tag 时与 encoding/json
// 一致，使用字段名本身；`json:"-"` 返回 false。
func jsonName(f reflect.StructField) (string, bool) {
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

// parseMapKey 把 patch 中的字符串键解析成 map 的实际键类型
// （如 "1" → map[int32] 的键 1），失败返回 false。
// 与 encoding/json 的 map 键解码一致：JSON 对象键总是字符串，
// 数字/布尔类型从字符串直接解析。
func parseMapKey(t reflect.Type, key string) (reflect.Value, bool) {
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
	case reflect.Bool:
		b, err := strconv.ParseBool(key)
		if err != nil {
			return reflect.Value{}, false
		}
		k.SetBool(b)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(key, t.Bits())
		if err != nil {
			return reflect.Value{}, false
		}
		k.SetFloat(f)
	default:
		if err := json.Unmarshal([]byte(strconv.Quote(key)), k.Addr().Interface()); err != nil {
			return reflect.Value{}, false
		}
	}
	return k, true
}
