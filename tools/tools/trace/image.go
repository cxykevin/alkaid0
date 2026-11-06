package trace

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/internal/imagestore"
	"github.com/cxykevin/alkaid0/storage/structs"
	u "github.com/cxykevin/alkaid0/utils"
	"gorm.io/gorm/clause"
)

// MediaKindImage 标记 Traces 行为图片 trace（内容不可 diff，永远整块注入）。
const MediaKindImage = "image"

// DefaultVisionMaxDimension 模型配置里 VisionMaxImageDimension 缺失时的兜底长边像素。
const DefaultVisionMaxDimension = 1568

var imageExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true,
	".gif": true, ".webp": true, ".bmp": true,
}

// ImageBlock 图片内容块的元数据：build 层据此在 user 消息里追加 image_url 内容块。
// DataURL 只在内存里存在（不落库、不进 trace 文本），图片本体按内容寻址存于
// .alkaid0/images。
type ImageBlock struct {
	ID      string
	MIME    string
	Width   int
	Height  int
	Bytes   int
	Tokens  int
	DataURL string
}

// isImageFilePath 判断 read 的路径是否可能是图片（按扩展名，内容由 imagestore 校验）。
func isImageFilePath(p string) bool {
	if p == "" || strings.HasPrefix(p, "@") && !isImageRefPath(p) {
		return false
	}
	if isImageRefPath(p) {
		return true
	}
	return imageExts[strings.ToLower(filepath.Ext(p))]
}

// isImageRefPath 判断是否是 AI 侧图片虚拟路径 @image/{id}。
func isImageRefPath(p string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(p)), "@image/")
}

// traceWorkspaceRoot 返回图片库所在的工作区根目录（.alkaid0/images 的父目录）。
// 会话根缺失（测试/异常路径）时退回当前工作目录，保证仍能读写图片库。
func traceWorkspaceRoot(session *structs.Chats, nowpath string) string {
	root := session.Root
	if root == "" {
		root = nowpath
	}
	if abs, err := filepath.Abs(root); err == nil {
		return abs
	}
	return root
}

// VisionMaxDimension 读取当前模型的图片长边上限；0 表示不缩放。
// 服务端解析用户消息图片（ACP image 块）时也需要这个值。
func VisionMaxDimension(session *structs.Chats) int {
	cfg := config.GlobalConfigSafe()
	if cfg == nil {
		return DefaultVisionMaxDimension
	}
	m, ok := cfg.Model.Models[int32(session.EffectiveModelID())]
	if !ok {
		return DefaultVisionMaxDimension
	}
	return m.VisionMaxImageDimension
}

// saveImageFromPath 读取并保存一个图片，返回已存储的图片与源字节 hash。
// path 是模型给的路径（工作区图片文件或 @image/{id}），nowpath 是会话激活目录。
func saveImageFromPath(session *structs.Chats, nowpath, path string) (imagestore.Image, string, error) {
	root := traceWorkspaceRoot(session, nowpath)
	maxDim := VisionMaxDimension(session)
	if isImageRefPath(path) {
		im, err := imagestore.Resolve(root, path)
		if err != nil {
			return imagestore.Image{}, "", err
		}
		return im, im.Hash, nil
	}
	path2 := filepath.Join(nowpath, path)
	path2 = filepath.Clean(path2)
	if err := u.EnsureWorkspacePath(nowpath, path2, ".alkaid0"); err != nil {
		return imagestore.Image{}, "", err
	}
	data, err := os.ReadFile(path2)
	if err != nil {
		return imagestore.Image{}, "", err
	}
	im, err := imagestore.Save(root, data, maxDim)
	if err != nil {
		return imagestore.Image{}, "", err
	}
	sum := sha256.Sum256(data)
	return im, hex.EncodeToString(sum[:]), nil
}

// writeImageTraceRow 写入/更新图片 trace 行（复合主键 upsert），并把缓存同步到数据库。
func writeImageTraceRow(session *structs.Chats, path string, im imagestore.Image, sourceHash string, maxDim int) error {
	row := structs.Traces{
		Path:            path,
		ChatID:          session.ID,
		AgentID:         session.NowAgent,
		MediaKind:       MediaKindImage,
		MediaPath:       im.Path,
		MediaSourceHash: sourceHash,
		MediaHash:       im.Hash,
		MediaMIME:       im.MIME,
		MediaWidth:      im.Width,
		MediaHeight:     im.Height,
		MediaBytes:      im.Bytes,
		MediaTokens:     im.Tokens,
		MediaMaxDim:     maxDim,
	}
	err := session.DB.Omit(clause.Associations).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "path"}, {Name: "chat_id"}, {Name: "agent_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"media_kind", "media_path", "media_source_hash", "media_hash", "media_mime",
			"media_width", "media_height", "media_bytes", "media_tokens", "media_max_dim",
			"last_content", "read_mode",
		}),
	}).Create(&row).Error
	if err != nil {
		return err
	}
	syncTraceCache(session)
	return nil
}

// TraceStoredImage 把一张已存储的图片注册到当前 agent 的读上下文，并锚定在
// anchorMsgID 所指的消息之后（用户消息附带的图片由服务端在落库后调用）。
// 行内的 AnchorMsgID 让图片块稳定落在原消息后面，后续轮次不需要搬家。
func TraceStoredImage(session *structs.Chats, im imagestore.Image, anchorMsgID uint64) error {
	if session == nil || session.DB == nil {
		return fmt.Errorf("session database is not available")
	}
	if im.ID == "" {
		return fmt.Errorf("empty image id")
	}
	path := "@image/" + im.ID
	row := structs.Traces{
		Path:            path,
		ChatID:          session.ID,
		AgentID:         session.NowAgent,
		MediaKind:       MediaKindImage,
		MediaPath:       im.Path,
		MediaSourceHash: im.Hash,
		MediaHash:       im.Hash,
		MediaMIME:       im.MIME,
		MediaWidth:      im.Width,
		MediaHeight:     im.Height,
		MediaBytes:      im.Bytes,
		MediaTokens:     im.Tokens,
		MediaMaxDim:     VisionMaxDimension(session),
		AnchorMsgID:     anchorMsgID,
	}
	err := session.DB.Omit(clause.Associations).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "path"}, {Name: "chat_id"}, {Name: "agent_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"media_kind", "media_path", "media_source_hash", "media_hash", "media_mime",
			"media_width", "media_height", "media_bytes", "media_tokens", "media_max_dim",
			"anchor_msg_id", "last_content", "read_mode",
		}),
	}).Create(&row).Error
	if err != nil {
		return err
	}
	syncTraceCache(session)
	return nil
}

// syncTraceCache 把当前 agent 的 traces 重新读入会话缓存（图片行新建/更新后调用）。
// 与 AddTempObject 的收尾逻辑一致：缓存必须与数据库同步，否则下一轮 RenderTraceBlocks
// 会按"首次注入"重新前移内容块。
func syncTraceCache(session *structs.Chats) {
	if session == nil || session.DB == nil {
		return
	}
	if session.TemporyDataOfSession == nil {
		session.TemporyDataOfSession = make(map[string]any)
	}
	if _, ok := session.TemporyDataOfSession["tools:trace"].(traceCache); !ok {
		session.TemporyDataOfSession["tools:trace"] = traceCache{}
	}
	traces := []structs.Traces{}
	if err := session.DB.Where("chat_id = ? AND agent_id = ?", session.ID, session.NowAgent).Find(&traces).Error; err != nil {
		logger.Warn("sync trace cache failed: %v", err)
		return
	}
	session.TemporyDataOfSession["tools:trace"].(traceCache)[session.NowAgent] = traces
}

// updateTraceImageRow 图片库里的字节被替换（或基线缺失）时同步元数据与缓存。
func updateTraceImageRow(session *structs.Chats, path string, im imagestore.Image, sourceHash string) {
	if session == nil || session.DB == nil {
		return
	}
	updates := map[string]any{
		"media_path":   im.Path,
		"media_hash":   im.Hash,
		"media_mime":   im.MIME,
		"media_width":  im.Width,
		"media_height": im.Height,
		"media_bytes":  im.Bytes,
		"media_tokens": im.Tokens,
	}
	if sourceHash != "" {
		updates["media_source_hash"] = sourceHash
	}
	if err := session.DB.Model(&structs.Traces{}).
		Where("chat_id = ? AND path = ? AND agent_id = ?", session.ID, path, session.NowAgent).
		Updates(updates).Error; err != nil {
		logger.Warn("update image trace failed for %q: %v", path, err)
		return
	}
	updateCachedTrace(session, path, func(t *structs.Traces) {
		t.MediaPath = im.Path
		t.MediaHash = im.Hash
		t.MediaMIME = im.MIME
		t.MediaWidth = im.Width
		t.MediaHeight = im.Height
		t.MediaBytes = im.Bytes
		t.MediaTokens = im.Tokens
		if sourceHash != "" {
			t.MediaSourceHash = sourceHash
		}
	})
}

// loadTraceImage 从图片库读取 traced 图片（不生成缩略图，避免每轮解码大图）。
func loadTraceImage(session *structs.Chats, nowpath string, traceObj structs.Traces) (imagestore.Image, bool) {
	if traceObj.MediaPath == "" {
		return imagestore.Image{}, false
	}
	im, err := imagestore.LoadRaw(traceWorkspaceRoot(session, nowpath), traceObj.MediaPath)
	if err != nil {
		logger.Warn("trace warning: load image %q failed: %v", traceObj.MediaPath, err)
		return imagestore.Image{}, false
	}
	return im, true
}

// imageFileBlock 生成图片的内容块：Text 是给模型的说明，图片本体通过 Image 字段
// 在 build 层作为 image_url 内容块附加。
func imageFileBlock(path string, im imagestore.Image) FileBlock {
	caption := fmt.Sprintf("[image] %s\n%s  %dx%d  ~%d tokens\nThe image is attached as an image content part.",
		path, im.MIME, im.Width, im.Height, im.Tokens)
	return FileBlock{
		Name:   path,
		Size:   strconv.Itoa(im.Bytes),
		Length: uint32(len(caption)),
		Text:   caption,
		Type:   MediaKindImage,
		Image: &ImageBlock{
			ID:      im.ID,
			MIME:    im.MIME,
			Width:   im.Width,
			Height:  im.Height,
			Bytes:   im.Bytes,
			Tokens:  im.Tokens,
			DataURL: im.DataURL(),
		},
	}
}
