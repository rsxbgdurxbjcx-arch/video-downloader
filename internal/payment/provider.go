// Package payment 提供统一支付抽象（PaymentProvider 接口）与 Mock 实现。
// 本阶段不接入任何真实支付平台；未来可通过实现该接口扩展爱发电等渠道。
package payment

import (
	"context"
	"net/http"
)

// 支付状态常量
const (
	StatusPending = "pending"
	StatusPaid    = "paid"
	StatusClosed  = "closed"
	StatusFailed  = "failed"
)

// CreatePaymentRequest 创建支付请求（金额一律由服务端订单决定）。
type CreatePaymentRequest struct {
	OrderNo    string
	Subject    string
	AmountCents int64
	Currency   string
	ReturnURL  string
	NotifyURL  string
}

// PaymentResult 创建支付结果。
type PaymentResult struct {
	Provider        string
	ProviderTradeNo string
	Status          string
	PayURL          string // 用户跳转支付页地址（Mock 为本站模拟支付页）
	QRCode          string
}

// PaymentStatus 查询支付状态。
type PaymentStatus struct {
	Provider        string
	ProviderTradeNo string
	Status          string
	Paid            bool
}

// PaymentNotification 支付回调/通知（已验证）。
type PaymentNotification struct {
	EventID  string // 渠道事件 ID（幂等键）
	OrderNo  string
	Status   string
	Paid     bool
	ProviderTradeNo string
	Raw      string // 脱敏后的原始载荷
}

// VerificationFailedError 回调验签失败。
type VerificationFailedError struct{}

func (VerificationFailedError) Error() string { return "回调验签失败" }

// Provider 支付渠道接口（未来真实渠道只需实现该接口并在 Manager 注册）。
type Provider interface {
	// Name 渠道名（如 mock / manual / afdian）。
	Name() string
	// CreatePayment 创建支付。
	CreatePayment(ctx context.Context, req CreatePaymentRequest) (*PaymentResult, error)
	// QueryPayment 主动查询订单状态。
	QueryPayment(ctx context.Context, orderNo string) (*PaymentStatus, error)
	// RefundPayment 退款（本阶段仅管理员手动标记，真实渠道留待后续实现）。
	RefundPayment(ctx context.Context, orderNo string, amountCents int64) error
	// VerifyCallback 校验回调请求并返回规范化通知。
	VerifyCallback(r *http.Request) (*PaymentNotification, error)
	// ClosePayment 关闭未支付订单。
	ClosePayment(ctx context.Context, orderNo string) error
}
