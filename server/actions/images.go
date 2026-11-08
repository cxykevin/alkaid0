package actions

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cxykevin/alkaid0/internal/imagestore"
	"github.com/cxykevin/alkaid0/tools/tools/trace"
	u "github.com/cxykevin/alkaid0/utils"
)

// maxPromptImages 单条 prompt 最多接受的图片数量（防止一次塞爆上下文）。
const maxPromptImages = 4

// encodeQueueImages 把图片引用列表序列化到队列行（MsgQueueItems.Images）。
func encodeQueueImages(images []string) string {
	if len(images) == 0 {
		return ""
	}
	raw, err := json.Marshal(images)
	if err != nil {
		return ""
	}
	return string(raw)
}

// decodeQueueImages 反序列化队列行里的图片引用列表。
func decodeQueueImages(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var images []string
	if err := json.Unmarshal([]byte(raw), &images); err != nil {
		return nil
	}
	return images
}

// parsePromptImages 解析 ACP prompt 里的 image 内容块：解码 base64、保存到图片库，
// 返回图片虚拟路径列表（@image/{id}）。错误信息会回给客户端。
func (obj *sessionObj) parsePromptImages(blocks []u.H) ([]string, error) {
	if obj == nil || obj.session == nil {
		return nil, fmt.Errorf("session not found")
	}
	var refs []string
	for _, block := range blocks {
		blockType, _ := u.GetH[string](block, "type")
		if blockType != "image" {
			continue
		}
		if len(refs) >= maxPromptImages {
			return nil, fmt.Errorf("too many images in one prompt (limit %d)", maxPromptImages)
		}
		data, err := decodeImageBlock(block)
		if err != nil {
			return nil, err
		}
		im, err := imagestore.Save(obj.session.Root, data, trace.VisionMaxDimension(obj.session))
		if err != nil {
			return nil, fmt.Errorf("cannot accept image: %v", err)
		}
		refs = append(refs, "@image/"+im.ID)
	}
	return refs, nil
}

// decodeImageBlock 从一个 ACP image 内容块取出原始字节。
// 支持两种形态：data(base64) + mimeType，或 data URI（uri = "data:...;base64,..."）。
func decodeImageBlock(block u.H) ([]byte, error) {
	if raw, ok := u.GetH[string](block, "data"); ok && raw != "" {
		data, err := base64.StdEncoding.DecodeString(raw)
		if err == nil {
			return data, nil
		}
		// 部分客户端省略 padding
		if data, rawErr := base64.RawStdEncoding.DecodeString(raw); rawErr == nil {
			return data, nil
		}
		return nil, fmt.Errorf("invalid base64 image data: %v", err)
	}
	if uri, ok := u.GetH[string](block, "uri"); ok && uri != "" {
		if payload, ok := strings.CutPrefix(uri, "data:"); ok {
			if before, after, ok := strings.Cut(payload, ","); ok {
				meta, encoded := before, after
				if strings.Contains(meta, ";base64") {
					data, err := base64.StdEncoding.DecodeString(encoded)
					if err == nil {
						return data, nil
					}
					if data, rawErr := base64.RawStdEncoding.DecodeString(encoded); rawErr == nil {
						return data, nil
					}
					return nil, fmt.Errorf("invalid base64 image data in uri")
				}
			}
		}
		return nil, fmt.Errorf("unsupported image uri (expected a data: URI)")
	}
	return nil, fmt.Errorf("image content block has neither data nor uri")
}

// attachPromptImages 把用户消息附带的图片注册为锚定在该消息上的图片 trace，
// 由请求构建阶段作为 image_url 内容块注入。
func (obj *sessionObj) attachPromptImages(refs []string, msgID uint64) {
	if obj == nil || obj.session == nil || len(refs) == 0 {
		return
	}
	for _, ref := range refs {
		im, err := imagestore.Resolve(obj.session.Root, ref)
		if err != nil {
			logger.Warn("resolve prompt image %q failed: %v", ref, err)
			continue
		}
		if err := trace.TraceStoredImage(obj.session, im, msgID); err != nil {
			logger.Warn("register prompt image %q failed: %v", ref, err)
		}
	}
}

// userMessageContent 构造 user_message 广播的 content：文本块 + 图片缩略图块。
// 客户端据此在时间线上显示缩略图；transcript 落库仍只有文本（图片按 @image/{id} 存库）。
func userMessageContent(root, text string, refs []string) []u.H {
	content := []u.H{{"type": "text", "text": text}}
	for _, ref := range refs {
		im, err := imagestore.Resolve(root, ref)
		if err != nil {
			continue
		}
		content = append(content, u.H{
			"type":     "image",
			"mimeType": im.ThumbMIME,
			"data":     im.ThumbData,
			// 指向图片库内的条目，客户端可用 fs/read("@image/{id}") 取原图。
			"alk.cxykevin.top/image_id": im.ID,
		})
	}
	return content
}
