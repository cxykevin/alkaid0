// Package response 解析与处理模型响应数据：
// 正文与 <think> 由 parser 处理，工具调用由原生 tool_calls 累积器解析并落库，
// 工具执行结果经回调写入 role:"tool" 消息。
package response
