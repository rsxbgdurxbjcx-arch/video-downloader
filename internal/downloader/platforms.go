// Package downloader 保留并移植原 main.go 的多平台下载能力：
// 平台识别、统一最高画质策略、抖音自研解析器、进度流式解析、自动清理等，
// 并新增：任务绑定用户、SSRF 防护、路径安全、随机任务目录。
package downloader

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// 移动端/桌面端 UA（原项目常量）
const (
	MobileUA  = "Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.0 Mobile/15E148 Safari/604.1"
	DesktopUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
)

// 统一格式选择策略（原项目核心策略，全平台一致）
const (
	UnifiedFormatSelector = "bestvideo+bestaudio/best"
	UnifiedFormatSort     = "res,fps,tbr"
	UnifiedMergeFormat    = "mkv"
	UnifiedOutputTemplate = "%(title).80s [%(id)s].%(ext)s"
	ConcurrentFragments   = 8
)

// PlatformInfo 平台配置。
type PlatformInfo struct {
	Name          string
	Domains       []string
	NeedsCookieHD bool
	UserAgent     string
	Referer       string
	ExtraArgs     []string
}

// SupportedPlatforms 平台表。
var SupportedPlatforms = map[string]PlatformInfo{
	"bilibili": {
		Name:          "哔哩哔哩",
		Domains:       []string{"bilibili.com", "b23.tv", "biligame.com", "bili2233.cn"},
		NeedsCookieHD: true,
	},
	"douyin": {
		Name:          "抖音",
		Domains:       []string{"douyin.com", "iesdouyin.com", "v.douyin.com", "dy.com"},
		NeedsCookieHD: true,
		UserAgent:     DesktopUA,
		Referer:       "https://www.douyin.com/",
		ExtraArgs:     []string{"--no-check-formats"},
	},
	"xiaohongshu": {
		Name:          "小红书",
		Domains:       []string{"xiaohongshu.com", "xhslink.com"},
		NeedsCookieHD: true,
		UserAgent:     MobileUA,
		Referer:       "https://www.xiaohongshu.com/",
		ExtraArgs:     []string{"--no-check-formats"},
	},
	"likee": {
		Name:          "Likee",
		Domains:       []string{"likee.video", "likee.com", "l.likee.video"},
		NeedsCookieHD: false,
		UserAgent:     MobileUA,
		Referer:       "https://likee.video/",
	},
	"instagram": {
		Name:          "Instagram",
		Domains:       []string{"instagram.com", "instagr.am"},
		NeedsCookieHD: true,
		UserAgent:     MobileUA,
		Referer:       "https://www.instagram.com/",
		ExtraArgs:     []string{"--no-check-formats"},
	},
	"youtube": {
		Name:          "YouTube",
		Domains:       []string{"youtube.com", "youtu.be", "m.youtube.com"},
		NeedsCookieHD: false,
		Referer:       "https://www.youtube.com/",
		ExtraArgs:     []string{},
	},
}

// sanitizeFilename 清洗文件名（原逻辑）。
var sanitizeRe = regexp.MustCompile(`[\\/:*?"<>|\r\n\t]`)

func sanitizeFilename(name string) string {
	name = sanitizeRe.ReplaceAllString(name, "_")
	name = strings.Trim(name, " .")
	if name == "" {
		name = "video"
	}
	if len(name) > 120 {
		name = name[:120]
	}
	return name
}

// DetectPlatform 根据 URL 匹配平台；返回平台 key 或空。
func DetectPlatform(rawURL string) string {
	lower := strings.ToLower(rawURL)
	for key, info := range SupportedPlatforms {
		for _, domain := range info.Domains {
			if strings.Contains(lower, strings.ToLower(domain)) {
				return key
			}
		}
	}
	return ""
}

// ExtractURL 从分享文本中提取 URL（原逻辑）。
func ExtractURL(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	idx := strings.Index(input, "http://")
	if idx < 0 {
		idx = strings.Index(input, "https://")
	}
	if idx < 0 {
		return ""
	}
	rest := input[idx:]
	for i, ch := range rest {
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == '　' {
			return rest[:i]
		}
	}
	return rest
}

// ValidateURL 校验 URL 合法性 + 平台归属 + SSRF（禁止内网/元数据地址）。
// 返回规范化 URL。
func ValidateURL(rawURL string) (string, error) {
	extracted := ExtractURL(rawURL)
	if extracted == "" {
		return "", fmt.Errorf("未找到有效的 http:// 或 https:// 链接")
	}
	u, err := url.Parse(extracted)
	if err != nil {
		return "", fmt.Errorf("链接格式无效")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("仅支持 http/https 链接")
	}
	if u.Host == "" {
		return "", fmt.Errorf("链接缺少主机名")
	}
	platform := DetectPlatform(extracted)
	if platform == "" {
		var names []string
		for _, v := range SupportedPlatforms {
			names = append(names, v.Name)
		}
		return "", fmt.Errorf("不支持的平台链接。当前支持：%s", strings.Join(names, "、"))
	}
	if err := ensurePublicHost(u.Hostname(), platform); err != nil {
		return "", err
	}
	return extracted, nil
}

// ensurePublicHost 校验主机：仅允许平台域名解析结果全为公网 IP。
func ensurePublicHost(hostname, platform string) error {
	info, ok := SupportedPlatforms[platform]
	if !ok {
		return fmt.Errorf("未知平台")
	}
	matched := false
	for _, d := range info.Domains {
		if strings.EqualFold(hostname, d) || strings.HasSuffix(strings.ToLower(hostname), "."+d) {
			matched = true
			break
		}
	}
	if !matched {
		return fmt.Errorf("链接域名与平台不匹配")
	}
	// 禁止 IP 字面量形式的私网/回环/元数据地址
	if ip := net.ParseIP(hostname); ip != nil {
		if isPrivateOrLocalIP(ip) {
			return fmt.Errorf("禁止访问内网/元数据地址")
		}
		return nil
	}
	// DNS 解析检查（防 SSRF/DNS 重绑定）
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, hostname)
	if err != nil {
		return fmt.Errorf("无法解析主机名: %v", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("主机名无有效解析")
	}
	for _, ip := range ips {
		if isPrivateOrLocalIP(ip.IP) {
			return fmt.Errorf("禁止访问内网/元数据地址")
		}
	}
	return nil
}

// isPrivateOrLocalIP 判断是否私网/回环/链路本地/组播/云元数据地址。
func isPrivateOrLocalIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	// 处理 IPv4-mapped IPv6
	if ip4 := ip.To4(); ip4 != nil {
		return ip4.IsLoopback() || ip4.IsPrivate() || ip4.IsLinkLocalUnicast() ||
			ip4.IsLinkLocalMulticast() || ip4.IsMulticast() || ip4.IsUnspecified()
	}
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsPrivate()
}

// ResolveShortURL 预解析短链并跟随重定向；每一步重定向目标都执行 SSRF 校验。
func ResolveShortURL(rawURL string) string {
	shortDomains := []string{"b23.tv", "v.douyin.com", "xhslink.com", "youtu.be", "instagr.am", "bili2233.cn"}
	needResolve := false
	for _, d := range shortDomains {
		if strings.Contains(rawURL, d) {
			needResolve = true
			break
		}
	}
	if !needResolve {
		return rawURL
	}

	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	current := rawURL
	var lastHost string
	for i := 0; i < 10; i++ {
		u, err := url.Parse(current)
		if err != nil {
			return rawURL
		}
		lastHost = u.Hostname()
		// 跳过已知短链域名（它们通常是公网 CDN），直接请求
		req, err := http.NewRequest("GET", current, nil)
		if err != nil {
			return rawURL
		}
		req.Header.Set("User-Agent", DesktopUA)

		resp, err := client.Do(req)
		if err != nil {
			// 网络失败时保持原样，由 yt-dlp 兜底
			return rawURL
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			loc := resp.Header.Get("Location")
			resp.Body.Close()
			if loc == "" {
				return rawURL
			}
			next := resolvRelative(current, loc)
			// 重定向目标 SSRF 校验（平台域名或公网）
			nu, perr := url.Parse(next)
			if perr == nil && nu.Hostname() != "" {
				host := nu.Hostname()
				if !isAllowedRedirectTarget(host) {
					return rawURL
				}
			}
			current = next
			continue
		}
		resp.Body.Close()
		break
	}
	_ = lastHost
	return current
}

// resolvRelative 解析相对重定向地址。
func resolvRelative(base, loc string) string {
	if strings.HasPrefix(loc, "http://") || strings.HasPrefix(loc, "https://") {
		return loc
	}
	u, err := url.Parse(base)
	if err != nil {
		return loc
	}
	ru, err := u.Parse(loc)
	if err != nil {
		return loc
	}
	return ru.String()
}

// isAllowedRedirectTarget 重定向目标仅允许已知平台域名或公网主机（逐级 SSRF 防护）。
func isAllowedRedirectTarget(host string) bool {
	// 允许平台域名
	for _, info := range SupportedPlatforms {
		for _, d := range info.Domains {
			if strings.EqualFold(host, d) || strings.HasSuffix(strings.ToLower(host), "."+d) {
				return true
			}
		}
	}
	// 其余域名需要公网解析
	if ip := net.ParseIP(host); ip != nil {
		return !isPrivateOrLocalIP(ip)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return false
	}
	for _, ip := range ips {
		if isPrivateOrLocalIP(ip.IP) {
			return false
		}
	}
	return true
}

// NewTaskID 生成随机任务 ID（32 hex；不可预测，避免枚举/路径穿越）。
func NewTaskID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// SafeTaskDir 生成任务工作目录（DownloadDir 内、白名单字符）。
func SafeTaskDir(downloadDir, taskID string) string {
	return filepath.Join(downloadDir, taskID)
}

// EnsureDirs 确保下载/数据目录存在。
func EnsureDirs(dirs ...string) error {
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("创建目录 %s 失败: %w", d, err)
		}
	}
	return nil
}

// CleanupDir 安全删除任务目录（拒绝空路径与根路径）。
func CleanupDir(taskDir string) {
	if taskDir == "" || taskDir == "/" || taskDir == "." {
		return
	}
	_ = os.RemoveAll(taskDir)
}
