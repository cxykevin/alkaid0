// Package openai 一个兼容 OpenAI API 的模拟服务器
//
// 使用说明:
//
//  1. 运行服务器:
//     go run openai.go
//     服务器将在 http://localhost:56108 启动
//
// 2. API 端点:
//
//		a) 聊天补全 (Chat Completion)
//		   POST /v1/chat/completions
//
//		   示例请求:
//		   curl -X POST http://localhost:56108/v1/chat/completions \
//		     -H "Content-Type: application/json" \
//		     -d '{
//		       "model": "test-chat",
//		       "messages": [
//		         {"role": "user", "content": "Hello, how are you?"}
//		       ]
//		     }'
//
//		   响应: 返回模拟的聊天回复和 token 使用情况
//
//		d) 流式聊天补全 (Streaming Chat Completion)
//		   POST /v1/chat/completions
//
//		   示例请求:
//		   curl -X POST http://localhost:56108/v1/chat/completions \
//		     -H "Content-Type: application/json" \
//		     -d '{
//		       "model": "test-chat",
//		       "messages": [
//		         {"role": "user", "content": "Hello, how are you?"}
//		       ],
//		       "stream": true
//		     }'
//
//		   响应: 返回 Server-Sent Events 格式的流式响应，每个 chunk 包含增量内容
//
//		b) 文本嵌入 (Embedding)
//		   POST /v1/embeddings
//
//		   示例请求:
//		   curl -X POST http://localhost:56108/v1/embeddings \
//		     -H "Content-Type: application/json" \
//		     -d '{
//		       "model": "test-embedding",
//		       "input": ["Hello world", "Test text"]
//		     }'
//
//		   响应: 返回 512 维的随机嵌入向量（同默认）
//
//		c) 模型列表 (Models)
//		   GET /v1/models
//
//		   示例请求:
//		   curl http://localhost:56108/v1/models
//
//		   响应: 返回可用的模型列表
//
//	 3. 配置选项:
//	    修改 Addr 常量可更改服务器监听地址和端口
//
// 4. 支持的模型:
//   - test-chat: 用于聊天补全测试
//   - test-chat-flash: 用于聊天补全测试（无延迟）
//   - test-embedding: 用于嵌入测试
//
// 5. 注意事项:
//   - 支持流式响应 (stream: true 返回 Server-Sent Events)
//   - Token 计算基于简单的空格分词，仅供参考
//   - 嵌入向量是随机生成的，仅用于测试目的
package openai

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// --- configs ---

// Addr 服务端口号
// 格式: ":端口" 或 "主机:端口"
// 示例: ":56108" 监听所有接口的 56108 端口
// 测试中改为 ":0" 使用随机端口，避免并行冲突
var Addr = ":56108"

// BaseURL 是当前 mock 服务器的基础 URL（含端口），测试通过此函数获取服务地址。
// 在 StartServerTask 返回后可用。
var BaseURL string

// SetAddr 在测试中设置监听地址，":0" 表示随机端口。
// 测试 init() 阶段调用，确保服务器启动时使用正确的地址。
func SetAddr(addr string) {
	Addr = addr
}

// Models 可用的模型列表
var Models = []Model{
	{
		ID:      "test-chat",
		Object:  "model",
		Created: time.Now().Unix(),
		OwnedBy: "mock",
	},
	{
		ID:      "test-chat-flash", // 关闭延迟
		Object:  "model",
		Created: time.Now().Unix(),
		OwnedBy: "mock",
	},
	{
		ID:      "test-chat-thinking", // 思维链
		Object:  "model",
		Created: time.Now().Unix(),
		OwnedBy: "mock",
	},
	{
		ID:      "test-chat-flash-thinking", // 关闭延迟，思维链
		Object:  "model",
		Created: time.Now().Unix(),
		OwnedBy: "mock",
	},
	{
		ID:      "test-embedding",
		Object:  "model",
		Created: time.Now().Unix(),
		OwnedBy: "mock",
	},
	{
		ID:      "echo-chat",
		Object:  "model",
		Created: time.Now().Unix(),
		OwnedBy: "mock",
	},
	{
		ID:      "echo-chat-flash", // 关闭延迟
		Object:  "model",
		Created: time.Now().Unix(),
		OwnedBy: "mock",
	},
}

// --- configs end ---

// ChatCompletionRequest 聊天补全请求
type ChatCompletionRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream,omitempty"`
}

// Message 消息结构
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatCompletionResponse 聊天补全响应
type ChatCompletionResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// Choice 选择项
type Choice struct {
	Index        int     `json:"index"`
	Delta        Message `json:"delta"`
	FinishReason string  `json:"finish_reason"`
}

// Usage 使用情况统计
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// EmbeddingRequest 嵌入请求
type EmbeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// EmbeddingResponse 嵌入响应
type EmbeddingResponse struct {
	Object string         `json:"object"`
	Data   []Embedding    `json:"data"`
	Model  string         `json:"model"`
	Usage  EmbeddingUsage `json:"usage"`
}

// Embedding 嵌入数据
type Embedding struct {
	Object    string    `json:"object"`
	Embedding []float64 `json:"embedding"`
	Index     int       `json:"index"`
}

// EmbeddingUsage 嵌入使用情况统计
type EmbeddingUsage struct {
	PromptTokens int `json:"prompt_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

// ModelsResponse 模型列表响应
type ModelsResponse struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

// Model 模型信息
type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// EmbeddingDim 嵌入向量的维度，默认为 512；测试可修改此值以匹配测试配置
var EmbeddingDim = 512

// generateID 生成带前缀的唯一 ID
func generateID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

// generateEmbedding 生成指定维度的随机嵌入向量用于测试
func generateEmbedding(dim int) []float64 {
	embedding := make([]float64, dim)
	for i := range embedding {
		embedding[i] = rand.Float64()*2 - 1
	}
	return embedding
}

// calculateTokens 基于空格分词估算 token 数量

func calculateTokens(text string) int {
	return len(strings.Fields(text))
}

// handleChatCompletion 处理聊天补全请求，支持流式和非流式模式

func handleChatCompletion(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[mock] panic in handleChatCompletion: %v", rec)
		}
	}()
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ChatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Stream {
		handleStreamingChatCompletion(w, r, req)
		return
	}

	promptTokens := 0
	for _, msg := range req.Messages {
		promptTokens += calculateTokens(msg.Content)
	}

	var responseText string
	if strings.Contains(req.Model, "echo") && len(req.Messages) > 0 {
		responseText = req.Messages[len(req.Messages)-1].Content
	} else {
		responseText = fmt.Sprintf("This is a mock response from model %s. Your message was received and processed.", req.Model)
	}
	completionTokens := calculateTokens(responseText)

	resp := ChatCompletionResponse{
		ID:      generateID("chatcmpl"),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []Choice{
			{
				Index: 0,
				Delta: Message{
					Role:    "assistant",
					Content: responseText,
				},
				FinishReason: "stop",
			},
		},
		Usage: Usage{
			PromptTokens:     promptTokens,
			CompletionTokens: completionTokens,
			TotalTokens:      promptTokens + completionTokens,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleStreamingChatCompletion 处理流式聊天补全请求，以 SSE 格式返回增量响应
func handleStreamingChatCompletion(w http.ResponseWriter, _ *http.Request, req ChatCompletionRequest) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[mock] panic in streaming handler: %v", rec)
		}
	}()
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	promptTokens := 0
	for _, msg := range req.Messages {
		promptTokens += calculateTokens(msg.Content)
	}
	responseText := ""
	if strings.Contains(req.Model, "-thinking") {
		responseText = responseText + "<think> This is a CoT string. </think> "
	}

	if strings.Contains(req.Model, "toolcall") {
		// 增量工具调用流式响应：`strings.Fields` 按空格把 JSON 拆成多个 chunk，
		// 参数逐 token 增量到达，用于验证工具调用增量流式广播。
		// 若请求已含工具返回（<tools_return>，即上一轮工具已执行），改回普通文本，
		// 避免 auto-approve 场景下工具调用无限循环。
		toolReturned := false
		for _, m := range req.Messages {
			// 工具结果以 user 角色消息回传（build 的 ToolResponseWrapTemplate 含 <tools_return> 标签）；
			// 仅检查 user 消息，避免 system 提示词里的 <tools_return> 字样误判
			if m.Role == "user" && strings.Contains(m.Content, "<tools_return>") {
				toolReturned = true
				break
			}
		}
		if toolReturned {
			responseText += fmt.Sprintf("This is a mock response from model %s. Tool executed.", req.Model)
		} else {
			responseText += `<tools> [ {"name": "edit", "id": "tid", "parameters": {"path": "a.txt", "target": "x", "text": "hello"}} ] </tools>`
		}
	} else if strings.Contains(req.Model, "echo") && len(req.Messages) > 0 {
		if strings.Contains(req.Messages[len(req.Messages)-1].Content, "<|show_full_messages|>") {
			for _, v := range req.Messages {
				responseText += fmt.Sprintf("---- role: %s ----\n%s\n\n", v.Role, v.Content)
			}
		} else {
			responseText += strings.TrimSpace(
				strings.ReplaceAll(
					strings.ReplaceAll(
						strings.ReplaceAll(
							req.Messages[len(req.Messages)-1].Content,
							"<!-- Alkaid User Prompt -->", ""),
						"<user_prompt>", ""),
					"</user_prompt>", ""),
			)
		}
	} else {
		responseText += fmt.Sprintf("This is a mock response from model %s. Your message was received and processed.", req.Model)
	}
	completionTokens := calculateTokens(responseText)

	responseID := generateID("chatcmpl")
	created := time.Now().Unix()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	words := strings.Fields(responseText)
	currentContent := ""

	for i, word := range words {
		currentContent = word

		choice := Choice{
			Index: 0,
			Delta: Message{
				Role:    "assistant",
				Content: string(currentContent) + " ",
			},
		}

		// 仅最后一个 chunk 带 finish_reason，否则符合 OpenAI 规范的客户端会提前判定流结束
		if i == len(words)-1 {
			choice.FinishReason = "stop"
		}

		resp := ChatCompletionResponse{
			ID:      responseID,
			Object:  "chat.completion.chunk",
			Created: created,
			Model:   req.Model,
			Choices: []Choice{choice},
			Usage: Usage{
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				TotalTokens:      promptTokens + completionTokens,
			},
		}

		data, err := json.Marshal(resp)
		if err != nil {
			http.Error(w, "Failed to marshal response", http.StatusInternalServerError)
			return
		}

		fmt.Fprintf(w, "data: %s\n\n", data)

		if !strings.Contains(req.Model, "-flash") {
			flusher.Flush()
			time.Sleep(50 * time.Millisecond)
		}
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// handleEmbedding 处理文本嵌入请求，返回随机生成的嵌入向量
func handleEmbedding(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("[mock] panic in embedding handler: %v", rec)
		}
	}()
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req EmbeddingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	embeddings := make([]Embedding, len(req.Input))
	totalTokens := 0

	for i, text := range req.Input {
		embeddings[i] = Embedding{
			Object:    "embedding",
			Embedding: generateEmbedding(EmbeddingDim),
			Index:     i,
		}
		totalTokens += calculateTokens(text)
	}

	resp := EmbeddingResponse{
		Object: "list",
		Data:   embeddings,
		Model:  req.Model,
		Usage: EmbeddingUsage{
			PromptTokens: totalTokens,
			TotalTokens:  totalTokens,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleModels 处理模型列表查询请求

func handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resp := ModelsResponse{
		Object: "list",
		Data:   Models,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

var (
	waitChan   chan bool
	serverOnce sync.Once
)

// acquireServer 阻塞直到当前进程成功绑定端口并启动服务器。
// 每个进程有自己的服务器实例，不共享、不依赖其他进程。
// Addr 为 ":0" 时监听随机端口，并行测试中每个包获得独立服务器。
func acquireServer() {
	for range 150 { // 最多重试 30s（150 × 200ms）
		listener, err := net.Listen("tcp", Addr)
		if err == nil {
			// 提取实际端口（Addr 为 ":0" 时由 OS 分配）
			_, port, _ := net.SplitHostPort(listener.Addr().String())
			BaseURL = fmt.Sprintf("http://localhost:%s/v1", port)
			if waitChan != nil {
				close(waitChan)
			}
			startServe(listener)
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	// 超时：通知 StartServerTask 解除阻塞，测试快速失败
	fmt.Printf("[mock] failed to bind %s after 150 retries\n", Addr)
	if waitChan != nil {
		close(waitChan)
	}
}

// startServe 使用已绑定的 listener 启动 HTTP 服务
func startServe(listener net.Listener) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", handleChatCompletion)
	mux.HandleFunc("/v1/embeddings", handleEmbedding)
	mux.HandleFunc("/v1/models", handleModels)

	server := &http.Server{
		Handler: mux,
	}

	if err := server.Serve(listener); err != nil {
		fmt.Printf("Server stopped: %v\n", err)
	}
}

// StartServerTask 启动服务器任务。
// serverOnce.Do 保证每个进程只启动一次。
// 始终使用随机端口（:0），避免并行测试冲突。
func StartServerTask() {
	serverOnce.Do(func() {
		// 仅当 Addr 仍为默认值/未配置时才重置为随机端口；
		// SetAddr 显式配置的端口应保留，否则 SetAddr 会失效
		if Addr == "" || Addr == ":56108" {
			Addr = ":0"
		}
		waitChan = make(chan bool, 1)
		go acquireServer()
		<-waitChan
		time.Sleep(100 * time.Millisecond)
	})
}

// func main() {
// 	StartServer()
// }

// init 在包被导入时自动启动 mock 服务器（随机端口）。
// 任何测试文件 import "mock/openai" 后，服务器会自动在后台启动，
// 通过 openai.BaseURL 获取服务地址。
// 生产环境中 ALKAID0_DEBUG_MOCKSERVER 未设置，init 直接返回。
func init() {
	if os.Getenv("ALKAID0_DEBUG_MOCKSERVER") == "true" {
		// 使用随机端口避免并行测试冲突
		Addr = ":0"
		StartServerTask()
	}
}

// Start 检查环境变量并启动服务器（main 程序入口调用）
func Start() {
	// init() 已经处理了环境变量检查，为保持向后兼容保留此函数
}
