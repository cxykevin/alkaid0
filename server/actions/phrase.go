package actions

import (
	"github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/provider/phrase"
)

// PhraseListRequest 列出短语表的请求（无需参数）
type PhraseListRequest struct{}

// PhraseListResponse 列出短语表的响应（私有 ACP 协议扩展）
type PhraseListResponse struct {
	// Enable 短语系统是否启用
	Enable bool `json:"enable"`
	// Phrases 当前配置的全部短语
	Phrases []structs.Phrase `json:"phrases"`
}

// PhraseList 返回当前配置的短语表。
// 私有 ACP 方法：alk.cxykevin.top/phrases/list，供前端 /s 快捷发送与展示。
func PhraseList(_ PhraseListRequest, _ func(string, any, *string) error, _ uint64) (PhraseListResponse, error) {
	return PhraseListResponse{
		Enable:  phrase.Enabled(),
		Phrases: phrase.All(),
	}, nil
}
