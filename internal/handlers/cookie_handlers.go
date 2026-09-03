package handlers

import (
	"fmt"
	"net/http"
	"strings"

	"video-downloader/internal/downloader"
)

// ---- GET /api/cookies（仅返回是否保存 + 脱敏预览；绝不返回明文）----
func (a *App) handleGetAllCookies(w http.ResponseWriter, r *http.Request) {
	result := make(map[string]interface{})
	for key, info := range downloader.SupportedPlatforms {
		mask := a.Engine.Cookies().Mask(key)
		result[key] = map[string]interface{}{
			"name":       info.Name,
			"has_cookie": mask != "",
			"mask":       mask,
		}
	}
	writeJSON(w, http.StatusOK, result)
}

// ---- GET/POST/DELETE /api/cookie/{platform} ----
func (a *App) handleCookieCRUD(w http.ResponseWriter, r *http.Request) {
	platform := r.PathValue("platform")
	if _, ok := downloader.SupportedPlatforms[platform]; !ok {
		writeErr(w, http.StatusNotFound, "不支持的平台")
		return
	}
	switch r.Method {
	case http.MethodGet:
		mask := a.Engine.Cookies().Mask(platform)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"platform":   platform,
			"has_cookie": mask != "",
			"mask":       mask,
		})
	case http.MethodPost:
		var req struct {
			Cookie string `json:"cookie"`
		}
		if err := decodeJSON(r, &req); err != nil {
			writeErr(w, http.StatusBadRequest, "请求数据无效")
			return
		}
		if strings.TrimSpace(req.Cookie) == "" {
			writeErr(w, http.StatusBadRequest, "cookie 不能为空")
			return
		}
		if err := a.Engine.Cookies().Save(platform, strings.TrimSpace(req.Cookie)); err != nil {
			writeErr(w, http.StatusInternalServerError, "保存失败")
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok":       true,
			"platform": platform,
			"name":     downloader.SupportedPlatforms[platform].Name,
		})
	case http.MethodDelete:
		deleted := a.Engine.Cookies().Delete(platform)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ok":       true,
			"deleted":  deleted,
			"platform": platform,
		})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// ---- POST /api/cookie/detect ----
func (a *App) handleDetectCookie(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Cookie string `json:"cookie"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求数据无效")
		return
	}
	if strings.TrimSpace(req.Cookie) == "" {
		writeJSON(w, http.StatusOK, map[string]interface{}{"ok": false, "platform": ""})
		return
	}
	platform := downloader.DetectCookiePlatform(req.Cookie)
	name := ""
	if platform != "" {
		name = downloader.SupportedPlatforms[platform].Name
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":       platform != "",
		"platform": platform,
		"name":     name,
	})
}

// ---- POST /api/cookies/delete ----
func (a *App) handleBatchDeleteCookies(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Platforms []string `json:"platforms"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求数据无效")
		return
	}
	deleteAll := len(req.Platforms) == 0
	for _, p := range req.Platforms {
		if p == "*" {
			deleteAll = true
			break
		}
	}
	targets := req.Platforms
	if deleteAll {
		targets = targets[:0]
		for key := range downloader.SupportedPlatforms {
			targets = append(targets, key)
		}
	}
	var deleted []string
	for _, p := range targets {
		if _, ok := downloader.SupportedPlatforms[p]; !ok {
			continue
		}
		if a.Engine.Cookies().Delete(p) {
			deleted = append(deleted, p)
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "deleted": deleted})
}

// ---- GET /api/membership（用户权益中心）----
func (a *App) handleMembership(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if user == nil {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return
	}
	ent, err := a.Entitle.GetUserEntitlement(r.Context(), user.ID, user.IsVerified())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	quota, err := a.Entitle.RemainingQuota(r.Context(), user.ID, ent)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"is_member":            ent.IsMember,
		"plan_name":            ent.PlanName,
		"plan_id":              ent.PlanID,
		"download_limit":       ent.DownloadLimit,
		"daily_download_limit": ent.DailyDownloadLimit,
		"max_concurrent":       ent.MaxConcurrentTasks,
		"max_file_size":        ent.MaxFileSize,
		"allowed_quality":      ent.AllowedQuality,
		"expires_at":           nilTimeUnix(ent.ExpiresAt),
		"quota":                quota,
	})
}

// ---- GET /api/plans（公开套餐列表）----
func (a *App) handlePlans(w http.ResponseWriter, r *http.Request) {
	plans, err := a.Store.ListPlans(r.Context(), true)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	out := make([]map[string]interface{}, 0, len(plans))
	for _, p := range plans {
		out = append(out, map[string]interface{}{
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
			"allowed_quality":      strings.Split(p.AllowedQuality, ","),
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"plans": out})
}

func formatCents(cents int64) string {
	return fmt.Sprintf("%.2f", float64(cents)/100)
}
