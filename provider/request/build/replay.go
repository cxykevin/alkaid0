package build

import (
	"github.com/cxykevin/alkaid0/prompts"
	"github.com/cxykevin/alkaid0/storage/structs"
)

// initCommandText /init 命令持久化到历史中的用户消息内容（命令字符串本身，见 server/actions）。
const initCommandText = "/init"

// replayUserContent 计算用户消息在历史回放时送模型的内容与引用。
//
// /init 命令消息（delta 为命令字符串本身）固定注入 init 提示词（prompts.InitTemplate），
// 而不是命令字符串本身：命令名对模型没有语义，历史回放要还原的是初始化任务指令。
// 该消息的引用一律丢弃：命令消息本身不携带引用，历史里过渡格式的模板引用也不能重复注入。
// 客户端直播/回放看到的始终是命令字符串本身。
func replayUserContent(delta string, refers structs.MessagesReferList) (string, structs.MessagesReferList, error) {
	if delta != initCommandText {
		return delta, refers, nil
	}
	rendered, err := prompts.Render(prompts.InitTemplate, struct{}{})
	if err != nil {
		return "", nil, err
	}
	return rendered, nil, nil
}
