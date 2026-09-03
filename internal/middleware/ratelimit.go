package middleware

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// RateLimiter 内存滑动窗口限流器（按 key 计数）。
type RateLimiter struct {
	mu      sync.Mutex
	window  time.Duration
	max     int
	entries map[string]*rlEntry
}

type rlEntry struct {
	times []time.Time
}

// NewRateLimiter 创建限流器：允许 window 内 max 次请求。
func NewRateLimiter(max int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		window:  window,
		max:     max,
		entries: map[string]*rlEntry{},
	}
	go rl.cleanupLoop()
	return rl
}

// Allow 检查并记录一次请求；放行返回 true。
func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	e, ok := rl.entries[key]
	if !ok {
		e = &rlEntry{}
		rl.entries[key] = e
	}
	cutoff := now.Add(-rl.window)
	kept := e.times[:0]
	for _, t := range e.times {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	e.times = kept
	if len(e.times) >= rl.max {
		return false
	}
	e.times = append(e.times, now)
	return true
}

func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.window)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		cutoff := time.Now().Add(-rl.window)
		for k, e := range rl.entries {
			if len(e.times) == 0 || e.times[len(e.times)-1].Before(cutoff) {
				delete(rl.entries, k)
			}
		}
		rl.mu.Unlock()
	}
}

// ClientIP 从请求中提取客户端 IP。
// 信任上游场景（反向代理 / Cloudflare）：
//   - Cloudflare 代理：优先权威单值头 CF-Connecting-IP（不可伪造）；
//   - 其他反向代理（Nginx/Caddy）：取 X-Forwarded-For 第一项（最左为客户端）；
//   - 直连：取 RemoteAddr。
func ClientIP(r *http.Request) string {
	if cf := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cf != "" {
		if ip := net.ParseIP(cf); ip != nil {
			return ip.String()
		}
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := indexByte(xff, ','); i > 0 {
			xff = xff[:i]
		}
		if ip := net.ParseIP(trimSpace(xff)); ip != nil {
			return ip.String()
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

// RateLimit 中间件：对每个客户端 IP 限量；超过返回 429。
func RateLimit(rl *RateLimiter) func(http.Handler) http.Handler {
	if rl == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !rl.Allow(ClientIP(r)) {
				writeJSONError(w, http.StatusTooManyRequests, "请求过于频繁，请稍后再试")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
