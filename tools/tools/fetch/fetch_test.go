package fetch

import (
	"context"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cxykevin/alkaid0/config"
	"github.com/cxykevin/alkaid0/product"
	"github.com/cxykevin/alkaid0/storage"
	"github.com/cxykevin/alkaid0/storage/structs"
)

func strPtr(s string) *any {
	a := any(s)
	return &a
}

func boolPtr(b bool) *any {
	a := any(b)
	return &a
}

func intPtr(i int) *any {
	a := any(i)
	return &a
}

func newTestSession(t *testing.T) *structs.Chats {
	t.Helper()
	db, err := storage.InitDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		sqlDB.Close()
	})
	chat := &structs.Chats{}
	if err := db.Create(chat).Error; err != nil {
		t.Fatal(err)
	}
	return &structs.Chats{
		ID: chat.ID,
		DB: db,
	}
}

// fetchTempContent 查询 session 的 temp 文件内容（Path 以 @temp/fetch/ 开头）。
func fetchTempContent(t *testing.T, session *structs.Chats) string {
	t.Helper()
	var refs []structs.ReferFiles
	if err := session.DB.Where("chat_id = ?", session.ID).Find(&refs).Error; err != nil {
		t.Fatal(err)
	}
	for i := range refs {
		// AddTempObject 的 ReferFiles.Path 不带 @temp/ 前缀（只有 Traces.Path 带）
		if strings.HasPrefix(refs[i].Path, "fetch/") {
			return refs[i].Content
		}
	}
	t.Fatalf("no @temp/fetch/ refer file found, got %d refs", len(refs))
	return ""
}

// assertSuccessPath 断言 fetchURL 返回 success=true 且 path 以 @temp/fetch/ 开头。
func assertSuccessPath(t *testing.T, pass bool, res map[string]*any) {
	t.Helper()
	if pass {
		t.Fatalf("expected pass=false (result final)")
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	sp, ok := res["success"]
	if !ok || sp == nil {
		t.Fatal("missing success")
	}
	if v, ok := (*sp).(bool); !ok || !v {
		t.Fatalf("expected success=true, got %v", *sp)
	}
	pp, ok := res["path"]
	if !ok || pp == nil {
		t.Fatal("missing path")
	}
	path, ok := (*pp).(string)
	if !ok || !strings.HasPrefix(path, "@temp/fetch/") {
		t.Fatalf("expected path to start with @temp/fetch/, got %v", *pp)
	}
}

func TestClampTimeout(t *testing.T) {
	cases := []struct {
		in   int
		want int
	}{
		{0, maxTimeout},
		{-5, maxTimeout},
		{1, 1},
		{30, 30},
		{31, maxTimeout},
		{999, maxTimeout},
	}
	for _, c := range cases {
		if got := clampTimeout(c.in); got != c.want {
			t.Errorf("clampTimeout(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestFetchGET(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html><body>hello world</body></html>"))
	}))
	defer srv.Close()

	session := newTestSession(t)
	mp := map[string]*any{
		"method": strPtr("get"), // 大小写不敏感
		"url":    strPtr(srv.URL),
		"_id":    strPtr("t1"),
	}
	pass, _, res, _ := fetchURL(session, mp, []*any{})
	assertSuccessPath(t, pass, res)

	content := fetchTempContent(t, session)
	if !strings.Contains(content, "[fetch] GET "+srv.URL) {
		t.Errorf("temp head should contain request, got:\n%s", content)
	}
	if !strings.Contains(content, "Status: 200") {
		t.Errorf("temp head should contain status, got:\n%s", content)
	}
	if !strings.Contains(content, "hello world") {
		t.Errorf("temp body should contain response, got:\n%s", content)
	}
}

func TestFetchPOSTBodyAndHeaders(t *testing.T) {
	var gotMethod, gotBody, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Write([]byte("posted"))
	}))
	defer srv.Close()

	session := newTestSession(t)
	mp := map[string]*any{
		"method":  strPtr("POST"),
		"url":     strPtr(srv.URL),
		"body":    strPtr(`{"name":"test"}`),
		"headers": strPtr("Content-Type: application/json"),
		"_id":     strPtr("t2"),
	}
	pass, _, res, _ := fetchURL(session, mp, []*any{})
	assertSuccessPath(t, pass, res)

	if gotMethod != "POST" {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	if gotCT != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", gotCT)
	}
	if gotBody != `{"name":"test"}` {
		t.Errorf("expected body %q, got %q", `{"name":"test"}`, gotBody)
	}
}

func TestFetchHiddenUA(t *testing.T) {
	var ua, acceptLang, dnt, secFetchDest, upgrade string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		acceptLang = r.Header.Get("Accept-Language")
		dnt = r.Header.Get("DNT")
		secFetchDest = r.Header.Get("Sec-Fetch-Dest")
		upgrade = r.Header.Get("Upgrade-Insecure-Requests")
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	session := newTestSession(t)
	mp := map[string]*any{
		"method": strPtr("GET"),
		"url":    strPtr(srv.URL),
		"hidden": boolPtr(true),
		"_id":    strPtr("t3"),
	}
	pass, _, res, _ := fetchURL(session, mp, []*any{})
	assertSuccessPath(t, pass, res)

	if !strings.HasPrefix(ua, "Mozilla/5.0") || !strings.Contains(ua, "Chrome/133") {
		t.Errorf("hidden UA should be Chrome-like, got %q", ua)
	}
	if acceptLang == "" {
		t.Error("hidden should set Accept-Language")
	}
	if dnt != "1" {
		t.Errorf("hidden should set DNT=1, got %q", dnt)
	}
	if secFetchDest != "document" {
		t.Errorf("hidden should set Sec-Fetch-Dest=document, got %q", secFetchDest)
	}
	if upgrade != "1" {
		t.Errorf("hidden should set Upgrade-Insecure-Requests=1, got %q", upgrade)
	}
}

func TestFetchNonHiddenUA(t *testing.T) {
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	session := newTestSession(t)
	mp := map[string]*any{
		"method": strPtr("GET"),
		"url":    strPtr(srv.URL),
		"_id":    strPtr("t4"),
	}
	pass, _, res, _ := fetchURL(session, mp, []*any{})
	assertSuccessPath(t, pass, res)

	if ua != product.UserAgent {
		t.Errorf("non-hidden UA should be product.UserAgent %q, got %q", product.UserAgent, ua)
	}
}

func TestFetchHeadersOverride(t *testing.T) {
	var ua, ct string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		ct = r.Header.Get("Content-Type")
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	session := newTestSession(t)
	mp := map[string]*any{
		"method":  strPtr("POST"),
		"url":     strPtr(srv.URL),
		"hidden":  boolPtr(true),
		"headers": strPtr("User-Agent: CustomUA/1.0\nContent-Type: text/plain"),
		"_id":     strPtr("t5"),
	}
	pass, _, res, _ := fetchURL(session, mp, []*any{})
	assertSuccessPath(t, pass, res)

	if ua != "CustomUA/1.0" {
		t.Errorf("user UA should override default, got %q", ua)
	}
	if ct != "text/plain" {
		t.Errorf("user Content-Type should be set, got %q", ct)
	}
}

func TestFetchTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // 等待客户端断开（超时触发），避免 Close() 阻塞
		w.Write([]byte("slow"))
	}))
	defer srv.Close()

	session := newTestSession(t)
	mp := map[string]*any{
		"method":  strPtr("GET"),
		"url":     strPtr(srv.URL),
		"timeout": intPtr(1),
		"_id":     strPtr("t6"),
	}
	start := time.Now()
	pass, _, res, _ := fetchURL(session, mp, []*any{})
	elapsed := time.Since(start)

	if pass {
		t.Error("expected pass=false on timeout")
	}
	if elapsed > 3*time.Second {
		t.Errorf("timeout too slow: %v", elapsed)
	}
	if res == nil || res["error"] == nil {
		t.Fatal("expected error on timeout")
	}
	if sp := res["success"]; sp != nil {
		if v, ok := (*sp).(bool); ok && v {
			t.Error("expected success=false on timeout")
		}
	}
}

func TestFetchTruncate(t *testing.T) {
	big := strings.Repeat("A", 100*1024) // 100KB 单行
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(big))
	}))
	defer srv.Close()

	session := newTestSession(t)
	mp := map[string]*any{
		"method": strPtr("GET"),
		"url":    strPtr(srv.URL),
		"_id":    strPtr("t7"),
	}
	pass, _, res, _ := fetchURL(session, mp, []*any{})
	assertSuccessPath(t, pass, res)

	tr, ok := res["truncated"]
	if !ok {
		t.Fatal("expected truncated flag for oversized body")
	}
	if v, ok := (*tr).(bool); !ok || !v {
		t.Errorf("expected truncated=true, got %v", *tr)
	}

	content := fetchTempContent(t, session)
	if len(content) > maxBodyBytes+2048 {
		t.Errorf("temp content should be bounded, got %d bytes", len(content))
	}
	if !strings.Contains(content, big[:64]) {
		t.Error("temp content should contain the head of the body")
	}
}

func TestFetchSummarize(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html><body>raw page content</body></html>"))
	}))
	defer srv.Close()

	// 配置总结模型 + 注入 mock summarizeFn
	oldModel := config.GlobalConfig.Context.SearchSummaryModel
	config.GlobalConfig.Context.SearchSummaryModel = 42
	defer func() { config.GlobalConfig.Context.SearchSummaryModel = oldModel }()

	var gotPrompt, gotRaw string
	var gotModel int32
	oldFn := summarizeFn
	summarizeFn = func(_ context.Context, rawContent, summaryPrompt string, modelID int32) (string, error) {
		gotPrompt = summaryPrompt
		gotRaw = rawContent
		gotModel = modelID
		return "## Summary\n" + rawContent, nil
	}
	defer func() { summarizeFn = oldFn }()

	session := newTestSession(t)
	mp := map[string]*any{
		"method":  strPtr("GET"),
		"url":     strPtr(srv.URL),
		"summary": strPtr("总结这个页面"),
		"_id":     strPtr("t8"),
	}
	pass, _, res, _ := fetchURL(session, mp, []*any{})
	assertSuccessPath(t, pass, res)

	if gotPrompt != "总结这个页面" {
		t.Errorf("summaryPrompt should pass through, got %q", gotPrompt)
	}
	if gotModel != 42 {
		t.Errorf("summary model should reuse SearchSummaryModel 42, got %d", gotModel)
	}
	if !strings.Contains(gotRaw, "raw page content") {
		t.Error("raw content should be passed to summarize")
	}
	content := fetchTempContent(t, session)
	if !strings.Contains(content, "## Summary") {
		t.Errorf("temp content should contain summary, got:\n%s", content)
	}
}

func TestFetchInvalidHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	session := newTestSession(t)
	mp := map[string]*any{
		"method":  strPtr("GET"),
		"url":     strPtr(srv.URL),
		"headers": strPtr("badline-no-colon"),
		"_id":     strPtr("t9"),
	}
	pass, _, res, _ := fetchURL(session, mp, []*any{})
	if pass {
		t.Error("expected pass=false (result final)")
	}
	if res == nil || res["error"] == nil {
		t.Fatal("expected error for invalid header")
	}
	if msg, ok := (*res["error"]).(string); !ok || !strings.Contains(msg, "invalid header") {
		t.Errorf("expected error to mention invalid header, got %v", *res["error"])
	}
}

func TestFetchMissingParams(t *testing.T) {
	session := newTestSession(t)
	pass, _, res, _ := fetchURL(session, map[string]*any{}, []*any{})
	if pass {
		t.Error("expected pass=false (result final)")
	}
	if res == nil || res["error"] == nil {
		t.Fatal("expected error for missing params")
	}
	if msg, ok := (*res["error"]).(string); !ok || !strings.Contains(msg, "missing required parameter: method") {
		t.Errorf("expected missing method error, got %v", *res["error"])
	}
}

// TestFetchRewriteHeaders 验证 Agent.Fetch.RewriteHeaders 按 URL 正则注入请求头。
func TestFetchRewriteHeaders(t *testing.T) {
	var gotUA, gotAuth, gotXToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotAuth = r.Header.Get("Authorization")
		gotXToken = r.Header.Get("X-Token")
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	oldHeaders := config.GlobalConfig.Agent.Fetch.RewriteHeaders
	config.GlobalConfig.Agent.Fetch.RewriteHeaders = map[string]map[string]string{
		`.*example\.com.*`: {"User-Agent": "RewriteUA/1.0", "X-Token": "injected"},
	}
	defer func() { config.GlobalConfig.Agent.Fetch.RewriteHeaders = oldHeaders }()

	session := newTestSession(t)
	mp := map[string]*any{
		"method": strPtr("GET"),
		"url":    strPtr(srv.URL), // 不匹配 example.com
		"_id":    strPtr("rh1"),
	}
	pass, _, res, _ := fetchURL(session, mp, []*any{})
	assertSuccessPath(t, pass, res)
	if gotUA != product.UserAgent {
		t.Errorf("non-matching URL should not inject UA, got %q", gotUA)
	}

	// 匹配 URL：注入配置 headers（正则精确匹配本地 srv.URL）
	config.GlobalConfig.Agent.Fetch.RewriteHeaders = map[string]map[string]string{
		regexp.QuoteMeta(srv.URL): {"User-Agent": "RewriteUA/1.0", "X-Token": "injected"},
	}
	mp["_id"] = strPtr("rh2")
	pass, _, res, _ = fetchURL(session, mp, []*any{})
	assertSuccessPath(t, pass, res)
	if gotUA != "RewriteUA/1.0" {
		t.Errorf("matching URL should inject UA, got %q", gotUA)
	}
	if gotXToken != "injected" {
		t.Errorf("matching URL should inject X-Token, got %q", gotXToken)
	}
	if gotAuth != "" {
		t.Errorf("unconfigured header should not be set, got %q", gotAuth)
	}

	// AI 显式 headers 覆盖注入值
	mp["headers"] = strPtr("User-Agent: AIUA/2.0")
	mp["_id"] = strPtr("rh3")
	pass, _, res, _ = fetchURL(session, mp, []*any{})
	assertSuccessPath(t, pass, res)
	if gotUA != "AIUA/2.0" {
		t.Errorf("AI explicit headers should override injected UA, got %q", gotUA)
	}
	if gotXToken != "injected" {
		t.Errorf("AI headers should not remove injected X-Token, got %q", gotXToken)
	}
}

// TestFetchRewriteHeadersInvalidPattern 验证无效正则在日志告警后被跳过，不影响请求。
func TestFetchRewriteHeadersInvalidPattern(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	oldHeaders := config.GlobalConfig.Agent.Fetch.RewriteHeaders
	config.GlobalConfig.Agent.Fetch.RewriteHeaders = map[string]map[string]string{
		`[invalid`: {"X-Token": "should-not-set"},
	}
	defer func() { config.GlobalConfig.Agent.Fetch.RewriteHeaders = oldHeaders }()

	session := newTestSession(t)
	mp := map[string]*any{
		"method": strPtr("GET"),
		"url":    strPtr(srv.URL),
		"_id":    strPtr("rh4"),
	}
	pass, _, res, _ := fetchURL(session, mp, []*any{})
	assertSuccessPath(t, pass, res)
}

// startConnectProxy 起一个转发 CONNECT 的假 HTTP 代理。
func startConnectProxy(t *testing.T, targetAddr string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "not connect", http.StatusMethodNotAllowed)
			return
		}
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		clientConn, _, err := hijacker.Hijack()
		if err != nil {
			return
		}
		targetConn, err := net.Dial("tcp", r.Host)
		if err != nil {
			clientConn.Close()
			return
		}
		if _, err := clientConn.Write([]byte("HTTP/1.1 200 Connection established\r\n\r\n")); err != nil {
			targetConn.Close()
			clientConn.Close()
			return
		}
		go func() {
			io.Copy(targetConn, clientConn)
			targetConn.Close()
		}()
		io.Copy(clientConn, targetConn)
		clientConn.Close()
	}))
	return srv
}

// TestFetchHiddenViaProxy 验证 hidden + FetchProxy 下 utls 指纹仍生效（手动 CONNECT 隧道）。
func TestFetchHiddenViaProxy(t *testing.T) {
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("proxy-tunnel-ok"))
	}))
	defer tlsSrv.Close()

	proxySrv := startConnectProxy(t, tlsSrv.Listener.Addr().String())
	defer proxySrv.Close()

	oldProxy := config.GlobalConfig.Agent.FetchProxy
	config.GlobalConfig.Agent.FetchProxy = proxySrv.URL
	defer func() { config.GlobalConfig.Agent.FetchProxy = oldProxy }()

	// 注入 httptest 自签证书，使 utls 握手通过
	pool := x509.NewCertPool()
	pool.AddCert(tlsSrv.Certificate())
	oldRoots := tlsRootCAs
	tlsRootCAs = pool
	defer func() { tlsRootCAs = oldRoots }()

	session := newTestSession(t)
	mp := map[string]*any{
		"method": strPtr("GET"),
		"url":    strPtr(tlsSrv.URL),
		"hidden": boolPtr(true),
		"_id":    strPtr("t10"),
	}
	pass, _, res, _ := fetchURL(session, mp, []*any{})
	assertSuccessPath(t, pass, res)

	content := fetchTempContent(t, session)
	if !strings.Contains(content, "proxy-tunnel-ok") {
		t.Errorf("hidden+proxy tunnel should reach target, got:\n%s", content)
	}
}
