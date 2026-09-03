// Package middleware 提供认证、限流、CSRF、CORS 等 HTTP 中间件。
package middleware

import (
	"context"
	"net/http"
	"video-downloader/internal/models"
)

type ctxKey int

const (
	ctxKeyUser ctxKey = iota
	ctxKeySessionToken
)

// UserFromContext 从请求上下文取当前用户；未登录返回 nil。
func UserFromContext(r *http.Request) *models.User {
	if v, ok := r.Context().Value(ctxKeyUser).(*models.User); ok {
		return v
	}
	return nil
}

// SessionTokenFromContext 当前会话令牌。
func SessionTokenFromContext(r *http.Request) string {
	if v, ok := r.Context().Value(ctxKeySessionToken).(string); ok {
		return v
	}
	return ""
}

// withUser 注入用户与会话令牌。
func withUser(r *http.Request, user *models.User, sessionToken string) *http.Request {
	ctx := r.Context()
	ctx = context.WithValue(ctx, ctxKeyUser, user)
	ctx = context.WithValue(ctx, ctxKeySessionToken, sessionToken)
	return r.WithContext(ctx)
}

// AuthService 认证服务接口（供中间件使用）。
type AuthService interface {
	UserBySession(ctx context.Context, sessionToken string) (*models.User, error)
}

// RequireAuth 需要登录的中间件；失败返回统一的 401。
func RequireAuth(svc AuthService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 认证信息一律走 HttpOnly Cookie，禁止 URL 传递
			c, err := r.Cookie(SessionCookieName)
			if err != nil || c.Value == "" {
				writeJSONError(w, http.StatusUnauthorized, "未登录或会话已过期")
				return
			}
			user, err := svc.UserBySession(r.Context(), c.Value)
			if err != nil {
				writeJSONError(w, http.StatusUnauthorized, "未登录或会话已过期")
				return
			}
			next.ServeHTTP(w, withUser(r, user, c.Value))
		})
	}
}

// RequireVerified 需要邮箱已验证（登录且验证）。
func RequireVerified(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r)
		if user == nil || !user.IsVerified() || user.Status != models.StatusActive {
			writeJSONError(w, http.StatusForbidden, "账户尚未完成邮箱验证")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAdmin 需要管理员角色（权限完全由数据库 role 决定）。
// 注：登录不再强制邮箱已验证（管理员可直接进入后台；注册/修改密码/重置密码才要求验证码）。
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r)
		if user == nil || !user.IsAdmin() {
			writeJSONError(w, http.StatusForbidden, "无权访问")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// SessionCookieName 会话 Cookie 名。
const SessionCookieName = "vd_session"

// CSRFCookieName CSRF Cookie 名。
const CSRFCookieName = "vd_csrf"

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(`{"detail":` + strconvQuote(msg) + `}`))
}

func strconvQuote(s string) string {
	// 简易 JSON 字符串转义（消息均为内部常量或受控文本）
	out := make([]byte, 0, len(s)+2)
	out = append(out, '"')
	for _, ch := range s {
		switch ch {
		case '\\', '"':
			out = append(out, '\\', byte(ch))
		case '\n':
			out = append(out, '\\', 'n')
		case '\r':
			out = append(out, '\\', 'r')
		case '\t':
			out = append(out, '\\', 't')
		default:
			if ch < 32 {
				out = append(out, ' ')
			} else {
				out = append(out, byte(ch))
			}
		}
	}
	out = append(out, '"')
	return string(out)
}
