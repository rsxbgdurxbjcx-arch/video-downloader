package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"video-downloader/internal/models"
)

const planCols = `id, name, description, price_cents, duration_days, download_limit,
	daily_download_limit, max_concurrent_tasks, max_file_size, allowed_quality,
	enabled, sort_order, created_at, updated_at`

func scanPlanRow(row *sql.Row) (*models.Plan, error) {
	var (
		p              models.Plan
		created, updated unixTime
	)
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.PriceCents, &p.DurationDays,
		&p.DownloadLimit, &p.DailyDownloadLimit, &p.MaxConcurrentTasks,
		&p.MaxFileSize, &p.AllowedQuality, &p.Enabled, &p.SortOrder, &created, &updated)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	p.CreatedAt = *created.t
	p.UpdatedAt = *updated.t
	return &p, nil
}

// GetPlan 按 ID 查找套餐（不管是否启用）。
func (s *Store) GetPlan(ctx context.Context, id int64) (*models.Plan, error) {
	return scanPlanRow(s.DB.QueryRowContext(ctx,
		`SELECT `+planCols+` FROM membership_plans WHERE id=?`, id))
}

// ListPlans 列出套餐（enabledOnly 时只返回启用项）。
func (s *Store) ListPlans(ctx context.Context, enabledOnly bool) ([]*models.Plan, error) {
	q := `SELECT ` + planCols + ` FROM membership_plans`
	if enabledOnly {
		q += ` WHERE enabled=1`
	}
	q += ` ORDER BY sort_order, id`
	rows, err := s.DB.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Plan
	for rows.Next() {
		var (
			p                models.Plan
			created, updated unixTime
		)
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.PriceCents, &p.DurationDays,
			&p.DownloadLimit, &p.DailyDownloadLimit, &p.MaxConcurrentTasks,
			&p.MaxFileSize, &p.AllowedQuality, &p.Enabled, &p.SortOrder, &created, &updated); err != nil {
			return nil, err
		}
		p.CreatedAt = *created.t
		p.UpdatedAt = *updated.t
		out = append(out, &p)
	}
	return out, rows.Err()
}

// CreatePlan 创建套餐。
func (s *Store) CreatePlan(ctx context.Context, p *models.Plan) (*models.Plan, error) {
	now := time.Now().Unix()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO membership_plans
		(name, description, price_cents, duration_days, download_limit, daily_download_limit,
		 max_concurrent_tasks, max_file_size, allowed_quality, enabled, sort_order, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.Name, p.Description, p.PriceCents, p.DurationDays, p.DownloadLimit, p.DailyDownloadLimit,
		p.MaxConcurrentTasks, p.MaxFileSize, p.AllowedQuality, boolToInt(p.Enabled), p.SortOrder, now, now)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetPlan(ctx, id)
}

// UpdatePlan 更新套餐（不更新金额校验由 handler 完成）。
func (s *Store) UpdatePlan(ctx context.Context, p *models.Plan) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE membership_plans SET
		name=?, description=?, price_cents=?, duration_days=?, download_limit=?,
		daily_download_limit=?, max_concurrent_tasks=?, max_file_size=?, allowed_quality=?,
		enabled=?, sort_order=?, updated_at=? WHERE id=?`,
		p.Name, p.Description, p.PriceCents, p.DurationDays, p.DownloadLimit,
		p.DailyDownloadLimit, p.MaxConcurrentTasks, p.MaxFileSize, p.AllowedQuality,
		boolToInt(p.Enabled), p.SortOrder, time.Now().Unix(), p.ID)
	return err
}

// SetPlanEnabled 启用/停用套餐。
func (s *Store) SetPlanEnabled(ctx context.Context, id int64, enabled bool) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE membership_plans SET enabled=?, updated_at=? WHERE id=?`,
		boolToInt(enabled), time.Now().Unix(), id)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
