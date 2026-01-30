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
	"math/rand"
	"net/http"
	"os"
	"strings"
	"time"
)

// --- configs ---

// Addr 服务端口号
// 格式: ":端口" 或 "主机:端口"
// 示例: ":56108" 监听所有接口的 56108 端口
const Addr = ":56108"

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

func generateID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func generateEmbedding() []float64 {
	embedding := make([]float64, 512)
	for i := range embedding {
		embedding[i] = rand.Float64()*2 - 1
	}
	return embedding
}

func calculateTokens(text string) int {
	return len(strings.Fields(text))
}

func handleChatCompletion(w http.ResponseWriter, r *http.Request) {
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

func handleStreamingChatCompletion(w http.ResponseWriter, _ *http.Request, req ChatCompletionRequest) {
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

	if strings.Contains(req.Model, "echo") && len(req.Messages) > 0 {
		responseText += strings.TrimSpace(
			strings.ReplaceAll(
				strings.ReplaceAll(
					strings.ReplaceAll(
						req.Messages[len(req.Messages)-1].Content,
						"<!-- Alkaid User Prompt -->", ""),
					"<user_prompt>", ""),
				"</user_prompt>", ""),
		)
	} else {
		responseText += responseText + fmt.Sprintf("This is a mock response from model %s. Your message was received and processed.", req.Model)
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
			FinishReason: "stop",
		}

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

func handleEmbedding(w http.ResponseWriter, r *http.Request) {
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
			Embedding: generateEmbedding(),
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

var waitChan chan bool

// StartServer 启动服务器
func StartServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", handleChatCompletion)
	mux.HandleFunc("/v1/embeddings", handleEmbedding)
	mux.HandleFunc("/v1/models", handleModels)

	server := &http.Server{
		Addr:    Addr,
		Handler: mux,
	}

	fmt.Println("Mock OpenAI-compatible API server running on http://localhost" + server.Addr)

	waitChan <- true
	if err := server.ListenAndServe(); err != nil {
		fmt.Printf("Server failed to start: %v\n", err)
		panic(err)
	}
}

// StartServerTask 启动服务器任务
func StartServerTask() {
	waitChan = make(chan bool)
	go StartServer()
	<-waitChan
	time.Sleep(100 * time.Millisecond)
}

// func main() {
// 	StartServer()
// }

// Start 检查环境变量并启动服务器
func Start() {
	if os.Getenv("ALKAID0_DEBUG_MOCKSERVER") == "true" {
		StartServerTask()
	}
}
