package trace

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/storage/structs"
)

func writeTestPNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 8, 6))
	for y := range 6 {
		for x := range 8 {
			img.Set(x, y, color.NRGBA{R: uint8(x * 30), G: uint8(y * 40), B: 128, A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create png: %v", err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
}

func newImageTestSession(t *testing.T) (*structs.Chats, string) {
	t.Helper()
	dir := t.TempDir()
	db := setupTestDB(t)
	return &structs.Chats{ID: 7, Root: dir, DB: db}, dir
}

func TestImageReadWritesMediaTrace(t *testing.T) {
	session, dir := newImageTestSession(t)
	writeTestPNG(t, filepath.Join(dir, "pic.png"))

	path := any("pic.png")
	_, _, res, err := Trace(session, map[string]*any{"path": &path}, nil)
	if err != nil {
		t.Fatalf("Trace returned error: %v", err)
	}
	if success, _ := (*res["success"]).(bool); !success {
		t.Fatalf("Trace did not succeed: %v", res)
	}

	var tr structs.Traces
	if err := session.DB.Where("chat_id = ? AND path = ?", session.ID, "pic.png").First(&tr).Error; err != nil {
		t.Fatalf("trace row missing: %v", err)
	}
	if tr.MediaKind != MediaKindImage {
		t.Fatalf("MediaKind = %q, want %q", tr.MediaKind, MediaKindImage)
	}
	if tr.MediaPath == "" || !strings.HasPrefix(tr.MediaPath, ".alkaid0/images/") {
		t.Fatalf("MediaPath = %q, want a store path", tr.MediaPath)
	}
	if tr.MediaWidth != 8 || tr.MediaHeight != 6 {
		t.Fatalf("dims = %dx%d, want 8x6", tr.MediaWidth, tr.MediaHeight)
	}
	if tr.MediaTokens <= 0 {
		t.Fatalf("MediaTokens = %d, want > 0", tr.MediaTokens)
	}
	if tr.LastContent != "" {
		t.Fatalf("LastContent = %q, want empty for image trace", tr.LastContent)
	}

	_, blocks, err := RenderTraceBlocks(session)
	if err != nil {
		t.Fatalf("RenderTraceBlocks: %v", err)
	}
	fb, ok := blocks["pic.png"]
	if !ok || fb.Image == nil {
		t.Fatalf("image block missing: %#v", blocks["pic.png"])
	}
	if !strings.HasPrefix(fb.Image.DataURL, "data:image/") {
		t.Fatalf("DataURL = %q, want image data URI", fb.Image.DataURL)
	}
	if fb.Type != MediaKindImage {
		t.Fatalf("block type = %q, want %q", fb.Type, MediaKindImage)
	}
	if !strings.Contains(fb.Text, "8x6") {
		t.Fatalf("caption = %q, want dimensions", fb.Text)
	}

	// 图片 token 会计入断链成本估算（锚点之后的内容块）。
	SetTraceAnchor(session, "pic.png", 5)
	if got := CountInjectedTokensFrom(session, 0); got < tr.MediaTokens {
		t.Fatalf("CountInjectedTokensFrom = %d, want >= %d", got, tr.MediaTokens)
	}
}

func TestImageReadRejectsFakeImage(t *testing.T) {
	session, dir := newImageTestSession(t)
	if err := os.WriteFile(filepath.Join(dir, "fake.png"), []byte("not an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := any("fake.png")
	_, _, res, err := Trace(session, map[string]*any{"path": &path}, nil)
	if err != nil {
		t.Fatalf("Trace returned error: %v", err)
	}
	if success, _ := (*res["success"]).(bool); success {
		t.Fatal("Trace should reject non-image content stored under an image extension")
	}
}

func TestTraceStoredImageAnchorsAtMessage(t *testing.T) {
	session, dir := newImageTestSession(t)
	writeTestPNG(t, filepath.Join(dir, "pic.png"))

	path := any("pic.png")
	if _, _, _, err := Trace(session, map[string]*any{"path": &path}, nil); err != nil {
		t.Fatalf("Trace: %v", err)
	}
	var tr structs.Traces
	if err := session.DB.Where("chat_id = ? AND path = ?", session.ID, "pic.png").First(&tr).Error; err != nil {
		t.Fatalf("trace row missing: %v", err)
	}
	src, ok := loadTraceImage(session, dir, tr)
	if !ok || src.Hash == "" {
		t.Fatal("loadTraceImage failed")
	}

	// 用户消息附带的图片：锚定在该消息上，下一轮原位注入（不搬家）。
	if err := TraceStoredImage(session, src, 42); err != nil {
		t.Fatalf("TraceStoredImage: %v", err)
	}
	_, blocks, err := RenderTraceBlocks(session)
	if err != nil {
		t.Fatalf("RenderTraceBlocks: %v", err)
	}
	refPath := "@image/" + src.ID
	if fb, ok := blocks[refPath]; !ok || fb.Image == nil {
		t.Fatalf("stored image block missing for %q", refPath)
	}
	plans, _ := session.TemporyDataOfSession[structs.TempKeyTraceAnchorPlan].(map[string]*AnchorPlan)
	plan := plans[refPath]
	if plan == nil || plan.Mode != AnchorFull || plan.MsgID != 42 {
		t.Fatalf("anchor plan = %#v, want AnchorFull at 42", plan)
	}
}
