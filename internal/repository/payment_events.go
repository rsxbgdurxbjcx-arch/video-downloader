package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"video-downloader/internal/models"
)

const paymentEventCols = `id, provider, event_id, order_no, payload, processed, created_at, processed_at`

// CreatePaymentEvent 写入支付事件（provider+event_id 唯一）。
func (s *Store) CreatePaymentEvent(ctx context.Context, provider, eventID, orderNo, payload string) (*models.PaymentEvent, error) {
	res, err := s.DB.ExecContext(ctx, `INSERT INTO payment_events
		(provider, event_id, order_no, payload, processed, created_at)
		VALUES (?, ?, ?, ?, 0, ?)`,
		provider, eventID, orderNo, payload, time.Now().Unix())
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
	return s.GetPaymentEvent(ctx, id)
}

// GetPaymentEvent 按 ID 查找。
func (s *Store) GetPaymentEvent(ctx context.Context, id int64) (*models.PaymentEvent, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+paymentEventCols+` FROM payment_events WHERE id=?`, id)
	var (
		e                   models.PaymentEvent
		processedAt         nullUnixTime
		created             unixTime
	)
	err := row.Scan(&e.ID, &e.Provider, &e.EventID, &e.OrderNo, &e.Payload, &e.Processed,
		&created, &processedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	e.CreatedAt = *created.t
	e.ProcessedAt = processedAt.t
	return &e, nil
}

// MarkPaymentEventProcessed 将事件标记为已处理（条件更新，保证只处理一次）。
func (s *Store) MarkPaymentEventProcessed(ctx context.Context, id int64) (bool, error) {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE payment_events SET processed=1, processed_at=? WHERE id=? AND processed=0`,
		time.Now().Unix(), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// ListPaymentEvents 支付事件列表（管理员）。
func (s *Store) ListPaymentEvents(ctx context.Context, limit, offset int) ([]*models.PaymentEvent, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+paymentEventCols+` FROM payment_events ORDER BY id DESC LIMIT ? OFFSET ?`,
		limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*models.PaymentEvent
	for rows.Next() {
		var (
			e           models.PaymentEvent
			processedAt nullUnixTime
			created     unixTime
		)
		if err := rows.Scan(&e.ID, &e.Provider, &e.EventID, &e.OrderNo, &e.Payload, &e.Processed,
			&created, &processedAt); err != nil {
			return nil, err
		}
		e.CreatedAt = *created.t
		e.ProcessedAt = processedAt.t
		out = append(out, &e)
	}
	return out, rows.Err()
}
