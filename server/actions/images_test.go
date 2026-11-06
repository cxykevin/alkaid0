package actions

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/cxykevin/alkaid0/internal/imagestore"
	"github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
)

func testPNGBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 6, 4))
	img.Set(1, 1, color.NRGBA{R: 200, G: 30, B: 60, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestDecodeImageBlock(t *testing.T) {
	raw := testPNGBytes(t)
	b64 := base64.StdEncoding.EncodeToString(raw)

	data, err := decodeImageBlock(u.H{"type": "image", "mimeType": "image/png", "data": b64})
	if err != nil || !bytes.Equal(data, raw) {
		t.Fatalf("decode data block: %v", err)
	}

	data, err = decodeImageBlock(u.H{"type": "image", "uri": "data:image/png;base64," + b64})
	if err != nil || !bytes.Equal(data, raw) {
		t.Fatalf("decode data uri: %v", err)
	}

	if _, err := decodeImageBlock(u.H{"type": "image"}); err == nil {
		t.Fatal("expected error for image block without data/uri")
	}
	if _, err := decodeImageBlock(u.H{"type": "image", "uri": "file:///etc/passwd"}); err == nil {
		t.Fatal("expected error for unsupported uri")
	}
}

func TestParsePromptImagesSavesToStore(t *testing.T) {
	dir := t.TempDir()
	obj := &sessionObj{session: &structs.Chats{ID: 1, Root: dir}}
	raw := testPNGBytes(t)
	refs, err := obj.parsePromptImages([]u.H{
		{"type": "text", "text": "look"},
		{"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(raw)},
	})
	if err != nil {
		t.Fatalf("parsePromptImages: %v", err)
	}
	if len(refs) != 1 || refs[0] == "" {
		t.Fatalf("refs = %v, want one @image ref", refs)
	}
	im, err := imagestore.Resolve(dir, refs[0])
	if err != nil {
		t.Fatalf("resolve stored image: %v", err)
	}
	if im.Width != 6 || im.Height != 4 {
		t.Fatalf("dims = %dx%d, want 6x4", im.Width, im.Height)
	}
}

func TestQueueImagesRoundTrip(t *testing.T) {
	images := []string{"@image/a", "@image/b"}
	raw := encodeQueueImages(images)
	got := decodeQueueImages(raw)
	if len(got) != 2 || got[0] != images[0] || got[1] != images[1] {
		t.Fatalf("round trip = %v, want %v", got, images)
	}
	if encodeQueueImages(nil) != "" {
		t.Fatal("empty image list should encode to empty string")
	}
	if decodeQueueImages("") != nil {
		t.Fatal("empty string should decode to nil")
	}
	if decodeQueueImages("{not json") != nil {
		t.Fatal("invalid json should decode to nil")
	}
}

func TestUserMessageContentIncludesThumbnail(t *testing.T) {
	dir := t.TempDir()
	obj := &sessionObj{session: &structs.Chats{ID: 1, Root: dir}}
	refs, err := obj.parsePromptImages([]u.H{
		{"type": "image", "data": base64.StdEncoding.EncodeToString(testPNGBytes(t))},
	})
	if err != nil {
		t.Fatalf("parsePromptImages: %v", err)
	}
	content := userMessageContent(dir, "hello", refs)
	if len(content) != 2 {
		t.Fatalf("content blocks = %d, want 2", len(content))
	}
	if typ, _ := u.GetH[string](content[0], "type"); typ != "text" {
		t.Fatalf("first block type = %q, want text", typ)
	}
	if typ, _ := u.GetH[string](content[1], "type"); typ != "image" {
		t.Fatalf("second block type = %q, want image", typ)
	}
	if data, _ := u.GetH[string](content[1], "data"); data == "" {
		t.Fatal("image block should carry a base64 thumbnail")
	}
}
