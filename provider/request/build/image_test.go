package build

import (
	"encoding/json"
	"strings"
	"testing"

	reqStruct "github.com/cxykevin/alkaid0/provider/request/structs"
	"github.com/cxykevin/alkaid0/tools/tools/trace"
)

func TestImageContentPartFromTraceBlock(t *testing.T) {
	fb := trace.FileBlock{
		Name:  "pic.png",
		Type:  trace.MediaKindImage,
		Text:  "caption",
		Image: &trace.ImageBlock{DataURL: "data:image/png;base64,AAAA", Tokens: 421},
	}
	part, ok := imageContentPart(fb)
	if !ok {
		t.Fatal("imageContentPart returned !ok")
	}
	if part.Type != "image_url" || part.ImageURL == nil || part.ImageURL.URL != "data:image/png;base64,AAAA" {
		t.Fatalf("part = %#v", part)
	}
	if part.EstimatedTokens != 421 {
		t.Fatalf("EstimatedTokens = %d, want 421", part.EstimatedTokens)
	}

	m := reqStruct.Message{Role: reqStruct.RoleUser, Content: "caption", ContentParts: []reqStruct.ContentPart{part}}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, `"type":"image_url"`) || !strings.Contains(body, `"url":"data:image/png;base64,AAAA"`) {
		t.Fatalf("serialized message missing image part: %s", body)
	}
	if !strings.Contains(body, `"type":"text"`) {
		t.Fatalf("serialized message missing text part: %s", body)
	}

	// 图片 token 按模型计费估算，而不是按 base64 字节长度。
	if got := messageTokens(m); got < 421 {
		t.Fatalf("messageTokens = %d, want >= 421", got)
	}
}

func TestImageContentPartSkipsEmptyImage(t *testing.T) {
	if _, ok := imageContentPart(trace.FileBlock{}); ok {
		t.Fatal("empty block should not produce an image part")
	}
	if _, ok := imageContentPart(trace.FileBlock{Image: &trace.ImageBlock{}}); ok {
		t.Fatal("image block without data URL should not produce a part")
	}
}
