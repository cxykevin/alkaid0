// Package parser 提供模型响应解析：
//   - parser.go：正文与 <think> 标签的流式解析；
//   - native.go：OpenAI 原生 tool_calls 增量的累积与参数提取。
//
// 工具调用不经由正文标签解析（不存在提示词/文本形态的工具调用通道）。
package parser
