package handlers

import (
	"io/fs"
	"net/http"
	"time"

	"video-downloader/internal/middleware"
)

// Routes 注册全部路由（Go 1.22 ServeMux 方法+路径模式）并组装中间件链。
// staticFS 为嵌入的前端资源。
func (a *App) Routes(staticFS fs.FS) http.Handler {
	mux := http.NewServeMux()

	// ===== 公开接口 =====
	mux.HandleFunc("GET /api/health", a.handleHealth)
	mux.HandleFunc("GET /api/platforms", a.handlePlatforms)
	mux.HandleFunc("POST /api/auth/register", a.handleRegister)
	mux.HandleFunc("GET /api/auth/verify-email", a.handleVerifyEmail)
	mux.HandleFunc("POST /api/auth/verify-email", a.handleVerifyEmail)
	mux.HandleFunc("POST /api/auth/resend-verification", a.handleResendVerification)
	mux.HandleFunc("GET /api/auth/verification-status", a.handleVerificationStatus)
	mux.HandleFunc("POST /api/auth/login", a.handleLogin)
	mux.HandleFunc("POST /api/auth/forgot-password", a.handleForgotPassword)
	mux.HandleFunc("POST /api/auth/reset-password", a.handleResetPassword)
	mux.HandleFunc("GET /api/plans", a.handlePlans)

	// ===== 认证接口 =====
	authenticated := middleware.RequireAuth(a.Auth)
	mux.Handle("POST /api/auth/logout", authenticated(http.HandlerFunc(a.handleLogout)))
	mux.Handle("POST /api/auth/logout-all", authenticated(http.HandlerFunc(a.handleLogoutAll)))
	mux.Handle("GET /api/auth/me", authenticated(http.HandlerFunc(a.handleMe)))
	mux.Handle("POST /api/auth/change-password", authenticated(http.HandlerFunc(a.handleChangePassword)))

	// ===== 用户中心 =====
	mux.Handle("GET /api/membership", authenticated(http.HandlerFunc(a.handleMembership)))
	mux.Handle("POST /api/email/test", authenticated(http.HandlerFunc(a.handleEmailTest)))

	// ===== 下载任务（须登录；/api/download 在 handler 内做邮箱验证与会员校验）=====
	mux.Handle("POST /api/download", authenticated(http.HandlerFunc(a.handleDownload)))
	mux.Handle("GET /api/status/{id}", authenticated(http.HandlerFunc(a.handleStatus)))
	mux.Handle("GET /api/file/{id}", authenticated(http.HandlerFunc(a.handleFile)))
	mux.Handle("DELETE /api/task/{id}", authenticated(http.HandlerFunc(a.handleDeleteTask)))
	mux.Handle("GET /api/tasks", authenticated(http.HandlerFunc(a.handleTasks)))

	// ===== Cookie 管理（须登录；不再返回明文）=====
	mux.Handle("GET /api/cookies", authenticated(http.HandlerFunc(a.handleGetAllCookies)))
	mux.Handle("POST /api/cookies/delete", authenticated(http.HandlerFunc(a.handleBatchDeleteCookies)))
	mux.Handle("POST /api/cookie/detect", authenticated(http.HandlerFunc(a.handleDetectCookie)))
	mux.Handle("GET /api/cookie/{platform}", authenticated(http.HandlerFunc(a.handleCookieCRUD)))
	mux.Handle("POST /api/cookie/{platform}", authenticated(http.HandlerFunc(a.handleCookieCRUD)))
	mux.Handle("DELETE /api/cookie/{platform}", authenticated(http.HandlerFunc(a.handleCookieCRUD)))

	// ===== 订单 =====
	mux.Handle("POST /api/orders", authenticated(http.HandlerFunc(a.handleCreateOrder)))
	mux.Handle("GET /api/orders", authenticated(http.HandlerFunc(a.handleListOrders)))
	mux.Handle("GET /api/orders/{no}", authenticated(http.HandlerFunc(a.handleGetOrder)))
	mux.Handle("POST /api/orders/{no}/close", authenticated(http.HandlerFunc(a.handleCloseOrder)))
	mux.Handle("POST /api/orders/{no}/simulate-pay", authenticated(http.HandlerFunc(a.handleSimulatePay)))

	// ===== 管理员（先 RequireAuth 注入用户，再 RequireAdmin 校验角色）=====
	adminHandler := func(h http.HandlerFunc) http.Handler {
		authenticated := middleware.RequireAuth(a.Auth)(http.HandlerFunc(h))
		return middleware.RequireAdmin(authenticated)
	}
	mux.Handle("GET /api/admin/users", adminHandler(a.adminUsers))
	mux.Handle("POST /api/admin/users/{id}/status", adminHandler(a.adminUserStatus))
	mux.Handle("POST /api/admin/users/{id}/verify", adminHandler(a.adminManualVerify))
	mux.Handle("GET /api/admin/orders", adminHandler(a.adminOrders))
	mux.Handle("POST /api/admin/orders/{no}/mark-paid", adminHandler(a.adminMarkPaid))
	mux.Handle("POST /api/admin/orders/{no}/refund", adminHandler(a.handleRefundOrder))
	mux.Handle("GET /api/admin/tasks", adminHandler(a.adminTasks))
	mux.Handle("GET /api/admin/plans", adminHandler(a.adminPlans))
	mux.Handle("POST /api/admin/plans", adminHandler(a.adminCreatePlan))
	mux.Handle("PUT /api/admin/plans/{id}", adminHandler(a.adminUpdatePlan))
	mux.Handle("POST /api/admin/plans/{id}/toggle", adminHandler(a.adminTogglePlan))
	mux.Handle("GET /api/admin/payment-events", adminHandler(a.adminPaymentEvents))
	mux.Handle("GET /api/admin/audit-logs", adminHandler(a.adminAuditLogs))
	mux.Handle("GET /api/admin/email-logs", adminHandler(a.adminEmailLogs))

	// ===== 静态资源 =====
	mux.Handle("GET /", http.FileServer(http.FS(staticFS)))

	// ===== 中间件链（由外到内：恢复 → 安全头 → CORS → CSRF → 限流）=====
	var h http.Handler = mux
	h = middleware.Recoverer(h)
	h = middleware.SecurityHeaders(h)
	h = middleware.CORS(a.Cfg.AllowedOrigins)(h)
	h = middleware.CSRF(a.Cfg.CSRFEnabled)(h)
	if a.Cfg.RateLimitEnabled {
		h = middleware.RateLimit(middleware.NewRateLimiter(120, 60*time.Second))(h) // 全局兜底 120 req/min/IP
	}
	return h
}
