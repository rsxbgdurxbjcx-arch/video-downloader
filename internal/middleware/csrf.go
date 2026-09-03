package middleware

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
)

// CSRF 防护：Double-Submit Cookie 模式。
// 每个会话初始化时下发 csrf cookie（非 HttpOnly，前端读取后放入 X-CSRF-Token 头）；
// 所有非安全方法（POST/PUT/DELETE/PATCH）必须携带与 Cookie 一致的令牌。
// 配合 SameSite=Lax/Strict 与 CORS 白名单实现纵深防御。

// GenerateCSRFToken 生成 CSRF 令牌（32 字节 hex）。
func GenerateCSRFToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// checkCSRF 校验：已登录会话（存在 vd_session）的写操作必须携带与 csrf Cookie 一致的
// X-CSRF-Token（Double-Submit）。公开接口（登录/注册等，无会话 Cookie）不受影响。
func checkCSRF(r *http.Request) bool {
	sess, err := r.Cookie(SessionCookieName)
	if err != nil || sess.Value == "" {
		// 无会话：无隐私面被跨站利用，放行（登录/注册/验证等公开流程）
		return true
	}
	c, err := r.Cookie(CSRFCookieName)
	if err != nil || c.Value == "" {
		return false
	}
	h := r.Header.Get("X-CSRF-Token")
	if h == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(h)) == 1
}

// CSRF 中间件工厂。
func CSRF(enabled bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if enabled {
				switch r.Method {
				case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
					if !checkCSRF(r) {
						writeJSONError(w, http.StatusForbidden, "CSRF 校验失败")
						return
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
