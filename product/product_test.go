package product

import (
	"runtime"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	if Version == "" {
		t.Error("Version should not be empty")
	}
	
	// 验证版本号格式（应该是 x.x.x 格式）
	parts := strings.Split(Version, ".")
	if len(parts) != 3 {
		t.Errorf("Version should be in x.x.x format, got %s", Version)
	}
}

func TestVersionID(t *testing.T) {
	if VersionID <= 0 {
		t.Errorf("VersionID should be positive, got %d", VersionID)
	}
}

func TestUserAgentTemplate(t *testing.T) {
	if UserAgentTemplate == "" {
		t.Error("UserAgentTemplate should not be empty")
	}
	
	// 验证模板包含必要的占位符
	requiredPlaceholders := []string{"{version}", "{system}", "{sysArch}", "{goVersion}"}
	for _, placeholder := range requiredPlaceholders {
		if !strings.Contains(UserAgentTemplate, placeholder) {
			t.Errorf("UserAgentTemplate should contain %s", placeholder)
		}
	}
}

func TestUserAgent(t *testing.T) {
	if UserAgent == "" {
		t.Error("UserAgent should not be empty")
	}
	
	// 验证 UserAgent 不包含占位符（应该都被替换了）
	placeholders := []string{"{version}", "{system}", "{sysArch}", "{goVersion}"}
	for _, placeholder := range placeholders {
		if strings.Contains(UserAgent, placeholder) {
			t.Errorf("UserAgent should not contain placeholder %s, got %s", placeholder, UserAgent)
		}
	}
	
	// 验证 UserAgent 包含实际值
	if !strings.Contains(UserAgent, Version) {
		t.Errorf("UserAgent should contain version %s, got %s", Version, UserAgent)
	}
	
	if !strings.Contains(UserAgent, runtime.GOOS) {
		t.Errorf("UserAgent should contain OS %s, got %s", runtime.GOOS, UserAgent)
	}
	
	if !strings.Contains(UserAgent, runtime.GOARCH) {
		t.Errorf("UserAgent should contain arch %s, got %s", runtime.GOARCH, UserAgent)
	}
	
	// 验证包含 Go 版本（至少包含 "go"）
	if !strings.Contains(strings.ToLower(UserAgent), "go") {
		t.Errorf("UserAgent should contain Go version info, got %s", UserAgent)
	}
}

func TestUserAgentFormat(t *testing.T) {
	// 验证 UserAgent 的基本格式
	// 应该类似: Alkaid0/0.0.1 (linux amd64) Go/go1.21.0
	
	if !strings.HasPrefix(UserAgent, "Alkaid0/") {
		t.Errorf("UserAgent should start with 'Alkaid0/', got %s", UserAgent)
	}
	
	if !strings.Contains(UserAgent, "(") || !strings.Contains(UserAgent, ")") {
		t.Errorf("UserAgent should contain parentheses for system info, got %s", UserAgent)
	}
	
	if !strings.Contains(UserAgent, "Go/") {
		t.Errorf("UserAgent should contain 'Go/' prefix for Go version, got %s", UserAgent)
	}
}

func TestUserAgentComponents(t *testing.T) {
	// 测试 UserAgent 的各个组成部分
	components := []struct {
		name     string
		expected string
	}{
		{"Version", Version},
		{"OS", runtime.GOOS},
		{"Arch", runtime.GOARCH},
	}
	
	for _, comp := range components {
		t.Run(comp.name, func(t *testing.T) {
			if !strings.Contains(UserAgent, comp.expected) {
				t.Errorf("UserAgent should contain %s: %s, got %s", comp.name, comp.expected, UserAgent)
			}
		})
	}
}

func TestUserAgentNoEmptyComponents(t *testing.T) {
	// 验证 UserAgent 不包含空的组件
	// 例如不应该有连续的空格或空括号
	
	if strings.Contains(UserAgent, "  ") {
		t.Errorf("UserAgent should not contain consecutive spaces, got %s", UserAgent)
	}
	
	if strings.Contains(UserAgent, "()") {
		t.Errorf("UserAgent should not contain empty parentheses, got %s", UserAgent)
	}
	
	if strings.Contains(UserAgent, "//") {
		t.Errorf("UserAgent should not contain consecutive slashes, got %s", UserAgent)
	}
}

func TestVersionConsistency(t *testing.T) {
	// 验证 Version 和 VersionID 的一致性
	// 虽然它们可能不直接相关，但至少应该都是有效的
	
	if Version == "" && VersionID > 0 {
		t.Error("If VersionID is set, Version should also be set")
	}
	
	if Version != "" && VersionID <= 0 {
		t.Error("If Version is set, VersionID should also be positive")
	}
}
