package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"video-downloader/internal/models"
)

const taskCols = `id, task_id, user_id, source_url, platform, status, progress, task_dir,
	output_path, filename, filesize, error_message, title, created_at, started_at,
	completed_at, expires_at`

func scanTaskRow(row *sql.Row) (*models.DownloadTask, error) {
	var (
		t                       models.DownloadTask
		started, completed      nullUnixTime
		created, expires        unixTime
	)
	err := row.Scan(&t.ID, &t.TaskID, &t.UserID, &t.SourceURL, &t.Platform, &t.Status,
		&t.Progress, &t.TaskDir, &t.OutputPath, &t.Filename, &t.FileSize, &t.ErrorMessage,
		&t.Title, &created, &started, &completed, &expires)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	t.CreatedAt = *created.t
	t.ExpiresAt = *expires.t
	t.StartedAt = started.t
	t.CompletedAt = completed.t
	return &t, nil
}

// CreateTask 创建下载任务。
func (s *Store) CreateTask(ctx context.Context, t *models.DownloadTask) (*models.DownloadTask, error) {
	now := time.Now().Unix()
	var started, completed any
	if t.StartedAt != nil {
		started = t.StartedAt.Unix()
	}
	if t.CompletedAt != nil {
		completed = t.CompletedAt.Unix()
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO download_tasks
		(task_id, user_id, source_url, platform, status, progress, task_dir, output_path,
		 filename, filesize, error_message, title, created_at, started_at, completed_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.TaskID, t.UserID, t.SourceURL, t.Platform, t.Status, t.Progress, t.TaskDir,
		t.OutputPath, t.Filename, t.FileSize, t.ErrorMessage, t.Title, now, started, completed,
		t.ExpiresAt.Unix())
	if err != nil {
		return nil, err
	}
	return s.GetTaskByID(ctx, t.TaskID)
}

// GetTaskByID 按任务 ID 查找。
func (s *Store) GetTaskByID(ctx context.Context, taskID string) (*models.DownloadTask, error) {
	return scanTaskRow(s.DB.QueryRowContext(ctx,
		`SELECT `+taskCols+` FROM download_tasks WHERE task_id=?`, taskID))
}

// GetTaskByIDAndUser 按任务 ID + 用户查找（属主校验）。
func (s *Store) GetTaskByIDAndUser(ctx context.Context, taskID string, userID int64) (*models.DownloadTask, error) {
	return scanTaskRow(s.DB.QueryRowContext(ctx,
		`SELECT `+taskCols+` FROM download_tasks WHERE task_id=? AND user_id=?`, taskID, userID))
}

// MarkTaskProcessing 标记任务进入处理中。
func (s *Store) MarkTaskProcessing(ctx context.Context, taskID string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE download_tasks SET
		status='processing', started_at=COALESCE(started_at, ?) WHERE task_id=?`,
		time.Now().Unix(), taskID)
	return err
}

// UpdateTaskProgress 更新进度。
func (s *Store) UpdateTaskProgress(ctx context.Context, taskID string, progress float64) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE download_tasks SET progress=? WHERE task_id=?`, progress, taskID)
	return err
}

// UpdateTaskComplete 标记完成并写入文件信息。
func (s *Store) UpdateTaskComplete(ctx context.Context, taskID, outputPath, filename string, filesize int64, title string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE download_tasks SET
		status='completed', progress=100, output_path=?, filename=?, filesize=?, title=?,
		completed_at=?, error_message='' WHERE task_id=?`,
		outputPath, filename, filesize, title, time.Now().Unix(), taskID)
	return err
}

// UpdateTaskFailed 标记失败。
func (s *Store) UpdateTaskFailed(ctx context.Context, taskID, errMsg string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE download_tasks SET
		status='failed', error_message=?, completed_at=? WHERE task_id=?`,
		errMsg, time.Now().Unix(), taskID)
	return err
}

// UpdateTaskCancelled 标记取消。
func (s *Store) UpdateTaskCancelled(ctx context.Context, taskID string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE download_tasks SET
		status='cancelled', completed_at=? WHERE task_id=?
		AND status IN ('queued','processing')`,
		time.Now().Unix(), taskID)
	return err
}

// CountUserActiveTasks 用户进行中任务数（并发限制）。
func (s *Store) CountUserActiveTasks(ctx context.Context, userID int64) (int64, error) {
	var n int64
	err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM download_tasks
		 WHERE user_id=? AND status IN ('queued','processing')`, userID).Scan(&n)
	return n, err
}

// CountUserTasksSince 用户自 since 起创建的任务数（每日额度/总额度）。
func (s *Store) CountUserTasksSince(ctx context.Context, userID int64, since time.Time) (int64, error) {
	var n int64
	err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM download_tasks WHERE user_id=? AND created_at>=?`,
		userID, since.Unix()).Scan(&n)
	return n, err
}

// ListUserTasks 用户任务列表。
func (s *Store) ListUserTasks(ctx context.Context, userID int64, limit, offset int) ([]*models.DownloadTask, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+taskCols+` FROM download_tasks WHERE user_id=? ORDER BY id DESC LIMIT ? OFFSET ?`,
		userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectTasks(rows)
}

// ListAllTasks 全部任务（管理员）。
func (s *Store) ListAllTasks(ctx context.Context, limit, offset int) ([]*models.DownloadTask, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+taskCols+` FROM download_tasks ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectTasks(rows)
}

func collectTasks(rows *sql.Rows) ([]*models.DownloadTask, error) {
	var out []*models.DownloadTask
	for rows.Next() {
		var (
			t                  models.DownloadTask
			started, completed nullUnixTime
			created, expires   unixTime
		)
		if err := rows.Scan(&t.ID, &t.TaskID, &t.UserID, &t.SourceURL, &t.Platform, &t.Status,
			&t.Progress, &t.TaskDir, &t.OutputPath, &t.Filename, &t.FileSize, &t.ErrorMessage,
			&t.Title, &created, &started, &completed, &expires); err != nil {
			return nil, err
		}
		t.CreatedAt = *created.t
		t.ExpiresAt = *expires.t
		t.StartedAt = started.t
		t.CompletedAt = completed.t
		out = append(out, &t)
	}
	return out, rows.Err()
}

// DeleteExpiredTaskRows 清理过期任务记录（文件层由 downloader 清理）。
func (s *Store) DeleteExpiredTaskRows(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM download_tasks WHERE expires_at < ?`,
		time.Now().Add(-24*time.Hour).Unix())
	return err
}
