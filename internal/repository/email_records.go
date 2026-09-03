package repository

import (
	"context"
	"time"

)

// CreateEmailSendRecord 记录一次邮件发送（全部字段脱敏）。
func (s *Store) CreateEmailSendRecord(ctx context.Context, emailHash string, userID *int64, purpose, ipHash string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO email_send_records
		(email_hash, user_id, purpose, request_ip_hash, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		emailHash, nullableInt64(userID), purpose, ipHash, time.Now().Unix())
	return err
}

// CountEmailSince 统计某邮箱（哈希）在 since 之后发送的邮件数。
func (s *Store) CountEmailSince(ctx context.Context, emailHash string, since time.Time) (int64, error) {
	var n int64
	err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM email_send_records WHERE email_hash=? AND created_at>=?`,
		emailHash, since.Unix()).Scan(&n)
	return n, err
}

// CountIPSince 统计某 IP（哈希）在 since 之后发送的邮件数。
func (s *Store) CountIPSince(ctx context.Context, ipHash string, since time.Time) (int64, error) {
	var n int64
	err := s.DB.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM email_send_records WHERE request_ip_hash=? AND created_at>=?`,
		ipHash, since.Unix()).Scan(&n)
	return n, err
}

// LatestEmailSend 某邮箱最近一次邮件发送时间。
func (s *Store) LatestEmailSend(ctx context.Context, emailHash string) (*time.Time, error) {
	var created unixTime
	err := s.DB.QueryRowContext(ctx,
		`SELECT created_at FROM email_send_records WHERE email_hash=? ORDER BY id DESC LIMIT 1`,
		emailHash).Scan(&created)
	if err != nil {
		return nil, err
	}
	return created.t, nil
}
