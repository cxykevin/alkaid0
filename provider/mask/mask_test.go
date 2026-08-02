package mask

import (
	"encoding/base64"
	"net"
	"strings"
	"testing"

	"github.com/cxykevin/alkaid0/config"
	configStructs "github.com/cxykevin/alkaid0/config/structs"
	"github.com/cxykevin/alkaid0/provider/request/structs"
	"github.com/cxykevin/alkaid0/storage"
	storageStructs "github.com/cxykevin/alkaid0/storage/structs"
	"gorm.io/gorm"
)

func newTestDB(t *testing.T) *gorm.DB {
	db, err := storage.InitDB(":memory:")
	if err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, e := db.DB(); e == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func enableMask(t *testing.T) {
	restore := config.GlobalConfigSwap(configStructs.Config{
		DataMask: configStructs.DataMaskConfig{
			Enable:      true,
			MaskAPIKey:  true,
			MaskPhone:   true,
			MaskIP:      true,
			MaskSession: true,
			MaskJWT:     true,
		},
	})
	t.Cleanup(restore)
}

func fullCfg() *configStructs.DataMaskConfig {
	return &configStructs.DataMaskConfig{
		Enable:      true,
		MaskAPIKey:  true,
		MaskPhone:   true,
		MaskIP:      true,
		MaskSession: true,
		MaskJWT:     true,
	}
}

func makeJWT() string {
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"1234567890","name":"John Doe"}`))
	sig := base64.RawURLEncoding.EncodeToString([]byte("a-very-long-signature-0123456789"))
	return hdr + "." + payload + "." + sig
}

// ---------- 检测 ----------

func TestDetectAPIKeyPrefix(t *testing.T) {
	spans := detectSensitive("my key is sk-or-v1-abc123def456ghi789 here", fullCfg(), nil)
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d: %+v", len(spans), spans)
	}
	s := spans[0]
	if s.Type != TypeAPIKey || s.Original != "sk-or-v1-abc123def456ghi789" {
		t.Fatalf("got %+v", s)
	}
}

func TestDetectAPIKeyNoPrefixEmbedded(t *testing.T) {
	// 前缀必须独立成词，避免误匹配标识符中间的内容
	spans := detectSensitive("foosk-or-v1-abc123def456ghi789bar", fullCfg(), nil)
	for _, s := range spans {
		if s.Type == TypeAPIKey {
			t.Fatalf("should not match embedded key: %+v", s)
		}
	}
}

func TestDetectPhone(t *testing.T) {
	spans := detectSensitive("call 13800138000 now", fullCfg(), nil)
	found := false
	for _, s := range spans {
		if s.Type == TypePhone && s.Original == "13800138000" {
			found = true
		}
	}
	if !found {
		t.Fatalf("phone not detected: %+v", spans)
	}
}

func TestDetectPhoneInLongNumber(t *testing.T) {
	// 长数字串中的子串不算手机号
	spans := detectSensitive("number 12313800138000", fullCfg(), nil)
	for _, s := range spans {
		if s.Type == TypePhone {
			t.Fatalf("should not detect phone inside longer number: %+v", s)
		}
	}
}

func TestDetectPublicIP(t *testing.T) {
	// 8.8.8.8 等知名公共 DNS 已进默认白名单，这里用普通公网 IP 验证
	spans := detectSensitive("connect to 45.45.45.45 now", fullCfg(), nil)
	found := false
	for _, s := range spans {
		if s.Type == TypeIP && s.Original == "45.45.45.45" {
			found = true
		}
	}
	if !found {
		t.Fatalf("public ip not detected: %+v", spans)
	}
}

func TestDetectPrivateLoopbackIP(t *testing.T) {
	for _, ip := range []string{"192.168.1.1", "10.0.0.1", "172.16.0.1", "127.0.0.1", "169.254.1.1", "::1", "fe80::1"} {
		spans := detectSensitive("host "+ip, fullCfg(), nil)
		for _, s := range spans {
			if s.Type == TypeIP {
				t.Fatalf("should not mask non-public ip %q: %+v", ip, s)
			}
		}
	}
}

func TestDetectIPDefaultWhitelist(t *testing.T) {
	// 知名公共 DNS/NTP 默认不脱敏
	for _, ip := range []string{"1.1.1.1", "1.0.0.1", "8.8.8.8", "8.8.4.4", "9.9.9.9",
		"114.114.114.114", "223.5.5.5", "119.29.29.29", "180.76.76.76", "101.6.6.6", "203.107.6.88"} {
		spans := detectSensitive("dns "+ip, fullCfg(), nil)
		for _, s := range spans {
			if s.Type == TypeIP {
				t.Fatalf("public dns/ntp %q should not be masked by default: %+v", ip, s)
			}
		}
	}
	// 同样公网但非白名单的普通地址仍应脱敏
	spans := detectSensitive("host 45.45.45.45", fullCfg(), nil)
	found := false
	for _, s := range spans {
		if s.Type == TypeIP && s.Original == "45.45.45.45" {
			found = true
		}
	}
	if !found {
		t.Fatalf("non-whitelisted public ip should still be masked: %+v", spans)
	}
}

func TestDetectIPConfigWhitelist(t *testing.T) {
	cfg := fullCfg()
	cfg.MaskIPWhitelist = []string{"45.0.0.0/16", "46.46.46.46"}
	// CIDR 段内命中 → 不脱敏
	spans := detectSensitive("host 45.0.1.2", cfg, nil)
	for _, s := range spans {
		if s.Type == TypeIP {
			t.Fatalf("cidr-whitelisted ip should not be masked: %+v", s)
		}
	}
	// 精确 IP 命中 → 不脱敏
	spans = detectSensitive("host 46.46.46.46", cfg, nil)
	for _, s := range spans {
		if s.Type == TypeIP {
			t.Fatalf("exact-whitelisted ip should not be masked: %+v", s)
		}
	}
	// 段外 → 仍脱敏
	spans = detectSensitive("host 45.45.45.45", cfg, nil)
	found := false
	for _, s := range spans {
		if s.Type == TypeIP && s.Original == "45.45.45.45" {
			found = true
		}
	}
	if !found {
		t.Fatalf("out-of-cidr ip should still be masked: %+v", spans)
	}
}

func TestDetectJWT(t *testing.T) {
	j := makeJWT()
	spans := detectSensitive("here is token: "+j, fullCfg(), nil)
	found := false
	for _, s := range spans {
		if s.Type == TypeJWT && s.Original == j {
			found = true
		}
	}
	if !found {
		t.Fatalf("jwt not detected: %+v", spans)
	}
}

func TestDetectJWTNotVersion(t *testing.T) {
	for _, v := range []string{"1.2.3", "v1.2.3", "1.2.3.4", "1.2"} {
		spans := detectSensitive("version "+v, fullCfg(), nil)
		for _, s := range spans {
			if s.Type == TypeJWT {
				t.Fatalf("should not match %q as jwt: %+v", v, s)
			}
		}
	}
}

func TestDetectSessionValue(t *testing.T) {
	spans := detectSensitive("session=abcdef12345678 and sid=xyz789", fullCfg(), nil)
	found := map[string]bool{"abcdef12345678": false, "xyz789": false}
	for _, s := range spans {
		if s.Type == TypeSession {
			found[s.Original] = true
		}
	}
	if !found["abcdef12345678"] || !found["xyz789"] {
		t.Fatalf("session values not detected: %+v", spans)
	}
}

func TestDetectCookieHeader(t *testing.T) {
	spans := detectSensitive("Cookie: session=abc123def456; theme=dark", fullCfg(), nil)
	cookieVals := map[string]bool{}
	for _, s := range spans {
		if s.Type == TypeCookie {
			cookieVals[s.Original] = true
		}
	}
	if !cookieVals["abc123def456"] {
		t.Fatalf("cookie session value not detected: %+v", spans)
	}
}

func TestDetectSessionSkipsFunctionCall(t *testing.T) {
	// token = getToken() 是函数调用，不应被当作 session 值脱敏
	spans := detectSensitive("const token = getToken()", fullCfg(), nil)
	for _, s := range spans {
		if s.Type == TypeSession {
			t.Fatalf("should not mask function call: %+v", s)
		}
	}
}

func TestDetectBareKeyThreshold(t *testing.T) {
	long := strings.Repeat("Ab3", 30) // 90 字符字母数字
	spans := detectSensitive(long, fullCfg(), nil)
	found := false
	for _, s := range spans {
		if s.Type == TypeAPIKey && s.Original == long {
			found = true
		}
	}
	if !found {
		t.Fatalf("long bare token not detected")
	}

	// git SHA-1（40 字符 hex）不应被当作 key
	sha := strings.Repeat("a", 40)
	spans = detectSensitive("commit "+sha, fullCfg(), nil)
	for _, s := range spans {
		if s.Type == TypeAPIKey {
			t.Fatalf("40-char sha should not be masked: %+v", s)
		}
	}
}

func TestDetectAlreadyMaskedSkipped(t *testing.T) {
	maskToOrig := map[string]string{"sk-xqz789abc": "sk-or-real-key"}
	spans := detectSensitive("use sk-xqz789abc", fullCfg(), maskToOrig)
	for _, s := range spans {
		if s.Type == TypeAPIKey {
			t.Fatalf("already-masked value should be skipped: %+v", s)
		}
	}
}

// ---------- 生成 ----------

func TestGenAPIKeyPreservesPrefixAndLength(t *testing.T) {
	orig := "sk-or-v1-abc123def456ghi789"
	fake := genFake(orig, TypeAPIKey)
	if !strings.HasPrefix(fake, "sk-or-v1-") {
		t.Fatalf("prefix lost: %s", fake)
	}
	if len(fake) != len(orig) {
		t.Fatalf("length mismatch: %d != %d", len(fake), len(orig))
	}
	if fake == orig {
		t.Fatalf("fake equals original")
	}
}

func TestGenHexKey(t *testing.T) {
	orig := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" // 64 hex
	fake := genFake(orig, TypeAPIKey)
	if len(fake) != len(orig) {
		t.Fatalf("length mismatch")
	}
	for i := 0; i < len(fake); i++ {
		c := fake[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			t.Fatalf("fake %q is not hex at %d", fake, i)
		}
	}
}

func TestGenPhoneFormat(t *testing.T) {
	for range 50 {
		fake := genPhone("13800138000")
		if len(fake) != 11 {
			t.Fatalf("phone length mismatch: %s", fake)
		}
		if fake[0] != '1' || fake[1] < '3' || fake[1] > '9' {
			t.Fatalf("phone format invalid: %s", fake)
		}
		for j := 0; j < len(fake); j++ {
			if fake[j] < '0' || fake[j] > '9' {
				t.Fatalf("phone contains non-digit: %s", fake)
			}
		}
	}
}

func TestGenIPPublicAndLength(t *testing.T) {
	for _, ip := range []string{"8.8.8.8", "203.0.113.1", "1.2.3.4"} {
		fake := genIP(ip)
		if len(fake) != len(ip) {
			t.Fatalf("ip %q length mismatch: %q", ip, fake)
		}
		parsed := net.ParseIP(fake)
		if parsed == nil || parsed.To4() == nil {
			t.Fatalf("ip %q is not valid public ipv4: %q", ip, fake)
		}
		if !isPublicIP(parsed) {
			t.Fatalf("ip %q fake is not public: %q", ip, fake)
		}
	}
}

func TestGenJWTKeepsPlaintext(t *testing.T) {
	j := makeJWT()
	parts := strings.Split(j, ".")
	fake := genJWT(j)
	fparts := strings.Split(fake, ".")
	if fparts[0] != parts[0] || fparts[1] != parts[1] {
		t.Fatalf("jwt header/payload changed: %s", fake)
	}
	if fparts[2] == parts[2] {
		t.Fatalf("jwt signature should differ")
	}
	if len(fparts[2]) != len(parts[2]) {
		t.Fatalf("jwt signature length mismatch")
	}
}

// ---------- 引擎 ----------

func TestEngineRoundtrip(t *testing.T) {
	enableMask(t)
	db := newTestDB(t)
	eng := NewEngine(db)
	if eng == nil {
		t.Fatal("engine is nil")
	}
	secrets := []string{"sk-or-v1-abc123def456ghi789", "13800138000", "45.45.45.45", "abcdef12345678"}
	content := "key sk-or-v1-abc123def456ghi789, phone 13800138000, ip 45.45.45.45, session=abcdef12345678"
	masked := eng.MaskMessages([]structs.Message{{Role: structs.RoleUser, Content: content}})[0].Content

	for _, s := range secrets {
		if strings.Contains(masked, s) {
			t.Errorf("masked content still contains %q: %s", s, masked)
		}
	}
	if !strings.Contains(masked, "sk-or-v1-") {
		t.Errorf("masked content lost prefix: %s", masked)
	}

	// 还原：逐 chunk 流式还原
	var restored strings.Builder
	for _, chunk := range splitChunks(masked, 7) {
		restored.WriteString(eng.RestoreContent(chunk))
	}
	c, _ := eng.FinishRestore()
	restored.WriteString(c)
	for _, s := range secrets {
		if !strings.Contains(restored.String(), s) {
			t.Errorf("restored missing %q: %s", s, restored.String())
		}
	}
}

func TestEngineDeterministic(t *testing.T) {
	enableMask(t)
	db := newTestDB(t)
	msg := []structs.Message{{Role: structs.RoleUser, Content: "key sk-or-v1-abc123def456ghi789"}}
	eng1 := NewEngine(db)
	m1 := eng1.MaskMessages(msg)[0].Content
	// 新引擎重新加载 db，同一 key 应得到同一假值
	eng2 := NewEngine(db)
	m2 := eng2.MaskMessages(msg)[0].Content
	if m1 != m2 {
		t.Fatalf("same key mapped differently: %q vs %q", m1, m2)
	}
}

func TestAddDelCustom(t *testing.T) {
	enableMask(t)
	db := newTestDB(t)
	if err := AddCustom(db, "my-secret-token"); err != nil {
		t.Fatal(err)
	}
	// 幂等：重复 add 不报错、不产生重复行
	if err := AddCustom(db, "my-secret-token"); err != nil {
		t.Fatalf("re-add should be idempotent: %v", err)
	}
	var rows []storageStructs.CustomMask
	if err := db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 custom row, got %d", len(rows))
	}
	// add 空值报错
	if err := AddCustom(db, "   "); err == nil {
		t.Fatal("empty value should error")
	}

	// 先产生映射，再 del：行与映射都应被清理
	eng := NewEngine(db)
	eng.MaskMessages([]structs.Message{{Role: structs.RoleUser, Content: "value my-secret-token here"}})
	var km []storageStructs.KeyMapping
	if err := db.Where("key_type = ?", TypeCustom).Find(&km).Error; err != nil {
		t.Fatal(err)
	}
	if len(km) != 1 {
		t.Fatalf("expected 1 custom mapping, got %d", len(km))
	}
	if err := DelCustom(db, "my-secret-token"); err != nil {
		t.Fatal(err)
	}
	if err := db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("custom row should be deleted")
	}
	if err := db.Where("key_type = ?", TypeCustom).Find(&km).Error; err != nil {
		t.Fatal(err)
	}
	if len(km) != 0 {
		t.Fatalf("custom mapping should be deleted")
	}
}

func TestEngineCustomMaskRoundtrip(t *testing.T) {
	enableMask(t)
	db := newTestDB(t)
	if err := AddCustom(db, "alice@corp.local"); err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(db)
	if eng == nil {
		t.Fatal("engine is nil")
	}
	content := "contact alice@corp.local for help"
	masked := eng.MaskMessages([]structs.Message{{Role: structs.RoleUser, Content: content}})[0].Content
	if strings.Contains(masked, "alice@corp.local") {
		t.Fatalf("custom value not masked: %s", masked)
	}
	if len(masked) != len(content) {
		t.Fatalf("custom mask changed length: %d != %d", len(masked), len(content))
	}

	// 流式还原（小 chunk 切分）
	var restored strings.Builder
	for _, chunk := range splitChunks(masked, 5) {
		restored.WriteString(eng.RestoreContent(chunk))
	}
	c, _ := eng.FinishRestore()
	restored.WriteString(c)
	if !strings.Contains(restored.String(), "alice@corp.local") {
		t.Fatalf("custom value not restored: %s", restored.String())
	}
}

func TestEngineMaskedStoredInDB(t *testing.T) {
	enableMask(t)
	db := newTestDB(t)
	eng := NewEngine(db)
	eng.MaskMessages([]structs.Message{{Role: structs.RoleUser, Content: "key sk-or-v1-abc123def456ghi789"}})
	var rows []storageStructs.KeyMapping
	if err := db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 mapping row, got %d", len(rows))
	}
	if rows[0].Original != "sk-or-v1-abc123def456ghi789" {
		t.Fatalf("unexpected original: %s", rows[0].Original)
	}
}

func TestEngineDisabled(t *testing.T) {
	restore := config.GlobalConfigSwap(configStructs.Config{
		DataMask: configStructs.DataMaskConfig{Enable: false},
	})
	t.Cleanup(restore)
	db := newTestDB(t)
	if eng := NewEngine(db); eng != nil {
		t.Fatal("engine should be nil when disabled")
	}
}

func TestEngineNilDB(t *testing.T) {
	enableMask(t)
	if eng := NewEngine(nil); eng != nil {
		t.Fatal("engine should be nil for nil db")
	}
}

func TestMaskMessagesMutatesReasoning(t *testing.T) {
	enableMask(t)
	db := newTestDB(t)
	eng := NewEngine(db)
	rc := "thinking about sk-or-v1-abc123def456ghi789"
	out := eng.MaskMessages([]structs.Message{{Role: structs.RoleAssistant, Content: "hi", ReasoningContent: &rc}})
	if out[0].ReasoningContent == nil {
		t.Fatal("reasoning content nil")
	}
	if strings.Contains(*out[0].ReasoningContent, "sk-or-v1-abc123def456ghi789") {
		t.Fatalf("reasoning not masked: %s", *out[0].ReasoningContent)
	}
}

func splitChunks(s string, n int) []string {
	var chunks []string
	runes := []rune(s)
	for i := 0; i < len(runes); i += n {
		end := min(i+n, len(runes))
		chunks = append(chunks, string(runes[i:end]))
	}
	return chunks
}
