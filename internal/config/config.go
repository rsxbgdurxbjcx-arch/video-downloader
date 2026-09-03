// Package config 负责从环境变量加载并校验应用配置。
// 所有密钥均从环境变量读取；生产环境对不安全默认值执行拒绝启动校验。
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"video-downloader/internal/redisx"
)

// DefaultAdminPassword 是默认管理员初始密码标记，生产环境必须修改。
const DefaultAdminPassword = "change-me"

// MailAPIBaseDefault Spug 推送助手 API 默认地址（官方文档：https://push.spug.cc/guide/mail）。
const MailAPIBaseDefault = "https://push.spug.cc"

// Config 应用配置。
type Config struct {
	AppEnv     string // development | production | test
	AppAddr    string
	AppBaseURL string

	DatabaseURL string

	SessionSecret       string
	CookieEncryptionKey string // 32 字节 hex；用于加密下载平台 Cookie

	AdminInitialEmail    string
	AdminInitialPassword string

	MailEnabled bool   // SPUG_MAIL_ENABLED：是否启用 Spug 推送助手邮件验证码
	MailAPIBase string // SPUG_MAIL_BASE_URL：Spug 推送助手 API 地址（默认 https://push.spug.cc）
	MailTemplateCode string // SPUG_MAIL_TEMPLATE_CODE：Spug 官方邮件模板编码（调用凭证，勿入日志/仓库）

	EmailVerifyExpireMinutes int // 验证码有效期（默认 5 分钟）
	EmailSendCooldownSeconds int // 同一邮箱发送冷却（默认 60 秒）
	EmailHourlyLimit         int // 同一邮箱每小时最多发送次数（默认 6）
	EmailIPHourlyLimit       int // 同一 IP 每小时最多发送次数（默认 30，防换邮箱刷邮件）

	VerifyCodeMaxAttempts int // 验证码错误允许的最大尝试次数（默认 5，超限作废）

	RedisURL string // 可选；配置后邮件限流使用 Redis 计数，未配置/不可用时降级数据库

	PaymentEnabled  bool
	PaymentProvider string // mock（仅开发环境） | manual

	DownloadDir string
	CookieDir   string
	MaxDownloadSize int64 // 0 表示不限

	RateLimitEnabled bool
	CSRFEnabled      bool

	HTTPOnly bool // 兼容旧部署：仅 HTTP 模式（不自动生成自签名证书）

	// AllowedOrigins CORS 白名单（逗号分隔的完整 Origin，如 https://a.com,https://b.com）
	AllowedOrigins []string

	YTDLPPath    string
	FFmpegPath   string
	FFprobePath  string

	TaskExpireHours int
}

// IsProduction 是否为生产环境。
func (c *Config) IsProduction() bool { return c.AppEnv == "production" }

// IsDevelopment 是否为开发环境。
func (c *Config) IsDevelopment() bool { return c.AppEnv == "development" }

// loadEnv 读取环境变量（带默认值）。
func loadEnv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func loadEnvBool(key string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func loadEnvInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func loadEnvInt64(key string, def int64) int64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}

// Load 从环境变量加载配置并执行基础校验。
func Load() (*Config, error) {
	cfg := &Config{
		AppEnv:     loadEnv("APP_ENV", "development"),
		// 默认监听 80（HTTP 默认端口）：Cloudflare 代理回源与服务器 IP 直连均无需填写端口
		AppAddr:    loadEnv("APP_ADDR", ":80"),
		AppBaseURL: strings.TrimRight(loadEnv("APP_BASE_URL", "http://localhost"), "/"),

		DatabaseURL: loadEnv("DATABASE_URL", "./data/app.db"),

		SessionSecret:       loadEnv("SESSION_SECRET", ""),
		CookieEncryptionKey: loadEnv("COOKIE_ENCRYPTION_KEY", ""),

		AdminInitialEmail:    strings.ToLower(loadEnv("ADMIN_INITIAL_EMAIL", "admin@example.com")),
		AdminInitialPassword: loadEnv("ADMIN_INITIAL_PASSWORD", DefaultAdminPassword),

		MailEnabled:      loadEnvBool("SPUG_MAIL_ENABLED", false),
		MailAPIBase:      strings.TrimRight(loadEnv("SPUG_MAIL_BASE_URL", MailAPIBaseDefault), "/"),
		MailTemplateCode: loadEnv("SPUG_MAIL_TEMPLATE_CODE", ""),
		EmailVerifyExpireMinutes: loadEnvInt("EMAIL_VERIFICATION_EXPIRE_MINUTES", 5),
		EmailSendCooldownSeconds: loadEnvInt("EMAIL_SEND_COOLDOWN_SECONDS", 60),
		EmailHourlyLimit:         loadEnvInt("EMAIL_HOURLY_LIMIT", 6),
		EmailIPHourlyLimit:       loadEnvInt("EMAIL_IP_HOURLY_LIMIT", 30),

		VerifyCodeMaxAttempts: loadEnvInt("VERIFY_CODE_MAX_ATTEMPTS", 5),

		RedisURL: loadEnv("REDIS_URL", ""),

		PaymentEnabled:  loadEnvBool("PAYMENT_ENABLED", false),
		PaymentProvider: loadEnv("PAYMENT_PROVIDER", "mock"),

		DownloadDir:      loadEnv("DOWNLOAD_DIR", "./downloads"),
		CookieDir:        loadEnv("COOKIE_DIR", "./cookies_store"),
		MaxDownloadSize:  loadEnvInt64("MAX_DOWNLOAD_SIZE", 0),
		RateLimitEnabled: loadEnvBool("RATE_LIMIT_ENABLED", true),
		CSRFEnabled:      loadEnvBool("CSRF_ENABLED", true),

		HTTPOnly: loadEnvBool("HTTP_ONLY", false),

		AllowedOrigins: parseOrigins(os.Getenv("CORS_ALLOWED_ORIGINS")),

		YTDLPPath:   loadEnv("YTDLP_PATH", "yt-dlp"),
		FFmpegPath:  loadEnv("FFMPEG_PATH", "ffmpeg"),
		FFprobePath: loadEnv("FFPROBE_PATH", "ffprobe"),

		TaskExpireHours: loadEnvInt("TASK_EXPIRE_HOURS", 6),
	}

	// 邮件参数兜底（管理员误配导致 0/负数/极端值时不生效）
	if cfg.EmailVerifyExpireMinutes <= 0 || cfg.EmailVerifyExpireMinutes > 60 {
		cfg.EmailVerifyExpireMinutes = 5
	}
	if cfg.EmailSendCooldownSeconds < 0 {
		cfg.EmailSendCooldownSeconds = 0
	}
	if cfg.EmailHourlyLimit <= 0 || cfg.EmailHourlyLimit > 100 {
		cfg.EmailHourlyLimit = 6
	}
	if cfg.EmailIPHourlyLimit <= 0 || cfg.EmailIPHourlyLimit > 1000 {
		cfg.EmailIPHourlyLimit = 30
	}
	if cfg.VerifyCodeMaxAttempts <= 0 || cfg.VerifyCodeMaxAttempts > 20 {
		cfg.VerifyCodeMaxAttempts = 5
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate 执行配置合法性校验；生产环境额外执行安全校验。
func (c *Config) Validate() error {
	if c.AppEnv != "development" && c.AppEnv != "production" && c.AppEnv != "test" {
		return fmt.Errorf("无效的 APP_ENV=%q（允许 development/production/test）", c.AppEnv)
	}
	if c.AppBaseURL == "" {
		return errors.New("APP_BASE_URL 不能为空")
	}
	if c.CookieEncryptionKey == "" {
		if c.IsProduction() {
			return errors.New("生产环境必须设置 COOKIE_ENCRYPTION_KEY（32 字节随机密钥，hex 编码）")
		}
		fmt.Fprintln(os.Stderr, "[warn] COOKIE_ENCRYPTION_KEY 未设置，开发环境将使用内置示例密钥（不可用于生产）")
	}
	if c.SessionSecret == "" {
		if c.IsProduction() {
			return errors.New("生产环境必须设置 SESSION_SECRET（至少 32 字节随机字符串）")
		}
		fmt.Fprintln(os.Stderr, "[warn] SESSION_SECRET 未设置，开发环境会话密钥使用随机值（重启后会话失效）")
	}
	// 生产环境默认管理员密码检测：拒绝启动
	if c.IsProduction() {
		if c.AdminInitialPassword == DefaultAdminPassword || c.AdminInitialPassword == "" {
			return errors.New("生产环境必须修改 ADMIN_INITIAL_PASSWORD（不能使用默认密码 change-me）")
		}
		if len(c.SessionSecret) < 32 {
			return errors.New("生产环境 SESSION_SECRET 长度必须 ≥ 32 字符")
		}
		if len(c.CookieEncryptionKey) < 32 {
			return errors.New("生产环境 COOKIE_ENCRYPTION_KEY 长度必须 ≥ 32 字符")
		}
		if c.PaymentProvider == "mock" && c.PaymentEnabled {
			return errors.New("生产环境禁止启用 PAYMENT_PROVIDER=mock（PAYMENT_ENABLED 必须为 false）")
		}
	}
	// APP_BASE_URL 仅用于生成站内支付跳转链接；
	// 邮箱验证方式为 6 位数字验证码（POST body 提交，不走 URL），
	// 因此不再强制生产环境 HTTPS（裸 IP/HTTP 服务器也可正常部署）。
	// Spug 推送助手邮件验证码（https://push.spug.cc/guide/mail）：
	// 模板编码（TEMPLATE_CODE）是调用凭证，仅保存在服务器本地 .env，绝不进入日志与 Git 仓库。
	if c.MailEnabled {
		if c.MailAPIBase == "" {
			return errors.New("SPUG_MAIL_ENABLED=true 时必须设置 SPUG_MAIL_BASE_URL")
		}
		if c.MailTemplateCode == "" {
			return errors.New("SPUG_MAIL_ENABLED=true 时必须设置 SPUG_MAIL_TEMPLATE_CODE（Spug 控制台 → 验证码 → 邮件 → 选中官方模板后复制「模板编码」）")
		}
	}
	if c.RedisURL != "" {
		if _, err := redisx.ParseURL(c.RedisURL); err != nil {
			return fmt.Errorf("REDIS_URL 无效: %w", err)
		}
	}
	return nil
}

// parseOrigins 解析 CORS 白名单。
func parseOrigins(raw string) []string {
	var out []string
	for _, o := range strings.Split(raw, ",") {
		o = strings.TrimSpace(o)
		if o != "" && strings.HasPrefix(o, "http") {
			out = append(out, o)
		}
	}
	return out
}

// SessionTTL 会话有效期。
func (c *Config) SessionTTL() time.Duration { return 7 * 24 * time.Hour }

// VerifyTokenTTL 邮箱验证令牌有效期。
func (c *Config) VerifyTokenTTL() time.Duration {
	return time.Duration(c.EmailVerifyExpireMinutes) * time.Minute
}

// OrderTTL 订单有效期。
func (c *Config) OrderTTL() time.Duration { return 20 * time.Minute }
