package downloader

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"video-downloader/internal/config"
	"video-downloader/internal/models"
	"video-downloader/internal/repository"
)

// Engine 下载引擎：管理任务生命周期、yt-dlp 执行与数据库镜像。
type Engine struct {
	store    *repository.Store
	cfg      *config.Config
	cookies  *CookieStore
	registry *TaskRegistry
}

// NewEngine 创建引擎。
func NewEngine(store *repository.Store, cfg *config.Config, cookies *CookieStore) *Engine {
	return &Engine{
		store:    store,
		cfg:      cfg,
		cookies:  cookies,
		registry: NewTaskRegistry(),
	}
}

// Registry 返回任务注册表（供 handler 查询）。
func (e *Engine) Registry() *TaskRegistry { return e.registry }

// Cookies 返回 Cookie 存储。
func (e *Engine) Cookies() *CookieStore { return e.cookies }

// Submit 创建并启动下载任务（权限校验由调用方完成）。
// rawCookie 已由 handler 进行来源处理：优先使用提交的 Cookie，否则使用加密存储。
func (e *Engine) Submit(rawURL, platform, cookie string, userID int64) (string, error) {
	taskID, err := NewTaskID()
	if err != nil {
		return "", err
	}
	taskDir := SafeTaskDir(e.cfg.DownloadDir, taskID)
	if err := os.MkdirAll(taskDir, 0755); err != nil {
		return "", fmt.Errorf("创建任务目录失败: %w", err)
	}
	expiresAt := time.Now().Add(time.Duration(e.cfg.TaskExpireHours) * time.Hour)
	_, err = e.store.CreateTask(context.Background(), &models.DownloadTask{
		TaskID:    taskID,
		UserID:    userID,
		SourceURL: redactURLForLog(rawURL),
		Platform:  platform,
		Status:    models.TaskQueued,
		Progress:  0,
		TaskDir:   taskDir,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		CleanupDir(taskDir)
		return "", fmt.Errorf("保存任务失败: %w", err)
	}

	rt := &RuntimeTask{
		TaskID:    taskID,
		UserID:    userID,
		SourceURL: rawURL,
		Status:    "queued",
		Stage:     "已加入队列",
		Platform:  platform,
		CreatedAt: time.Now().Unix(),
		expiresAt: expiresAt,
	}
	e.registry.Add(rt)
	log.Printf("[downloader] 任务创建: %s (platform=%s, user=%d)", taskID, platform, userID)

	ctx, cancel := context.WithCancel(context.Background())
	rt.cancel = cancel
	go e.runDownloadTask(ctx, rt, rawURL, cookie, platform, taskDir)

	return taskID, nil
}

// Cancel 取消任务（仅运行时；DB 标记由 Delete 处理）。
func (e *Engine) Cancel(taskID string) {
	if t := e.registry.Get(taskID); t != nil && t.cancel != nil {
		t.cancel()
	}
}

// Get 获取运行时任务。
func (e *Engine) Get(taskID string) *RuntimeTask { return e.registry.Get(taskID) }

// DeleteTask 删除任务（内存 + 目录；DB 记录标记 cancelled）。
func (e *Engine) DeleteTask(taskID string) error {
	e.Cancel(taskID)
	t := e.registry.Get(taskID)
	if t != nil {
		_ = e.store.UpdateTaskCancelled(context.Background(), taskID)
		e.registry.Remove(taskID)
		if t.Filepath == "" {
			CleanupDir(SafeTaskDir(e.cfg.DownloadDir, taskID))
		} else {
			CleanupDir(filepath.Dir(t.Filepath))
		}
		return nil
	}
	// 无运行时任务：按数据库路径清理（属主校验在调用方）
	dt, err := e.store.GetTaskByID(context.Background(), taskID)
	if err == nil {
		_ = e.store.UpdateTaskCancelled(context.Background(), taskID)
		if dt.TaskDir != "" && safeTaskDirInside(dt.TaskDir, e.cfg.DownloadDir) {
			CleanupDir(dt.TaskDir)
		}
		return nil
	}
	// 兜底：直接按 id 尝试清理（必须经过安全边界检查）
	dir := SafeTaskDir(e.cfg.DownloadDir, taskID)
	if strings.TrimPrefix(dir, filepath.Clean(e.cfg.DownloadDir)+string(filepath.Separator)) != "" {
		CleanupDir(dir)
	}
	return nil
}

// safeTaskDirInside 防止路径穿越清理（只允许删除下载目录内的任务目录）。
func safeTaskDirInside(taskDir, downloadDir string) bool {
	abs := filepath.Clean(taskDir)
	base := filepath.Clean(downloadDir)
	if abs == base || !strings.HasPrefix(abs, base+string(filepath.Separator)) {
		return false
	}
	// 任务目录名要求 32 位 hex（随机 ID）
	name := filepath.Base(abs)
	if len(name) != 32 {
		return false
	}
	for _, ch := range name {
		if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')) {
			return false
		}
	}
	return true
}

// ============ 核心下载流程（移植原 main.go runDownloadTask）============

func (e *Engine) runDownloadTask(ctx context.Context, task *RuntimeTask, rawURL, cookie, platform, workDir string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[downloader] 任务 panic 恢复: %s: %v", task.TaskID, r)
			task.Status = "failed"
			task.Stage = "失败"
			task.Error = "内部错误"
			_ = e.store.UpdateTaskFailed(context.Background(), task.TaskID, "内部错误")
		}
	}()

	platformName := "未知平台"
	if info, ok := SupportedPlatforms[platform]; ok {
		platformName = info.Name
	}
	task.Status = "processing"
	task.Stage = "解析" + platformName + "视频信息中..."
	task.Progress = 5
	_ = e.store.UpdateTaskProgress(ctx, task.TaskID, 5)
	if err := e.store.MarkTaskProcessing(ctx, task.TaskID); err != nil {
		log.Printf("[downloader] 标记任务进行中失败: %v", err)
	}

	resolvedURL := ResolveShortURL(rawURL)
	if resolvedURL != rawURL {
		task.Stage = "短链已解析，获取视频信息中..."
		task.Progress = 8
		log.Printf("[downloader] 短链解析: %s", redactURLForLog(resolvedURL))
	}

	// 抖音：自研解析器直连最高画质（原逻辑）
	if platform == "douyin" {
		dv, derr := resolveDirectVideo(platform, resolvedURL, cookie)
		if derr == nil {
			e.runDirectDownload(ctx, task, workDir, platform, dv)
			return
		}
		log.Printf("[抖音] 自研解析失败，回退 yt-dlp: %v", derr)
	}

	// Cookie 文件（仅 yt-dlp 流程；目录内随机命名，任务结束/失败即清理）
	var cookieFile string
	if strings.TrimSpace(cookie) != "" {
		cookieFile = filepath.Join(workDir, "cookies.txt")
		if werr := WriteNetscapeCookieFile(strings.TrimSpace(cookie), cookieFile); werr != nil {
			task.Status = "failed"
			task.Stage = "失败"
			task.Error = "生成 cookie 文件失败: " + werr.Error()
			_ = e.store.UpdateTaskFailed(ctx, task.TaskID, task.Error)
			return
		}
	}

	infoOut, infoErr, rc := RunCmdCtx(ctx, BuildParseCmd(resolvedURL, cookieFile, platform))

	var bypassArgs []string
	if rc != 0 && platform == "youtube" && IsBotWall(infoErr) {
		log.Printf("[YouTube] 检测到反爬墙，自动切换备用客户端重试: %s", task.TaskID)
		task.Stage = "检测到反爬验证，自动切换客户端重试..."
		bypassArgs = youtubeBotBypassArgs
		infoOut, infoErr, rc = RunCmdCtx(ctx, BuildParseCmd(resolvedURL, cookieFile, platform, bypassArgs...))
	}

	if rc != 0 {
		task.Status = "failed"
		task.Stage = "失败"
		task.Error = "解析失败: " + truncate(infoErr, 500)
		_ = e.store.UpdateTaskFailed(ctx, task.TaskID, task.Error)
		CleanupDir(workDir)
		return
	}

	var info YtdlpInfo
	if err := json.Unmarshal([]byte(infoOut), &info); err != nil {
		task.Status = "failed"
		task.Stage = "失败"
		task.Error = "解析 JSON 失败: " + err.Error()
		_ = e.store.UpdateTaskFailed(ctx, task.TaskID, task.Error)
		CleanupDir(workDir)
		return
	}

	title := info.Title
	if title == "" {
		title = info.ID
	}
	if title == "" {
		title = "video"
	}
	task.Title = title
	task.Platform = platformName
	task.Stage = "已解析: " + title
	task.Progress = 15

	var bestVideo, bestAudio *YtdlpFormat
	if len(info.RequestedFormats) > 0 {
		bestVideo = &info.RequestedFormats[0]
		if len(info.RequestedFormats) > 1 {
			bestAudio = &info.RequestedFormats[1]
		}
	} else {
		bestVideo = selectBestVideo(info.Formats)
		bestAudio = selectBestAudio(info.Formats)
	}
	if bestVideo == nil && info.Vcodec != "" && info.Vcodec != "none" {
		bestVideo = &YtdlpFormat{
			Width: info.Width, Height: info.Height, Fps: info.Fps,
			Tbr: info.Tbr, Vcodec: info.Vcodec, FormatID: info.Format,
		}
	}

	if bestVideo != nil && bestVideo.Vcodec != "" {
		task.VideoFmt = map[string]interface{}{
			"format_id":  bestVideo.FormatID,
			"resolution": fmt.Sprintf("%dx%d", bestVideo.Width, bestVideo.Height),
			"fps":        bestVideo.Fps,
			"vcodec":     bestVideo.Vcodec,
			"vbr":        bestVideo.Vbr,
			"tbr":        bestVideo.Tbr,
		}
	}
	if bestAudio != nil {
		task.AudioFmt = map[string]interface{}{
			"format_id": bestAudio.FormatID,
			"acodec":    bestAudio.Acodec,
			"abr":       bestAudio.Abr,
			"asr":       bestAudio.Asr,
		}
	}
	if bestVideo != nil {
		task.Stage = fmt.Sprintf("已选择: %dx%d @ %.0ffps，开始下载...", bestVideo.Width, bestVideo.Height, bestVideo.Fps)
	}
	task.Progress = 20

	outputTemplate := filepath.Join(workDir, UnifiedOutputTemplate)
	downloadCmd := BuildDownloadCmd(resolvedURL, cookieFile, outputTemplate, platform, bypassArgs...)
	log.Printf("[%s] 执行下载命令: %s", platformName, joinCmdForLog(downloadCmd))

	segments := len(info.RequestedFormats)
	if segments < 1 {
		segments = 1
	}
	if segments > 2 {
		segments = 2
	}

	dlErr := e.runWithProgress(ctx, downloadCmd, task, 25, 95, segments, platformName)
	if dlErr != nil && platform == "youtube" && len(bypassArgs) == 0 && IsBotWall(dlErr.Error()) {
		log.Printf("[YouTube] 下载阶段检测到反爬墙，自动切换备用客户端重试: %s", task.TaskID)
		task.Stage = "检测到反爬验证，自动切换客户端重试..."
		task.Progress = 20
		retryCmd := BuildDownloadCmd(resolvedURL, cookieFile, outputTemplate, platform, youtubeBotBypassArgs...)
		log.Printf("[%s] 执行下载命令(重试): %s", platformName, joinCmdForLog(retryCmd))
		dlErr = e.runWithProgress(ctx, retryCmd, task, 25, 95, segments, platformName)
	}
	if dlErr != nil {
		task.Status = "failed"
		task.Stage = "失败"
		task.Error = redactErrorMessage(dlErr.Error())
		_ = e.store.UpdateTaskFailed(ctx, task.TaskID, task.Error)
		CleanupDir(workDir)
		return
	}

	_ = os.Remove(cookieFile)
	e.completeTask(ctx, task, workDir)
}

// runWithProgress 运行命令并流式更新任务状态。
func (e *Engine) runWithProgress(ctx context.Context, cmd []string, task *RuntimeTask, startPct, endPct float64, segments int, label string) error {
	sink := &ProgressSink{
		OnStage: func(stage string, progress float64) {
			task.Stage = stage
			if progress > 0 {
				task.Progress = progress
			}
		},
		OnProgress: func(p float64) {
			task.Progress = p
			lastWrite := atomic.LoadInt64(&task.lastProgressWrite)
			if time.Now().Unix() > lastWrite+2 {
				atomic.StoreInt64(&task.lastProgressWrite, time.Now().Unix())
				_ = e.store.UpdateTaskProgress(ctx, task.TaskID, p)
			}
		},
	}
	return RunCommandCtx(ctx, cmd, sink, startPct, endPct, segments, label)
}

// runDirectDownload 直连下载（原逻辑）。
func (e *Engine) runDirectDownload(ctx context.Context, task *RuntimeTask, workDir, platform string, dv *directVideo) {
	platformName := SupportedPlatforms[platform].Name
	task.Platform = platformName
	if strings.TrimSpace(dv.Title) != "" {
		task.Title = dv.Title
	} else {
		task.Title = platformName + "_video"
	}
	if dv.Width > 0 && dv.Height > 0 {
		task.VideoFmt = map[string]interface{}{
			"resolution": fmt.Sprintf("%dx%d", dv.Width, dv.Height),
			"quality":    dv.Quality,
		}
	}
	task.Stage = fmt.Sprintf("已选择: %s（%s），开始下载...", task.Title, dv.Quality)
	task.Progress = 20

	outputTemplate := filepath.Join(workDir, "video.%(ext)s")
	cmd := BuildDirectDownloadCmd(dv.URL, outputTemplate, platform, MobileUA)
	log.Printf("[%s] 直连下载: %s", platformName, redactURLForLog(dv.URL))
	log.Printf("[%s] 执行命令: %s", platformName, joinCmdForLog(cmd))

	if err := e.runWithProgress(ctx, cmd, task, 25, 95, 1, platformName); err != nil {
		task.Status = "failed"
		task.Stage = "失败"
		task.Error = redactErrorMessage(err.Error())
		_ = e.store.UpdateTaskFailed(ctx, task.TaskID, task.Error)
		CleanupDir(workDir)
		return
	}
	e.completeTask(ctx, task, workDir)
}

// completeTask 完成处理：查找文件 → 随机化磁盘文件名 → 标记完成（原逻辑扩展）。
func (e *Engine) completeTask(ctx context.Context, task *RuntimeTask, workDir string) {
	finalPath, finalName, err := findOutputFile(workDir)
	if err != nil {
		task.Status = "failed"
		task.Stage = "失败"
		task.Error = err.Error()
		_ = e.store.UpdateTaskFailed(ctx, task.TaskID, task.Error)
		CleanupDir(workDir)
		return
	}
	// 重命名为视频标题（保留展示名）；磁盘文件名随机化由下载模板 ID 决定，安全性由目录隔离保证
	if task.Title != "" && !strings.Contains(finalName, task.Title) {
		ext := filepath.Ext(finalName)
		newName := sanitizeFilename(task.Title) + ext
		newPath := filepath.Join(workDir, newName)
		if err := os.Rename(finalPath, newPath); err == nil {
			finalPath, finalName = newPath, newName
		}
	}

	fi, _ := os.Stat(finalPath)
	task.Status = "completed"
	task.Stage = "下载完成，等待浏览器拉取"
	task.Progress = 100
	task.Filename = finalName
	task.Filepath = finalPath
	task.AutoClean = true
	if fi != nil {
		task.Filesize = fi.Size()
	}
	task.CompletedAt = time.Now().Unix()
	_ = e.store.UpdateTaskComplete(ctx, task.TaskID, finalPath, finalName, task.Filesize, task.Title)
	log.Printf("[%s] 下载完成: %s (%d bytes)", task.Platform, finalName, task.Filesize)
}

// findOutputFile 查找最大有效媒体文件（原逻辑）。
func findOutputFile(workDir string) (string, string, error) {
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return "", "", fmt.Errorf("读取下载目录失败: %w", err)
	}
	var bestPath, bestName string
	var maxSize int64 = -1
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		name := ent.Name()
		lower := strings.ToLower(name)
		if strings.HasSuffix(lower, ".part") ||
			strings.HasSuffix(lower, ".ytdl") ||
			strings.HasSuffix(lower, ".temp") ||
			strings.HasSuffix(lower, ".frag") ||
			strings.HasSuffix(lower, ".tmp") ||
			strings.HasSuffix(lower, ".json") ||
			name == "cookies.txt" {
			continue
		}
		info, err := ent.Info()
		if err != nil {
			continue
		}
		if info.Size() > maxSize {
			maxSize = info.Size()
			bestPath = filepath.Join(workDir, name)
			bestName = name
		}
	}
	if bestPath == "" || maxSize <= 0 {
		return "", "", fmt.Errorf("yt-dlp 未生成有效的输出文件")
	}
	return bestPath, bestName, nil
}

// StartCleanupTimer 后台清理（原逻辑：10 分钟全量 + 拉取后宽限清理）。
func (e *Engine) StartCleanupTimer() {
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			e.CleanupAll()
		}
	}()
	go func() {
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			_ = e.store.CloseExpiredOrders(context.Background())
			_ = e.store.DeleteExpiredSessions(context.Background())
			_ = e.store.PurgeExpiredVerifyTokens(context.Background())
			_ = e.store.ExpireStaleMemberships(context.Background())
			_ = e.store.DeleteExpiredTaskRows(context.Background())
		}
	}()
}

// CleanupAll 清理过期残留（原 cleanupAllDownloads 逻辑）。
func (e *Engine) CleanupAll() {
	const maxAge = 30 * time.Minute
	now := time.Now()

	protected := e.registry.protectedDirs("")

	entries, err := os.ReadDir(e.cfg.DownloadDir)
	if err == nil {
		for _, ent := range entries {
			if ent.Name() == ".gitkeep" || protected[ent.Name()] {
				continue
			}
			info, err := ent.Info()
			if err != nil {
				continue
			}
			if now.Sub(info.ModTime()) < maxAge {
				continue
			}
			os.RemoveAll(filepath.Join(e.cfg.DownloadDir, ent.Name()))
			log.Printf("🧹 清理过期残留: %s", ent.Name())
		}
	}
}

// scheduleTaskCleanup 拉取结束后宽限清理（原逻辑：60 秒）。
func (e *Engine) scheduleTaskCleanup(taskID string) {
	go func() {
		for {
			task := e.registry.Get(taskID)
			if task == nil {
				return
			}
			if atomic.LoadInt32(&task.PullActive) > 0 {
				time.Sleep(30 * time.Second)
				continue
			}
			ca := atomic.LoadInt64(&task.CleanupAfter)
			if ca <= 0 {
				return
			}
			if wait := time.Until(time.Unix(ca, 0)); wait > 0 {
				time.Sleep(wait)
				continue
			}
			CleanupDir(filepath.Dir(task.Filepath))
			e.registry.Remove(taskID)
			log.Printf("🧹 已自动清理任务: %s", taskID)
			return
		}
	}()
}

// MarkPullDone 拉取结束后调度清理（原 handleFile 尾部逻辑）。
func (e *Engine) MarkPullDone(taskID string) {
	task := e.registry.Get(taskID)
	if task == nil {
		return
	}
	if atomic.AddInt32(&task.PullActive, -1) == 0 && task.AutoClean {
		atomic.StoreInt64(&task.CleanupAfter, time.Now().Add(60*time.Second).Unix())
		log.Printf("推送结束，60 秒后自动清理: %s", taskID)
		e.scheduleTaskCleanup(taskID)
	}
}

// StartPull 拉取开始保护。
func (e *Engine) StartPull(taskID string) bool {
	task := e.registry.Get(taskID)
	if task == nil {
		return false
	}
	// 仅已完成任务可拉取（DB 记录的已完成任务同样允许）
	if task.Status != "completed" {
		return false
	}
	atomic.AddInt32(&task.PullActive, 1)
	return true
}

// TaskFilePath 完成任务文件绝对路径（属主校验在调用方完成；路径安全由生成逻辑保证）。
func (e *Engine) TaskFilePath(task *RuntimeTask) string {
	if task == nil {
		return ""
	}
	dir := filepath.Dir(task.Filepath)
	if !safeTaskDirInside(dir, e.cfg.DownloadDir) {
		return ""
	}
	return task.Filepath
}

// ============ 工具 ============

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// redactURLForLog 日志脱敏 URL（仅保留 scheme+host+路径，剔除查询串与令牌）。
func redactURLForLog(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "[invalid-url]"
	}
	return u.Scheme + "://" + u.Host + "/...?[redacted]"
}

// redactErrorMessage 错误信息脱敏（剔除 Cookie/令牌等关键字前后内容）。
func redactErrorMessage(s string) string {
	if s == "" {
		return s
	}
	return redactMessage(s)
}

// joinCmdForLog 命令日志脱敏（不含 cookie 值；剔除内嵌敏感片段）。
func joinCmdForLog(cmd []string) string {
	joined := strings.Join(cmd, " ")
	return redactMessage(joined)
}

// redactMessage 保守脱敏：遇到敏感模式即截断。
func redactMessage(s string) string {
	patterns := []string{"SESSDATA", "sessionid", "web_session", "msToken", "__Secure-", "Authorization:", "Cookie:", "token=", "password="}
	lower := strings.ToLower(s)
	for _, p := range patterns {
		if idx := strings.Index(lower, strings.ToLower(p)); idx >= 0 {
			return s[:idx] + p + " [REDACTED]"
		}
	}
	return s
}

