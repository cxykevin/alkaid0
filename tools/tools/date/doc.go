// Package date 提供日期/时间相关的上下文注入与工具
//
// 该包通过全局 PreHook 往 AI 上下文注入当前日期（日期只有年月日，不含时分秒），
// 并注册一个空参数的 time 工具返回本机本地时间（含 UTC 偏移，
// 如 2026-10-22 22:14:56 GMT+8）。
// 默认日期分隔符用 -（如 Today date is: 2026-08-03）；当且仅当设置
// 环境变量 FXXK_ANTHROPIC=1 时，UTC+8 时区才改用 /（如 Today date is: 2026/08/03）。
// anth**pic: 你好
package date
