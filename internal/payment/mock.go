package payment

import (
	"context"
	"fmt"
	"net/http"
)

// MockProvider Mock 支付渠道。
// 仅在 APP_ENV=development（或 test）可用；生产环境由 Manager 拒绝注册。
// 模拟支付与真实支付走完全相同的订单处理链路（payment_events + 幂等开通）。
type MockProvider struct {
	baseURL string
}

// NewMockProvider 创建 Mock 渠道。
func NewMockProvider(baseURL string) *MockProvider {
	return &MockProvider{baseURL: baseURL}
}

// Name 渠道名。
func (p *MockProvider) Name() string { return "mock" }

// CreatePayment 生成站内模拟支付页面地址（由前端展示"模拟支付"按钮）。
func (p *MockProvider) CreatePayment(ctx context.Context, req CreatePaymentRequest) (*PaymentResult, error) {
	if req.AmountCents < 0 {
		return nil, fmt.Errorf("金额非法")
	}
	return &PaymentResult{
		Provider: p.Name(),
		Status:   StatusPending,
		PayURL:   fmt.Sprintf("%s/api/orders/%s/simulate-pay", p.baseURL, req.OrderNo),
	}, nil
}

// QueryPayment 查询订单状态（数据库为唯一事实来源，由 Manager 装配仓储查询）。
// 实际查询链路：模拟支付没有外部状态，始终以本地订单为准。
func (p *MockProvider) QueryPayment(ctx context.Context, orderNo string) (*PaymentStatus, error) {
	return nil, fmt.Errorf("mock 渠道无外部状态，请查询本地订单")
}

// RefundPayment 模拟退款：由管理员手动标记订单退款（本阶段为开发功能）。
func (p *MockProvider) RefundPayment(ctx context.Context, orderNo string, amountCents int64) error {
	// 模拟渠道退款无副作用；真实渠道未来实现渠道侧退款
	return nil
}

// VerifyCallback Mock 不提供外部回调（本地 simulate-pay 已经过服务端订单处理）。
func (p *MockProvider) VerifyCallback(r *http.Request) (*PaymentNotification, error) {
	return nil, VerificationFailedError{}
}

// ClosePayment 关闭支付。
func (p *MockProvider) ClosePayment(ctx context.Context, orderNo string) error {
	return nil
}

// ManualProvider 手动支付渠道（管理员线下确认后标记收款；预留能力）。
type ManualProvider struct{}

// Name 渠道名。
func (p *ManualProvider) Name() string { return "manual" }

// CreatePayment 手动渠道生成"等待管理员确认"的支付结果。
func (p *ManualProvider) CreatePayment(ctx context.Context, req CreatePaymentRequest) (*PaymentResult, error) {
	return &PaymentResult{
		Provider: p.Name(),
		Status:   StatusPending,
		PayURL:   fmt.Sprintf("%s/#/order/%s?manual=1", req.ReturnURL, req.OrderNo),
	}, nil
}

// QueryPayment 手动渠道无外部状态。
func (p *ManualProvider) QueryPayment(ctx context.Context, orderNo string) (*PaymentStatus, error) {
	return &PaymentStatus{Provider: p.Name(), Status: StatusPending, Paid: false}, nil
}

// RefundPayment 手动渠道退款由管理员操作。
func (p *ManualProvider) RefundPayment(ctx context.Context, orderNo string, amountCents int64) error {
	return nil
}

// VerifyCallback 手动渠道无回调。
func (p *ManualProvider) VerifyCallback(r *http.Request) (*PaymentNotification, error) {
	return nil, VerificationFailedError{}
}

// ClosePayment 关闭支付。
func (p *ManualProvider) ClosePayment(ctx context.Context, orderNo string) error {
	return nil
}

var _ Provider = (*MockProvider)(nil)
var _ Provider = (*ManualProvider)(nil)
