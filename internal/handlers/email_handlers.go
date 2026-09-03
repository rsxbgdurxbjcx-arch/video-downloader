package handlers

import (
	"net/http"
	"strconv"
	"time"

	"video-downloader/internal/middleware"
)

// emailTestLimiter 测试邮件发送限流（同一用户 60 秒一次；进程内保护，防刷）。
var emailTestLimiter = middleware.NewRateLimiter(1, 60*time.Second)

// ---- POST /api/email/test（登录 + 已验证；发送测试邮件定位 Spug 邮件推送配置问题）----
// 发送结果包含具体错误（不包含模板编码/Token），便于诊断：
//   - "Spug 邮件接口连接失败"     → 服务器出网问题 / SPUG_MAIL_BASE_URL 配置错误
//   - "Spug 邮件发送被拒绝: ..."  → 模板编码无效、IP 不在白名单、邮件余额不足、发送频率超限等
//     （push.spug.cc → 控制台 → 开发者设置 → 安全设置 可查看/修改 IP 白名单）
func (a *App) handleEmailTest(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if user == nil {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return
	}
	if !user.IsVerified() {
		writeErr(w, http.StatusForbidden, "账户尚未完成邮箱验证，无法发送测试邮件")
		return
	}
	if !emailTestLimiter.Allow("u" + strconv.FormatInt(user.ID, 10)) {
		writeErr(w, http.StatusTooManyRequests, "测试邮件过于频繁，请 60 秒后再试")
		return
	}
	if !a.Mailer.Enabled() {
		if a.Cfg.IsDevelopment() {
			writeErr(w, http.StatusBadRequest, "Spug 邮件推送未配置（SPUG_MAIL_ENABLED=false）：开发模式验证码仅输出到控制台；请编辑 .env 配置后重启服务")
		} else {
			writeErr(w, http.StatusInternalServerError, "Spug 邮件推送未配置（SPUG_MAIL_ENABLED=false），无法发送邮件")
		}
		return
	}
	// 测试邮件走与真实验证码相同的官方模板与链路（正文含随机测试验证码）
	if err := a.Mailer.SendTest(user.Email); err != nil {
		writeErr(w, http.StatusInternalServerError, "发送失败："+middleware.RedactSecret(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "message": "测试邮件已发送，请检查收件箱（含垃圾邮件）"})
}
