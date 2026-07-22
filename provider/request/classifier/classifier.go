package classifier

import (
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/tools/trace"
	promptsplitter "promptsplitter/splitter"
)

//go:embed models/model-t15.bin
var model15Bin []byte

//go:embed models/model-t22.bin
var model22Bin []byte

//go:embed models/model-t30.bin
var model30Bin []byte

var (
	splitterInstance *promptsplitter.Splitter
	once             sync.Once
	initErr          error
	logger           = log.New("classifier")
)

// SegmentInfo 分类段信息，供调用方持久化。
type SegmentInfo struct {
	Label    string // "prompt" | "code" | "log"
	Text     string // 段文本
	TempPath string // 存储到 @temp 的路径（code/log），prompt 段为空
}

// getSplitter 返回分类器单例。
// 优先加载集成模型（3 个阈值模型的集成），失败则回退到纯规则模式（splitter.New()）。
func getSplitter() (*promptsplitter.Splitter, error) {
	once.Do(func() {
		s, err := promptsplitter.LoadEnsembleBytes(model15Bin, model22Bin, model30Bin)
		if err != nil {
			logger.Warn("failed to load ensemble models: %v, falling back to rules-only", err)
			s = promptsplitter.New()
		}
		splitterInstance = s
	})
	return splitterInstance, initErr
}

// generateRandomID 生成 8 位随机 hex 字符串。
func generateRandomID() string {
	b := make([]byte, 4)
	_, err := rand.Read(b)
	if err != nil {
		return "00000000"
	}
	return hex.EncodeToString(b)
}

// ClassifyAndTransform 对用户消息进行语义分类并转换：
//   - prompt 段：保留原文本
//   - code 段：保存到 @temp/prompt/code-{id}，原文本后追加 [path:@temp/prompt/code-{id}]
//   - log 段：保存到 @temp/prompt/log-{id}，整个替换为 [path:@temp/prompt/log-{id}]
//
// code 和 log 段存储时不创建 Traces 记录，不会自动注入 LLM 上下文。
// 返回转换后的消息文本和段信息列表。
func ClassifyAndTransform(session *structs.Chats, msg string) (string, []SegmentInfo, error) {
	if msg == "" {
		return msg, nil, nil
	}

	s, err := getSplitter()
	if err != nil {
		return msg, nil, err
	}

	result, err := s.Split(msg, promptsplitter.Options{})
	if err != nil {
		return msg, nil, fmt.Errorf("classifier split failed: %w", err)
	}

	// 只有一个段或没有段时，直接返回原消息
	if len(result.Segments) <= 1 {
		return msg, nil, nil
	}

	var output strings.Builder
	var segInfos []SegmentInfo

	for _, seg := range result.Segments {
		// 确保边界有效
		if seg.Start < 0 || seg.End > len(msg) || seg.Start > seg.End {
			output.WriteString(msg)
			continue
		}
		segmentText := msg[seg.Start:seg.End]

		switch seg.Label {
		case promptsplitter.Prompt:
			output.WriteString(segmentText)
			segInfos = append(segInfos, SegmentInfo{
				Label: "prompt",
				Text:  segmentText,
			})

		case promptsplitter.Code:
			randID := generateRandomID()
			path := fmt.Sprintf("prompt/code-%s", randID)
			fullPath := fmt.Sprintf("@temp/prompt/code-%s", randID)

			if err := trace.StoreTempObject(session, path, segmentText, false); err != nil {
				logger.Warn("failed to store code object at %s: %v", fullPath, err)
				output.WriteString(segmentText)
			} else {
				output.WriteString(segmentText)
				output.WriteString(fmt.Sprintf("\n[path:%s]", fullPath))
			}
			segInfos = append(segInfos, SegmentInfo{
				Label:    "code",
				Text:     segmentText,
				TempPath: fullPath,
			})

		case promptsplitter.Log:
			randID := generateRandomID()
			path := fmt.Sprintf("prompt/log-%s", randID)
			fullPath := fmt.Sprintf("@temp/prompt/log-%s", randID)

			if err := trace.StoreTempObject(session, path, segmentText, false); err != nil {
				logger.Warn("failed to store log object at %s: %v", fullPath, err)
				output.WriteString(segmentText)
			} else {
				output.WriteString(fmt.Sprintf("[path:%s]", fullPath))
			}
			segInfos = append(segInfos, SegmentInfo{
				Label:    "log",
				Text:     segmentText,
				TempPath: fullPath,
			})
		}
	}

	return output.String(), segInfos, nil
}
