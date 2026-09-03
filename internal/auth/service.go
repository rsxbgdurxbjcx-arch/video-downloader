// Package auth 认证服务：注册、邮箱验证（6 位数字验证码）、登录、会话。
package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"video-downloader/internal/config"
	"video-downloader/internal/email"
	"video-downloader/internal/models"
	"video-downloader/internal/repository"
)

// 业务错误
var (
	ErrInvalidCredentials = errors.New("邮箱或密码错误，或者账户尚未完成验证") // 防止枚举的统一错误
	ErrEmailUnverified    = errors.New("账户尚未完成邮箱验证")
	ErrAccountDisabled    = errors.New("账户已被禁用")
	ErrRateLimited        = errors.New("操作过于频繁，请稍后再试")
	ErrTokenInvalid       = errors.New("验证码无效或已过期")
	ErrTokenExpired       = errors.New("验证码已过期")
	ErrTokenUsed          = errors.New("验证码已被使用")
	ErrTooManyAttempts    = errors.New("验证码错误次数过多，请重新发送")
	ErrMailNotConfigured  = errors.New("邮件服务未配置，请联系管理员")
	ErrEmailSendFailed    = errors.New("验证邮件发送失败，请稍后重试")
	// ErrDuplicate 邮箱已注册（对外层统一转换为通用提示，防枚举）。
	ErrDuplicate = errors.New("邮箱已注册")
)

// Service 认证与用户服务。
type Service struct {
	store        *repository.Store
	cfg          *config.Config
	mailer       *email.Sender
	loginLimiter *LoginLimiter
	rateLimiter  *email.RateLimiter
	// OnCodeGenerated 仅测试/开发环境捕获验证码（生产环境绝不设置）。
	OnCodeGenerated func(code string)
}

// NewService 创建认证服务。
func NewService(store *repository.Store, cfg *config.Config, mailer *email.Sender) *Service {
	return &Service{
		store:        store,
		cfg:          cfg,
		mailer:       mailer,
		loginLimiter: NewLoginLimiter(),
		rateLimiter:  email.NewRateLimiter(cfg, store),
	}
}

// RegisterResult 注册结果。
type RegisterResult struct {
	User      *models.User
	NeedEmail bool // 是否实际发送了验证邮件（开发环境输出验证码）
}

// Register 邮箱注册：规范化邮箱 → 校验 → 创建 pending 用户 → 生成 6 位验证码并发送邮件。
// 若邮箱已存在，返回 ErrDuplicate（调用方转换为与成功一致的通用提示，防枚举）。
func (s *Service) Register(ctx context.Context, rawEmail, password, ip string) (*RegisterResult, error) {
	emailNorm := NormalizeEmail(rawEmail)
	if err := ValidateEmail(emailNorm); err != nil {
		return nil, err
	}
	if password == "" {
		return nil, errors.New("密码不能为空")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	user, err := s.store.CreateUser(ctx, emailNorm, hash, models.RoleUser, models.StatusPending)
	if err != nil {
		if errors.Is(err, repository.ErrDuplicate) {
			return nil, ErrDuplicate
		}
		return nil, err
	}
	// 生成验证码并发送邮件（失败不删除用户，用户可稍后重发）
	if err := s.SendVerification(ctx, user, models.EmailPurposeRegister, ip); err != nil {
		if errors.Is(err, ErrMailNotConfigured) && s.cfg.IsDevelopment() {
			// 开发模式允许控制台提示（生产由 main 启动校验兜底）
			return &RegisterResult{User: user, NeedEmail: true}, nil
		}
		return &RegisterResult{User: user, NeedEmail: false}, ErrEmailSendFailed
	}
	return &RegisterResult{User: user, NeedEmail: true}, nil
}

// SendVerification 生成并发送 6 位数字验证码（注册或重发）。
// 限流：同一邮箱冷却期（默认 60s）、每邮箱每小时上限（默认 6 次）、同一 IP 每小时上限；
// 使用 Redis（REDIS_URL）计数，Redis 不可用时降级数据库统计。
func (s *Service) SendVerification(ctx context.Context, user *models.User, purpose, ip string) error {
	// 生产环境邮件推送未配置：拒绝发送（不静默绕过邮箱验证）
	if !s.mailer.Enabled() {
		if s.cfg.IsProduction() {
			return ErrMailNotConfigured
		}
		// 开发环境：控制台输出验证码（不含日志文件）
	}

	if err := s.checkEmailRate(ctx, user, ip); err != nil {
		return err
	}

	code, err := GenerateVerifyCode()
	if err != nil {
		return err
	}
	tokenHash := HashTokenData(code)
	expiresAt := time.Now().Add(s.cfg.VerifyTokenTTL())

	// 作废旧验证码，签发新验证码
	if err := s.store.InvalidteUserTokens(ctx, user.ID); err != nil {
		return fmt.Errorf("作废旧验证码失败: %w", err)
	}
	if err := s.store.CreateVerifyToken(ctx, user.ID, tokenHash, HashTokenData(ip), expiresAt); err != nil {
		return fmt.Errorf("保存验证码失败: %w", err)
	}

	if s.OnCodeGenerated != nil {
		s.OnCodeGenerated(code)
	}
	emailHash := HashEmail(user.Email)
	// 通过 Spug 推送助手官方邮件模板发送（正文由模板渲染，含 scene/code/minute）
	if err := s.mailer.SendVerifyCode(user.Email, verifyScene(purpose), code, s.cfg.EmailVerifyExpireMinutes); err != nil {
		// 发送失败：验证码保留（用户可重发，重发会作废旧码再签新码）
		if s.cfg.IsDevelopment() && !s.mailer.Enabled() {
			log.Printf("[auth][dev] 邮箱验证码（仅开发环境，生产环境不输出）: %s", code)
		} else {
			log.Printf("[auth] 验证邮件发送失败（不含验证码/邮箱正文）: %v", err)
		}
		// 发送日志（管理员后台可查看精确原因；err 已脱敏，不含凭据）
		_ = s.store.CreateEmailSendLog(ctx, emailHash, purpose, false, middlewareRedact(err.Error()))
		return ErrEmailSendFailed
	}
	if s.cfg.IsDevelopment() {
		log.Printf("[auth][dev] 已通过 Spug 推送助手发送验证邮件（正文不落日志）")
	}
	_ = s.store.CreateEmailSendLog(ctx, emailHash, purpose, true, "")

	// 记录脱敏发送记录（仅当发送成功）
	if err := s.store.CreateEmailSendRecord(ctx, emailHash, &user.ID, purpose, HashTokenData(ip)); err != nil {
		log.Printf("[auth] 记录邮件发送记录失败: %v", err)
	}
	return nil
}

// verifyScene 依据邮件用途生成 Spug 官方模板的「验证场景」参数
// （≤12 字符，不得包含链接或域名）。
func verifyScene(purpose string) string {
	switch purpose {
	case models.EmailPurposeRegister:
		return "邮箱注册验证"
	case models.EmailPurposeResetPassword:
		return "重置登录密码"
	default: // resend_verification / 其他
		return "邮箱验证"
	}
}

// middlewareRedact 发送日志错误信息脱敏（截断 + 抹除敏感关键字）。
func middlewareRedact(s string) string {
	if len(s) > 500 {
		s = s[:500]
	}
	for _, p := range []string{"SPUG_MAIL_TEMPLATE_CODE", "TEMPLATE_CODE", "Authorization:", "password=", "token=", "Cookie:"} {
		if i := strings.Index(strings.ToLower(s), strings.ToLower(p)); i >= 0 {
			return s[:i] + p + "[redacted]"
		}
	}
	return s
}

// checkEmailRate 邮件发送限流（Redis 优先，数据库降级）。
func (s *Service) checkEmailRate(ctx context.Context, user *models.User, ip string) error {
	err := s.rateLimiter.Check(ctx, HashEmail(user.Email), HashTokenData(ip))
	if err != nil {
		if errors.Is(err, email.ErrMailRateLimited) {
			return ErrRateLimited
		}
		return fmt.Errorf("查询邮件计数失败: %w", err)
	}
	return nil
}

// CooldownRemaining 后端计时的重发冷却剩余秒数（0 表示可发送）。
// 前端以此为准展示 60 秒倒计时（基于服务器时间，切换后台/恢复后依然准确）。
func (s *Service) CooldownRemaining(ctx context.Context, email string) int {
	return s.rateLimiter.CooldownRemaining(ctx, HashEmail(email))
}

// VerifyEmailResult 邮箱验证结果。
type VerifyEmailResult struct {
	User *models.User
}

// VerifyEmail 校验邮箱 + 6 位数字验证码（注册后验证邮箱）：
//   - 验证码只存哈希；比对采用常数时间比较；错误次数超过上限（默认 5 次）立即作废；
//   - 验证成功：事务内激活账户并【立即删除】全部未用验证码（单次使用）。
func (s *Service) VerifyEmail(ctx context.Context, rawEmail, code string) (*VerifyEmailResult, error) {
	emailNorm := NormalizeEmail(rawEmail)
	if emailNorm == "" || !ValidateVerifyCode(code) {
		return nil, ErrTokenInvalid
	}
	user, err := s.store.GetUserByEmail(ctx, emailNorm)
	if err != nil {
		// 邮箱不存在/未注册：通用失败（防枚举）
		return nil, ErrTokenInvalid
	}
	// 校验验证码（含错误次数限制）
	if err := s.checkCode(ctx, user.ID, code); err != nil {
		return nil, err
	}
	tok, err := s.store.GetLatestPendingToken(ctx, user.ID, time.Now())
	if err != nil {
		return nil, ErrTokenInvalid
	}

	// 事务：激活账户 + 删除全部未用验证码（"验证成功后立即删除验证码"）
	tx, err := s.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`UPDATE email_verification_tokens SET used_at=? WHERE id=? AND used_at IS NULL`,
		time.Now().Unix(), tok.ID); err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET email_verified_at=?, status='active', updated_at=? WHERE id=?`,
		now, now, user.ID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM email_verification_tokens WHERE user_id=? AND used_at IS NULL`,
		user.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &VerifyEmailResult{User: user}, nil
}

// checkCode 校验用户最新未用未过期验证码（常数时间比较 + 错误次数限制）。
// 只做校验与尝试计数，不做任何副作用（激活/删码由调用方按用途处理）。
func (s *Service) checkCode(ctx context.Context, userID int64, code string) error {
	if !ValidateVerifyCode(code) {
		return ErrTokenInvalid
	}
	tok, err := s.store.GetLatestPendingToken(ctx, userID, time.Now())
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return ErrTokenInvalid
		}
		return err
	}
	if tok.UsedAt != nil {
		return ErrTokenUsed
	}
	if time.Now().After(tok.ExpiresAt) {
		return ErrTokenExpired
	}
	if subtle.ConstantTimeCompare([]byte(tok.TokenHash), []byte(HashTokenData(code))) != 1 {
		attempts, aerr := s.store.IncrementVerifyAttempts(ctx, tok.ID)
		if aerr != nil {
			return aerr
		}
		if attempts >= s.cfg.VerifyCodeMaxAttempts {
			// 超过上限：作废验证码，用户需重新发送
			_ = s.store.MarkVerifyTokenUsed(ctx, tok.ID)
			return ErrTooManyAttempts
		}
		return ErrTokenInvalid
	}
	return nil
}

// Login 邮箱+密码登录（无需验证码；未完成邮箱验证的账户也可登录，功能受限）。
// 统一错误信息防枚举。成功返回会话令牌（调用方负责写 HttpOnly Cookie）与用户。
func (s *Service) Login(ctx context.Context, rawEmail, password, ip, userAgent string) (string, *models.User, error) {
	emailNorm := NormalizeEmail(rawEmail)
	if emailNorm == "" || password == "" {
		return "", nil, ErrInvalidCredentials
	}
	// 登录限流（按 email + IP）
	if !s.loginLimiter.Allow(emailNorm, ip) {
		return "", nil, ErrRateLimited
	}

	user, err := s.store.GetUserByEmail(ctx, emailNorm)
	if err != nil {
		// 统一失败信息 + 记录失败
		s.loginLimiter.RecordFailure(emailNorm, ip)
		return "", nil, ErrInvalidCredentials
	}
	if user.Status == models.StatusDisabled {
		s.loginLimiter.RecordFailure(emailNorm, ip)
		return "", nil, ErrAccountDisabled
	}
	if !CheckPassword(user.PasswordHash, password) {
		s.loginLimiter.RecordFailure(emailNorm, ip)
		return "", nil, ErrInvalidCredentials
	}

	sessionToken, err := RandomToken()
	if err != nil {
		return "", nil, err
	}
	if err := s.store.CreateSession(ctx, user.ID, HashTokenData(sessionToken), sanitizeUA(userAgent), HashTokenData(ip), time.Now().Add(s.cfg.SessionTTL())); err != nil {
		return "", nil, fmt.Errorf("创建会话失败: %w", err)
	}
	if err := s.store.UpdateLastLogin(ctx, user.ID); err != nil {
		log.Printf("[auth] 更新登录时间失败: %v", err)
	}
	return sessionToken, user, nil
}

// Logout 退出当前会话。
func (s *Service) Logout(ctx context.Context, sessionToken string) error {
	if sessionToken == "" {
		return nil
	}
	return s.store.DeleteSession(ctx, HashTokenData(sessionToken))
}

// LogoutAll 退出用户全部会话。
func (s *Service) LogoutAll(ctx context.Context, userID int64) error {
	return s.store.DeleteUserSessions(ctx, userID)
}

// ChangePassword 修改密码（登录后）：邮箱验证码 + 新密码（无需旧密码）。
// 校验验证码 → 更新密码 → 立即删除全部未用验证码 → 注销全部旧会话。
func (s *Service) ChangePassword(ctx context.Context, user *models.User, code, newPassword string) error {
	if !ValidatePassword(newPassword) {
		return ErrWeakPassword
	}
	if err := s.checkCode(ctx, user.ID, code); err != nil {
		return err
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	tx, err := s.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET password_hash=?, updated_at=? WHERE id=?`,
		hash, time.Now().Unix(), user.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM email_verification_tokens WHERE user_id=? AND used_at IS NULL`,
		user.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, user.ID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// ForgotPassword 忘记密码（公开）：若邮箱已注册且未被禁用，发送 6 位验证码。
// 邮箱不存在/被禁用时同样返回通用成功提示（防枚举）。
func (s *Service) ForgotPassword(ctx context.Context, rawEmail, ip string) error {
	emailNorm := NormalizeEmail(rawEmail)
	if emailNorm == "" {
		return ErrTokenInvalid
	}
	user, err := s.store.GetUserByEmail(ctx, emailNorm)
	if err != nil || user.Status == models.StatusDisabled {
		// 防枚举：不存在的邮箱同样视为"已受理"（不返回任何区别信息）
		log.Printf("[auth] forgot-password: 邮箱未注册或已禁用，未发送验证码（防枚举）: %s", emailNorm)
		return nil
	}
	log.Printf("[auth] forgot-password: 已向 %s 发送重置验证码", emailNorm)
	return s.SendVerification(ctx, user, models.EmailPurposeResetPassword, ip)
}

// ResetPassword 重置密码（公开）：邮箱 + 验证码 + 新密码。
// 校验验证码 → 更新密码 → 立即删除验证码 → 注销全部会话（不改变邮箱验证状态）。
func (s *Service) ResetPassword(ctx context.Context, rawEmail, code, newPassword string) error {
	emailNorm := NormalizeEmail(rawEmail)
	if emailNorm == "" || !ValidateVerifyCode(code) {
		return ErrTokenInvalid
	}
	user, err := s.store.GetUserByEmail(ctx, emailNorm)
	if err != nil || user.Status == models.StatusDisabled {
		// 防枚举：通用失败
		return ErrTokenInvalid
	}
	if !ValidatePassword(newPassword) {
		return ErrWeakPassword
	}
	if err := s.checkCode(ctx, user.ID, code); err != nil {
		return err
	}
	hash, err := HashPassword(newPassword)
	if err != nil {
		return err
	}
	tx, err := s.store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`UPDATE users SET password_hash=?, updated_at=? WHERE id=?`,
		hash, time.Now().Unix(), user.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM email_verification_tokens WHERE user_id=? AND used_at IS NULL`,
		user.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, user.ID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// UserBySession 根据会话 Token 查找用户（校验过期。
func (s *Service) UserBySession(ctx context.Context, sessionToken string) (*models.User, error) {
	if sessionToken == "" {
		return nil, ErrInvalidCredentials
	}
	userID, err := s.store.GetSessionByHash(ctx, HashTokenData(sessionToken))
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	user, err := s.store.GetUserByID(ctx, *userID)
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	if user.Status == models.StatusDisabled {
		return nil, ErrAccountDisabled
	}
	_ = s.store.TouchSession(ctx, HashTokenData(sessionToken))
	return user, nil
}

// sanitizeUA 截断并清洗 User-Agent（防止控制字符进入数据库）。
func sanitizeUA(ua string) string {
	ua = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, ua)
	if len(ua) > 512 {
		ua = ua[:512]
	}
	return ua
}

// LoginLimiter 登录失败限流：内存滑动窗口 + 临时锁定。
type LoginLimiter struct {
	mu      sync.Mutex
	entries map[string]*loginEntry
}

type loginEntry struct {
	failures    int
	lastFail    time.Time
	blockedUntil time.Time
}

// NewLoginLimiter 创建登录限流器。
func NewLoginLimiter() *LoginLimiter {
	l := &LoginLimiter{
		entries: map[string]*loginEntry{},
	}
	go l.cleanupLoop()
	return l
}

func (l *LoginLimiter) key(email, ip string) string { return email + "|" + ip }

// Allow 检查是否允许尝试登录。
func (l *LoginLimiter) Allow(email, ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[l.key(email, ip)]
	if !ok {
		return true
	}
	if time.Now().Before(e.blockedUntil) {
		return false
	}
	return true
}

// RecordFailure 记录失败（连续 5 次后锁定 15 分钟）。
func (l *LoginLimiter) RecordFailure(email, ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	k := l.key(email, ip)
	e, ok := l.entries[k]
	if !ok {
		e = &loginEntry{}
		l.entries[k] = e
	}
	e.failures++
	e.lastFail = time.Now()
	if e.failures >= 5 {
		e.blockedUntil = time.Now().Add(15 * time.Minute)
		e.failures = 0
	}
}

func (l *LoginLimiter) cleanupLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		l.mu.Lock()
		now := time.Now()
		for k, e := range l.entries {
			if now.Sub(e.lastFail) > 30*time.Minute && now.After(e.blockedUntil) {
				delete(l.entries, k)
			}
		}
		l.mu.Unlock()
	}
}
