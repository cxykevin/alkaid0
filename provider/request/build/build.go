package build

import (
	"github.com/cxykevin/alkaid0/provider/parser"
	reqStruct "github.com/cxykevin/alkaid0/provider/request/structs"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"gorm.io/gorm"
)

// Build 构造请求体
func Build(db *gorm.DB, session *storageStructs.Chats) (*reqStruct.ChatCompletionRequest, error) {
	// lastChatID := storage.GlobalConfig.CurrentChatID
	// if lastChatID == 0 {
	// 	logger.Error("no last chat id")
	// 	return nil, errors.New("no last chat id")
	// }
	// 构造工具
	var scopes, traces string
	var tools *[]*parser.ToolsDefine = &[]*parser.ToolsDefine{}
	logger.Info("building request body for chatID=%d, agent=%s", session.ID, session.NowAgent)
	chatLine := &storageStructs.Chats{}
	if err := db.Where("id = ?", session.ID).First(chatLine).Error; err != nil {
		// 重读失败时直接返回错误，避免用零值 chatLine 静默降级到错误的模型/agent 上下文
		logger.Error("db error %v", err)
		return nil, err
	}
	// DB 等 gorm:"-" 运行时字段不会被 First 填充：这里补上句柄，否则 RequestBody 里
	// trace 内容块按末尾落位回写注入锚点/旧端存档时会因 session.DB == nil 静默跳过，
	// 表现为块每轮都重新前移到末尾（前缀缓存白丢一轮）。RequestBody 也有同样的兜底。
	chatLine.DB = db
	if !session.InTestFlag {
		// 检测每个 path 最近一次 read/edit 事件，写入 session.TemporyDataOfSession。
		// trace/task 的全局 PreHook 据此分区（顶部 vs 事件块），RequestBody 据此按事件插入。
		if err := DetectTraceEvents(db, session, chatLine.NowAgent); err != nil {
			logger.Warn("detect trace events error: %v", err) // 非致命：降级为全部顶部聚合
		}
		var err error
		scopes, traces, tools, err = Tools(session)
		if err != nil {
			logger.Error("build tools error %v", err)
			return nil, err
		}
	}
	// 会话级临时数据可能尚未初始化（测试/直接调用路径），先确保可用再交给 RequestBody。
	if session.TemporyDataOfSession == nil {
		session.TemporyDataOfSession = make(map[string]any)
	}
	// 把运行时临时数据和一次性内部通知传给 RequestBody。
	chatLine.TemporyDataOfSession = session.TemporyDataOfSession
	// scopes 是稳定前缀，留在 system 消息里。
	addSystemPrompt := scopes
	if session.SystemPrompt != "" {
		// 内部运行期通知（后台任务结束 / shell 停止等）改走消息列表末尾的独立块：
		// system 消息在 tools 之后，任何一次通知都会把 tools 之后的整个前缀（含全部历史）
		// 打掉——实测命中率从 ~95% 掉到 tools 前缀大小。放在末尾只重算末尾那一小块。
		// 只读取、不在这里清空：请求失败时调用方（ui/loop 指数退避重试）会重新调用
		// Build 构建请求，构建即消费会让重试请求丢掉排队的通知。
		// 消费由 SendRequest 在请求成功后完成。
		session.TemporyDataOfSession[storageStructs.TempKeySystemNotices] = session.SystemPrompt
	}
	// 模型解析必须与传输层（request.SendRequest）共用同一个入口：子代理激活时走
	// AgentModel。此前这里直接用 chatLine.LastModelID，于是请求体里的 model 与全部
	// 模型参数取自父模型，而连接目标/落库/计费/上下文统计按子代理模型走——轻则参数
	// 不符，重则子代理模型跨供应商时直接 404。
	body, err := RequestBody(session.ID, int32(session.EffectiveModelID()), chatLine.NowAgent, tools, db, addSystemPrompt, traces, session.CurrentAgentConfig, chatLine)
	if err != nil {
		logger.Error("build request body error %v", err)
		return nil, err
	}
	return body, nil
}
