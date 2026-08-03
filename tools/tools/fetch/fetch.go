package fetch

import (
	"bufio"
	"context"
	"crypto/x509"
	_ "embed"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	tls "github.com/refraction-networking/utls"
	"golang.org/x/net/proxy"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/log"
	"github.com/cxykevin/alkaid0/product"
	"github.com/cxykevin/alkaid0/provider/parser"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/actions"
	"github.com/cxykevin/alkaid0/tools/index"
	"github.com/cxykevin/alkaid0/tools/toolobj"
	"github.com/cxykevin/alkaid0/tools/tools/trace"
	u "github.com/cxykevin/alkaid0/utils"
)

const toolName = "fetch"

//go:embed prompt.md
var prompt string

// maxBodyBytes 原始响应体截断上限，防止回灌 LLM 的上下文爆炸
const maxBodyBytes = 64 * 1024 // 64KB

// maxTimeout 超时上限（秒）
const maxTimeout = 30

// clampTimeout 将超时截断到 [1, maxTimeout]，0/负值回退到 maxTimeout。
func clampTimeout(t int) int {
	if t <= 0 {
		return maxTimeout
	}
	if t > maxTimeout {
		return maxTimeout
	}
	return t
}

// chromeUA hidden 模式使用的 Chrome UA
const chromeUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/133.0.0.0 Safari/537.36"

var logger = log.New("tools:fetch")

var paras = map[string]parser.ToolParameters{
	"method": {
		Type:        parser.ToolTypeString,
		Required:    true,
		Description: "HTTP method (GET/POST/PUT/DELETE/PATCH/HEAD), case-insensitive, e.g. \"GET\"",
	},
	"url": {
		Type:        parser.ToolTypeString,
		Required:    true,
		Description: "The URL to request, e.g. https://example.com/api",
	},
	"headers": {
		Type:        parser.ToolTypeString,
		Required:    false,
		Description: "Request headers, one \"Key: Value\" per line. Example: \"Content-Type: application/json\\nAuthorization: Bearer xxx\"",
	},
	"body": {
		Type:        parser.ToolTypeString,
		Required:    false,
		Description: "Request body (raw string). For POST/PUT usually.",
	},
	"hidden": {
		Type:        parser.ToolTypeBoolean,
		Required:    false,
		Description: "If true, disguise the request as a real browser: utls Chrome TLS fingerprint + Chrome-style headers (User-Agent, Sec-Fetch-*, etc). Default false (uses built-in UA).",
	},
	"summary": {
		Type:        parser.ToolTypeString,
		Required:    false,
		Description: "If non-empty, the fetched content is summarized via LLM into markdown using this text as the summary instruction. Recommended when fetching web pages (not debug APIs).",
	},
	"timeout": {
		Type:        parser.ToolTypeNumber,
		Required:    false,
		Description: "Timeout in seconds. Values above 30 are truncated to 30. Default 30.",
	},
}

func load() string {
	actions.AddTool(&toolobj.Tools{
		Scope:           "",
		Name:            toolName,
		UserDescription: prompt,
		Parameters:      paras,
		ID:              toolName,
	})
	if err := actions.HookTool(toolName, &toolobj.Hook{
		Scope: "",
		PreHook: toolobj.PreHookFunction{
			Priority: 100,
			Func:     nil,
		},
		OnHook: toolobj.OnHookFunction{
			Priority: 100,
			Func:     updateInfo,
		},
		PostHook: toolobj.PostHookFunction{
			Priority: 100,
			Func:     fetchURL,
		},
	}); err != nil {
		panic(err)
	}
	return toolName
}

func init() {
	index.AddIndex(load)
}

// updateInfo 在 UI 上展示请求预览（仿 search 工具）。
func updateInfo(session *structs.Chats, mp map[string]*any, cross []*any, toolID string) (bool, []*any, error) {
	toolCallID := fmt.Sprintf("call_%d_%d_%s", session.ID, session.CurrentMessageID, toolID)
	respString := ""
	if p, ok := mp["method"]; ok && p != nil {
		if s, ok := (*p).(string); ok {
			respString += "Method: " + s + "\n"
		}
	}
	if p, ok := mp["url"]; ok && p != nil {
		if s, ok := (*p).(string); ok {
			respString += "URL: " + s + "\n"
		}
	}
	if p, ok := mp["hidden"]; ok && p != nil {
		if b, ok := (*p).(bool); ok {
			respString += "Hidden: " + u.Ternary(b, "true", "false") + "\n"
		}
	}
	if p, ok := mp["summary"]; ok && p != nil {
		if s, ok := (*p).(string); ok && s != "" {
			respString += "Summary: " + s + "\n"
		}
	}
	respObj := []u.H{{
		"type":    "content",
		"content": u.H{"type": "text", "text": respString},
	}, {
		"type":      "alk.cxykevin.top/calling_info",
		"name":      toolName,
		"messageID": session.CurrentMessageID,
		"args": u.H{
			"method":  mp["method"],
			"url":     mp["url"],
			"hidden":  mp["hidden"],
			"summary": mp["summary"],
		},
	}}
	session.SetToolCalling(toolCallID, respObj, toolName)
	return true, cross, nil
}

// fetchURL 执行 HTTP 请求，响应写入 temp 对象并返回 @temp/fetch/... 路径。
func fetchURL(session *structs.Chats, mp map[string]*any, cross []*any) (bool, []*any, map[string]*any, error) {
	method, err := getStringParam(mp, "method")
	if err != nil {
		return errResult(err.Error(), cross)
	}
	urlStr, err := getStringParam(mp, "url")
	if err != nil {
		return errResult(err.Error(), cross)
	}
	hidden, _ := getBoolParamDefault(mp, "hidden", false)
	summary, _ := getStringParamDefault(mp, "summary", "")
	headerRaw, _ := getStringParamDefault(mp, "headers", "")
	body, _ := getStringParamDefault(mp, "body", "")
	timeout, _ := getIntParamDefault(mp, "timeout", maxTimeout)
	timeout = clampTimeout(timeout)

	// 上下文：优先继承会话上下文，再叠加超时
	ctx := context.Background()
	if session != nil {
		if sc := session.GetContext(); sc != nil {
			ctx = sc
		}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	// 构造请求
	var reqBody io.Reader
	if body != "" {
		reqBody = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), urlStr, reqBody)
	if err != nil {
		return errResult(fmt.Sprintf("invalid request: %v", err), cross)
	}

	// 用户自定义 headers（多行 "Key: Value"），优先于默认 headers
	if err := parseHeaders(req, headerRaw); err != nil {
		return errResult(err.Error(), cross)
	}

	// 默认 UA 与浏览器风格 headers
	if req.Header.Get("User-Agent") == "" {
		if hidden {
			req.Header.Set("User-Agent", chromeUA)
		} else {
			req.Header.Set("User-Agent", product.UserAgent)
		}
	}
	if hidden {
		setBrowserHeaders(req) // 仅在未设置时填充
	}

	client := &http.Client{
		Transport: buildTransport(hidden),
		Timeout:   time.Duration(timeout) * time.Second,
	}
	resp, err := client.Do(req)
	if err != nil {
		return errResult(fmt.Sprintf("request failed: %v", err), cross)
	}
	defer resp.Body.Close()

	// 读 body，超出 maxBodyBytes 截断并标记
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return errResult(fmt.Sprintf("read body: %v", err), cross)
	}
	truncated := len(raw) > maxBodyBytes
	if truncated {
		raw = raw[:maxBodyBytes]
	}
	content := string(raw)

	// LLM 总结（summary 非空且配置了总结模型时）
	if summary != "" && summarizeFn != nil {
		modelID := resolveSummaryModel()
		if modelID == 0 {
			logger.Warn("no SearchSummaryModel configured, skipping summary for %s", urlStr)
		} else {
			s, err := summarizeFn(context.Background(), content, summary, modelID)
			if err != nil {
				logger.Error("fetch summary failed: %v, falling back to raw content", err)
			} else if s != "" {
				content = s
			}
		}
	}

	// 读 toolID（ExecToolPostHook 注入）
	toolID := "unknown"
	if idAny, ok := mp["_id"]; ok && idAny != nil {
		if v, ok := (*idAny).(string); ok && v != "" {
			toolID = v
		}
	}

	// 构造 temp 内容：头部拼上请求内容，避免 AI 认不出
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[fetch] %s %s\n", strings.ToUpper(method), urlStr))
	sb.WriteString(fmt.Sprintf("Status: %d\n", resp.StatusCode))
	if headerRaw != "" {
		sb.WriteString("Request headers:\n" + headerRaw + "\n")
	}
	if body != "" {
		sb.WriteString("Request body:\n" + body + "\n")
	}
	sb.WriteString("\n" + content)
	outStr := sb.String()

	// 写入 temp 对象（仿 run 工具）
	path := "fetch/" + toolID + "-" + time.Now().Format("20060102-150405")
	if session == nil {
		return errResult("fetch: session is nil", cross)
	}
	if err := trace.AddTempObject(session, path, outStr, true); err != nil {
		return errResult(fmt.Sprintf("failed to save fetch result: %v", err), cross)
	}
	outPth := "@temp/" + path
	logger.Info("fetch %s %s done (status=%d), output saved to: %s", strings.ToUpper(method), urlStr, resp.StatusCode, outPth)

	successAny := any(true)
	outAny := any(outPth)
	result := map[string]*any{
		"success":     &successAny,
		"path":        &outAny,
		"status_code": intAny(resp.StatusCode),
	}
	if truncated {
		tAny := any(true)
		result["truncated"] = &tAny
	}
	return false, cross, result, nil
}

func intAny(v int) *any {
	a := any(v)
	return &a
}

// parseHeaders 解析多行 "Key: Value" 请求头。
func parseHeaders(req *http.Request, raw string) error {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(k) == "" {
			return fmt.Errorf("invalid header line %q, expected \"Key: Value\"", line)
		}
		req.Header.Set(strings.TrimSpace(k), strings.TrimSpace(v))
	}
	return nil
}

// buildTransport 构建 HTTP 传输层。hidden=false 用标准库（代理交给 Go 原生处理）；
// hidden=true 用 utls 伪装，代理在 DialTLSContext 内手动建隧道以保留指纹。
func buildTransport(hidden bool) *http.Transport {
	if !hidden {
		return &http.Transport{
			Proxy:                 proxyFromConfig(),
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
		}
	}
	return buildHiddenTransport()
}

// proxyFromConfig 读取全局 Agent.FetchProxy；为空时返回 nil（直连，不使用系统环境代理）。
func proxyFromConfig() func(*http.Request) (*url.URL, error) {
	p := config.GlobalConfig.Agent.FetchProxy
	if strings.TrimSpace(p) == "" {
		return nil
	}
	proxyURL, err := url.Parse(strings.TrimSpace(p))
	if err != nil {
		logger.Warn("invalid FetchProxy %q: %v, ignoring", p, err)
		return nil
	}
	return http.ProxyURL(proxyURL)
}

// fetchProxyURL 解析全局 FetchProxy 为 *url.URL（hidden 模式手动隧道用），失败返回 nil。
func fetchProxyURL() *url.URL {
	p := config.GlobalConfig.Agent.FetchProxy
	if strings.TrimSpace(p) == "" {
		return nil
	}
	proxyURL, err := url.Parse(strings.TrimSpace(p))
	if err != nil {
		logger.Warn("invalid FetchProxy %q: %v, ignoring", p, err)
		return nil
	}
	return proxyURL
}

// buildHiddenTransport 构建带 utls Chrome 指纹的传输层。
// 不设置 transport.Proxy：配置了 FetchProxy 时在 DialTLSContext 内手动建立
// CONNECT/socks5 隧道再包裹 utls，避免 Go 原生 Proxy 在 https+代理下跳过
// 自定义 TLS dialer 导致指纹失效。
func buildHiddenTransport() *http.Transport {
	dialer := &net.Dialer{
		Timeout:   maxTimeout * time.Second,
		KeepAlive: 30 * time.Second,
	}
	spec := buildFingerprintSpec()
	proxyURL := fetchProxyURL()

	transport := &http.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			var conn net.Conn
			var err error
			if proxyURL == nil {
				conn, err = dialer.DialContext(ctx, network, addr)
			} else {
				conn, err = dialViaProxy(ctx, dialer, proxyURL, addr)
			}
			if err != nil {
				return nil, err
			}

			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				conn.Close()
				return nil, err
			}

			tlsCfg := &tls.Config{ServerName: host}
			if tlsRootCAs != nil {
				tlsCfg.RootCAs = tlsRootCAs
			}
			uconn := tls.UClient(conn, tlsCfg, tls.HelloCustom)
			if err := uconn.ApplyPreset(&spec); err != nil {
				conn.Close()
				return nil, fmt.Errorf("utls apply preset: %w", err)
			}
			if err := uconn.Handshake(); err != nil {
				conn.Close()
				return nil, fmt.Errorf("utls handshake: %w", err)
			}
			return uconn, nil
		},
		ForceAttemptHTTP2:   false, // 已 strip ALPN，强制 HTTP/1.1
		MaxIdleConns:        5,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: maxTimeout * time.Second,
	}
	return transport
}

// dialViaProxy 手动建立代理隧道（HTTP CONNECT / SOCKS5），返回未加密的隧道连接，
// 交 DialTLSContext 用 utls 包裹。
func dialViaProxy(ctx context.Context, dialer *net.Dialer, proxyURL *url.URL, targetAddr string) (net.Conn, error) {
	switch proxyURL.Scheme {
	case "http", "https":
		conn, err := dialer.DialContext(ctx, "tcp", proxyURL.Host)
		if err != nil {
			return nil, err
		}
		if dl, ok := ctx.Deadline(); ok {
			conn.SetDeadline(dl)
		}
		if err := sendCONNECT(conn, proxyURL, targetAddr); err != nil {
			conn.Close()
			return nil, err
		}
		conn.SetDeadline(time.Time{})
		return conn, nil
	case "socks5", "socks5h":
		var auth *proxy.Auth
		if proxyURL.User != nil {
			pwd, _ := proxyURL.User.Password()
			auth = &proxy.Auth{
				User:     proxyURL.User.Username(),
				Password: pwd,
			}
		}
		d, err := proxy.SOCKS5("tcp", proxyURL.Host, auth, dialer)
		if err != nil {
			return nil, err
		}
		cd, ok := d.(proxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("socks5 dialer does not support context")
		}
		return cd.DialContext(ctx, "tcp", targetAddr)
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q", proxyURL.Scheme)
	}
}

// sendCONNECT 向 HTTP 代理发送 CONNECT 隧道请求并校验 200。
func sendCONNECT(conn net.Conn, proxyURL *url.URL, targetAddr string) error {
	var sb strings.Builder
	sb.WriteString("CONNECT " + targetAddr + " HTTP/1.1\r\n")
	sb.WriteString("Host: " + targetAddr + "\r\n")
	if proxyURL.User != nil {
		pwd, _ := proxyURL.User.Password()
		cred := base64Credential(proxyURL.User.Username(), pwd)
		sb.WriteString("Proxy-Authorization: Basic " + cred + "\r\n")
	}
	sb.WriteString("\r\n")

	if _, err := io.WriteString(conn, sb.String()); err != nil {
		return fmt.Errorf("proxy CONNECT write: %w", err)
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "CONNECT"})
	if err != nil {
		return fmt.Errorf("proxy CONNECT read: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("proxy CONNECT failed: %s", resp.Status)
	}
	return nil
}

// tlsRootCAs 测试钩子：生产代码保持 nil。测试注入 httptest 自签证书的根证书池，
// 使 utls 握手能通过证书验证。
var tlsRootCAs *x509.CertPool

// buildFingerprintSpec 构建预生成的 Chrome TLS 指纹规格（去掉 ALPN）。
func buildFingerprintSpec() tls.ClientHelloSpec {
	spec, err := tls.UTLSIdToSpec(tls.HelloChrome_133)
	if err != nil {
		spec, _ = tls.UTLSIdToSpec(tls.HelloChrome_120) // 兜底
	}
	return stripALPN(spec)
}

// stripALPN 移除 ALPN 扩展，确保 TLS 握手后不协商 h2，强制 HTTP/1.1
// （Go 的 net/http 与 utls 在 HTTP/2 下存在兼容问题）。
func stripALPN(spec tls.ClientHelloSpec) tls.ClientHelloSpec {
	var exts []tls.TLSExtension
	for _, ext := range spec.Extensions {
		if _, ok := ext.(*tls.ALPNExtension); !ok {
			exts = append(exts, ext)
		}
	}
	spec.Extensions = exts
	return spec
}

// setBrowserHeaders 设置浏览器风格 headers，仅填充尚未设置的键（用户自定义优先）。
func setBrowserHeaders(req *http.Request) {
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	}
	if req.Header.Get("Accept-Language") == "" {
		req.Header.Set("Accept-Language", "en-US,en;q=0.9,zh-CN,zh;q=0.8")
	}
	if req.Header.Get("Sec-Fetch-Dest") == "" {
		req.Header.Set("Sec-Fetch-Dest", "document")
	}
	if req.Header.Get("Sec-Fetch-Mode") == "" {
		req.Header.Set("Sec-Fetch-Mode", "navigate")
	}
	if req.Header.Get("Sec-Fetch-Site") == "" {
		req.Header.Set("Sec-Fetch-Site", "none")
	}
	if req.Header.Get("Sec-Fetch-User") == "" {
		req.Header.Set("Sec-Fetch-User", "?1")
	}
	if req.Header.Get("DNT") == "" {
		req.Header.Set("DNT", "1")
	}
	if req.Header.Get("Upgrade-Insecure-Requests") == "" {
		req.Header.Set("Upgrade-Insecure-Requests", "1")
	}
}

// ---------------------------------------------------------------------------
// LLM 总结 — 函数指针注入（仿 search 工具，避免循环导入）
// ---------------------------------------------------------------------------

// SummarizeFn 抓取内容总结函数类型，由 SetSummarizeFn 注入。
type SummarizeFn func(ctx context.Context, rawContent, summaryPrompt string, modelID int32) (string, error)

// summarizeFn 函数指针，由 SetSummarizeFn 在启动时注入。
var summarizeFn SummarizeFn

// SetSummarizeFn 设置抓取内容总结函数（在 ui/startup 中调用）。
func SetSummarizeFn(fn SummarizeFn) {
	summarizeFn = fn
}

// resolveSummaryModel 直接共用全局 SearchSummaryModel 配置（用户要求，不做回退链）。
// 返回 0 表示未配置，调用方应降级返回原始内容。
func resolveSummaryModel() int32 {
	return config.GlobalConfig.Context.SearchSummaryModel
}

// ---------------------------------------------------------------------------
// 参数提取工具（与 search 工具同款）
// ---------------------------------------------------------------------------

func getStringParam(mp map[string]*any, key string) (string, error) {
	p, ok := mp[key]
	if !ok || p == nil {
		return "", fmt.Errorf("missing required parameter: %s", key)
	}
	v, ok := (*p).(string)
	if !ok {
		return "", fmt.Errorf("parameter %s must be a string", key)
	}
	if v == "" {
		return "", fmt.Errorf("parameter %s cannot be empty", key)
	}
	return v, nil
}

func getStringParamDefault(mp map[string]*any, key, def string) (string, error) {
	p, ok := mp[key]
	if !ok || p == nil {
		return def, nil
	}
	v, ok := (*p).(string)
	if !ok {
		return def, nil
	}
	return v, nil
}

func getBoolParamDefault(mp map[string]*any, key string, def bool) (bool, error) {
	p, ok := mp[key]
	if !ok || p == nil {
		return def, nil
	}
	v, ok := (*p).(bool)
	if !ok {
		return def, nil
	}
	return v, nil
}

func getIntParamDefault(mp map[string]*any, key string, def int) (int, error) {
	p, ok := mp[key]
	if !ok || p == nil {
		return def, nil
	}
	switch v := (*p).(type) {
	case float64:
		return int(v), nil
	case int:
		return v, nil
	case int64:
		return int(v), nil
	default:
		return def, nil
	}
}

func errResult(msg string, cross []*any) (bool, []*any, map[string]*any, error) {
	f := false
	s := any(f)
	e := any(msg)
	return false, cross, map[string]*any{"success": &s, "error": &e}, nil
}

// base64Credential 计算 Basic 认证凭据。
func base64Credential(user, pwd string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pwd))
}
