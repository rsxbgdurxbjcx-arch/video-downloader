package handlers

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"video-downloader/internal/auth"
	"video-downloader/internal/models"
)

// RecordAudit 记录审计日志（高风险管理员操作调用；detail 必须已脱敏）。
func (a *App) RecordAudit(r *http.Request, action, detail string) {
	user := currentUser(r)
	var uid *int64
	if user != nil {
		uid = &user.ID
	}
	if err := a.Store.RecordAudit(r.Context(), uid, action, redactText(detail), auth.HashTokenData(clientIP(r))); err != nil {
		log.Printf("[admin] 审计日志写入失败: action=%s err=%v", action, err)
	}
}

// redactText 审计文本脱敏。
func redactText(s string) string {
	if len(s) > 500 {
		s = s[:500]
	}
	for _, pat := range []string{"cookie", "password", "secret", "token", "cookie="} {
		if idx := strings.Index(strings.ToLower(s), strings.ToLower(pat)); idx >= 0 {
			return s[:idx] + pat + "[redacted]"
		}
	}
	return s
}

// ---- GET /api/admin/users ----
func (a *App) adminUsers(w http.ResponseWriter, r *http.Request) {
	page, size := pageParams(r)
	users, err := a.Store.ListUsers(r.Context(), size, (page-1)*size)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	out := make([]map[string]interface{}, 0, len(users))
	for _, u := range users {
		out = append(out, map[string]interface{}{
			"id":         u.ID,
			"email":      u.Email,
			"verified":   u.IsVerified(),
			"role":       u.Role,
			"status":     u.Status,
			"created_at": u.CreatedAt.Unix(),
			"last_login": nilTimeUnix(u.LastLoginAt),
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"users": out, "page": page})
}

// ---- POST /api/admin/users/{id}/status ----
func (a *App) adminUserStatus(w http.ResponseWriter, r *http.Request) {
	id := parseInt(r.PathValue("id"), 0)
	if id <= 0 {
		writeErr(w, http.StatusBadRequest, "无效的用户 ID")
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求数据无效")
		return
	}
	if req.Status != models.StatusActive && req.Status != models.StatusDisabled {
		writeErr(w, http.StatusBadRequest, "状态仅支持 active / disabled")
		return
	}
	if err := a.Store.UpdateUserStatus(r.Context(), int64(id), req.Status); err != nil {
		writeErr(w, http.StatusInternalServerError, "操作失败")
		return
	}
	a.RecordAudit(r, "admin.user.status", "user_id="+r.PathValue("id")+" status="+req.Status)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- GET /api/admin/orders ----
func (a *App) adminOrders(w http.ResponseWriter, r *http.Request) {
	page, size := pageParams(r)
	orders, err := a.Store.ListAllOrders(r.Context(), size, (page-1)*size)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	out := make([]map[string]interface{}, 0, len(orders))
	for _, o := range orders {
		out = append(out, orderJSON(o))
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"orders": out, "page": page})
}

// ---- POST /api/admin/orders/{no}/mark-paid（手动确认收款）----
func (a *App) adminMarkPaid(w http.ResponseWriter, r *http.Request) {
	orderNo := r.PathValue("no")
	if err := a.Order.ProcessManualPaid(r.Context(), orderNo); err != nil {
		writeErr(w, http.StatusBadRequest, "标记失败："+err.Error())
		return
	}
	a.RecordAudit(r, "admin.order.mark-paid", "order_no="+orderNo)
	order, _ := a.Store.GetOrderByNo(r.Context(), orderNo)
	writeJSON(w, http.StatusOK, orderJSON(order))
}

// ---- GET /api/admin/tasks ----
func (a *App) adminTasks(w http.ResponseWriter, r *http.Request) {
	page, size := pageParams(r)
	tasks, err := a.Store.ListAllTasks(r.Context(), size, (page-1)*size)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	out := make([]map[string]interface{}, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, map[string]interface{}{
			"task_id":    t.TaskID,
			"user_id":    t.UserID,
			"status":     t.Status,
			"platform":   t.Platform,
			"title":      t.Title,
			"filename":   t.Filename,
			"filesize":   t.FileSize,
			"error":      redactText(t.ErrorMessage),
			"created_at": t.CreatedAt.Unix(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"tasks": out, "page": page})
}

// ---- 会员套餐管理 ----
func (a *App) adminPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := a.Store.ListPlans(r.Context(), false)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	out := make([]map[string]interface{}, 0, len(plans))
	for _, p := range plans {
		m := planJSON(p)
		out = append(out, m)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"plans": out})
}

func planJSON(p *models.Plan) map[string]interface{} {
	return map[string]interface{}{
		"id":                   p.ID,
		"name":                 p.Name,
		"description":          p.Description,
		"price_cents":          p.PriceCents,
		"price":                formatCents(p.PriceCents),
		"duration_days":        p.DurationDays,
		"download_limit":       p.DownloadLimit,
		"daily_download_limit": p.DailyDownloadLimit,
		"max_concurrent_tasks": p.MaxConcurrentTasks,
		"max_file_size":        p.MaxFileSize,
		"allowed_quality":      p.AllowedQuality,
		"enabled":              p.Enabled,
		"sort_order":           p.SortOrder,
	}
}

func (a *App) adminCreatePlan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name                string `json:"name"`
		Description         string `json:"description"`
		PriceCents          int64  `json:"price_cents"`
		DurationDays        int    `json:"duration_days"`
		DownloadLimit       int    `json:"download_limit"`
		DailyDownloadLimit  int    `json:"daily_download_limit"`
		MaxConcurrentTasks  int    `json:"max_concurrent_tasks"`
		MaxFileSize         int64  `json:"max_file_size"`
		AllowedQuality      string `json:"allowed_quality"`
		SortOrder           int    `json:"sort_order"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求数据无效")
		return
	}
	if strings.TrimSpace(req.Name) == "" || req.PriceCents < 0 || req.DurationDays <= 0 {
		writeErr(w, http.StatusBadRequest, "参数无效：名称不能为空，价格 ≥0，时长 >0")
		return
	}
	plan, err := a.Store.CreatePlan(r.Context(), &models.Plan{
		Name:               strings.TrimSpace(req.Name),
		Description:        req.Description,
		PriceCents:         req.PriceCents,
		DurationDays:       req.DurationDays,
		DownloadLimit:      req.DownloadLimit,
		DailyDownloadLimit: req.DailyDownloadLimit,
		MaxConcurrentTasks: req.MaxConcurrentTasks,
		MaxFileSize:        req.MaxFileSize,
		AllowedQuality:     req.AllowedQuality,
		Enabled:            true,
		SortOrder:          req.SortOrder,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "创建失败")
		return
	}
	a.RecordAudit(r, "admin.plan.create", "plan_id="+intToString(plan.ID))
	writeJSON(w, http.StatusOK, planJSON(plan))
}

func (a *App) adminUpdatePlan(w http.ResponseWriter, r *http.Request) {
	id := parseInt(r.PathValue("id"), 0)
	if id <= 0 {
		writeErr(w, http.StatusBadRequest, "无效的套餐 ID")
		return
	}
	existing, err := a.Store.GetPlan(r.Context(), int64(id))
	if err != nil {
		writeErr(w, http.StatusNotFound, "套餐不存在")
		return
	}
	var req struct {
		Name                string `json:"name"`
		Description         string `json:"description"`
		PriceCents          int64  `json:"price_cents"`
		DurationDays        int    `json:"duration_days"`
		DownloadLimit       int    `json:"download_limit"`
		DailyDownloadLimit  int    `json:"daily_download_limit"`
		MaxConcurrentTasks  int    `json:"max_concurrent_tasks"`
		MaxFileSize         int64  `json:"max_file_size"`
		AllowedQuality      string `json:"allowed_quality"`
		SortOrder           int    `json:"sort_order"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求数据无效")
		return
	}
	if strings.TrimSpace(req.Name) == "" || req.PriceCents < 0 || req.DurationDays <= 0 {
		writeErr(w, http.StatusBadRequest, "参数无效")
		return
	}
	existing.Name = strings.TrimSpace(req.Name)
	existing.Description = req.Description
	existing.PriceCents = req.PriceCents
	existing.DurationDays = req.DurationDays
	existing.DownloadLimit = req.DownloadLimit
	existing.DailyDownloadLimit = req.DailyDownloadLimit
	existing.MaxConcurrentTasks = req.MaxConcurrentTasks
	existing.MaxFileSize = req.MaxFileSize
	existing.AllowedQuality = req.AllowedQuality
	existing.SortOrder = req.SortOrder
	if err := a.Store.UpdatePlan(r.Context(), existing); err != nil {
		writeErr(w, http.StatusInternalServerError, "更新失败")
		return
	}
	a.RecordAudit(r, "admin.plan.update", "plan_id="+intToString(existing.ID))
	writeJSON(w, http.StatusOK, planJSON(existing))
}

func (a *App) adminTogglePlan(w http.ResponseWriter, r *http.Request) {
	id := parseInt(r.PathValue("id"), 0)
	if id <= 0 {
		writeErr(w, http.StatusBadRequest, "无效的套餐 ID")
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求数据无效")
		return
	}
	if err := a.Store.SetPlanEnabled(r.Context(), int64(id), req.Enabled); err != nil {
		writeErr(w, http.StatusInternalServerError, "操作失败")
		return
	}
	a.RecordAudit(r, "admin.plan.toggle", "plan_id="+intToString(int64(id))+" enabled="+boolToString(req.Enabled))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- GET /api/admin/payment-events ----
func (a *App) adminPaymentEvents(w http.ResponseWriter, r *http.Request) {
	page, size := pageParams(r)
	events, err := a.Store.ListPaymentEvents(r.Context(), size, (page-1)*size)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	out := make([]map[string]interface{}, 0, len(events))
	for _, e := range events {
		out = append(out, map[string]interface{}{
			"id":           e.ID,
			"provider":     e.Provider,
			"event_id":     e.EventID,
			"order_no":     e.OrderNo,
			"processed":    e.Processed,
			"payload":      redactText(e.Payload),
			"created_at":   e.CreatedAt.Unix(),
			"processed_at": nilUnix(e.ProcessedAt),
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"events": out, "page": page})
}

// ---- GET /api/admin/audit-logs ----
func (a *App) adminAuditLogs(w http.ResponseWriter, r *http.Request) {
	page, size := pageParams(r)
	logs, err := a.Store.ListAuditLogs(r.Context(), size, (page-1)*size)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	out := make([]map[string]interface{}, 0, len(logs))
	for _, l := range logs {
		out = append(out, map[string]interface{}{
			"id":         l.ID,
			"user_id":    l.UserID,
			"action":     l.Action,
			"detail":     l.Detail,
			"created_at": l.CreatedAt.Unix(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"logs": out, "page": page})
}

// ---- GET /api/admin/email-logs（邮件发送诊断：每次发送的结果与服务器错误）----
func (a *App) adminEmailLogs(w http.ResponseWriter, r *http.Request) {
	page, size := pageParams(r)
	logs, err := a.Store.ListEmailSendLogs(r.Context(), size, (page-1)*size)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	out := make([]map[string]interface{}, 0, len(logs))
	for _, l := range logs {
		out = append(out, map[string]interface{}{
			"id":         l.ID,
			"email_hash": l.EmailHash[:16], // 仅前 16 位（脱敏指纹，足够区分不同收件人）
			"purpose":    l.Purpose,
			"ok":         l.OK,
			"err_msg":    l.ErrMsg,
			"created_at": l.CreatedAt.Unix(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"logs": out, "page": page})
}

// ---- POST /api/admin/users/{id}/verify（管理员辅助：手动标记已验证——仅开发环境）----
func (a *App) adminManualVerify(w http.ResponseWriter, r *http.Request) {
	if a.Cfg.IsProduction() {
		writeErr(w, http.StatusNotFound, "接口不存在")
		return
	}
	id := parseInt(r.PathValue("id"), 0)
	if id <= 0 {
		writeErr(w, http.StatusBadRequest, "无效的用户 ID")
		return
	}
	if err := a.Store.MarkUserVerified(r.Context(), int64(id)); err != nil {
		writeErr(w, http.StatusInternalServerError, "操作失败")
		return
	}
	a.RecordAudit(r, "admin.user.manual-verify", "user_id="+intToString(int64(id)))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func intToString(v int64) string {
	return strconv.FormatInt(v, 10)
}

func boolToString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
