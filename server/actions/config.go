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

// ---- Handler functions ----

// ConfigGet 返回完整的当前配置
func ConfigGet(_ ConfigGetRequest, _ func(string, any, *string) error, _ uint64) (ConfigGetResponse, error) {
	return ConfigGetResponse{Config: config.GlobalConfig}, nil
}

// ConfigSet 写入配置并自动重载
// 接受完整的或部分的配置 JSON，通过 json.Unmarshal 合并到现有配置中
// 只有请求中显式指定的字段会被更新，未指定的字段保持现有值
// 保存后自动触发所有注册的重载钩子（配置广播推送等）
func ConfigSet(req ConfigSetRequest, _ func(string, any, *string) error, _ uint64) (any, error) {
	if req.Config == nil {
		return nil, fmt.Errorf("config is required")
	}

	// 校验为合法 JSON
	if !json.Valid(req.Config) {
		return nil, fmt.Errorf("invalid JSON config")
	}

	// 合并到 GlobalConfig（json.Unmarshal 到已存在的对象只覆盖 JSON 中出现的字段）。
	// 在写锁下完成合并，避免与并发读者（buildModelList、各 handler）形成数据竞争。
	cfg, unlock := config.GlobalConfigForWrite()
	unmarshalErr := json.Unmarshal(req.Config, cfg)
	if unmarshalErr == nil {
		// 补充真正的删除语义：Go 的 encoding/json 对 map 值传 null 只会把该键
		// 置零而不删除（见 applyNullDeletes），config/set 用 unmarshal 合并，
		// 因此这里显式删除 patch 中标记为 null 的 map 键。
		applyNullDeletes(cfg, req.Config)
	}
	unlock()
	if unmarshalErr != nil {
		return nil, fmt.Errorf("failed to apply config: %v", unmarshalErr)
	}

	// 保存到文件并触发重载钩子
	config.Save()

	return nil, nil
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
