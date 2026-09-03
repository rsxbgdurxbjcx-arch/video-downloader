package downloader

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CookieStore 平台 Cookie 加密存储（AES-256-GCM）。
// 明文绝不出现在日志、错误信息或前端响应；管理员仅能查看是否存在。
type CookieStore struct {
	dir string
	key []byte
}

// NewCookieStore 创建 Cookie 存储。
// key 从 COOKIE_ENCRYPTION_KEY 读取；缺失时使用派生数组（仅开发环境）。
func NewCookieStore(dir, keyHex string) (*CookieStore, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	key, err := parseKey(keyHex)
	if err != nil {
		return nil, err
	}
	return &CookieStore{dir: dir, key: key}, nil
}

// parseKey 解析 32 字节密钥：优先 hex，其次 UTF-8 字节。
func parseKey(keyHex string) ([]byte, error) {
	k := strings.TrimSpace(keyHex)
	if k == "" {
		// 开发环境派生密钥（生产在 config.Validate 已拒绝启动）
		kb := sha256.Sum256([]byte("dev-insecure-cookie-key-video-downloader"))
		return kb[:], nil
	}
	if len(k) == 64 {
		if b, err := hex.DecodeString(k); err == nil {
			return b, nil
		}
	}
	if len(k) >= 32 {
		return []byte(k)[:32], nil
	}
	return nil, fmt.Errorf("COOKIE_ENCRYPTION_KEY 长度不足 32 字节")
}

// Save 加密保存（覆盖式）。
func (c *CookieStore) Save(platform, cookie string) error {
	if strings.TrimSpace(cookie) == "" {
		return fmt.Errorf("cookie 不能为空")
	}
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	sealed := gcm.Seal(nil, nonce, []byte(cookie), []byte(platform))
	// 存储格式: nonce|ciphertext (hex)
	payload := hex.EncodeToString(append(nonce, sealed...))
	return os.WriteFile(c.filePath(platform), []byte(payload), 0600)
}

// Load 解密读取；不存在返回 ok=false。
func (c *CookieStore) Load(platform string) (string, bool) {
	data, err := os.ReadFile(c.filePath(platform))
	if err != nil {
		return "", false
	}
	raw, err := hex.DecodeString(strings.TrimSpace(string(data)))
	if err != nil || len(raw) <= 12 {
		return "", false
	}
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return "", false
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", false
	}
	nonce := raw[:gcm.NonceSize()]
	ct := raw[gcm.NonceSize():]
	pt, err := gcm.Open(nil, nonce, ct, []byte(platform))
	if err != nil {
		return "", false
	}
	return string(pt), true
}

// Exists 是否存在。
func (c *CookieStore) Exists(platform string) bool {
	_, err := os.Stat(c.filePath(platform))
	return err == nil
}

// Delete 删除。
func (c *CookieStore) Delete(platform string) bool {
	err := os.Remove(c.filePath(platform))
	return err == nil
}

// DeleteAllPlatforms 删除指定平台（保留平台表合法校验由调用方完成）。
func (c *CookieStore) DeleteAllPlatforms(platforms []string) []string {
	var deleted []string
	for _, p := range platforms {
		if c.Delete(p) {
			deleted = append(deleted, p)
		}
	}
	return deleted
}

// Mask 生成脱敏预览（仅前缀与长度，不泄露明文）。
func (c *CookieStore) Mask(platform string) string {
	cookie, ok := c.Load(platform)
	if !ok {
		return ""
	}
	if len(cookie) <= 8 {
		return "****"
	}
	return cookie[:2] + "****" + cookie[len(cookie)-2:]
}

func (c *CookieStore) filePath(platform string) string {
	// platform 仅取白名单字符，防路径穿越
	safe := sanitizeFilename(platform)
	if safe == "" || safe == "video" {
		safe = "unknown"
	}
	return filepath.Join(c.dir, safe+".enc")
}

// detectPlatformCookie 平台 Cookie 特征识别（原逻辑精简保留）。
type cookieSignature struct {
	Name   string
	Weight int
}

var platformCookieSignatures = map[string][]cookieSignature{
	"bilibili": {
		{"SESSDATA", 5}, {"bili_jct", 5}, {"DedeUserID", 5}, {"buvid3", 4},
		{"b_nut", 4}, {"buvid4", 4}, {"bili_ticket", 4}, {"b_lsid", 3},
	},
	"youtube": {
		{"__Secure-1PSID", 5}, {"__Secure-3PSID", 5}, {"__Secure-1PAPISID", 5},
		{"LOGIN_INFO", 5}, {"VISITOR_INFO1_LIVE", 4}, {"SAPISID", 3},
		{"APISID", 3}, {"HSID", 2}, {"SSID", 2}, {"SID", 1}, {"YSC", 2},
		{"PREF", 1}, {"GPS", 2},
	},
	"instagram": {
		{"ds_user_id", 5}, {"ig_did", 5}, {"ig_nrcb", 4}, {"rur", 4},
		{"mid", 3}, {"datr", 2}, {"dpr", 1}, {"wd", 1},
	},
	"xiaohongshu": {
		{"web_session", 5}, {"xsec_token", 5}, {"a1", 4}, {"webId", 4},
		{"webBuild", 4}, {"gid", 4}, {"xhsTrackerId", 4}, {"abRequestId", 3},
	},
	"douyin": {
		{"sessionid_ss", 5}, {"msToken", 5}, {"ttwid", 5}, {"odin_tt", 5},
		{"passport_csrf_token", 5}, {"sid_guard", 4}, {"uid_tt", 4},
		{"sid_tt", 4}, {"sessionid", 3}, {"bd_ticket_guard_client_data", 4},
		{"dy_swidth", 2}, {"s_v_web_id", 3},
	},
	"likee": {
		{"likee_session", 6}, {"likee_uid", 6}, {"bigo_token", 5}, {"bigo_uid", 5},
		{"likee_country", 4}, {"likee", 3}, {"bigo", 3},
	},
}

// DetectCookiePlatform 识别 Cookie 所属平台 key。
func DetectCookiePlatform(cookieStr string) string {
	names := map[string]bool{}
	for _, part := range strings.Split(cookieStr, ";") {
		part = strings.TrimSpace(part)
		if part == "" || !strings.Contains(part, "=") {
			continue
		}
		name := strings.TrimSpace(part[:strings.Index(part, "=")])
		if name != "" {
			names[name] = true
		}
	}
	if len(names) == 0 {
		return ""
	}
	scores := map[string]int{}
	for platform, sigs := range platformCookieSignatures {
		for _, sig := range sigs {
			if names[sig.Name] {
				scores[platform] += sig.Weight
			}
		}
	}
	substringRules := []struct {
		Sub      string
		Platform string
		Weight   int
	}{
		{"bili", "bilibili", 2},
		{"likee", "likee", 3},
		{"bigo", "likee", 2},
		{"douyin", "douyin", 3},
		{"xhs", "xiaohongshu", 3},
	}
	for name := range names {
		lower := strings.ToLower(name)
		for _, rule := range substringRules {
			if strings.Contains(lower, rule.Sub) {
				scores[rule.Platform] += rule.Weight
			}
		}
	}
	best, bestScore := "", 0
	for platform, score := range scores {
		if score > bestScore {
			bestScore = score
			best = platform
		}
	}
	if bestScore < 3 {
		return ""
	}
	if _, ok := SupportedPlatforms[best]; !ok {
		return ""
	}
	return best
}

// WriteNetscapeCookieFile 生成 yt-dlp 用的 Netscape Cookie 文件（原逻辑）。
var netscapeBaseDomains = []string{
	"bilibili.com", "b23.tv", "douyin.com", "iesdouyin.com",
	"xiaohongshu.com", "xhslink.com",
	"likee.video", "instagram.com", "youtube.com", "google.com",
}

// WriteNetscapeCookieFile 将 cookie 串转为 Netscape 文件并返回路径。
func WriteNetscapeCookieFile(cookieStr, destPath string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return err
	}
	var lines []string
	lines = append(lines, "# Netscape HTTP Cookie File")
	lines = append(lines, "# Generated by video-downloader")
	var pairs [][2]string
	for _, part := range strings.Split(cookieStr, ";") {
		part = strings.TrimSpace(part)
		if part == "" || !strings.Contains(part, "=") {
			continue
		}
		idx := strings.Index(part, "=")
		name := strings.TrimSpace(part[:idx])
		value := strings.TrimSpace(part[idx+1:])
		if name != "" {
			pairs = append(pairs, [2]string{name, value})
		}
	}
	for _, pair := range pairs {
		name, value := pair[0], pair[1]
		for _, domain := range netscapeBaseDomains {
			lines = append(lines, fmt.Sprintf(".%s\tTRUE\t/\tTRUE\t9999999999\t%s\t%s", domain, name, value))
			lines = append(lines, fmt.Sprintf("%s\tFALSE\t/\tTRUE\t9999999999\t%s\t%s", domain, name, value))
		}
	}
	return os.WriteFile(destPath, []byte(strings.Join(lines, "\n")+"\n"), 0600)
}
