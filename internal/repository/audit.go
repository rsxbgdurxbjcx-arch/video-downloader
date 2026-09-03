package repository

import (
	"context"
	"strconv"
	"time"

	"video-downloader/internal/models"
)

// RecordAudit 写入审计日志（detail 必须已脱敏）。
func (s *Store) RecordAudit(ctx context.Context, userID *int64, action, detail, ipHash string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO audit_logs
		(user_id, action, detail, ip_hash, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		nullableInt64(userID), action, detail, ipHash, time.Now().Unix())
	return err
}

// ListAuditLogs 审计日志列表（管理员）。
func (s *Store) ListAuditLogs(ctx context.Context, limit, offset int) ([]*models.AuditLog, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, user_id, action, detail, ip_hash, created_at
		 FROM audit_logs ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.AuditLog
	for rows.Next() {
		var (
			a       models.AuditLog
			userID  sqlNullInt64
			ipHash  sqlNullString
			created unixTime
		)
		if err := rows.Scan(&a.ID, &userID, &a.Action, &a.Detail, &ipHash, &created); err != nil {
			return nil, err
		}
		a.UserID = userID.ptr()
		a.IPHash = ipHash.str()
		a.CreatedAt = *created.t
		out = append(out, &a)
	}
	return out, rows.Err()
}

// sqlNullInt64 包装可空整数列。
type sqlNullInt64 struct {
	valid bool
	v     int64
}

func (n *sqlNullInt64) Scan(src any) error {
	if src == nil {
		n.valid = false
		return nil
	}
	switch v := src.(type) {
	case int64:
		n.v, n.valid = v, true
	case []byte:
		parsed, err := strconv.ParseInt(string(v), 10, 64)
		if err != nil {
			return err
		}
		n.v, n.valid = parsed, true
	case int:
		n.v, n.valid = int64(v), true
	default:
		return &scanTypeError{src: src}
	}
	return nil
}

func (n *sqlNullInt64) ptr() *int64 {
	if !n.valid {
		return nil
	}
	return &n.v
}

// sqlNullString 包装可空字符串。
type sqlNullString struct {
	valid bool
	s     string
}

func (n *sqlNullString) Scan(src any) error {
	if src == nil {
		n.valid = false
		return nil
	}
	switch v := src.(type) {
	case string:
		n.s, n.valid = v, true
	case []byte:
		n.s, n.valid = string(v), true
	default:
		return &scanTypeError{src: src}
	}
	return nil
}

func (n *sqlNullString) str() string { return n.s }
