package imagestore

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // register GIF decoder
	"image/jpeg"
	"image/png"
	"math/big"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/cxykevin/alkaid0/terminal/sandbox"
	_ "golang.org/x/image/bmp" // register BMP decoder
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // register WebP decoder
)

const (
	// Dir is the workspace-relative image store directory.
	Dir = ".alkaid0/images"
	// DefaultThumbnailDimension is the long-side size of ACP thumbnails.
	DefaultThumbnailDimension = 256
	// MaxPixels guards against decompression bombs.
	MaxPixels = 40_000_000
	// MaxImageBytes is the hard cap for a single stored image.
	MaxImageBytes = 10 * 1024 * 1024

	jpegQuality  = 85
	thumbQuality = 80
)

// Image is a stored, content-addressed image.
//
// Data is the exact bytes stored on disk (already downscaled to the model
// limit). It is never persisted in the database and never exposed to the model
// as a path; the model only sees ID (an @image/{id} virtual path).
type Image struct {
	ID      string // base36 of sha256(stored)[:16]
	Hash    string // hex sha256 of Data
	Path    string // workspace-relative store path, slash-separated
	AbsPath string
	MIME    string
	Ext     string
	Width   int
	Height  int
	Bytes   int
	Tokens  int

	Data []byte

	ThumbData   string // base64, for ACP preview only
	ThumbMIME   string
	ThumbWidth  int
	ThumbHeight int
}

// DataURL returns the OpenAI-compatible data URI for the stored image.
func (im Image) DataURL() string {
	if len(im.Data) == 0 {
		return ""
	}
	return "data:" + im.MIME + ";base64," + base64.StdEncoding.EncodeToString(im.Data)
}

// ThumbURL returns a data URI for the thumbnail.
func (im Image) ThumbURL() string {
	if im.ThumbData == "" || im.ThumbMIME == "" {
		return ""
	}
	return "data:" + im.ThumbMIME + ";base64," + im.ThumbData
}

// Save normalizes (optionally downscales), stores and describes an image.
// Only the final (possibly downscaled) bytes are written; the original bytes
// are discarded.
func Save(workspace string, data []byte, maxDim int) (Image, error) {
	if len(data) == 0 {
		return Image{}, errors.New("empty image data")
	}
	if len(data) > MaxImageBytes {
		return Image{}, fmt.Errorf("image too large: %d bytes (max %d)", len(data), MaxImageBytes)
	}
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return Image{}, fmt.Errorf("resolve workspace: %w", err)
	}
	mime, width, height, err := inspect(data)
	if err != nil {
		return Image{}, err
	}

	stored, storedMIME, storedW, storedH, decoded, err := normalize(data, mime, width, height, maxDim)
	if err != nil {
		return Image{}, err
	}
	if decoded == nil {
		decoded, err = decodeImage(stored)
		if err != nil {
			return Image{}, fmt.Errorf("decode image: %w", err)
		}
	}

	sum := sha256.Sum256(stored)
	hashHex := hex.EncodeToString(sum[:])
	ext := ExtForMIME(storedMIME)
	if ext == "" {
		return Image{}, fmt.Errorf("unsupported stored image type %q", storedMIME)
	}
	rel := path.Join(Dir, hashHex+ext)
	abs := filepath.Join(absWorkspace, filepath.FromSlash(rel))
	if err := sandbox.MkdirAllConfigured(filepath.Dir(abs), 0o755); err != nil {
		return Image{}, fmt.Errorf("create image directory: %w", err)
	}
	if err := writeFileAtomic(abs, stored); err != nil {
		return Image{}, fmt.Errorf("store image: %w", err)
	}

	thumbData, thumbMIME, thumbW, thumbH, err := makeThumbnail(decoded)
	if err != nil {
		return Image{}, fmt.Errorf("generate thumbnail: %w", err)
	}

	return Image{
		ID:          IDFromHashHex(hashHex),
		Hash:        hashHex,
		Path:        rel,
		AbsPath:     abs,
		MIME:        storedMIME,
		Ext:         ext,
		Width:       storedW,
		Height:      storedH,
		Bytes:       len(stored),
		Tokens:      DeepSeekImageTokens(storedW, storedH, true),
		Data:        stored,
		ThumbData:   thumbData,
		ThumbMIME:   thumbMIME,
		ThumbWidth:  thumbW,
		ThumbHeight: thumbH,
	}, nil
}

// Resolve maps "@image/{id}" or a store-relative path to a stored Image.
func Resolve(workspace, pathOrID string) (Image, error) {
	raw := strings.TrimSpace(pathOrID)
	if raw == "" {
		return Image{}, errors.New("empty image path")
	}
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return Image{}, fmt.Errorf("resolve workspace: %w", err)
	}
	if id, ok := strings.CutPrefix(raw, "@image/"); ok {
		return resolveID(absWorkspace, id)
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(raw)))
	if !isStorePath(clean) {
		return Image{}, fmt.Errorf("not an image store path: %s", raw)
	}
	return Load(absWorkspace, clean)
}

func resolveID(absWorkspace, id string) (Image, error) {
	prefix, err := hashPrefixFromID(id)
	if err != nil {
		return Image{}, fmt.Errorf("invalid image id %q: %w", id, err)
	}
	dir := filepath.Join(absWorkspace, filepath.FromSlash(Dir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return Image{}, fmt.Errorf("image %q not found", "@image/"+id)
		}
		return Image{}, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := strings.ToLower(entry.Name())
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		rel := path.Join(Dir, entry.Name())
		if im, err := Load(absWorkspace, rel); err == nil {
			return im, nil
		}
	}
	return Image{}, fmt.Errorf("image %q not found", "@image/"+id)
}

// Load reads an already-stored image by workspace-relative path.
func Load(workspace, relPath string) (Image, error) {
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return Image{}, fmt.Errorf("resolve workspace: %w", err)
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(relPath)))
	if !isStorePath(clean) {
		return Image{}, fmt.Errorf("not an image store path: %s", relPath)
	}
	abs := filepath.Join(absWorkspace, filepath.FromSlash(clean))
	if err := ensureWithinStore(absWorkspace, abs); err != nil {
		return Image{}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return Image{}, err
	}
	if len(data) > MaxImageBytes {
		return Image{}, fmt.Errorf("stored image too large: %d bytes", len(data))
	}
	mime, width, height, err := inspect(data)
	if err != nil {
		return Image{}, err
	}
	decoded, err := decodeImage(data)
	if err != nil {
		return Image{}, fmt.Errorf("decode image: %w", err)
	}
	thumbData, thumbMIME, thumbW, thumbH, err := makeThumbnail(decoded)
	if err != nil {
		return Image{}, fmt.Errorf("generate thumbnail: %w", err)
	}
	sum := sha256.Sum256(data)
	hashHex := hex.EncodeToString(sum[:])
	return Image{
		ID:          IDFromHashHex(hashHex),
		Hash:        hashHex,
		Path:        clean,
		AbsPath:     abs,
		MIME:        mime,
		Ext:         ExtForMIME(mime),
		Width:       width,
		Height:      height,
		Bytes:       len(data),
		Tokens:      DeepSeekImageTokens(width, height, true),
		Data:        data,
		ThumbData:   thumbData,
		ThumbMIME:   thumbMIME,
		ThumbWidth:  thumbW,
		ThumbHeight: thumbH,
	}, nil
}

// LoadRaw reads an already-stored image without decoding pixels or building a
// thumbnail. It is used on the request-building hot path, where only the bytes,
// MIME type and dimensions are needed for the image content part.
func LoadRaw(workspace, relPath string) (Image, error) {
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return Image{}, fmt.Errorf("resolve workspace: %w", err)
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(relPath)))
	if !isStorePath(clean) {
		return Image{}, fmt.Errorf("not an image store path: %s", relPath)
	}
	abs := filepath.Join(absWorkspace, filepath.FromSlash(clean))
	if err := ensureWithinStore(absWorkspace, abs); err != nil {
		return Image{}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return Image{}, err
	}
	if len(data) > MaxImageBytes {
		return Image{}, fmt.Errorf("stored image too large: %d bytes", len(data))
	}
	mime, width, height, err := inspect(data)
	if err != nil {
		return Image{}, err
	}
	sum := sha256.Sum256(data)
	hashHex := hex.EncodeToString(sum[:])
	return Image{
		ID:      IDFromHashHex(hashHex),
		Hash:    hashHex,
		Path:    clean,
		AbsPath: abs,
		MIME:    mime,
		Ext:     ExtForMIME(mime),
		Width:   width,
		Height:  height,
		Bytes:   len(data),
		Tokens:  DeepSeekImageTokens(width, height, true),
		Data:    data,
	}, nil
}

// IsImagePath reports whether p is an AI-facing image virtual path or an
// internal store path. The latter is only used by internal callers; it is not
// advertised to the model or the client.
func IsImagePath(p string) bool {
	p = filepath.ToSlash(strings.TrimSpace(p))
	if strings.HasPrefix(strings.ToLower(p), "@image/") {
		return true
	}
	return isStorePath(filepath.ToSlash(filepath.Clean(filepath.FromSlash(p))))
}

// ImageRefID returns the id part of "@image/{id}", or "".
func ImageRefID(p string) string {
	p = strings.TrimSpace(p)
	if !strings.HasPrefix(strings.ToLower(p), "@image/") {
		return ""
	}
	return strings.TrimSpace(p[len("@image/"):])
}

// IDFromHashHex returns the base36 id for a hex sha256 digest.
func IDFromHashHex(hashHex string) string {
	hashHex = strings.ToLower(strings.TrimSpace(hashHex))
	if len(hashHex) < 32 {
		return ""
	}
	raw, err := hex.DecodeString(hashHex[:32])
	if err != nil {
		return ""
	}
	return new(big.Int).SetBytes(raw).Text(36)
}

// hashPrefixFromID decodes a base36 id back to the first 16 bytes of the hash
// and returns its hex representation (32 chars, left padded).
func hashPrefixFromID(id string) (string, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return "", errors.New("empty id")
	}
	for _, r := range id {
		if (r < '0' || r > '9') && (r < 'a' || r > 'z') {
			return "", errors.New("not base36")
		}
	}
	n, ok := new(big.Int).SetString(id, 36)
	if !ok || n.Sign() <= 0 {
		return "", errors.New("not base36")
	}
	raw := n.Bytes()
	if len(raw) > 16 {
		return "", errors.New("id too large")
	}
	padded := make([]byte, 16)
	copy(padded[16-len(raw):], raw)
	return hex.EncodeToString(padded), nil
}

// ExtForMIME maps a supported image MIME type to its file extension.
func ExtForMIME(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/bmp":
		return ".bmp"
	default:
		return ""
	}
}

func inspect(data []byte) (string, int, int, error) {
	mime := strings.ToLower(http.DetectContentType(data))
	if ExtForMIME(mime) == "" {
		return "", 0, 0, fmt.Errorf("unsupported or non-image content (%s)", mime)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", 0, 0, fmt.Errorf("unsupported or corrupt image: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return "", 0, 0, errors.New("invalid image dimensions")
	}
	if int64(cfg.Width)*int64(cfg.Height) > MaxPixels {
		return "", 0, 0, fmt.Errorf("image too large: %dx%d (max %d pixels)", cfg.Width, cfg.Height, MaxPixels)
	}
	return mime, cfg.Width, cfg.Height, nil
}

func normalize(data []byte, mime string, width, height, maxDim int) ([]byte, string, int, int, image.Image, error) {
	if maxDim <= 0 || (width <= maxDim && height <= maxDim) {
		return data, mime, width, height, nil, nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", 0, 0, nil, fmt.Errorf("decode image for scaling: %w", err)
	}
	scale := float64(maxDim) / float64(width)
	if s := float64(maxDim) / float64(height); s < scale {
		scale = s
	}
	nw := int(float64(width) * scale)
	nh := int(float64(height) * scale)
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewNRGBA(image.Rect(0, 0, nw, nh))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), xdraw.Src, nil)
	stored, storedMIME, err := encodeImage(dst)
	if err != nil {
		return nil, "", 0, 0, nil, err
	}
	return stored, storedMIME, nw, nh, dst, nil
}

func decodeImage(data []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

func encodeImage(img image.Image) ([]byte, string, error) {
	var buf bytes.Buffer
	if hasAlpha(img) {
		if err := png.Encode(&buf, img); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), "image/png", nil
	}
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), "image/jpeg", nil
}

func makeThumbnail(img image.Image) (string, string, int, int, error) {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 {
		return "", "", 0, 0, errors.New("empty image")
	}
	maxDim := DefaultThumbnailDimension
	tw, th := w, h
	var thumb image.Image = img
	if w > maxDim || h > maxDim {
		scale := float64(maxDim) / float64(w)
		if s := float64(maxDim) / float64(h); s < scale {
			scale = s
		}
		tw = max(1, int(float64(w)*scale))
		th = max(1, int(float64(h)*scale))
		dst := image.NewNRGBA(image.Rect(0, 0, tw, th))
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, bounds, xdraw.Src, nil)
		thumb = dst
	}
	var buf bytes.Buffer
	mime := "image/jpeg"
	if hasAlpha(thumb) {
		mime = "image/png"
		if err := png.Encode(&buf, thumb); err != nil {
			return "", "", 0, 0, err
		}
	} else {
		if err := jpeg.Encode(&buf, thumb, &jpeg.Options{Quality: thumbQuality}); err != nil {
			return "", "", 0, 0, err
		}
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), mime, tw, th, nil
}

func hasAlpha(img image.Image) bool {
	type opaqueImage interface{ Opaque() bool }
	if o, ok := img.(opaqueImage); ok {
		return !o.Opaque()
	}
	return false
}

func writeFileAtomic(abs string, data []byte) error {
	if _, err := os.Stat(abs); err == nil {
		return nil
	}
	dir := filepath.Dir(abs)
	f, err := os.CreateTemp(dir, ".alk-image-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Chmod(0o644); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	sandbox.ApplyConfiguredFileOwner(tmp)
	if err := os.Rename(tmp, abs); err != nil {
		// Windows cannot replace an existing file; a concurrent writer won.
		if _, statErr := os.Stat(abs); statErr == nil {
			return nil
		}
		return err
	}
	return nil
}

func isStorePath(cleanSlash string) bool {
	clean := strings.ToLower(filepath.ToSlash(cleanSlash))
	return clean == Dir || strings.HasPrefix(clean, Dir+"/")
}

func ensureWithinStore(absWorkspace, abs string) error {
	storeDir := filepath.Join(absWorkspace, filepath.FromSlash(Dir))
	rel, err := filepath.Rel(storeDir, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("image path escapes the image store")
	}
	return nil
}
