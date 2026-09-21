package u

import (
	"path/filepath"
	"strings"
)

// IsSensitivePathName 判断路径分量（文件名）是否属于凭据/密钥类敏感文件。
//
// 为什么放在 utils：此前同一份黑名单在 tools/tools/tree 与 tools/tools/search
// 各复制了一份字面枚举，且与 provider/request/rules/reject.expr（read/edit 的
// 自动拒绝规则）不一致：
//
//   - reject.expr 用 \.env($|\.) 覆盖整个 .env 家族，而两处枚举只列了
//     .env / .env.local / .env.production / .env.development，
//     .env.staging、.env.backup、.env.prod 之类的变体照样被搜索出来；
//   - reject.expr 的扩展名家族（.pem/.key/.crt/.p12/.pfx/.der/.jks/.keystore）
//     在两处枚举里完全没有；
//   - .git-credentials、.pypirc 等同样缺失。
//
// search 属于自动批准工具（approve.expr），它的黑名单是唯一防线；一旦它比
// 审批规则更窄，就出现"规则拦得住、工具照读"的缺口。统一到这里，两处共用。
func IsSensitivePathName(name string) bool {
	if name == "" {
		return false
	}
	lower := strings.ToLower(name)

	// .env 家族：.env、.env.local、.env.production.bak……（字面枚举必然漏）
	if lower == ".env" || strings.HasPrefix(lower, ".env.") {
		return true
	}

	switch lower {
	case ".npmrc", ".pypirc", ".netrc", ".git-credentials", ".pgpass",
		"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519",
		"authorized_keys", "known_hosts", "credentials":
		return true
	}

	switch filepath.Ext(lower) {
	case ".pem", ".key", ".crt", ".p12", ".pfx", ".der", ".jks", ".keystore":
		return true
	}
	return false
}
