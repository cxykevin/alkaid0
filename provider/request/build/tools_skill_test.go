package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/internal/skillpath"
	"github.com/cxykevin/alkaid0/storage/structs"
	"github.com/cxykevin/alkaid0/tools/toolobj"
)

// TestToolsRendersSkillScopes 验证工作区里发现的 skill 会以 skill.<name> 的形式
// 出现在 <scopes> 段，并按会话的 EnableScopes 渲染 enable 状态。
func TestToolsRendersSkillScopes(t *testing.T) {
	originalScopes := toolobj.Scopes
	originalTools := toolobj.ToolsList
	defer func() {
		toolobj.Scopes = originalScopes
		toolobj.ToolsList = originalTools
	}()

	toolobj.Scopes = map[string]string{"": "Global"}
	toolobj.ToolsList = map[string]*toolobj.Tools{
		"": {Name: "Global", ID: ""},
	}

	ws := t.TempDir()
	dir := filepath.Join(ws, ".claude", "skills", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: demo\ndescription: 演示技能\n---\n"
	if err := os.WriteFile(filepath.Join(dir, skillpath.ManifestName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	session := &structs.Chats{ID: 1, Root: ws, EnableScopes: map[string]bool{"": true}}
	scopeStr, _, _, err := Tools(session)
	if err != nil {
		t.Fatalf("Tools failed: %v", err)
	}
	if !strings.Contains(scopeStr, `id="skill.demo" enable="false"`) {
		t.Fatalf("<scopes> 未按预期列出 skill.demo:\n%s", scopeStr)
	}
	if !strings.Contains(scopeStr, "演示技能") {
		t.Fatalf("<scopes> 缺少 skill 描述:\n%s", scopeStr)
	}

	session.EnableScopes["skill.demo"] = true
	scopeStr, _, _, err = Tools(session)
	if err != nil {
		t.Fatalf("Tools failed: %v", err)
	}
	if !strings.Contains(scopeStr, `id="skill.demo" enable="true"`) {
		t.Fatalf("启用后 skill.demo 应为 enable=\"true\":\n%s", scopeStr)
	}
}
