package openai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cxykevin/alkaid0/config"
	cfgstructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/product"
	"github.com/cxykevin/alkaid0/provider/request"
	reqstructs "github.com/cxykevin/alkaid0/provider/request/structs"
	"github.com/cxykevin/alkaid0/stats"
)

const apiPrefix = "/openai/v1"

type Handler struct{}

func NewHandler() *Handler { return &Handler{} }

// Register 将 OpenAI-compatible API 挂载到调用方提供的 mux。
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc(apiPrefix+"/models", h.models)
	mux.HandleFunc(apiPrefix+"/chat/completions", h.chatCompletions)
	mux.HandleFunc(apiPrefix+"/embeddings", h.embeddings)
}

type modelInfo struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

type modelListResponse struct {
	Object string      `json:"object"`
	Data   []modelInfo `json:"data"`
}

func (h *Handler) models(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		h.error(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error", nil)
		return
	}
	models := visibleModels()
	out := modelListResponse{Object: "list", Data: make([]modelInfo, 0, len(models))}
	for _, item := range models {
		out.Data = append(out.Data, modelInfo{ID: item.config.ModelID, Object: "model", Created: 0, OwnedBy: "alkaid0"})
	}
	h.json(w, http.StatusOK, out)
}

type configuredModel struct {
	id     int32
	config cfgstructs.ModelConfig
}

func visibleModels() []configuredModel {
	models := make([]configuredModel, 0, len(config.GlobalConfig.Model.Models))
	for id, model := range config.GlobalConfig.Model.Models {
		if strings.TrimSpace(model.ModelID) == "" {
			continue
		}
		models = append(models, configuredModel{id: id, config: model})
	}
	for i := 1; i < len(models); i++ {
		for j := i; j > 0 && models[j].id < models[j-1].id; j-- {
			models[j], models[j-1] = models[j-1], models[j]
		}
	}
	return models
}

func findModel(name string, wantEmbedding bool) (configuredModel, bool) {
	for _, item := range visibleModels() {
		if item.config.ModelID != name && item.config.ModelName != name && strconv.FormatInt(int64(item.id), 10) != name {
			continue
		}
		if wantEmbedding != (item.config.Type == cfgstructs.ModelTypeEmbedding) {
			return configuredModel{}, false
		}
		if !wantEmbedding && item.config.Type != cfgstructs.ModelTypeLLM {
			return configuredModel{}, false
		}
		return item, true
	}
	return configuredModel{}, false
}

func providerConfig(model configuredModel) (string, string) {
	baseURL, key := model.config.ProviderURL, model.config.ProviderKey
	if baseURL == "" {
		baseURL = config.GlobalConfig.Model.ProviderURL
	}
	if key == "" {
		key = config.GlobalConfig.Model.ProviderKey
	}
	return baseURL, key
}

func (h *Handler) chatCompletions(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		h.error(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error", nil)
		return
	}
	var body reqstructs.ChatCompletionRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&body); err != nil {
		h.error(w, http.StatusBadRequest, "invalid request body: "+err.Error(), "invalid_request_error", nil)
		return
	}
	if strings.TrimSpace(body.Model) == "" || len(body.Messages) == 0 {
		h.error(w, http.StatusBadRequest, "model and messages are required", "invalid_request_error", nil)
		return
	}
	if body.N != nil && *body.N != 1 {
		h.error(w, http.StatusBadRequest, "only n=1 is supported", "invalid_request_error", "n")
		return
	}
	model, ok := findModel(body.Model, false)
	if !ok {
		h.error(w, http.StatusNotFound, "model not found or is not a chat model", "invalid_request_error", "model")
		return
	}
	baseURL, providerKey := providerConfig(model)
	if baseURL == "" {
		h.error(w, http.StatusBadGateway, "model provider URL is not configured", "server_error", nil)
		return
	}
	body.Model = model.config.ModelID
	if body.Stream {
		h.streamChat(w, r, body, model, baseURL, providerKey)
		return
	}
	h.completeChat(w, r, body, model, baseURL, providerKey)
}

type chatAccumulator struct {
	mu        sync.Mutex
	id        string
	created   int64
	model     string
	content   strings.Builder
	reasoning strings.Builder
	role      string
	finish    string
	toolCall  map[int]reqstructs.StreamToolCall
	usage     reqstructs.Usage
	hasUsage  bool
}

func (a *chatAccumulator) add(resp reqstructs.ChatCompletionResponse) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if resp.ID != "" {
		a.id = resp.ID
	}
	if resp.Created != 0 {
		a.created = resp.Created
	}
	if resp.Model != "" {
		a.model = resp.Model
	}
	for _, choice := range resp.Choices {
		if choice.Index != 0 {
			continue
		}
		// Some gateways ignore stream=true and return a regular response.
		message := choice.Delta
		if choice.Message.Role != "" || choice.Message.Content != "" ||
			choice.Message.ReasoningContent != nil || len(choice.Message.ToolCalls) > 0 {
			message = choice.Message
		}
		if message.Role != "" {
			a.role = message.Role
		}
		a.content.WriteString(message.Content)
		if message.ReasoningContent != nil {
			a.reasoning.WriteString(*message.ReasoningContent)
		}
		if choice.FinishReason != "" {
			a.finish = choice.FinishReason
		}
		if a.toolCall == nil {
			a.toolCall = make(map[int]reqstructs.StreamToolCall)
		}
		for _, call := range message.ToolCalls {
			current := a.toolCall[call.Index]
			if call.ID != "" {
				current.ID = call.ID
			}
			if call.Type != "" {
				current.Type = call.Type
			}
			if call.Function != nil {
				if current.Function == nil {
					current.Function = &reqstructs.StreamToolCallFunc{}
				}
				current.Function.Name += call.Function.Name
				current.Function.Arguments += call.Function.Arguments
			}
			a.toolCall[call.Index] = current
		}
	}
	if resp.Usage != nil {
		a.hasUsage = true
		a.usage.PromptTokens = max32(a.usage.PromptTokens, resp.Usage.PromptTokens)
		a.usage.CompletionTokens = max32(a.usage.CompletionTokens, resp.Usage.CompletionTokens)
		a.usage.TotalTokens = max32(a.usage.TotalTokens, resp.Usage.TotalTokens)
		if a.usage.TotalTokens == 0 {
			a.usage.TotalTokens = a.usage.PromptTokens + a.usage.CompletionTokens
		}
		a.usage.CachedTokens = max32(a.usage.CachedTokens, resp.Usage.CachedTokens)
		a.usage.DeepseekCachedToken = max32(a.usage.DeepseekCachedToken, resp.Usage.DeepseekCachedToken)
		if resp.Usage.PromptTokensDetails != nil {
			a.usage.PromptTokensDetails = resp.Usage.PromptTokensDetails
		}
		if resp.Usage.BillingUsage != nil {
			a.usage.BillingUsage = resp.Usage.BillingUsage
		}
	}
}

func max32(a, b uint32) uint32 {
	if a > b {
		return a
	}
	return b
}

func (h *Handler) streamChat(w http.ResponseWriter, r *http.Request, body reqstructs.ChatCompletionRequest, model configuredModel, baseURL, providerKey string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		h.error(w, http.StatusInternalServerError, "streaming unsupported", "server_error", nil)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	includeUsage := body.StreamOptions != nil && body.StreamOptions.IncludeUsage
	var acc chatAccumulator
	err := request.SimpleOpenAIRequest(r.Context(), baseURL, providerKey, model.config.ModelID, body, nil, func(resp reqstructs.ChatCompletionResponse) error {
		acc.add(resp)
		if !includeUsage {
			resp.Usage = nil
		}
		// Normalize regular upstream responses into streaming chunks.
		for i := range resp.Choices {
			choice := &resp.Choices[i]
			if choice.Index != 0 {
				continue
			}
			if choice.Message.Role != "" || choice.Message.Content != "" ||
				choice.Message.ReasoningContent != nil || len(choice.Message.ToolCalls) > 0 {
				choice.Delta = choice.Message
			}
			choice.Message = reqstructs.Message{}
		}
		if resp.Object == "" || resp.Object == "chat.completion" {
			resp.Object = "chat.completion.chunk"
		}
		if resp.Model == "" {
			resp.Model = model.config.ModelID
		}
		data, err := json.Marshal(resp)
		if err != nil {
			return err
		}
		if _, err = fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	})
	if err != nil {
		if r.Context().Err() == nil {
			// 流式响应已经开始后无法再改写 HTTP 状态码，只能发送协议内错误事件。
			streamErr := reqstructs.ErrorResponse{Error: reqstructs.APIError{
				Message: err.Error(),
				Type:    "upstream_error",
			}}
			if data, marshalErr := json.Marshal(streamErr); marshalErr == nil {
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
		}
		return
	}
	h.recordUsage(model, &acc)
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
}

type nonStreamingChoice struct {
	Index        int                `json:"index"`
	Message      reqstructs.Message `json:"message"`
	FinishReason string             `json:"finish_reason"`
}

type nonStreamingResponse struct {
	ID      string               `json:"id"`
	Object  string               `json:"object"`
	Created int64                `json:"created"`
	Model   string               `json:"model"`
	Choices []nonStreamingChoice `json:"choices"`
	Usage   *reqstructs.Usage    `json:"usage,omitempty"`
}

func (h *Handler) completeChat(w http.ResponseWriter, r *http.Request, body reqstructs.ChatCompletionRequest, model configuredModel, baseURL, providerKey string) {
	var acc chatAccumulator
	err := request.SimpleOpenAIRequest(r.Context(), baseURL, providerKey, model.config.ModelID, body, nil, func(resp reqstructs.ChatCompletionResponse) error {
		acc.add(resp)
		return nil
	})
	if err != nil {
		h.error(w, http.StatusBadGateway, err.Error(), "upstream_error", nil)
		return
	}
	h.recordUsage(model, &acc)
	acc.mu.Lock()
	message := reqstructs.Message{Role: acc.role, Content: acc.content.String()}
	if message.Role == "" {
		message.Role = reqstructs.RoleAssistant
	}
	if reasoning := acc.reasoning.String(); reasoning != "" {
		message.ReasoningContent = &reasoning
	}
	indices := make([]int, 0, len(acc.toolCall))
	for index := range acc.toolCall {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	for _, index := range indices {
		message.ToolCalls = append(message.ToolCalls, acc.toolCall[index])
	}
	resp := nonStreamingResponse{ID: acc.id, Object: "chat.completion", Created: acc.created, Model: model.config.ModelID, Choices: []nonStreamingChoice{{Index: 0, Message: message, FinishReason: acc.finish}}}
	if resp.ID == "" {
		resp.ID = "chatcmpl-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	if acc.hasUsage {
		usage := acc.usage
		resp.Usage = &usage
	}
	acc.mu.Unlock()
	h.json(w, http.StatusOK, resp)
}

func (h *Handler) recordUsage(model configuredModel, acc *chatAccumulator) {
	acc.mu.Lock()
	defer acc.mu.Unlock()
	if !acc.hasUsage {
		return
	}
	cached := max32(acc.usage.CachedTokens, acc.usage.DeepseekCachedToken)
	if acc.usage.PromptTokensDetails != nil {
		cached = max32(cached, acc.usage.PromptTokensDetails.CachedTokens)
	}
	if acc.usage.BillingUsage != nil && acc.usage.BillingUsage.ClaudeUsage != nil {
		cached = max32(cached, acc.usage.BillingUsage.ClaudeUsage.CacheReadInputTokens)
	}
	stats.AddUsage(uint32(model.id), model.config.ModelName, acc.usage.PromptTokens, acc.usage.CompletionTokens, cached)
}

func (h *Handler) embeddings(w http.ResponseWriter, r *http.Request) {
	if !h.authorize(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		h.error(w, http.StatusMethodNotAllowed, "method not allowed", "invalid_request_error", nil)
		return
	}
	var body reqstructs.EmbeddingRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&body); err != nil {
		h.error(w, http.StatusBadRequest, "invalid request body: "+err.Error(), "invalid_request_error", nil)
		return
	}
	if strings.TrimSpace(body.Model) == "" || len(body.Input) == 0 && len(body.InputRaw) == 0 {
		h.error(w, http.StatusBadRequest, "model and input are required", "invalid_request_error", nil)
		return
	}
	model, ok := findModel(body.Model, true)
	if !ok {
		h.error(w, http.StatusNotFound, "model not found or is not an embedding model", "invalid_request_error", "model")
		return
	}
	baseURL, providerKey := providerConfig(model)
	if baseURL == "" {
		h.error(w, http.StatusBadGateway, "model provider URL is not configured", "server_error", nil)
		return
	}
	body.Model = model.config.ModelID
	resp, err := request.SimpleOpenAIEmbeddingResponse(r.Context(), baseURL, providerKey, model.config.ModelID, body)
	if err != nil {
		h.error(w, http.StatusBadGateway, err.Error(), "upstream_error", nil)
		return
	}
	if resp.Model == "" {
		resp.Model = model.config.ModelID
	}
	h.json(w, http.StatusOK, resp)
}

func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) bool {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || !ValidateAPIKey(strings.TrimSpace(parts[1])) {
		h.error(w, http.StatusUnauthorized, "invalid API key", "authentication_error", nil)
		return false
	}
	return true
}

func (h *Handler) json(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Server", product.UserAgent)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (h *Handler) error(w http.ResponseWriter, status int, message, typ string, param any) {
	h.json(w, status, reqstructs.ErrorResponse{Error: reqstructs.APIError{Message: message, Type: typ, Param: param}})
}
