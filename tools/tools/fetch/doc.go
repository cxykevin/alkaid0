// Package fetch 提供 HTTP 请求工具。
//
// fetch 向任意 URL 发起 HTTP 请求，响应写入 temp 对象（@temp/fetch/...）供 AI 读取。
// 支持：
//   - hidden 模式：用 utls 做 Chrome TLS 指纹伪装 + 浏览器风格 headers 模拟真实浏览器，
//     配合 FetchProxy 时手动建立 CONNECT/socks5 隧道以保留指纹；
//   - summary 模式：非空时调用 LLM 按提示词把抓取内容总结成 markdown；
//   - FetchProxy 配置：顶层 Agent 段全局 HTTP 代理（http/https/socks5）。
package fetch
