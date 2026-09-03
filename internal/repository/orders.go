package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"video-downloader/internal/models"
)

// ErrOrderNotPayable 订单状态不允许该操作（由服务层统一映射为用户可读信息）。
var ErrOrderNotPayable = errors.New("order not payable")

const orderCols = `id, order_no, user_id, plan_id, amount_cents, currency, provider,
	provider_trade_no, status, subject, paid_at, expires_at, created_at, updated_at`

func scanOrderRow(row *sql.Row) (*models.Order, error) {
	var (
		o                    models.Order
		providerTradeNo      nullString
		paidAt               nullUnixTime
		expires, created     unixTime
		updated              unixTime
	)
	err := row.Scan(&o.ID, &o.OrderNo, &o.UserID, &o.PlanID, &o.AmountCents, &o.Currency,
		&o.Provider, &providerTradeNo, &o.Status, &o.Subject, &paidAt, &expires, &created, &updated)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	o.ProviderTradeNo = providerTradeNo.s
	o.PaidAt = paidAt.t
	o.ExpiresAt = *expires.t
	o.CreatedAt = *created.t
	o.UpdatedAt = *updated.t
	return &o, nil
}

// CreateOrder 创建订单（初始 status=pending）。
func (s *Store) CreateOrder(ctx context.Context, o *models.Order) (*models.Order, error) {
	now := time.Now().Unix()
	res, err := s.DB.ExecContext(ctx, `INSERT INTO orders
		(order_no, user_id, plan_id, amount_cents, currency, provider, status, subject, expires_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		o.OrderNo, o.UserID, o.PlanID, o.AmountCents, o.Currency, o.Provider, models.OrderPending,
		o.Subject, o.ExpiresAt.Unix(), now, now)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetOrderByID(ctx, id)
}

// GetOrderByID 按内部 ID 查找订单。
func (s *Store) GetOrderByID(ctx context.Context, id int64) (*models.Order, error) {
	return scanOrderRow(s.DB.QueryRowContext(ctx,
		`SELECT `+orderCols+` FROM orders WHERE id=?`, id))
}

// GetOrderByNo 按订单号查找订单。
func (s *Store) GetOrderByNo(ctx context.Context, orderNo string) (*models.Order, error) {
	return scanOrderRow(s.DB.QueryRowContext(ctx,
		`SELECT `+orderCols+` FROM orders WHERE order_no=?`, orderNo))
}

// FindPendingOrder 查找用户指定套餐的未支付订单（幂等创建）。
func (s *Store) FindPendingOrder(ctx context.Context, userID, planID int64) (*models.Order, error) {
	return scanOrderRow(s.DB.QueryRowContext(ctx,
		`SELECT `+orderCols+` FROM orders
		 WHERE user_id=? AND plan_id=? AND status='pending' AND expires_at>=?
		 ORDER BY id DESC LIMIT 1`,
		userID, planID, time.Now().Unix()))
}

// ListUserOrders 用户订单列表。
func (s *Store) ListUserOrders(ctx context.Context, userID int64, limit, offset int) ([]*models.Order, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+orderCols+` FROM orders WHERE user_id=? ORDER BY id DESC LIMIT ? OFFSET ?`,
		userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrders(rows)
}

// ListAllOrders 全部订单（管理员）。
func (s *Store) ListAllOrders(ctx context.Context, limit, offset int) ([]*models.Order, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+orderCols+` FROM orders ORDER BY id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectOrders(rows)
}

func collectOrders(rows *sql.Rows) ([]*models.Order, error) {
	var out []*models.Order
	for rows.Next() {
		var (
			o               models.Order
			providerTradeNo nullString
			paidAt          nullUnixTime
			expires, created unixTime
			updated         unixTime
		)
		if err := rows.Scan(&o.ID, &o.OrderNo, &o.UserID, &o.PlanID, &o.AmountCents, &o.Currency,
			&o.Provider, &providerTradeNo, &o.Status, &o.Subject, &paidAt, &expires, &created, &updated); err != nil {
			return nil, err
		}
		o.ProviderTradeNo = providerTradeNo.s
		o.PaidAt = paidAt.t
		o.ExpiresAt = *expires.t
		o.CreatedAt = *created.t
		o.UpdatedAt = *updated.t
		out = append(out, &o)
	}
	return out, rows.Err()
}

// MarkOrderPaid 标记订单支付成功（条件更新，防重放）。
func (s *Store) MarkOrderPaid(ctx context.Context, id int64, providerTradeNo string) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `UPDATE orders SET
		status='paid', paid_at=?, provider_trade_no=?, updated_at=?
		WHERE id=? AND status='pending'`,
		time.Now().Unix(), providerTradeNo, time.Now().Unix(), id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// CloseExpiredOrders 关闭过期未支付订单。
func (s *Store) CloseExpiredOrders(ctx context.Context) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE orders SET status='closed', updated_at=? WHERE status='pending' AND expires_at < ?`,
		time.Now().Unix(), time.Now().Unix())
	return err
}

// CloseOrderByNo 关闭订单（幂等条件更新）。
func (s *Store) CloseOrderByNo(ctx context.Context, orderNo string) (bool, error) {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE orders SET status='closed', updated_at=? WHERE order_no=? AND status='pending'`,
		time.Now().Unix(), orderNo)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// MarkOrderRefunded 管理员手动退款。
func (s *Store) MarkOrderRefunded(ctx context.Context, orderNo string) (bool, error) {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE orders SET status='refunded', updated_at=? WHERE order_no=? AND status='paid'`,
		time.Now().Unix(), orderNo)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// MarkOrderFailed 标记订单失败。
func (s *Store) MarkOrderFailed(ctx context.Context, orderNo string) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE orders SET status='failed', updated_at=? WHERE order_no=?`,
		time.Now().Unix(), orderNo)
	return err
}


// MarkPaidAndGrant 事务化：标记订单支付（仅 pending）+ 开通/续费会员（幂等）。
// 续费顺延：effectiveStart = max(当前有效会员 expires_at, now)。
// 返回 granted（本次是否实际开通了会员）或错误；订单非 pending 时不切换状态。
func (s *Store) MarkPaidAndGrant(ctx context.Context, orderID, userID, planID int64, providerTradeNo string) (bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	now := time.Now().Unix()
	res, err := tx.ExecContext(ctx, `UPDATE orders SET
		status='paid', paid_at=?, provider_trade_no=?, updated_at=?
		WHERE id=? AND status='pending'`, now, providerTradeNo, now, orderID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		// 非 pending：查询当前状态决定是否可视为幂等成功
		var st string
		if err := tx.QueryRowContext(ctx,
			`SELECT status FROM orders WHERE id=?`, orderID).Scan(&st); err != nil {
			return false, err
		}
		if st == models.OrderPaid || st == models.OrderRefunded {
			return false, nil // 已处理过（幂等成功）
		}
		return false, ErrOrderNotPayable
	}

	// 已存在该订单的会员记录（幂等）
	var exist int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM user_memberships WHERE source_order_id=? AND user_id=?`,
		orderID, userID).Scan(&exist); err != nil {
		return false, err
	}
	if exist > 0 {
		return false, tx.Commit()
	}

	// 续费顺延：从当前有效会员到期时间之后开始（事务内查询，避免跨连接死锁）
	effectiveStart := time.Unix(now, 0).UTC()
	var maxExp sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		`SELECT MAX(expires_at) FROM user_memberships WHERE user_id=? AND status='active'`,
		userID).Scan(&maxExp); err != nil {
		return false, err
	}
	if maxExp.Valid && maxExp.Int64 > now {
		effectiveStart = time.Unix(maxExp.Int64, 0).UTC()
	}

	var durationDays int
	if err := tx.QueryRowContext(ctx,
		`SELECT duration_days FROM membership_plans WHERE id=?`, planID).Scan(&durationDays); err != nil {
		return false, err
	}
	orderIDPtr := orderID
	if _, err := tx.ExecContext(ctx, `INSERT INTO user_memberships
		(user_id, plan_id, starts_at, expires_at, status, source_order_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'active', ?, ?, ?)`,
		userID, planID, effectiveStart.Unix(),
		effectiveStart.AddDate(0, 0, durationDays).Unix(),
		orderIDPtr, now, now); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
