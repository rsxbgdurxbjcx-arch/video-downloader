package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// CreateSession 创建会话（仅存 Token 哈希）。
func (s *Store) CreateSession(ctx context.Context, userID int64, tokenHash, userAgent, ipHash string, expiresAt time.Time) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO sessions
		(user_id, token_hash, expires_at, created_at, last_seen_at, user_agent, ip_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		userID, tokenHash, expiresAt.Unix(), time.Now().Unix(), time.Now().Unix(), userAgent, ipHash)
	return err
}

// GetSessionByHash 按 Token 哈希查找会话。
func (s *Store) GetSessionByHash(ctx context.Context, tokenHash string) (*int64, error) {
	var userID int64
	err := s.DB.QueryRowContext(ctx,
		`SELECT user_id FROM sessions WHERE token_hash=? AND expires_at>?`,
		tokenHash, time.Now().Unix()).Scan(&userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &userID, nil
}

// TouchSession 更新会话 last_seen_at。
func (s *Store) TouchSession(ctx context.Context, tokenHash string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE sessions SET last_seen_at=? WHERE token_hash=?`, time.Now().Unix(), tokenHash)
	return err
}

// DeleteSession 删除单条会话（退出登录）。
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, tokenHash)
	return err
}

// DeleteUserSessions 删除用户全部会话（退出全部 / 修改密码）。
func (s *Store) DeleteUserSessions(ctx context.Context, userID int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, userID)
	return err
}

// DeleteExpiredSessions 清理过期会话。
func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, time.Now().Unix())
	return err
}
