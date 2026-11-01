package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/internal/skillpath"
)

// writeSkill 在 root/<name> 下写一个 SKILL.md。
func writeSkill(t *testing.T, root, name, content string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, skillpath.ManifestName), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	return dir
}

// findByName 从发现结果里取指定名字的 skill。
func findByName(skills []Skill, name string) (Skill, bool) {
	for _, s := range skills {
		if s.Name == name {
			return s, true
		}
	}
	return Skill{}, false
}

// requireSymlink 在平台不支持符号链接时跳过用例（Windows 非开发者模式）。
func requireSymlink(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "link")); err != nil {
		t.Skipf("当前环境不支持符号链接: %v", err)
	}
}

func TestDiscover(t *testing.T) {
	ws := t.TempDir()
	claudeRoot := filepath.Join(ws, ".claude", "skills")
	agentsRoot := filepath.Join(ws, ".agents", "skills")

	wantDir := writeSkill(t, claudeRoot, "pdf-tool", "---\nname: PDF Tool\ndescription:  PDF 处理\n  skill\n---\n# Body\n")
	// 没有 frontmatter：用目录名作为名称，首行作为描述。
	writeSkill(t, agentsRoot, "plain-skill", "# Plain Skill\n\nDoes plain things.\n")
	// 同名 skill：工作区 .claude 优先于 .agents。
	writeSkill(t, claudeRoot, "dup", "---\nname: dup\ndescription: from claude\n---\n")
	writeSkill(t, agentsRoot, "dup", "---\nname: dup\ndescription: from agents\n---\n")
	// 没有 SKILL.md 的目录不是 skill。
	if err := os.MkdirAll(filepath.Join(claudeRoot, "not-a-skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	// frontmatter 非法：跳过。
	writeSkill(t, claudeRoot, "broken", "---\nname: [unclosed\n---\n")

	skills := Discover(ws)

	pdf, ok := findByName(skills, "pdf-tool")
	if !ok {
		t.Fatalf("未发现 pdf-tool: %+v", skills)
	}
	if pdf.Description != "PDF 处理 skill" {
		t.Errorf("描述应压成单行，得到 %q", pdf.Description)
	}
	if pdf.Dir != wantDir {
		t.Errorf("Dir = %q, want %q", pdf.Dir, wantDir)
	}
	if pdf.Scope() != "skill.pdf-tool" {
		t.Errorf("Scope = %q", pdf.Scope())
	}

	plain, ok := findByName(skills, "plain-skill")
	if !ok {
		t.Fatalf("未发现 plain-skill")
	}
	if plain.Description != "Plain Skill" {
		t.Errorf("无 frontmatter 时描述应取首行，得到 %q", plain.Description)
	}

	dup, ok := findByName(skills, "dup")
	if !ok {
		t.Fatalf("未发现 dup")
	}
	if dup.Description != "from claude" {
		t.Errorf("同名 skill 应优先取工作区 .claude，得到 %q", dup.Description)
	}

	if _, ok := findByName(skills, "not-a-skill"); ok {
		t.Error("没有 SKILL.md 的目录不应被发现")
	}
	if _, ok := findByName(skills, "broken"); ok {
		t.Error("frontmatter 非法的 skill 应被跳过")
	}

	// 结果按名称排序
	for i := 1; i < len(skills); i++ {
		if skills[i-1].Name > skills[i].Name {
			t.Fatalf("Discover 结果未排序: %v", skills)
		}
	}
}

func TestScopesAndLookup(t *testing.T) {
	ws := t.TempDir()
	writeSkill(t, filepath.Join(ws, ".claude", "skills"), "alpha", "---\nname: alpha\ndescription: first\n---\n")

	scopes := Scopes(ws)
	found := false
	for _, s := range scopes {
		if s.ID == "skill.alpha" {
			found = true
			if s.Description != "first" {
				t.Errorf("Description = %q", s.Description)
			}
		}
	}
	if !found {
		t.Fatalf("scopes 中没有 skill.alpha: %+v", scopes)
	}

	if _, ok := Lookup(ws, "skill.alpha"); !ok {
		t.Error("Lookup(skill.alpha) 应命中")
	}
	if _, ok := Lookup(ws, "alpha"); !ok {
		t.Error("Lookup(alpha) 应命中")
	}
	if _, ok := Lookup(ws, "skill.missing"); ok {
		t.Error("Lookup(skill.missing) 不应命中")
	}
}

func TestEnableDisable(t *testing.T) {
	requireSymlink(t)
	ws := t.TempDir()
	dir := writeSkill(t, filepath.Join(ws, ".claude", "skills"), "demo", "---\nname: demo\ndescription: demo skill\n---\n")

	sk, ok := Lookup(ws, "skill.demo")
	if !ok {
		t.Fatal("未找到 demo skill")
	}
	if err := Enable(ws, sk); err != nil {
		t.Fatalf("Enable failed: %v", err)
	}
	link := filepath.Join(ws, ".alkaid0", "skills", "demo")
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("链接不存在: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("应创建符号链接")
	}
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	if target != dir {
		t.Errorf("链接目标 = %q, want %q", target, dir)
	}

	// 重复启用幂等
	if err := Enable(ws, sk); err != nil {
		t.Fatalf("重复 Enable 应成功: %v", err)
	}

	// 通过链接读到技能内容
	data, err := os.ReadFile(filepath.Join(link, "SKILL.md"))
	if err != nil || !strings.Contains(string(data), "demo skill") {
		t.Fatalf("通过链接读取失败: %v %q", err, data)
	}

	if err := Disable(ws, "demo"); err != nil {
		t.Fatalf("Disable failed: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Errorf("停用后链接应被删除: %v", err)
	}
	// 真实技能目录不受影响
	if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
		t.Errorf("停用不应删除真实目录: %v", err)
	}
	// 再次停用幂等
	if err := Disable(ws, "demo"); err != nil {
		t.Errorf("重复 Disable 应成功: %v", err)
	}
}

func TestEnableRefusesRealDir(t *testing.T) {
	ws := t.TempDir()
	dir := writeSkill(t, filepath.Join(ws, ".claude", "skills"), "demo", "---\nname: demo\n---\n")
	// 在链接位置放一个真实目录：不能删除、要报错。
	occupied := filepath.Join(ws, ".alkaid0", "skills", "demo")
	if err := os.MkdirAll(occupied, 0o755); err != nil {
		t.Fatal(err)
	}
	sk, _ := Lookup(ws, "skill.demo")
	if err := Enable(ws, sk); err == nil {
		t.Fatal("同名真实目录存在时 Enable 应报错")
	}
	if _, err := os.Stat(occupied); err != nil {
		t.Errorf("真实目录不应被删除: %v", err)
	}
	if err := Disable(ws, "demo"); err == nil {
		t.Error("Disable 不应删除非符号链接")
	}
	if _, err := os.Stat(occupied); err != nil {
		t.Errorf("Disable 不应删除真实目录: %v", err)
	}

	// 指向别处的旧链接会被替换
	requireSymlink(t)
	other := t.TempDir()
	if err := os.RemoveAll(occupied); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, occupied); err != nil {
		t.Fatal(err)
	}
	if err := Enable(ws, sk); err != nil {
		t.Fatalf("替换旧链接失败: %v", err)
	}
	target, err := os.Readlink(occupied)
	if err != nil || target != dir {
		t.Fatalf("旧链接应被替换为 %q，得到 %q (%v)", dir, target, err)
	}
}

func TestSyncEnabled(t *testing.T) {
	requireSymlink(t)
	ws := t.TempDir()
	writeSkill(t, filepath.Join(ws, ".claude", "skills"), "one", "---\nname: one\n---\n")
	writeSkill(t, filepath.Join(ws, ".claude", "skills"), "two", "---\nname: two\n---\n")

	SyncEnabled(ws, map[string]bool{"skill.one": true, "skill.two": false})
	if _, err := os.Lstat(filepath.Join(ws, ".alkaid0", "skills", "one")); err != nil {
		t.Errorf("已启用的 skill 应补上链接: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(ws, ".alkaid0", "skills", "two")); !os.IsNotExist(err) {
		t.Errorf("未启用的 skill 不应创建链接: %v", err)
	}

	// 链接被手动删除后再次同步应恢复
	if err := os.Remove(filepath.Join(ws, ".alkaid0", "skills", "one")); err != nil {
		t.Fatal(err)
	}
	SyncEnabled(ws, map[string]bool{"skill.one": true})
	if _, err := os.Lstat(filepath.Join(ws, ".alkaid0", "skills", "one")); err != nil {
		t.Errorf("同步应恢复丢失的链接: %v", err)
	}
}

func TestIsLinkedPath(t *testing.T) {
	requireSymlink(t)
	ws := t.TempDir()
	dir := writeSkill(t, filepath.Join(ws, ".claude", "skills"), "demo", "---\nname: demo\n---\n")
	sk, _ := Lookup(ws, "skill.demo")
	if err := Enable(ws, sk); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(ws, ".alkaid0", "skills", "demo")

	if !IsLinkedPath(ws, filepath.Join(link, "SKILL.md")) {
		t.Error("已启用 skill 的内容应被放行")
	}
	if IsLinkedPath(ws, filepath.Join(ws, ".alkaid0", "db.sqlite")) {
		t.Error(".alkaid0 下的普通文件不应被放行")
	}
	if IsLinkedPath(ws, filepath.Join(dir, "SKILL.md")) {
		t.Error("非链接目录下的路径不应被放行")
	}

	// 用户手动放在链接目录里、指向非 skill 目录的链接也不放行
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	evil := filepath.Join(ws, ".alkaid0", "skills", "evil")
	if err := os.Symlink(outside, evil); err != nil {
		t.Skipf("无法创建符号链接: %v", err)
	}
	if IsLinkedPath(ws, filepath.Join(evil, "secret.txt")) {
		t.Error("指向 skill 根之外的链接不应被放行")
	}
}

func TestSplitFrontmatter(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		wantYAML  string
		wantBody  string
		wantFound bool
	}{
		{
			name:      "标准",
			text:      "---\nname: a\ndescription: b\n---\nbody\n",
			wantYAML:  "name: a\ndescription: b",
			wantBody:  "body\n",
			wantFound: true,
		},
		{
			name:      "CRLF 与 BOM",
			text:      "\ufeff---\r\nname: a\r\n---\r\nbody",
			wantYAML:  "name: a\r",
			wantBody:  "body",
			wantFound: true,
		},
		{
			name: "无 frontmatter",
			text: "# Title\nbody",
		},
		{
			name: "未闭合",
			text: "---\nname: a\nbody",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			yamlPart, body, found := splitFrontmatter(c.text)
			if found != c.wantFound {
				t.Fatalf("found = %v, want %v", found, c.wantFound)
			}
			if !found {
				if body != c.text {
					t.Errorf("未命中时正文应为原文")
				}
				return
			}
			if yamlPart != c.wantYAML {
				t.Errorf("yaml = %q, want %q", yamlPart, c.wantYAML)
			}
			if body != c.wantBody {
				t.Errorf("body = %q, want %q", body, c.wantBody)
			}
		})
	}
}

func TestSanitizeName(t *testing.T) {
	cases := map[string]string{
		"PDF Tool":       "pdf-tool",
		"  Foo_Bar.v2  ": "foo_bar.v2",
		"../escape":      "escape",
		"":               "",
		"中文技能":           "中文技能",
		"..":             "",
	}
	for input, want := range cases {
		if got := sanitizeName(input); got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", input, got, want)
		}
	}
}
