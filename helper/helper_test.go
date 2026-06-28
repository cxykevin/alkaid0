package helper

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cxykevin/alkaid0/config/structs"
)

func TestBuildHelperConfig(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		env     map[string]string
		want    structs.RPCConfig
		wantErr bool
	}{
		{
			name: "default config",
			args: []string{"helper"},
			env: map[string]string{
				"ALKAID0_CONFIG_PATH": "/nonexistent",
			},
			want: structs.RPCConfig{
				Host:               "127.0.0.1",
				Port:               7433,
				Path:               "/acp",
				Key:                "<empty>",
				DisableStdioServer: false,
			},
		},
		{
			name: "with flags",
			args: []string{"helper", "-host", "localhost", "-port", "8080", "-path", "/test", "-key", "testkey"},
			env: map[string]string{
				"ALKAID0_CONFIG_PATH": "/nonexistent",
			},
			want: structs.RPCConfig{
				Host:               "localhost",
				Port:               8080,
				Path:               "/test",
				Key:                "testkey",
				DisableStdioServer: false,
			},
		},
		{
			name: "with env",
			args: []string{"helper"},
			env: map[string]string{
				"ALKAID0_CONFIG_PATH": "/nonexistent",
				"ALKAID0_HELPER_HOST": "envhost",
				"ALKAID0_HELPER_PORT": "9090",
				"ALKAID0_HELPER_PATH": "/envpath",
				"ALKAID0_HELPER_KEY":  "envkey",
			},
			want: structs.RPCConfig{
				Host:               "envhost",
				Port:               9090,
				Path:               "/envpath",
				Key:                "envkey",
				DisableStdioServer: false,
			},
		},
		{
			name: "invalid port",
			args: []string{"helper"},
			env: map[string]string{
				"ALKAID0_CONFIG_PATH": "/nonexistent",
				"ALKAID0_HELPER_PORT": "invalid",
			},
			wantErr: true,
		},
		{
			name: "zero port",
			args: []string{"helper", "-port", "0"},
			env: map[string]string{
				"ALKAID0_CONFIG_PATH": "/nonexistent",
			},
			wantErr: true,
		},
		{
			name: "empty host",
			args: []string{"helper", "-host", ""},
			env: map[string]string{
				"ALKAID0_CONFIG_PATH": "/nonexistent",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set env
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			t.Logf("env ALKAID0_HELPER_HOST: %s", os.Getenv("ALKAID0_HELPER_HOST"))

			got, err := buildHelperConfig(tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("buildHelperConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("buildHelperConfig() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLoadConfigFile(t *testing.T) {
	// Create temp dir
	tempDir := t.TempDir()

	tests := []struct {
		name     string
		fileName string
		content  string
		want     structs.RPCConfig
		wantErr  bool
	}{
		{
			name:     "no file",
			fileName: "",
			want:     structs.RPCConfig{},
		},
		{
			name:     "nonexistent file",
			fileName: filepath.Join(tempDir, "nonexistent.json"),
			want:     structs.RPCConfig{},
		},
		{
			name:     "full config",
			fileName: filepath.Join(tempDir, "full.json"),
			content:  `{"server":{"host":"fullhost","port":5678,"path":"/full","key":"fullkey"}}`,
			want: structs.RPCConfig{
				Host: "fullhost",
				Port: 5678,
				Path: "/full",
				Key:  "fullkey",
			},
		},
		{
			name:     "invalid json",
			fileName: filepath.Join(tempDir, "invalid.json"),
			content:  `invalid`,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.content != "" {
				err := os.WriteFile(tt.fileName, []byte(tt.content), 0644)
				if err != nil {
					t.Fatal(err)
				}
			}

			got, err := loadConfigFile(tt.fileName)
			if (err != nil) != tt.wantErr {
				t.Errorf("loadConfigFile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("loadConfigFile() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExpandPath(t *testing.T) {
	home, _ := os.UserHomeDir()

	tests := []struct {
		name    string
		path    string
		want    string
		wantErr bool
	}{
		{
			name: "absolute path",
			path: "/absolute/path",
			want: "/absolute/path",
		},
		{
			name: "home path",
			path: "~/test",
			want: filepath.Join(home, "test"),
		},
		{
			name:    "unsupported ~+",
			path:    "~+/test",
			wantErr: true,
		},
		{
			name: "relative path",
			path: "relative/path",
			want: "relative/path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := expandPath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("expandPath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("expandPath() = %v, want %v", got, filepath.Clean(tt.want))
			}
		})
	}
}

func TestBuildWebSocketURL(t *testing.T) {
	tests := []struct {
		name    string
		cfg     structs.RPCConfig
		want    string
		wantErr bool
	}{
		{
			name: "basic",
			cfg: structs.RPCConfig{
				Host: "localhost",
				Port: 8080,
				Path: "/ws",
			},
			want: "ws://localhost:8080/ws",
		},
		{
			name: "with key",
			cfg: structs.RPCConfig{
				Host: "example.com",
				Port: 9000,
				Path: "/api",
				Key:  "secret",
			},
			want: "ws://example.com:9000/api?key=secret",
		},
		{
			name: "empty host",
			cfg: structs.RPCConfig{
				Port: 8080,
			},
			wantErr: true,
		},
		{
			name: "zero port",
			cfg: structs.RPCConfig{
				Host: "localhost",
			},
			wantErr: true,
		},
		{
			name: "path without slash",
			cfg: structs.RPCConfig{
				Host: "localhost",
				Port: 8080,
				Path: "ws",
			},
			want: "ws://localhost:8080/ws",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildWebSocketURL(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("buildWebSocketURL() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("buildWebSocketURL() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMergeRPCConfig(t *testing.T) {
	tests := []struct {
		name string
		dst  structs.RPCConfig
		src  structs.RPCConfig
		want structs.RPCConfig
	}{
		{
			name: "merge all",
			dst: structs.RPCConfig{
				Host: "dst",
				Port: 1,
				Path: "/dst",
				Key:  "dstkey",
			},
			src: structs.RPCConfig{
				Host: "src",
				Port: 2,
				Path: "/src",
				Key:  "srckey",
			},
			want: structs.RPCConfig{
				Host: "src",
				Port: 2,
				Path: "/src",
				Key:  "srckey",
			},
		},
		{
			name: "merge partial",
			dst: structs.RPCConfig{
				Host: "dst",
				Port: 1,
				Path: "/dst",
				Key:  "dstkey",
			},
			src: structs.RPCConfig{
				Host: "",
				Port: 2,
				Path: "",
				Key:  "srckey",
			},
			want: structs.RPCConfig{
				Host: "dst",
				Port: 2,
				Path: "/dst",
				Key:  "srckey",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mergeRPCConfig(&tt.dst, tt.src)
			if tt.dst != tt.want {
				t.Errorf("mergeRPCConfig() = %v, want %v", tt.dst, tt.want)
			}
		})
	}
}

func TestSystemConfigPath(t *testing.T) {
	path := systemConfigPathFn()
	if path == "" {
		t.Error("systemConfigPathFn() returned empty")
	}
	// Must be an absolute path with config.json suffix
	if path[len(path)-11:] != "config.json" {
		t.Errorf("systemConfigPathFn() = %q, expected config.json path", path)
	}
	if path[0] != '/' && (len(path) < 3 || path[1] != ':') {
		t.Errorf("systemConfigPathFn() = %q, expected absolute path", path)
	}
}

func TestLoadConfigChain(t *testing.T) {
	sysDir := t.TempDir()
	sysConfig := filepath.Join(sysDir, "config.json")

	// Override systemConfigPathFn for this test
	origSysPath := systemConfigPathFn
	systemConfigPathFn = func() string {
		return sysConfig
	}
	t.Cleanup(func() { systemConfigPathFn = origSysPath })

	userFile := filepath.Join(t.TempDir(), "user.json")

	t.Run("no config files", func(t *testing.T) {
		// Override systemConfigPathFn to return nonexistent path for this subtest
		orig := systemConfigPathFn
		systemConfigPathFn = func() string { return "/nonexistent-alkaid0-test/config.json" }
		defer func() { systemConfigPathFn = orig }()

		cfg := structs.RPCConfig{Host: "default", Port: 7433, Path: "/acp", Key: "defaultkey"}
		loadConfigChain(&cfg, "/nonexistent/config.json", false)
		if cfg.Host != "default" {
			t.Errorf("Host = %q, want %q", cfg.Host, "default")
		}
	})

	t.Run("chain loads system then user", func(t *testing.T) {
		// Write system config
		writeTempJSON(t, sysConfig, `{"server":{"host":"syshost","port":1234,"path":"/sys","key":"syskey"}}`)

		// Write user config (overrides host and key)
		writeTempJSON(t, userFile, `{"server":{"host":"userhost","key":"userkey"}}`)

		cfg := structs.RPCConfig{Host: "default", Port: 7433, Path: "/acp", Key: ""}
		loadConfigChain(&cfg, userFile, false)

		if cfg.Host != "userhost" {
			t.Errorf("Host = %q, want %q", cfg.Host, "userhost")
		}
		if cfg.Port != 1234 {
			t.Errorf("Port = %d, want %d", cfg.Port, 1234)
		}
		if cfg.Path != "/sys" {
			t.Errorf("Path = %q, want %q", cfg.Path, "/sys")
		}
		if cfg.Key != "userkey" {
			t.Errorf("Key = %q, want %q", cfg.Key, "userkey")
		}
	})

	t.Run("explicit config skips system config", func(t *testing.T) {
		writeTempJSON(t, userFile, `{"server":{"host":"explicit","port":9999,"path":"/exp","key":"expkey"}}`)

		cfg := structs.RPCConfig{Host: "default", Port: 7433, Path: "/acp", Key: ""}
		loadConfigChain(&cfg, userFile, true)

		if cfg.Host != "explicit" {
			t.Errorf("Host = %q, want %q", cfg.Host, "explicit")
		}
		if cfg.Port != 9999 {
			t.Errorf("Port = %d, want %d", cfg.Port, 9999)
		}
	})
}

func writeTempJSON(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
