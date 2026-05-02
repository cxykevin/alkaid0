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
