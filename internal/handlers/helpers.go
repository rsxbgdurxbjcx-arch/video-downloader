// Package handlers 实现全部 HTTP 接口。
package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"video-downloader/internal/auth"
	"video-downloader/internal/config"
	"video-downloader/internal/downloader"
	"video-downloader/internal/email"
	"video-downloader/internal/middleware"
	"video-downloader/internal/models"
	"video-downloader/internal/payment"
	"video-downloader/internal/repository"
	"video-downloader/internal/services"
)

// App 应用依赖集合。
type App struct {
	Cfg     *config.Config
	Store   *repository.Store
	Auth    *auth.Service
	Entitle *services.EntitlementService
	Order   *services.OrderService
	Engine  *downloader.Engine
	Mailer  *email.Sender
	PayMgr  *payment.Manager
}

// writeJSON 输出 JSON。
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("[handler] 写响应失败: %v", err)
	}
}

// writeErr 输出统一错误 JSON。
func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"detail": msg})
}

// decodeJSON 解码请求体。
func decodeJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	return dec.Decode(dst)
}

// pageParams 解析分页参数。
func pageParams(r *http.Request) (int, int) {
	page := parseInt(r.URL.Query().Get("page"), 1)
	size := parseInt(r.URL.Query().Get("page_size"), 20)
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	return page, size
}

func parseInt(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// clientIP 客户端 IP。
func clientIP(r *http.Request) string {
	return middleware.ClientIP(r)
}

// currentUser 当前登录用户。
func currentUser(r *http.Request) *models.User {
	return middleware.UserFromContext(r)
}

// currentToken 当前会话令牌。
func currentToken(r *http.Request) string {
	return middleware.SessionTokenFromContext(r)
}
