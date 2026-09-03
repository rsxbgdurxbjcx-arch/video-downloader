package repository

import (
	"context"
	"time"

	"video-downloader/internal/models"
)

// CreateEmailSendLog 记录一次邮件发送结果（emailHash 脱敏；errMsg 不含凭据/验证码）。
func (s *Store) CreateEmailSendLog(ctx context.Context, emailHash, purpose string, ok bool, errMsg string) error {
	okInt := 0
	if ok {
		okInt = 1
	}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO email_send_logs
		(email_hash, purpose, ok, err_msg, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		emailHash, purpose, okInt, errMsg, time.Now().Unix())
	return err
}

// ListEmailSendLogs 最近邮件发送日志（管理员诊断用）。
func (s *Store) ListEmailSendLogs(ctx context.Context, limit, offset int) ([]*models.EmailSendLog, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, email_hash, purpose, ok, err_msg, created_at
		FROM email_send_logs ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.EmailSendLog
	for rows.Next() {
		var (
			l       models.EmailSendLog
			created unixTime
		)
		if err := rows.Scan(&l.ID, &l.EmailHash, &l.Purpose, &l.OK, &l.ErrMsg, &created); err != nil {
			return nil, err
		}
		l.CreatedAt = *created.t
		out = append(out, &l)
	}
	return out, rows.Err()
}
