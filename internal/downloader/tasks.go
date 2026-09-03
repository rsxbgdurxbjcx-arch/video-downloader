package downloader

import (
	"sync"
	"sync/atomic"
	"time"
)

// RuntimeTask 运行时任务（内存态；持久化镜像在 download_tasks 表）。
type RuntimeTask struct {
	TaskID     string
	UserID     int64
	SourceURL  string
	Status     string
	Stage      string
	Progress   float64
	Title      string
	Platform   string
	Error      string
	Filename   string
	Filepath   string
	Filesize   int64
	CreatedAt  int64
	CompletedAt int64
	VideoFmt   map[string]interface{}
	AudioFmt   map[string]interface{}
	AutoClean  bool
	// 拉取保护（原逻辑）
	PullActive   int32
	CleanupAfter int64
	lastProgressWrite int64
	expiresAt    time.Time
	cancel       func()
}

// TaskJSON 兼容旧前端的任务 JSON（新字段均安全、无内部路径）。
type TaskJSON struct {
	TaskID      string                 `json:"task_id"`
	Status      string                 `json:"status"`
	Stage       string                 `json:"stage"`
	Progress    float64                `json:"progress"`
	Title       string                 `json:"title"`
	Platform    string                 `json:"platform"`
	Error       string                 `json:"error"`
	Filename    string                 `json:"filename"`
	Filesize    int64                  `json:"filesize"`
	CreatedAt   int64                  `json:"created_at"`
	CompletedAt int64                  `json:"completed_at"`
	VideoFmt    map[string]interface{} `json:"video_format,omitempty"`
	AudioFmt    map[string]interface{} `json:"audio_format,omitempty"`
	AutoClean   bool                   `json:"auto_clean,omitempty"`
}

// ToJSON 转为对外 JSON（不含 filepath 等内部字段）。
func (t *RuntimeTask) ToJSON() TaskJSON {
	return TaskJSON{
		TaskID:      t.TaskID,
		Status:      t.Status,
		Stage:       t.Stage,
		Progress:    t.Progress,
		Title:       t.Title,
		Platform:    t.Platform,
		Error:       t.Error,
		Filename:    t.Filename,
		Filesize:    t.Filesize,
		CreatedAt:   t.CreatedAt,
		CompletedAt: t.CompletedAt,
		VideoFmt:    t.VideoFmt,
		AudioFmt:    t.AudioFmt,
		AutoClean:   t.AutoClean,
	}
}

// TaskRegistry 任务注册表（并发安全）。
type TaskRegistry struct {
	mu    sync.RWMutex
	tasks map[string]*RuntimeTask
}

// NewTaskRegistry 创建注册表。
func NewTaskRegistry() *TaskRegistry {
	return &TaskRegistry{tasks: map[string]*RuntimeTask{}}
}

// Add 注册任务。
func (r *TaskRegistry) Add(t *RuntimeTask) {
	r.mu.Lock()
	r.tasks[t.TaskID] = t
	r.mu.Unlock()
}

// Get 获取任务。
func (r *TaskRegistry) Get(taskID string) *RuntimeTask {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tasks[taskID]
}

// Remove 移除任务。
func (r *TaskRegistry) Remove(taskID string) {
	r.mu.Lock()
	delete(r.tasks, taskID)
	r.mu.Unlock()
}

// List 列出任务（支持按用户过滤；nil 为全部）。惰性返回运行时任务副本（指针）。
func (r *TaskRegistry) List(userID int64) []*RuntimeTask {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*RuntimeTask
	for _, t := range r.tasks {
		if userID > 0 && t.UserID != userID {
			continue
		}
		out = append(out, t)
	}
	return out
}

// protectedDirs 保护任务集中（清理跳过进行中/拉取中/宽限期任务）。
func (r *TaskRegistry) protectedDirs(exclude string) map[string]bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := map[string]bool{}
	now := time.Now()
	for id, t := range r.tasks {
		if id == exclude {
			continue
		}
		if t.Status == "queued" || t.Status == "processing" {
			out[id] = true
			continue
		}
		if atomic.LoadInt32(&t.PullActive) > 0 {
			out[id] = true
			continue
		}
		if ca := atomic.LoadInt64(&t.CleanupAfter); ca > 0 && now.Unix() < ca {
			out[id] = true
		}
	}
	return out
}
