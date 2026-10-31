// Package tree 生成工作区目录树，作为只读虚拟对象 @tree 注入模型上下文。
//
// 读语义（BuildTree/BuildString/TreeContent 及会话级快照缓存）仍在线上：
// 全局 PreHook 每轮刷新快照，trace 层按落位/差分机制注入 @tree 内容块。
//
// 写语义（通过 edit 工具编辑 @tree 增删/复制/移动文件）已下线：AI 从不调用，
// edit 工具的提示词、类型信息与写回 Hook 均已移除。files.go 中的
// SolveCall/BuildNodeFromString 及差异表实现暂时保留，仅供差分逻辑复用与
// 回归测试，不应再接入新的 AI 工具入口；相关函数标记为 Deprecated。
package tree
