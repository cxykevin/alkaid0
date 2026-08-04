package actions

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	feedbacksdk "github.com/cxykevin/feederback/sdk"

	"github.com/cxykevin/alkaid0/config"
	cfgStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/product"
)

// TestBuildTelemetryContent 验证 Telemetry 内容以 [Telemetry] 标识且含版本信息。
func TestBuildTelemetryContent(t *testing.T) {
	c := buildTelemetryContent()
	if !strings.HasPrefix(c, "[Telemetry]") {
		t.Errorf("telemetry content should start with [Telemetry], got %q", c)
	}
	if !strings.Contains(c, product.Version) {
		t.Errorf("telemetry content should contain version %q, got %q", product.Version, c)
	}
}

// TestUsedModelIDs 验证模型 ID 列表收集：去重、忽略空白、排序。
func TestUsedModelIDs(t *testing.T) {
	restore := config.GlobalConfigSwap(cfgStructs.Config{})
	defer restore()

	if got := usedModelIDs(); len(got) != 0 {
		t.Errorf("expected empty list with empty config, got %v", got)
	}

	restore2 := config.GlobalConfigSwap(cfgStructs.Config{
		Model: cfgStructs.ModelsConfig{
			Models: map[int32]cfgStructs.ModelConfig{
				0: {ModelID: "model-b"},
				1: {ModelID: "model-a"},
				2: {ModelID: "model-b"}, // 重复去重
				3: {ModelID: "   "},     // 空白忽略
			},
		},
	})
	defer restore2()

	want := []string{"model-a", "model-b"}
	if got := usedModelIDs(); !slices.Equal(got, want) {
		t.Errorf("usedModelIDs = %v, want %v", got, want)
	}
}

// TestBuildTelemetryModelLogs 验证模型列表放入 logs 位置且每行一个。
func TestBuildTelemetryModelLogs(t *testing.T) {
	restore := config.GlobalConfigSwap(cfgStructs.Config{
		Model: cfgStructs.ModelsConfig{
			Models: map[int32]cfgStructs.ModelConfig{
				0: {ModelID: "model-b"},
				1: {ModelID: "model-a"},
			},
		},
	})
	defer restore()

	want := "model-a\nmodel-b"
	if got := buildTelemetryModelLogs(); got != want {
		t.Errorf("buildTelemetryModelLogs = %q, want %q", got, want)
	}
}

// TestRunAutoTelemetry 验证自动 Telemetry 的禁用、周期与上报行为。
func TestRunAutoTelemetry(t *testing.T) {
	t.Setenv("ALKAID0_DEBUG", "false")

	oldPath := telemetryLastPath
	oldSubmit := feedbackSubmit
	defer func() { telemetryLastPath = oldPath }()
	defer func() { feedbackSubmit = oldSubmit }()

	tmp := t.TempDir()
	path := filepath.Join(tmp, "telemetry_last")
	telemetryLastPath = func() string { return path }

	t.Run("disabled", func(t *testing.T) {
		restore := config.GlobalConfigSwap(cfgStructs.Config{
			Feedback: cfgStructs.FeedbackConfig{DisableAutoTelemetry: true},
		})
		defer restore()
		called := false
		feedbackSubmit = func(context.Context, string, string, string) (*feedbacksdk.Result, error) {
			called = true
			return nil, nil
		}
		runAutoTelemetry()
		if called {
			t.Error("submitter should not be called when disabled")
		}
	})

	t.Run("first run skips", func(t *testing.T) {
		restore := config.GlobalConfigSwap(cfgStructs.Config{})
		defer restore()
		_ = os.Remove(path)
		called := false
		feedbackSubmit = func(context.Context, string, string, string) (*feedbacksdk.Result, error) {
			called = true
			return nil, nil
		}
		runAutoTelemetry()
		if called {
			t.Error("submitter should not be called on first run (no timestamp)")
		}
	})

	t.Run("within interval skips", func(t *testing.T) {
		restore := config.GlobalConfigSwap(cfgStructs.Config{})
		defer restore()
		_ = os.WriteFile(path, []byte(strconv.FormatInt(time.Now().Unix(), 10)), 0600)
		called := false
		feedbackSubmit = func(context.Context, string, string, string) (*feedbacksdk.Result, error) {
			called = true
			return nil, nil
		}
		runAutoTelemetry()
		if called {
			t.Error("submitter should not be called within the monthly interval")
		}
	})

	t.Run("past interval submits", func(t *testing.T) {
		restore := config.GlobalConfigSwap(cfgStructs.Config{
			Model: cfgStructs.ModelsConfig{
				Models: map[int32]cfgStructs.ModelConfig{
					0: {ModelID: "model-b"},
					1: {ModelID: "model-a"},
				},
			},
		})
		defer restore()
		old := time.Now().Unix() - 31*24*3600
		_ = os.WriteFile(path, []byte(strconv.FormatInt(old, 10)), 0600)
		var gotContent, gotLogs string
		called := false
		feedbackSubmit = func(_ context.Context, content, logs, osInfo string) (*feedbacksdk.Result, error) {
			called = true
			gotContent = content
			gotLogs = logs
			return &feedbacksdk.Result{FeedbackID: "t-1"}, nil
		}
		runAutoTelemetry()
		if !called {
			t.Fatal("submitter should be called when past the monthly interval")
		}
		if !strings.HasPrefix(gotContent, "[Telemetry]") {
			t.Errorf("content should start with [Telemetry], got %q", gotContent)
		}
		if gotLogs != "model-a\nmodel-b" {
			t.Errorf("logs should contain model list one per line, got %q", gotLogs)
		}
		if b, err := os.ReadFile(path); err != nil || len(b) == 0 {
			t.Errorf("telemetry timestamp should be written after success, err=%v", err)
		}
	})
}
