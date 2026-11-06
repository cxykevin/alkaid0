package structs

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMessageMarshalContentParts(t *testing.T) {
	msg := Message{
		Role:    RoleUser,
		Content: "look",
		ContentParts: []ContentPart{{
			Type:     "image_url",
			ImageURL: &ImageURL{URL: "data:image/png;base64,AAAA"},
		}},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Content []ContentPart `json:"content"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("content is not an array: %v (%s)", err, data)
	}
	if len(raw.Content) != 2 || raw.Content[0].Type != "text" || raw.Content[1].Type != "image_url" {
		t.Fatalf("parts = %+v", raw.Content)
	}
	if raw.Content[0].Text != "look" || raw.Content[1].ImageURL == nil {
		t.Fatalf("parts = %+v", raw.Content)
	}
}

func TestMessageMarshalContentPartsIgnoredForAssistant(t *testing.T) {
	msg := Message{
		Role:    RoleAssistant,
		Content: "text",
		ContentParts: []ContentPart{{
			Type:     "image_url",
			ImageURL: &ImageURL{URL: "data:image/png;base64,AAAA"},
		}},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "image_url") {
		t.Fatalf("assistant message must not carry parts: %s", data)
	}
}

func TestMessageMarshalCacheControlAndParts(t *testing.T) {
	msg := Message{
		Role:         RoleUser,
		Content:      "x",
		CacheControl: &PromptCacheControl{Type: "ephemeral"},
		ContentParts: []ContentPart{{
			Type:     "image_url",
			ImageURL: &ImageURL{URL: "data:image/png;base64,AAAA"},
		}},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "cache_control") {
		t.Fatalf("multipart message must not carry cache_control: %s", data)
	}
	if !strings.Contains(string(data), "image_url") {
		t.Fatalf("missing image part: %s", data)
	}
}

func TestMessageMarshalImageOnly(t *testing.T) {
	msg := Message{
		Role: RoleUser,
		ContentParts: []ContentPart{{
			Type:     "image_url",
			ImageURL: &ImageURL{URL: "data:image/png;base64,AAAA"},
		}},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Content []ContentPart `json:"content"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("content is not an array: %v (%s)", err, data)
	}
	if len(raw.Content) != 1 || raw.Content[0].Type != "image_url" {
		t.Fatalf("parts = %+v", raw.Content)
	}
}
