package structs

import "testing"

// 图片在工具调用展示内容里以 base64 存在；落库前必须去掉 data，只保留元数据。
func TestSanitizeToolCallingContentStripsImageData(t *testing.T) {
	in := []any{
		map[string]any{
			"type": "content",
			"content": map[string]any{
				"type":                      "image",
				"mimeType":                  "image/png",
				"data":                      "AAAA",
				"alk.cxykevin.top/image_id": "@image/abc",
			},
		},
		map[string]any{"type": "text", "text": "kept"},
	}
	out, ok := sanitizeToolCallingContent(in).([]any)
	if !ok || len(out) != 2 {
		t.Fatalf("unexpected shape: %#v", out)
	}
	block := out[0].(map[string]any)
	content := block["content"].(map[string]any)
	if _, exists := content["data"]; exists {
		t.Fatal("image data must be stripped before persisting")
	}
	if content["mimeType"] != "image/png" {
		t.Fatalf("mimeType = %v, want image/png", content["mimeType"])
	}
	if content["alk.cxykevin.top/image_id"] != "@image/abc" {
		t.Fatalf("image_id lost: %#v", content)
	}
	text := out[1].(map[string]any)
	if text["text"] != "kept" {
		t.Fatalf("text changed: %#v", text)
	}
}

func TestSanitizeToolCallingContentKeepsPlainValues(t *testing.T) {
	if got := sanitizeToolCallingContent(nil); got != nil {
		t.Fatalf("nil should stay nil, got %#v", got)
	}
	in := map[string]any{"type": "text", "text": "hello"}
	out := sanitizeToolCallingContent(in)
	m, ok := out.(map[string]any)
	if !ok || m["text"] != "hello" {
		t.Fatalf("plain content changed: %#v", out)
	}
}
