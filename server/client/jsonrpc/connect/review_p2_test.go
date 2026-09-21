package connect

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
)

// TestInfoEndpointRequiresServerKey 回归：/info 暴露进程内存、会话/DB 数、
// token 用量等运行态数据，必须与 WebSocket 升级同级鉴权。
func TestInfoEndpointRequiresServerKey(t *testing.T) {
	restore := config.GlobalConfigSwap(cfgStructs.Config{Server: cfgStructs.RPCConfig{Key: "test-key"}})
	defer restore()

	oldCount := getSessionCount
	getSessionCount = func() (int, int) { return 0, 0 }
	defer func() { getSessionCount = oldCount }()

	// 无 key：401
	req := httptest.NewRequest(http.MethodGet, "/info", nil)
	rec := httptest.NewRecorder()
	handleInfo(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("无 key 的 /info 应返回 401，got %d body=%s", rec.Code, rec.Body.String())
	}

	// 查询参数 key：200
	req = httptest.NewRequest(http.MethodGet, "/info?token=test-key", nil)
	rec = httptest.NewRecorder()
	handleInfo(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("携带正确 key 的 /info 应返回 200，got %d", rec.Code)
	}

	// Authorization: Bearer key：200
	req = httptest.NewRequest(http.MethodGet, "/info", nil)
	req.Header.Set("Authorization", "Bearer test-key")
	rec = httptest.NewRecorder()
	handleInfo(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("Authorization Bearer 应通过 /info 鉴权，got %d", rec.Code)
	}
}
