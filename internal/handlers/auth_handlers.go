package handlers

import (
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"video-downloader/internal/auth"
	"video-downloader/internal/middleware"
	"video-downloader/internal/models"
)

const (
	sessionCookieName = middleware.SessionCookieName
	csrfCookieName    = middleware.CSRFCookieName
)

// setAuthCookies 设置会话与 CSRF Cookie（HttpOnly、SameSite=Lax、生产 Secure）。
func (a *App) setAuthCookies(w http.ResponseWriter, sessionToken string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    sessionToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((7 * 24 * time.Hour).Seconds()),
	})
	// Double-Submit CSRF 令牌（非 HttpOnly，前端放入 X-CSRF-Token 头）
	csrf, err := middleware.GenerateCSRFToken()
	if err != nil {
		log.Printf("[handler] 生成 CSRF 令牌失败: %v", err)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    csrf,
		Path:     "/",
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((7 * 24 * time.Hour).Seconds()),
	})
}

func (a *App) clearAuthCookies(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1})
	http.SetCookie(w, &http.Cookie{Name: csrfCookieName, Value: "", Path: "/", MaxAge: -1})
}

func secureCookies(r *http.Request) bool {
	// 生产环境或 HTTPS 请求一律 Secure
	if a := r.Header.Get("X-Forwarded-Proto"); strings.Contains(strings.ToLower(a), "https") {
		return true
	}
	return strings.HasPrefix(r.URL.Scheme, "https") || r.TLS != nil
}

// ---- POST /api/auth/register ----
func (a *App) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Confirm  string `json:"confirm_password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求数据无效")
		return
	}
	if req.Password != req.Confirm {
		writeErr(w, http.StatusBadRequest, "两次输入的密码不一致")
		return
	}
	_, err := a.Auth.Register(r.Context(), req.Email, req.Password, clientIP(r))
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrWeakPassword):
			writeErr(w, http.StatusBadRequest, "密码至少 8 位且必须同时包含字母和数字")
		case errors.Is(err, auth.ErrDuplicate):
			// 防枚举：与成功提示一致（不区分邮箱是否已存在）
			writeJSON(w, http.StatusOK, map[string]string{
				"message": "注册成功，请前往邮箱完成验证（若邮箱已注册且未验证，请检查垃圾邮件或重发验证邮件）",
			})
		case errors.Is(err, auth.ErrEmailSendFailed), errors.Is(err, auth.ErrMailNotConfigured):
			writeErr(w, http.StatusServiceUnavailable, "验证邮件发送失败，请稍后在登录页重试")
		default:
			// 邮箱格式校验错误等
			writeErr(w, http.StatusBadRequest, "注册失败："+err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":     "注册成功，请前往邮箱完成验证",
		"retry_after": a.Cfg.EmailSendCooldownSeconds,
	})
}

// ---- POST/GET /api/auth/verify-email（6 位数字验证码）----
// 请求：POST {"email": "...", "code": "123456"}；GET ?email=...&code=...
func (a *App) handleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	email := r.URL.Query().Get("email")
	code := r.URL.Query().Get("code")
	if r.Method == http.MethodPost {
		var req struct {
			Email string `json:"email"`
			Code  string `json:"code"`
		}
		if err := decodeJSON(r, &req); err == nil {
			email = req.Email
			code = req.Code
		}
	}
	if strings.TrimSpace(email) == "" || code == "" {
		writeErr(w, http.StatusBadRequest, "缺少邮箱或验证码")
		return
	}
	result, err := a.Auth.VerifyEmail(r.Context(), email, code)
	if err != nil {
		// 通用失败信息（不区分无效/过期/已使用/超限，防枚举）
		writeErr(w, http.StatusBadRequest, "验证码无效或已过期，请重新获取")
		return
	}
	log.Printf("[auth] 邮箱验证成功: user=%d", result.User.ID)
	writeJSON(w, http.StatusOK, map[string]string{
		"message": "邮箱验证成功，现在可以使用邮箱登录",
		"email":   result.User.Email,
	})
}

// ---- POST /api/auth/resend-verification ----
func (a *App) handleResendVerification(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求数据无效")
		return
	}
	emailNorm := auth.NormalizeEmail(req.Email)
	if err := auth.ValidateEmail(emailNorm); err != nil {
		writeErr(w, http.StatusBadRequest, "邮箱格式无效")
		return
	}
	user, err := a.Store.GetUserByEmail(r.Context(), emailNorm)
	if err != nil || user.Status == models.StatusDisabled {
		// 防枚举：不存在的邮箱同样返回通用提示
		writeJSON(w, http.StatusOK, map[string]string{
			"message": "若该邮箱已注册且未完成验证，验证邮件将发送至您的邮箱",
		})
		return
	}
	if user.IsVerified() {
		writeErr(w, http.StatusBadRequest, "该邮箱已完成验证，请直接登录")
		return
	}
	if err := a.Auth.SendVerification(r.Context(), user, models.EmailPurposeResendVerification, clientIP(r)); err != nil {
		if errors.Is(err, auth.ErrRateLimited) {
			// 后端计时：返回实际剩余秒数，前端据服务器时间展示
			retry := a.Auth.CooldownRemaining(r.Context(), emailNorm)
			writeJSON(w, http.StatusTooManyRequests, map[string]interface{}{
				"detail":      "邮件发送过于频繁，请稍后再试",
				"retry_after": retry,
			})
			return
		}
		// 防枚举：发送失败也返回通用提示
		log.Printf("[auth] 重发验证邮件失败（脱敏）: %v", err)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":     "若该邮箱已注册且未完成验证，验证邮件将发送至您的邮箱",
		"retry_after": a.Cfg.EmailSendCooldownSeconds,
	})
}

// ---- GET /api/auth/verification-status ----
func (a *App) handleVerificationStatus(w http.ResponseWriter, r *http.Request) {
	emailNorm := auth.NormalizeEmail(r.URL.Query().Get("email"))
	if err := auth.ValidateEmail(emailNorm); err != nil {
		// 不泄露邮箱是否注册/验证；仅返回自身信息引导
		writeJSON(w, http.StatusOK, map[string]bool{"registered": false})
		return
	}
	user, err := a.Store.GetUserByEmail(r.Context(), emailNorm)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]bool{"registered": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"registered":       true,
		"verified":         user.IsVerified(),
		"status":           user.Status,
	})
}

// ---- POST /api/auth/login ----
func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求数据无效")
		return
	}
	sessionToken, user, err := a.Auth.Login(r.Context(), req.Email, req.Password, clientIP(r), r.UserAgent())
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrRateLimited):
			writeErr(w, http.StatusTooManyRequests, "尝试过于频繁，请 15 分钟后再试")
		case errors.Is(err, auth.ErrAccountDisabled):
			writeErr(w, http.StatusForbidden, "账户已被禁用")
		default:
			// 统一错误信息（防枚举：不区分邮箱不存在/密码错误/未验证）
			writeErr(w, http.StatusUnauthorized, "邮箱或密码错误，或者账户尚未完成验证")
		}
		return
	}
	a.setAuthCookies(w, sessionToken, secureCookies(r))
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"registered": true,
		"verified":   user.IsVerified(),
		"is_admin":   user.IsAdmin(),
		"email":      user.Email,
	})
}

// ---- POST /api/auth/logout ----
func (a *App) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := a.Auth.Logout(r.Context(), currentToken(r)); err != nil {
		log.Printf("[auth] 退出登录失败: %v", err)
	}
	a.clearAuthCookies(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- POST /api/auth/logout-all ----
func (a *App) handleLogoutAll(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if user == nil {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return
	}
	if err := a.Auth.LogoutAll(r.Context(), user.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, "退出失败")
		return
	}
	a.clearAuthCookies(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- GET /api/auth/me ----
func (a *App) handleMe(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if user == nil {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"id":             user.ID,
		"email":          user.Email,
		"verified":       user.IsVerified(),
		"email_verified": user.IsVerified(),
		"status":         user.Status,
		"role":           user.Role,
		"is_admin":       user.IsAdmin(),
		"created_at":     user.CreatedAt.Unix(),
		"last_login_at":  nilTimeUnix(user.LastLoginAt),
	})
}

func nilTimeUnix(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	v := t.Unix()
	return &v
}

// ---- POST /api/auth/change-password（登录后：邮箱验证码 + 新密码）----
func (a *App) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if user == nil {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return
	}
	var req struct {
		Code        string `json:"code"`
		NewPassword string `json:"new_password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求数据无效")
		return
	}
	if err := a.Auth.ChangePassword(r.Context(), user, req.Code, req.NewPassword); err != nil {
		switch {
		case errors.Is(err, auth.ErrWeakPassword):
			writeErr(w, http.StatusBadRequest, "新密码至少 8 位且必须同时包含字母和数字")
		case errors.Is(err, auth.ErrTokenInvalid), errors.Is(err, auth.ErrTokenExpired), errors.Is(err, auth.ErrTooManyAttempts):
			writeErr(w, http.StatusBadRequest, "验证码无效或已过期，请先获取验证码")
		default:
			writeErr(w, http.StatusBadRequest, "修改失败："+err.Error())
		}
		return
	}
	// 修改密码后旧会话全部失效：清除当前 Cookie
	a.clearAuthCookies(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- POST /api/auth/forgot-password（公开：发送重置验证码，防枚举）----
func (a *App) handleForgotPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求数据无效")
		return
	}
	emailNorm := auth.NormalizeEmail(req.Email)
	if err := auth.ValidateEmail(emailNorm); err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"message":     "若该邮箱已注册，重置验证码将发送至您的邮箱",
			"retry_after": a.Cfg.EmailSendCooldownSeconds,
		})
		return
	}
	if err := a.Auth.ForgotPassword(r.Context(), emailNorm, clientIP(r)); err != nil {
		if errors.Is(err, auth.ErrRateLimited) {
			// 后端计时：返回实际剩余秒数，前端据服务器时间展示
			retry := a.Auth.CooldownRemaining(r.Context(), emailNorm)
			writeJSON(w, http.StatusTooManyRequests, map[string]interface{}{
				"detail":      "邮件发送过于频繁，请稍后再试",
				"retry_after": retry,
			})
			return
		}
		log.Printf("[auth] 忘记密码发信失败（脱敏）: %v", err)
	}
	// 防枚举：无论邮箱是否存在均返回通用提示
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":     "若该邮箱已注册，重置验证码将发送至您的邮箱",
		"retry_after": a.Cfg.EmailSendCooldownSeconds,
	})
}

// ---- POST /api/auth/reset-password（公开：邮箱+验证码+新密码）----
func (a *App) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email       string `json:"email"`
		Code        string `json:"code"`
		NewPassword string `json:"new_password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求数据无效")
		return
	}
	if err := a.Auth.ResetPassword(r.Context(), auth.NormalizeEmail(req.Email), req.Code, req.NewPassword); err != nil {
		switch {
		case errors.Is(err, auth.ErrWeakPassword):
			writeErr(w, http.StatusBadRequest, "新密码至少 8 位且必须同时包含字母和数字")
		case errors.Is(err, auth.ErrTokenInvalid), errors.Is(err, auth.ErrTokenExpired), errors.Is(err, auth.ErrTooManyAttempts):
			writeErr(w, http.StatusBadRequest, "验证码无效或已过期，请重新获取")
		default:
			writeErr(w, http.StatusBadRequest, "重置失败："+err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"message": "密码已重置，请使用新密码登录",
	})
}
