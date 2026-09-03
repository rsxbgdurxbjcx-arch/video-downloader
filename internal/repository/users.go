package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"video-downloader/internal/models"
)

// ErrNotFound 表示记录不存在。
var ErrNotFound = errors.New("record not found")

// ErrDuplicate 表示唯一约束冲突（邮箱已存在等）。
var ErrDuplicate = errors.New("duplicate record")

const userCols = `id, email, password_hash, email_verified_at, role, status, created_at, updated_at, last_login_at`

func scanUserRow(row *sql.Row) (*models.User, error) {
	var (
		u                   models.User
		verified, lastLogin nullUnixTime
		created, updated    unixTime
	)
	err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &verified, &u.Role, &u.Status,
		&created, &updated, &lastLogin)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.EmailVerifiedAt = verified.t
	u.LastLoginAt = lastLogin.t
	u.CreatedAt = *created.t
	u.UpdatedAt = *updated.t
	return &u, nil
}

// CreateUser 创建用户。
func (s *Store) CreateUser(ctx context.Context, email, passwordHash, role, status string) (*models.User, error) {
	now := time.Now().Unix()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO users
		(email, password_hash, role, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		email, passwordHash, role, status, now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrDuplicate
		}
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetUserByID(ctx, id)
}

// GetUserByID 按 ID 查找用户。
func (s *Store) GetUserByID(ctx context.Context, id int64) (*models.User, error) {
	return scanUserRow(s.DB.QueryRowContext(ctx,
		`SELECT `+userCols+` FROM users WHERE id=?`, id))
}

// GetUserByEmail 按邮箱（已小写化）查找用户。
func (s *Store) GetUserByEmail(ctx context.Context, email string) (*models.User, error) {
	return scanUserRow(s.DB.QueryRowContext(ctx,
		`SELECT `+userCols+` FROM users WHERE email=?`, email))
}

// ListUsers 分页列出用户（管理员）。
func (s *Store) ListUsers(ctx context.Context, limit, offset int) ([]*models.User, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+userCols+` FROM users ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.User
	for rows.Next() {
		var (
			u                   models.User
			verified, lastLogin nullUnixTime
			created, updated    unixTime
		)
		if err := rows.Scan(&u.ID, &u.Email, &u.PasswordHash, &verified, &u.Role, &u.Status,
			&created, &updated, &lastLogin); err != nil {
			return nil, err
		}
		u.EmailVerifiedAt = verified.t
		u.LastLoginAt = lastLogin.t
		u.CreatedAt = *created.t
		u.UpdatedAt = *updated.t
		out = append(out, &u)
	}
	return out, rows.Err()
}

// CountUsers 用户总数。
func (s *Store) CountUsers(ctx context.Context) (int64, error) {
	var n int64
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM users`).Scan(&n)
	return n, err
}

// UpdateUserStatus 更新用户状态（禁用/解禁）。
func (s *Store) UpdateUserStatus(ctx context.Context, id int64, status string) error {
	now := time.Now().Unix()
	_, err := s.DB.ExecContext(ctx,
		`UPDATE users SET status=?, updated_at=? WHERE id=?`, status, now, id)
	return err
}

// MarkUserVerified 将用户标记为已验证并激活。
func (s *Store) MarkUserVerified(ctx context.Context, id int64) error {
	now := time.Now().Unix()
	_, err := s.DB.ExecContext(ctx,
		`UPDATE users SET email_verified_at=?, status='active', updated_at=? WHERE id=?`,
		now, now, id)
	return err
}

// UpdateLastLogin 更新最后登录时间。
func (s *Store) UpdateLastLogin(ctx context.Context, id int64) error {
	now := time.Now().Unix()
	_, err := s.DB.ExecContext(ctx,
		`UPDATE users SET last_login_at=?, updated_at=? WHERE id=?`, now, now, id)
	return err
}

// UpdateUserPassword 更新密码。
func (s *Store) UpdateUserPassword(ctx context.Context, id int64, passwordHash string) error {
	now := time.Now().Unix()
	_, err := s.DB.ExecContext(ctx,
		`UPDATE users SET password_hash=?, updated_at=? WHERE id=?`, passwordHash, now, id)
	return err
}

// UpdateUserEmail 更新邮箱（管理员初始自举迁移用；邮箱唯一约束由数据库保证）。
func (s *Store) UpdateUserEmail(ctx context.Context, id int64, email string) error {
	now := time.Now().Unix()
	_, err := s.DB.ExecContext(ctx,
		`UPDATE users SET email=?, updated_at=? WHERE id=?`, email, now, id)
	return err
}

// SetUserRole 设置角色（仅管理员功能）。
func (s *Store) SetUserRole(ctx context.Context, id int64, role string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE users SET role=?, updated_at=? WHERE id=?`, role, time.Now().Unix(), id)
	return err
}

// isUniqueViolation 判断唯一约束错误（SQLITE_CONSTRAINT / UNIQUE）。
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return hasSubstr(msg, "UNIQUE constraint failed") || hasSubstr(msg, "constraint failed")
}

func hasSubstr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
