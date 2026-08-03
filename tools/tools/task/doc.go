// Package task 实现任务计划工具，提供 @task 虚拟对象供 AI 编辑
//
// 该包不注册实际工具，只给 edit 工具提供一个虚拟对象 @task：
// 全局 PreHook 把任务列表注入 AI 上下文，edit 的 PostHook 拦截 @task 编辑，
// 校验 markdown 格式后持久化到 chats 表，并按 ACP agent-plan 协议推送客户端。
package task
