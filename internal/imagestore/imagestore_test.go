package imagestore

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x % 255), G: uint8(y % 255), B: 80, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDeepSeekImageTokens(t *testing.T) {
	cases := []struct {
		w, h     int
		cropping bool
		want     int
	}{
		{384, 384, true, 421},
		{768, 768, true, 1023},
		{1024, 1024, true, 2017},
		{1920, 1080, true, 1807},
		{384, 3456, true, 2101},
		{1024, 1024, false, 421},
	}
	for _, c := range cases {
		if got := DeepSeekImageTokens(c.w, c.h, c.cropping); got != c.want {
			t.Errorf("DeepSeekImageTokens(%d,%d,%v) = %d, want %d", c.w, c.h, c.cropping, got, c.want)
		}
	}
}

func TestSaveResolveAndScaling(t *testing.T) {
	dir := t.TempDir()
	data := testPNG(t, 2000, 1000)

	im, err := Save(dir, data, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if im.Width != 1024 || im.Height != 512 {
		t.Fatalf("scaled size = %dx%d, want 1024x512", im.Width, im.Height)
	}
	if !strings.HasPrefix(im.Path, Dir+"/") {
		t.Fatalf("path = %q", im.Path)
	}
	if _, err := os.Stat(im.AbsPath); err != nil {
		t.Fatalf("stored file: %v", err)
	}
	if im.ID == "" || im.DataURL() == "" || im.ThumbData == "" || im.ThumbMIME == "" {
		t.Fatalf("missing metadata: %+v", im)
	}
	if im.ThumbWidth > DefaultThumbnailDimension || im.ThumbHeight > DefaultThumbnailDimension {
		t.Fatalf("thumbnail too large: %dx%d", im.ThumbWidth, im.ThumbHeight)
	}

	got, err := Resolve(dir, "@image/"+im.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Hash != im.Hash || got.Path != im.Path || got.MIME != im.MIME {
		t.Fatalf("resolved = %+v, want %+v", got, im)
	}

	// Same bytes must dedupe to the same file.
	im2, err := Save(dir, data, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if im2.AbsPath != im.AbsPath {
		t.Fatalf("dedupe failed: %q != %q", im2.AbsPath, im.AbsPath)
	}
	entries, err := os.ReadDir(filepath.Join(dir, filepath.FromSlash(Dir)))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("store has %d files, want 1", len(entries))
	}
}

func TestSaveNoScalingSmallImage(t *testing.T) {
	dir := t.TempDir()
	data := testPNG(t, 320, 200)
	im, err := Save(dir, data, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if im.Width != 320 || im.Height != 200 {
		t.Fatalf("size = %dx%d", im.Width, im.Height)
	}
	if im.MIME != "image/png" {
		t.Fatalf("mime = %q", im.MIME)
	}
}

func TestResolveRejectsBadID(t *testing.T) {
	dir := t.TempDir()
	if _, err := Resolve(dir, "@image/not-base36!!"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := Resolve(dir, "not-a-store-path"); err == nil {
		t.Fatal("expected error")
	}
}

func TestIsImagePath(t *testing.T) {
	if !IsImagePath("@image/abc") {
		t.Fatal("@image path")
	}
	if !IsImagePath(".alkaid0/images/abc.png") {
		t.Fatal("store path")
	}
	if IsImagePath("a.png") {
		t.Fatal("workspace path should not be a store path")
	}
}
