package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"video-downloader/internal/models"
)

// CreateVerifyToken 写入验证码哈希（6 位数字验证码的 SHA-256；attempts 初始 0）。
func (s *Store) CreateVerifyToken(ctx context.Context, userID int64, tokenHash, ipHash string, expiresAt time.Time) error {
	now := time.Now().Unix()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO email_verification_tokens
		(user_id, token_hash, expires_at, created_at, requested_ip_hash, attempts)
		VALUES (?, ?, ?, ?, ?, 0)`,
		userID, tokenHash, expiresAt.Unix(), now, ipHash)
	return err
}

// InvalidteUserTokens 作废某用户全部未使用验证码（生成新验证码时调用）。
func (s *Store) InvalidteUserTokens(ctx context.Context, userID int64) error {
	now := time.Now().Unix()
	_, err := s.DB.ExecContext(ctx,
		`UPDATE email_verification_tokens SET used_at=? WHERE user_id=? AND used_at IS NULL`,
		now, userID)
	return err
}

// GetLatestPendingToken 返回某用户最新一条未使用且未过期的验证码令牌。
func (s *Store) GetLatestPendingToken(ctx context.Context, userID int64, now time.Time) (*models.VerifyToken, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT id, user_id, token_hash, expires_at, used_at, created_at, requested_ip_hash, attempts
		FROM email_verification_tokens
		WHERE user_id=? AND used_at IS NULL AND expires_at>?
		ORDER BY id DESC LIMIT 1`, userID, now.Unix())
	return scanVerifyToken(row)
}

// GetVerifyTokenByHash 按哈希查找令牌（含过期标志，由调用方校验；兼容/防御保留）。
func (s *Store) GetVerifyTokenByHash(ctx context.Context, tokenHash string) (*models.VerifyToken, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT id, user_id, token_hash, expires_at, used_at, created_at, requested_ip_hash, attempts
		FROM email_verification_tokens WHERE token_hash=?`, tokenHash)
	return scanVerifyToken(row)
}

func scanVerifyToken(row *sql.Row) (*models.VerifyToken, error) {
	var (
		t       models.VerifyToken
		usedAt  nullUnixTime
		expires unixTime
		created unixTime
		ipHash  nullString
	)
	err := row.Scan(&t.ID, &t.UserID, &t.TokenHash, &expires, &usedAt, &created, &ipHash, &t.Attempts)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	t.ExpiresAt = *expires.t
	t.UsedAt = usedAt.t
	t.CreatedAt = *created.t
	t.RequestedIPHash = ipHash.s
	return &t, nil
}

// IncrementVerifyAttempts 递增验证码错误尝试次数。
func (s *Store) IncrementVerifyAttempts(ctx context.Context, id int64) (int, error) {
	if _, err := s.DB.ExecContext(ctx,
		`UPDATE email_verification_tokens SET attempts=attempts+1 WHERE id=?`, id); err != nil {
		return 0, err
	}
	var n int
	err := s.DB.QueryRowContext(ctx,
		`SELECT attempts FROM email_verification_tokens WHERE id=?`, id).Scan(&n)
	return n, err
}

// MarkVerifyTokenUsed 标记令牌已使用（单次使用）。
func (s *Store) MarkVerifyTokenUsed(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE email_verification_tokens SET used_at=? WHERE id=? AND used_at IS NULL`,
		time.Now().Unix(), id)
	return err
}

// DeletePendingVerifyTokens 删除某用户全部未使用验证码（验证成功后"立即删除验证码"）。
func (s *Store) DeletePendingVerifyTokens(ctx context.Context, userID int64) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM email_verification_tokens WHERE user_id=? AND used_at IS NULL`, userID)
	return err
}

// PurgeExpiredVerifyTokens 清理过期令牌（后台任务）。
func (s *Store) PurgeExpiredVerifyTokens(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx,
		`DELETE FROM email_verification_tokens WHERE expires_at < ?`, time.Now().Add(-24*time.Hour).Unix())
	return err
}
