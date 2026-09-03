package handlers

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"video-downloader/internal/downloader"
	"video-downloader/internal/models"
	"video-downloader/internal/services"
)

// ---- POST /api/download（登录 + 邮箱验证 + 会员权限）----
func (a *App) handleDownload(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if user == nil {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return
	}
	if !user.IsVerified() || user.Status != models.StatusActive {
		writeErr(w, http.StatusForbidden, "账户尚未完成邮箱验证，无法创建下载任务")
		return
	}
	var req struct {
		URL    string `json:"url"`
		Cookie string `json:"cookie"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求数据无效")
		return
	}
	// URL 校验（平台白名单 + SSRF 防护）
	validated, err := downloader.ValidateURL(req.URL)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	platform := downloader.DetectPlatform(validated)
	// 会员权益检查（后端唯一权威）
	ent, err := a.Entitle.GetUserEntitlement(r.Context(), user.ID, user.IsVerified())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "权益查询失败")
		return
	}
	if err := a.Entitle.CheckCreateDownload(r.Context(), user.ID, ent); err != nil {
		status := http.StatusForbidden
		code := ""
		if errors.Is(err, services.ErrQuotaExceeded) {
			status = http.StatusTooManyRequests
			code = "quota_exceeded"
		} else if errors.Is(err, services.ErrConcurrencyExceeded) {
			status = http.StatusTooManyRequests
			code = "concurrency_exceeded"
		}
		writeJSON(w, status, map[string]string{"detail": err.Error(), "code": code})
		return
	}

	// Cookie 来源：优先本次提交，否则使用加密存储中的 Cookie
	cookie := strings.TrimSpace(req.Cookie)
	if cookie != "" {
		if err := a.Engine.Cookies().Save(platform, cookie); err != nil {
			log.Printf("[cookie] 保存 cookie 失败（不记录内容）: platform=%s", platform)
		}
	} else {
		if saved, ok := a.Engine.Cookies().Load(platform); ok {
			cookie = saved
		}
	}

	taskID, err := a.Engine.Submit(validated, platform, cookie, user.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "创建任务失败："+err.Error())
		return
	}
	platformName := platform
	if info, ok := downloader.SupportedPlatforms[platform]; ok {
		platformName = info.Name
	}
	writeJSON(w, http.StatusOK, map[string]string{"task_id": taskID, "platform": platformName})
}

// ---- GET /api/status/{taskID}（属主或管理员）----
func (a *App) handleStatus(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if user == nil {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return
	}
	taskID := r.PathValue("id")
	task := a.Engine.Get(taskID)
	allow := user.IsAdmin()
	if task != nil && task.UserID == user.ID {
		allow = true
	}
	if task == nil {
		// 运行时任务不存在：回退数据库记录（重启后仍可查看）
		dt, err := a.Store.GetTaskByIDAndUser(r.Context(), taskID, user.ID)
		if err != nil {
			if user.IsAdmin() {
				if dt2, err2 := a.Store.GetTaskByID(r.Context(), taskID); err2 == nil {
					dt = dt2
				}
			}
			if dt == nil {
				writeErr(w, http.StatusNotFound, "任务不存在")
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"task_id":      dt.TaskID,
			"status":       dt.Status,
			"stage":        dbStatusStage(dt.Status),
			"progress":     dt.Progress,
			"title":        dt.Title,
			"platform":     dt.Platform,
			"error":        dt.ErrorMessage,
			"filename":     dt.Filename,
			"filesize":     dt.FileSize,
			"created_at":   dt.CreatedAt.Unix(),
			"completed_at": nilUnix(dt.CompletedAt),
		})
		return
	}
	if !allow {
		writeErr(w, http.StatusForbidden, "无权查看该任务")
		return
	}
	writeJSON(w, http.StatusOK, task.ToJSON())
}

func dbStatusStage(status string) string {
	switch status {
	case models.TaskQueued:
		return "已加入队列"
	case models.TaskProcessing:
		return "下载中..."
	case models.TaskCompleted:
		return "下载完成，等待浏览器拉取"
	case models.TaskFailed:
		return "失败"
	case models.TaskCancelled:
		return "已取消"
	}
	return status
}

func nilUnix(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	v := t.Unix()
	return &v
}

// ---- GET /api/file/{taskID}（属主）----
func (a *App) handleFile(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if user == nil {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return
	}
	taskID := r.PathValue("id")
	task := a.Engine.Get(taskID)
	if task != nil {
		if task.UserID != user.ID && !user.IsAdmin() {
			writeErr(w, http.StatusForbidden, "无权访问该文件")
			return
		}
		if task.Status != "completed" {
			writeErr(w, http.StatusNotFound, "文件不存在")
			return
		}
		filePath := a.Engine.TaskFilePath(task)
		if filePath == "" {
			writeErr(w, http.StatusNotFound, "文件不存在")
			return
		}
		if !a.Engine.StartPull(taskID) {
			writeErr(w, http.StatusNotFound, "文件不存在")
			return
		}
		streamErr := streamFile(w, r, filePath, task.Filename)
		a.Engine.MarkPullDone(taskID)
		if streamErr != nil {
			log.Printf("⚠️ 拉取中断: %s — %v", taskID, streamErr)
		}
		return
	}
	// 无运行时任务：数据库路径（属主校验）
	dt, err := a.Store.GetTaskByIDAndUser(r.Context(), taskID, user.ID)
	if err != nil {
		if !user.IsAdmin() {
			writeErr(w, http.StatusForbidden, "无权访问该文件")
			return
		}
		if dt, err = a.Store.GetTaskByID(r.Context(), taskID); err != nil {
			writeErr(w, http.StatusNotFound, "文件不存在")
			return
		}
	}
	if dt.Status != models.TaskCompleted || dt.OutputPath == "" {
		writeErr(w, http.StatusNotFound, "文件不存在")
		return
	}
	// 路径安全：只允许下载目录内的文件
	if !safeOutputPath(dt.OutputPath, a.Cfg.DownloadDir) {
		writeErr(w, http.StatusNotFound, "文件不存在")
		return
	}
	if err := streamFile(w, r, dt.OutputPath, dt.Filename); err != nil {
		log.Printf("⚠️ 拉取中断: %s — %v", taskID, err)
	}
}

// safeOutputPath 防止路径穿越。
func safeOutputPath(path, baseDir string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	base, err := filepath.Abs(baseDir)
	if err != nil {
		return false
	}
	return strings.HasPrefix(abs, base+string(filepath.Separator))
}

// ---- DELETE /api/task/{taskID}（属主）----
func (a *App) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if user == nil {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return
	}
	taskID := r.PathValue("id")
	task := a.Engine.Get(taskID)
	if task != nil && task.UserID != user.ID && !user.IsAdmin() {
		writeErr(w, http.StatusForbidden, "无权操作该任务")
		return
	}
	if task == nil {
		if _, err := a.Store.GetTaskByIDAndUser(r.Context(), taskID, user.ID); err != nil {
			if !user.IsAdmin() {
				writeErr(w, http.StatusForbidden, "无权操作该任务")
				return
			}
		}
	}
	if err := a.Engine.DeleteTask(taskID); err != nil {
		writeErr(w, http.StatusInternalServerError, "删除失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- GET /api/tasks（本人任务列表）----
func (a *App) handleTasks(w http.ResponseWriter, r *http.Request) {
	user := currentUser(r)
	if user == nil {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return
	}
	page, size := pageParams(r)
	limit, offset := size, (page-1)*size
	tasks, err := a.Store.ListUserTasks(r.Context(), user.ID, limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "查询失败")
		return
	}
	out := make([]map[string]interface{}, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, map[string]interface{}{
			"task_id":      t.TaskID,
			"status":       t.Status,
			"stage":        dbStatusStage(t.Status),
			"progress":     t.Progress,
			"title":        t.Title,
			"platform":     t.Platform,
			"filename":     t.Filename,
			"filesize":     t.FileSize,
			"error":        t.ErrorMessage,
			"created_at":   t.CreatedAt.Unix(),
			"completed_at": nilUnix(t.CompletedAt),
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"tasks": out, "page": page})
}

// ---- GET /api/platforms ----
func (a *App) handlePlatforms(w http.ResponseWriter, r *http.Request) {
	type plat struct {
		Key           string `json:"key"`
		Name          string `json:"name"`
		NeedsCookieHD bool   `json:"needs_cookie_for_hd"`
	}
	var plts []plat
	for k, v := range downloader.SupportedPlatforms {
		plts = append(plts, plat{k, v.Name, v.NeedsCookieHD})
	}
	sort.Slice(plts, func(i, j int) bool { return plts[i].Name < plts[j].Name })
	writeJSON(w, http.StatusOK, map[string]interface{}{"platforms": plts})
}

// ---- GET /api/health ----
func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	out, _, rc := downloader.RunCmd([]string{a.Cfg.YTDLPPath, "--version"})
	result := map[string]string{"status": "ok"}
	if rc == 0 {
		result["yt_dlp"] = strings.TrimSpace(out)
	}
	if err := a.Store.DB.PingContext(r.Context()); err != nil {
		result["status"] = "degraded"
		result["database"] = "error"
	} else {
		result["database"] = "ok"
	}
	writeJSON(w, http.StatusOK, result)
}

// streamFile 流式推送（原逻辑）。
func streamFile(w http.ResponseWriter, r *http.Request, filePath, filename string) error {
	info, err := os.Stat(filePath)
	if err != nil {
		return errors.New("文件不存在")
	}
	f, err := os.Open(filePath)
	if err != nil {
		return errors.New("打开文件失败")
	}
	defer f.Close()

	w.Header().Set("Content-Type", guessMimeType(filename))
	w.Header().Set("Content-Disposition", contentDisposition(filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	written, err := io.Copy(w, f)
	if err != nil {
		return fmt.Errorf("推送中断（已发送 %d/%d 字节）: %v", written, info.Size(), err)
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return nil
}

func guessMimeType(filename string) string {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".mp4":
		return "video/mp4"
	case ".mkv":
		return "video/x-matroska"
	case ".webm":
		return "video/webm"
	case ".flv":
		return "video/x-flv"
	case ".m4a":
		return "audio/mp4"
	case ".mp3":
		return "audio/mpeg"
	default:
		return "application/octet-stream"
	}
}

func contentDisposition(filename string) string {
	var b strings.Builder
	for _, ch := range filename {
		if ch >= 32 && ch < 127 && ch != '"' && ch != '\\' {
			b.WriteRune(ch)
		}
	}
	fallback := b.String()
	if fallback == "" {
		fallback = "video" + filepath.Ext(filename)
	}
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, fallback, url.PathEscape(filename))
}
