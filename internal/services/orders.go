package services

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"video-downloader/internal/config"
	"video-downloader/internal/models"
	"video-downloader/internal/payment"
	"video-downloader/internal/repository"
)

// 订单业务错误
var (
	ErrOrderNotFound   = errors.New("订单不存在")
	ErrOrderClosed     = errors.New("订单已关闭")
	ErrOrderPaid       = errors.New("订单已支付")
)

// OrderService 订单服务：创建订单、支付事件处理（幂等）、退款标记。
type OrderService struct {
	store *repository.Store
	cfg   *config.Config
}

// NewOrderService 创建订单服务。
func NewOrderService(store *repository.Store, cfg *config.Config) *OrderService {
	return &OrderService{store: store, cfg: cfg}
}

// CreateOrder 创建订单：只接收 plan_id，价格/时长/权益全部由服务端读取。
// 幂等：同一套餐存在未支付且未过期的订单时返回既有订单。
func (s *OrderService) CreateOrder(ctx context.Context, userID int64, planID int64) (*models.Order, error) {
	plan, err := s.store.GetPlan(ctx, planID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrPlanNotFound
		}
		return nil, err
	}
	if !plan.Enabled {
		return nil, ErrPlanDisabled
	}
	// 幂等：复用未支付订单
	if existing, err := s.store.FindPendingOrder(ctx, userID, planID); err == nil {
		return existing, nil
	} else if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}
	orderNo, err := generateOrderNo()
	if err != nil {
		return nil, err
	}
	order := &models.Order{
		OrderNo:     orderNo,
		UserID:      userID,
		PlanID:      planID,
		AmountCents: plan.PriceCents,
		Currency:    "CNY",
		Status:      models.OrderPending,
		Subject:     fmt.Sprintf("%s（%d 天）", plan.Name, plan.DurationDays),
		ExpiresAt:   time.Now().Add(s.cfg.OrderTTL()),
	}
	return s.store.CreateOrder(ctx, order)
}

// ProcessPaidEvent 处理"订单已支付"事件（Mock 与未来真实渠道共用同一链路）。
// 幂等保证：
//   - payment_events(provider, event_id) 唯一约束 → 重复事件忽略；
//   - MarkPaidAndGrant 内条件更新（仅 pending 才标记支付）→ 已支付订单不重复；
//   - user_memberships.source_order_id 唯一 → 不重复开通；
//   - 会员续费顺延：从当前有效会员 expires_at 之后开始。
func (s *OrderService) ProcessPaidEvent(ctx context.Context, provider payment.Provider, orderNo, payload string) error {
	trustedPayload := redactPayload(payload)
	// 1. 事件入库（唯一约束拦重复 → 重复直接视为已处理）
	event, err := s.store.CreatePaymentEvent(ctx, provider.Name(), "sim-"+orderNo, orderNo, trustedPayload)
	if err != nil {
		if errors.Is(err, repository.ErrDuplicate) {
			return nil // 已处理过（或并发窗口内其他请求已处理）
		}
		return err
	}
	order, err := s.store.GetOrderByNo(ctx, orderNo)
	if err != nil {
		return ErrOrderNotFound
	}
	// 2. 事务：标记支付 + 开通会员（幂等）
	if _, err := s.store.MarkPaidAndGrant(ctx, order.ID, order.UserID, order.PlanID,
		fmt.Sprintf("mock-%d", event.ID)); err != nil {
		if errors.Is(err, repository.ErrOrderNotPayable) {
			// 订单已 closed/refunded：不重复开通
			return nil
		}
		return err
	}
	// 3. 标记事件已处理（幂等）
	if _, err := s.store.MarkPaymentEventProcessed(ctx, event.ID); err != nil {
		return err
	}
	return nil
}

// ProcessManualPaid 管理员手动标记支付（manual 渠道；开发阶段确认能力）。
func (s *OrderService) ProcessManualPaid(ctx context.Context, orderNo string) error {
	order, err := s.store.GetOrderByNo(ctx, orderNo)
	if err != nil {
		return ErrOrderNotFound
	}
	if order.Status != models.OrderPending {
		return repository.ErrOrderNotPayable
	}
	return s.ProcessPaidEvent(ctx, &manualProvider{}, orderNo, `{"manual":"admin-confirmed"}`)
}

// CloseOrder 关闭订单（仅 pending）。
func (s *OrderService) CloseOrder(ctx context.Context, orderNo string) (*models.Order, error) {
	order, err := s.store.GetOrderByNo(ctx, orderNo)
	if err != nil {
		return nil, ErrOrderNotFound
	}
	if order.Status == models.OrderPaid {
		return nil, ErrOrderPaid
	}
	ok, err := s.store.CloseOrderByNo(ctx, orderNo)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, repository.ErrOrderNotPayable
	}
	return s.store.GetOrderByNo(ctx, orderNo)
}

// RefundOrder 管理员标记退款（开发阶段功能；不调用真实支付平台，由管理员确认线下退款后操作）。
func (s *OrderService) RefundOrder(ctx context.Context, orderNo string) (*models.Order, error) {
	order, err := s.store.GetOrderByNo(ctx, orderNo)
	if err != nil {
		return nil, ErrOrderNotFound
	}
	if order.Status != models.OrderPaid {
		return nil, repository.ErrOrderNotPayable
	}
	ok, err := s.store.MarkOrderRefunded(ctx, orderNo)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, repository.ErrOrderNotPayable
	}
	return s.store.GetOrderByNo(ctx, orderNo)
}

// manualProvider 轻量渠道（仅用于事件命名）。
type manualProvider struct{}

func (manualProvider) Name() string { return "manual" }
func (manualProvider) CreatePayment(ctx context.Context, req payment.CreatePaymentRequest) (*payment.PaymentResult, error) {
	return nil, errors.New("manual 渠道不使用 CreatePayment")
}
func (manualProvider) QueryPayment(ctx context.Context, orderNo string) (*payment.PaymentStatus, error) {
	return &payment.PaymentStatus{Provider: "manual", Status: payment.StatusPending}, nil
}
func (manualProvider) RefundPayment(ctx context.Context, orderNo string, amountCents int64) error { return nil }
func (manualProvider) VerifyCallback(r *http.Request) (*payment.PaymentNotification, error) {
	return nil, payment.VerificationFailedError{}
}
func (manualProvider) ClosePayment(ctx context.Context, orderNo string) error { return nil }

func generateOrderNo() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%s", time.Now().Format("20060102150405"), hex.EncodeToString(b)), nil
}

// redactPayload 载荷脱敏（不记录密码/Cookie/密钥正文）。
func redactPayload(p string) string {
	if len(p) > 512 {
		p = p[:512]
	}
	for _, pat := range []string{"cookie", "password", "secret", "token", "cookie"} {
		if idx := indexFold(p, pat); idx >= 0 {
			return p[:idx] + pat + "[redacted]"
		}
	}
	return p
}

func indexFold(s, sub string) int {
	ls := toLowerASCII(s)
	lsub := toLowerASCII(sub)
	for i := 0; i+len(lsub) <= len(ls); i++ {
		if ls[i:i+len(lsub)] == lsub {
			return i
		}
	}
	return -1
}

func toLowerASCII(s string) string {
	b := []byte(s)
	for i, ch := range b {
		if ch >= 'A' && ch <= 'Z' {
			b[i] = ch + 32
		}
	}
	return string(b)
}
