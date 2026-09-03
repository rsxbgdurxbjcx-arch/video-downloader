package middleware

import (
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

// CORS 白名单：默认仅同源；可通过 CORS_ALLOWED_ORIGINS 追加。
// 使用反射式校验：Origin 不在白名单内时，禁止跨站读取。
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, o := range allowedOrigins {
		if o = strings.TrimRight(strings.TrimSpace(o), "/"); o != "" {
			allowed[o] = true
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && allowed[origin] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-CSRF-Token")
				w.Header().Set("Access-Control-Max-Age", "600")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// SecurityHeaders 安全响应头。
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Permissions-Policy", "geolocation=(), microphone=(), camera=()")
		next.ServeHTTP(w, r)
	})
}

// BodyLimit 请求体大小限制（默认 1MB；下载/Cookie 接口较大，可单独放宽）。
func BodyLimit(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil && maxBytes > 0 {
				r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Recoverer panic 恢复：统一返回 500，不把堆栈返回给客户端。
func Recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[panic] %s %s: %v\n%s", r.Method, r.URL.Path, rec, debug.Stack())
				writeJSONError(w, http.StatusInternalServerError, "服务器内部错误")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// Logging 访问日志（脱敏：不记录 URL 查询参数、Cookie、Authorization）。
func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("[http] %s %s %s %s", r.Method, redactPath(r.URL.Path), r.RemoteAddr, time.Since(start).Round(time.Millisecond))
	})
}

// redactPath 脱敏路径（保留结构，剔除敏感查询串——此处只取 Path）。
func redactPath(p string) string {
	return p
}

// RedactSecret 从文本中抹除敏感关键字（用于错误信息脱敏）。
func RedactSecret(s string) string {
	return RedactSecretWithTokens(s,
		"Cookie:", "Authorization:", "password=", "token=",
		"SESSDATA", "sessionid", "web_session", "msToken", "__Secure-")
}

// RedactSecretWithTokens 脱敏指定模式。
func RedactSecretWithTokens(s string, patterns ...string) string {
	for _, p := range patterns {
		if strings.Contains(s, p) {
			// 找到模式后从该处截断，最保守处理：绝不完整输出敏感值
			idx := strings.Index(s, p)
			return s[:idx] + p + " [REDACTED]"
		}
	}
	return s
}
