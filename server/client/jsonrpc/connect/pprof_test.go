package connect

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
)

func newPprofTestMux() *http.ServeMux {
	mux := http.NewServeMux()
	registerPprof(mux)
	return mux
}

// TestPprofRequiresKey 验证 /debug/pprof 下的所有端点与 /info、/gc 同级鉴权：
// 无 key / 错误 key 一律 401，且不会进入 pprof 处理器。
func TestPprofRequiresKey(t *testing.T) {
	restoreCfg := config.GlobalConfigSwap(cfgStructs.Config{
		Server: cfgStructs.RPCConfig{Key: "pprof-secret"},
	})
	defer restoreCfg()

	mux := newPprofTestMux()
	targets := []string{
		"/debug/pprof",
		"/debug/pprof/",
		"/debug/pprof/cmdline",
		"/debug/pprof/profile",
		"/debug/pprof/symbol",
		"/debug/pprof/trace",
		"/debug/pprof/heap",
	}
	variants := []string{"", "?k=wrong"}
	for _, target := range targets {
		for _, variant := range variants {
			url := target + variant
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("GET %s status = %d, want 401", url, rec.Code)
			}
			if !strings.Contains(rec.Body.String(), "invalid token") {
				t.Errorf("GET %s body = %q, want invalid token", url, rec.Body.String())
			}
		}
	}
}

// TestPprofAuthorized 验证携带正确 key（查询参数或 Authorization 头）时可以
// 正常访问 pprof 端点；只覆盖不需要长时间采样的处理器。
func TestPprofAuthorized(t *testing.T) {
	restoreCfg := config.GlobalConfigSwap(cfgStructs.Config{
		Server: cfgStructs.RPCConfig{Key: "pprof-secret"},
	})
	defer restoreCfg()

	mux := newPprofTestMux()

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/pprof/?k=pprof-secret", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /debug/pprof/ status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "/debug/pprof/") {
		t.Errorf("index page missing profile paths: %q", rec.Body.String()[:min(200, rec.Body.Len())])
	}

	// Authorization: Bearer <key> 与查询参数等价
	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/cmdline", nil)
	req.Header.Set("Authorization", "Bearer pprof-secret")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /debug/pprof/cmdline with bearer status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() == 0 {
		t.Error("cmdline response should not be empty")
	}

	// 命名 profile 由 /debug/pprof/ 子树处理器负责
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/pprof/heap?debug=1&k=pprof-secret", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /debug/pprof/heap status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "heap profile") {
		t.Errorf("heap profile body = %q, want heap profile header", rec.Body.String()[:min(200, rec.Body.Len())])
	}
}

// TestPprofBarePrefixRedirect 验证 /debug/pprof（无斜杠）在鉴权后重定向，
// 未鉴权请求不会先拿到 301。
func TestPprofBarePrefixRedirect(t *testing.T) {
	restoreCfg := config.GlobalConfigSwap(cfgStructs.Config{
		Server: cfgStructs.RPCConfig{Key: "pprof-secret"},
	})
	defer restoreCfg()

	mux := newPprofTestMux()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/debug/pprof?k=pprof-secret", nil))
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("GET /debug/pprof status = %d, want 301", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/debug/pprof/" {
		t.Errorf("Location = %q, want /debug/pprof/", loc)
	}
}
