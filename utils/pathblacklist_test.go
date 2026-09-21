package u

import "testing"

// TestIsSensitivePathName 验证与 provider/request/rules/reject.expr 对齐的敏感文件名判定。
// 旧实现（tree/search 各一份字面枚举）漏掉 .env 家族变体、密钥扩展名与 .git-credentials，
// 导致 search（自动批准工具）能读到 reject.expr 拒绝 read/edit 的文件。
func TestIsSensitivePathName(t *testing.T) {
	sensitive := []string{
		".env", ".env.local", ".env.production", ".env.staging", ".env.backup",
		".env.production.bak", ".ENV", ".Env.Test",
		"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519",
		"authorized_keys", "known_hosts", "credentials",
		".npmrc", ".pypirc", ".netrc", ".git-credentials", ".pgpass",
		"server.pem", "PRIVATE.KEY", "cert.crt", "bundle.p12", "sign.pfx",
		"app.der", "store.jks", "app.keystore",
	}
	for _, name := range sensitive {
		if !IsSensitivePathName(name) {
			t.Errorf("IsSensitivePathName(%q) = false, want true", name)
		}
	}

	notSensitive := []string{
		"", "main.go", "README.md", "environment.txt", ".envrc",
		".alkaid0-backup", "mycredentials.json", "public.pem.bak.txt",
		"keyboard.crt.old", "config", "secrets.md",
	}
	for _, name := range notSensitive {
		if IsSensitivePathName(name) {
			t.Errorf("IsSensitivePathName(%q) = true, want false", name)
		}
	}
}
