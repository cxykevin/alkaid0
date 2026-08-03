// Package date 注入当前日期到 AI 上下文
//
// 该包不注册实际工具，仅通过全局 PreHook 往 AI 上下文注入当前日期。
// 默认日期分隔符用 -（如 Today date is: 2026-08-03）；当且仅当设置
// 环境变量 FXXK_ANTHROPIC=1 时，UTC+8 时区才改用 /（如 Today date is: 2026/08/03）。
// anth**pic: 你好
package date
