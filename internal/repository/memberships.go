package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"video-downloader/internal/models"
)

const membershipCols = `id, user_id, plan_id, starts_at, expires_at, status, source_order_id, created_at, updated_at`

func scanMembershipRow(row *sql.Row) (*models.Membership, error) {
	var (
		m                  models.Membership
		sourceOrder        sql.NullInt64
		starts, expires    unixTime
		created, updated   unixTime
	)
	err := row.Scan(&m.ID, &m.UserID, &m.PlanID, &starts, &expires, &m.Status,
		&sourceOrder, &created, &updated)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	m.StartsAt = *starts.t
	m.ExpiresAt = *expires.t
	m.CreatedAt = *created.t
	m.UpdatedAt = *updated.t
	if sourceOrder.Valid {
		v := sourceOrder.Int64
		m.SourceOrderID = &v
	}
	return &m, nil
}

// GetActiveMembership 返回用户当前有效会员记录；无则 ErrNotFound。
func (s *Store) GetActiveMembership(ctx context.Context, userID int64) (*models.Membership, error) {
	return scanMembershipRow(s.DB.QueryRowContext(ctx,
		`SELECT `+membershipCols+` FROM user_memberships
		 WHERE user_id=? AND status='active' AND expires_at>=?
		 ORDER BY expires_at DESC LIMIT 1`,
		userID, time.Now().Unix()))
}

// GetMembershipByOrder 按订单查询会员记录（用于幂等校验）。
func (s *Store) GetMembershipByOrder(ctx context.Context, orderID int64) (*models.Membership, error) {
	return scanMembershipRow(s.DB.QueryRowContext(ctx,
		`SELECT `+membershipCols+` FROM user_memberships WHERE source_order_id=?`,
		orderID))
}

// CreateMembership 创建会员记录。
// 续费顺延逻辑：effectiveStart = max(当前有效会员 expires_at, now)。
// 调用方负责在事务中执行。
func (s *Store) CreateMembership(ctx context.Context, userID, planID int64, durationDays int, effectiveStart time.Time, sourceOrderID *int64) (*models.Membership, error) {
	now := time.Now().Unix()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO user_memberships
		(user_id, plan_id, starts_at, expires_at, status, source_order_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'active', ?, ?, ?)`,
		userID, planID, effectiveStart.Unix(), effectiveStart.AddDate(0, 0, durationDays).Unix(),
		nullableInt64(sourceOrderID), now, now)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return scanMembershipRow(s.DB.QueryRowContext(ctx,
		`SELECT `+membershipCols+` FROM user_memberships WHERE id=?`, id))
}

// ListUserMemberships 用户会员记录列表。
func (s *Store) ListUserMemberships(ctx context.Context, userID int64) ([]*models.Membership, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+membershipCols+` FROM user_memberships WHERE user_id=? ORDER BY id DESC`,
		userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.Membership
	for rows.Next() {
		var (
			m                models.Membership
			sourceOrder      sql.NullInt64
			starts, expires  unixTime
			created, updated unixTime
		)
		if err := rows.Scan(&m.ID, &m.UserID, &m.PlanID, &starts, &expires, &m.Status,
			&sourceOrder, &created, &updated); err != nil {
			return nil, err
		}
		m.StartsAt = *starts.t
		m.ExpiresAt = *expires.t
		m.CreatedAt = *created.t
		m.UpdatedAt = *updated.t
		if sourceOrder.Valid {
			v := sourceOrder.Int64
			m.SourceOrderID = &v
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

// SetMembershipStatus 更新会员记录状态（管理员修复用）。
func (s *Store) SetMembershipStatus(ctx context.Context, id int64, status string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE user_memberships SET status=?, updated_at=? WHERE id=?`,
		status, time.Now().Unix(), id)
	return err
}

// ExpireStaleMemberships 将已到期会员标记为 expired（后台任务）。
func (s *Store) ExpireStaleMemberships(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE user_memberships SET status='expired', updated_at=?
		 WHERE status='active' AND expires_at < ?`,
		time.Now().Unix(), time.Now().Unix())
	return err
}
