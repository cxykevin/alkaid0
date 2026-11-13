package connect

import (
	"crypto/subtle"
	"net/http"
	"net/http/pprof"

	"github.com/cxykevin/alkaid0/config"
)

// pprofEndpointPrefix 是 pprof 端点的公共前缀。
const pprofEndpointPrefix = "/debug/pprof/"

// registerPprof 把 net/http/pprof 的处理器挂到 /debug/pprof/ 下。
//
// 路由由显式 mux 完成，不使用 http.DefaultServeMux（pprof 的 init 会往默认
// mux 注册，本进程没有任何服务监听默认 mux，因此不会被意外暴露）。
func registerPprof(mux *http.ServeMux) {
	// /debug/pprof 本身：鉴权后重定向到 /debug/pprof/，避免未鉴权请求先拿到 301
	mux.Handle(pprofEndpointPrefix[:len(pprofEndpointPrefix)-1], handlePprof("", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, pprofEndpointPrefix, http.StatusMovedPermanently)
	})))
	// /debug/pprof/ 同时负责命名 profile（heap/goroutine/allocs/block/mutex/...）
	mux.Handle(pprofEndpointPrefix, handlePprof("", http.HandlerFunc(pprof.Index)))
	mux.Handle(pprofEndpointPrefix+"cmdline", handlePprof("cmdline", http.HandlerFunc(pprof.Cmdline)))
	mux.Handle(pprofEndpointPrefix+"profile", handlePprof("profile", http.HandlerFunc(pprof.Profile)))
	mux.Handle(pprofEndpointPrefix+"symbol", handlePprof("symbol", http.HandlerFunc(pprof.Symbol)))
	mux.Handle(pprofEndpointPrefix+"trace", handlePprof("trace", http.HandlerFunc(pprof.Trace)))
}

// handlePprof 用服务端 key 保护 pprof 处理器，鉴权规则与 /info、/gc 完全一致
// （查询参数或 Authorization 头，恒定时间比较）：性能剖析数据包含调用栈、
// 堆对象与命令行，不应向未授权请求暴露。
func handlePprof(name string, next http.Handler) http.HandlerFunc {
	endpoint := pprofEndpointPrefix + name
	return func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(httpRequestServerKey(r)), []byte(config.GlobalConfig.Server.Key)) != 1 {
			loggerWs.Error("invalid token on %s, rejecting request", endpoint)
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	}
}
