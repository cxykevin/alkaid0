// Package phrase 实现基于配置的短语系统：/s <short> 展开为长内容发送。
// 短语表读取自配置文件 Context 块下的 Phrase（config.Context.Phrase）。
package phrase

import (
	"fmt"
	"strings"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
)

// Enabled 短语系统是否启用。
func Enabled() bool {
	return config.GlobalConfig.Context.Phrase.Enable
}

// All 返回配置的全部短语（仅在启用时，保持配置顺序）。
func All() []cfgStructs.Phrase {
	if !Enabled() {
		return nil
	}
	return config.GlobalConfig.Context.Phrase.Phrases
}

// Lookup 按短键查询短语（短键两侧空白被忽略）。
func Lookup(short string) (cfgStructs.Phrase, bool) {
	short = strings.TrimSpace(short)
	for _, p := range All() {
		if p.Short == short {
			return p, true
		}
	}
	return cfgStructs.Phrase{}, false
}

// ListText 渲染短语列表文本（/s 无参数时的输出）。
func ListText() string {
	list := All()
	if len(list) == 0 {
		return "**No phrases configured.** 在 `config.json` 的 `phrase.phrases` 中配置 `{short, text, desc}` 并将 `phrase.enable` 设为 `true`。"
	}
	var b strings.Builder
	b.WriteString("**Configured phrases:**\n")
	for _, p := range list {
		b.WriteString(fmt.Sprintf("  **`%s`**", p.Short))
		if p.Desc != "" {
			b.WriteString(fmt.Sprintf(" — %s", p.Desc))
		}
		b.WriteString(fmt.Sprintf(": %s\n", p.Text))
	}
	return b.String()
}
