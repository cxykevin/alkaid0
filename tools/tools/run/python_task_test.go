package run

import (
	"testing"

	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
)

// TestWorkflowConnInfo 验证注入给 dynworkflow 的 AgentClient 连接信息：
// 通配监听地址改写为回环地址、缺省值补齐、key 非空时附加查询参数并转义。
func TestWorkflowConnInfo(t *testing.T) {
	tests := []struct {
		name string
		cfg  cfgStructs.RPCConfig
		want string
	}{
		{
			name: "default config",
			cfg:  cfgStructs.RPCConfig{Host: "127.0.0.1", Port: 7433, Path: "/acp", Key: "a"},
			want: "ws://127.0.0.1:7433/acp?key=a",
		},
		{
			name: "wildcard host rewritten",
			cfg:  cfgStructs.RPCConfig{Host: "0.0.0.0", Port: 7433, Path: "/acp"},
			want: "ws://127.0.0.1:7433/acp",
		},
		{
			name: "ipv6 wildcard rewritten",
			cfg:  cfgStructs.RPCConfig{Host: "::", Port: 7433, Path: "/acp"},
			want: "ws://127.0.0.1:7433/acp",
		},
		{
			name: "empty config falls back to defaults",
			cfg:  cfgStructs.RPCConfig{},
			want: "ws://127.0.0.1:7433/acp",
		},
		{
			name: "path without leading slash",
			cfg:  cfgStructs.RPCConfig{Host: "127.0.0.1", Port: 8000, Path: "acp"},
			want: "ws://127.0.0.1:8000/acp",
		},
		{
			name: "ipv6 host",
			cfg:  cfgStructs.RPCConfig{Host: "::1", Port: 7433, Path: "/acp"},
			want: "ws://[::1]:7433/acp",
		},
		{
			name: "key escaped",
			cfg:  cfgStructs.RPCConfig{Host: "127.0.0.1", Port: 1, Path: "/acp", Key: "a b&c"},
			want: "ws://127.0.0.1:1/acp?key=a+b%26c",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := workflowConnInfo(tt.cfg); got != tt.want {
				t.Errorf("workflowConnInfo() = %q, want %q", got, tt.want)
			}
		})
	}
}
