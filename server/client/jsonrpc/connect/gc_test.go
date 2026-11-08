package connect

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
)

// TestHandleGCRequiresKey 验证 /gc 与 /info 同级鉴权：无 key / 错误 key 一律 401，
// 且不得执行强制回收；携带正确 key（?k=）时返回 JSON 结果。
func TestHandleGCRequiresKey(t *testing.T) {
	restoreCfg := config.GlobalConfigSwap(cfgStructs.Config{
		Server: cfgStructs.RPCConfig{Key: "gc-secret"},
	})
	defer restoreCfg()

	old := forceGC
	defer func() { forceGC = old }()

	called := 0
	SetForceGC(func() any {
		called++
		return map[string]any{"success": true, "released": 2}
	})

	for _, target := range []string{"/gc", "/gc?k=wrong"} {
		rec := httptest.NewRecorder()
		handleGC(rec, httptest.NewRequest(http.MethodGet, target, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s status = %d, want 401", target, rec.Code)
		}
	}
	if called != 0 {
		t.Errorf("鉴权失败时不应执行强制 GC，called = %d", called)
	}

	rec := httptest.NewRecorder()
	handleGC(rec, httptest.NewRequest(http.MethodGet, "/gc?k=gc-secret", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /gc?k=gc-secret status = %d, want 200", rec.Code)
	}
	var body struct {
		Success  bool `json:"success"`
		Released int  `json:"released"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode /gc response: %v", err)
	}
	if !body.Success || body.Released != 2 {
		t.Errorf("/gc response = %+v, want success=true released=2", body)
	}
	if called != 1 {
		t.Errorf("合法请求应执行一次强制 GC，called = %d", called)
	}
}

// TestHandleGCUnavailable 验证未注入实现时返回 503（例如未启动完整服务端的场景）。
func TestHandleGCUnavailable(t *testing.T) {
	restoreCfg := config.GlobalConfigSwap(cfgStructs.Config{
		Server: cfgStructs.RPCConfig{Key: "gc-secret"},
	})
	defer restoreCfg()

	old := forceGC
	forceGC = nil
	defer func() { forceGC = old }()

	rec := httptest.NewRecorder()
	handleGC(rec, httptest.NewRequest(http.MethodGet, "/gc?k=gc-secret", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("GET /gc without handler status = %d, want 503", rec.Code)
	}
}
