package actions

import (
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/prompts"
)

// TestInitCommandRegistered 验证 /init 命令已注册且字段完整。
func TestInitCommandRegistered(t *testing.T) {
	cmd, ok := commandMaps["/init"]
	if !ok {
		t.Fatal("/init command not registered")
	}
	if cmd.Description == "" {
		t.Error("/init command has empty Description")
	}
	if cmd.Hint == "" {
		t.Error("/init command has empty Hint")
	}
	if cmd.Function == nil {
		t.Error("/init command has nil Function")
	}
}

// TestInitPromptRendered 验证 /init 提示词模板可渲染，
// 且包含 AGENTS.md 与定制的 Alkaid0 引导语。
func TestInitPromptRendered(t *testing.T) {
	rendered, err := prompts.Render(prompts.InitTemplate, struct{}{})
	if err != nil {
		t.Fatalf("Render InitTemplate error = %v", err)
	}
	for _, want := range []string{
		"AGENTS.md",
		"This file provides guidance to Alkaid0 agent when working with code in this repository.",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered init prompt missing %q", want)
		}
	}
}

// TestFeedbackCommandRegistered 验证 /feedback 命令已注册且字段完整。
func TestFeedbackCommandRegistered(t *testing.T) {
	cmd, ok := commandMaps["/feedback"]
	if !ok {
		t.Fatal("/feedback command not registered")
	}
	if cmd.Description == "" {
		t.Error("/feedback command has empty Description")
	}
	if cmd.Hint == "" {
		t.Error("/feedback command has empty Hint")
	}
	if cmd.Function == nil {
		t.Error("/feedback command has nil Function")
	}
}
