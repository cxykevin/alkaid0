package build_test

import (
	"reflect"
	"testing"

	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/provider/request/build"
)

// TestToolParametersToJSONSchema 五类类型 + required 排序 + description 省略。
func TestToolParametersToJSONSchema(t *testing.T) {
	params := map[string]parser.ToolParameters{
		"expression": {Type: parser.ToolTypeString, Required: true, Description: "数学表达式"},
		"precision":  {Type: parser.ToolTypeNumber},
		"enabled":    {Type: parser.ToolTypeBoolean, Required: true},
		"items":      {Type: parser.ToolTypeArray},
		"options":    {Type: parser.ToolTypeObject},
	}

	schema := build.ToolParametersToJSONSchema(params)

	if schema["type"] != "object" {
		t.Fatalf("root type = %v, want object", schema["type"])
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("properties missing")
	}
	// 类型映射
	typeChecks := map[string]string{
		"expression": "string",
		"precision":  "number",
		"enabled":    "boolean",
		"items":      "array",
		"options":    "object",
	}
	for name, want := range typeChecks {
		prop, ok := props[name].(map[string]any)
		if !ok {
			t.Fatalf("property %q missing", name)
		}
		if prop["type"] != want {
			t.Fatalf("property %q type = %v, want %v", name, prop["type"], want)
		}
	}
	// description 非空才写
	if _, ok := props["expression"].(map[string]any)["description"]; !ok {
		t.Fatal("expression description should be present")
	}
	if _, ok := props["precision"].(map[string]any)["description"]; ok {
		t.Fatal("precision description should be omitted when empty")
	}
	// required 排序确定
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatal("required missing")
	}
	wantRequired := []string{"enabled", "expression"}
	if !reflect.DeepEqual(required, wantRequired) {
		t.Fatalf("required = %v, want %v", required, wantRequired)
	}
}
