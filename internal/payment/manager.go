package payment

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"video-downloader/internal/config"
)

// ErrProviderNotAllowed 渠道在当前环境不可用（如生产环境 mock）。
var ErrProviderNotAllowed = errors.New("当前环境禁止使用该支付渠道")

// Manager 支付渠道管理：按配置注册可用的 Provider。
// mock 仅在 development/test 环境注册；生产环境即使 PAYMENT_PROVIDER=mock 也拒绝注册。
type Manager struct {
	mu        sync.RWMutex
	providers map[string]Provider
	defaultP  string
	cfg       *config.Config
}

// NewManager 创建渠道管理器并按配置注册。
func NewManager(cfg *config.Config) (*Manager, error) {
	m := &Manager{
		providers: map[string]Provider{},
		defaultP:  cfg.PaymentProvider,
		cfg:       cfg,
	}
	if cfg.PaymentEnabled {
		switch cfg.PaymentProvider {
		case "mock":
			if cfg.IsProduction() {
				return nil, fmt.Errorf("%w: PAYMENT_PROVIDER=mock 仅限开发环境", ErrProviderNotAllowed)
			}
			m.providers["mock"] = NewMockProvider(cfg.AppBaseURL)
		case "manual":
			m.providers["manual"] = &ManualProvider{}
		default:
			return nil, fmt.Errorf("不支持的 PAYMENT_PROVIDER=%q", cfg.PaymentProvider)
		}
	} else {
		// 支付关闭时注册 manual（本地手动确认，不产生真实交易）
		m.providers["manual"] = &ManualProvider{}
		m.defaultP = "manual"
	}
	return m, nil
}

// Get 获取渠道。
func (m *Manager) Get(name string) (Provider, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if p, ok := m.providers[name]; ok {
		return p, nil
	}
	if p, ok := m.providers[m.defaultP]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("支付渠道不可用")
}

// Default 默认渠道。
func (m *Manager) Default() string { return m.defaultP }

// Names 已注册渠道名。
func (m *Manager) Names() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var names []string
	for n := range m.providers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// MockEnabled mock 渠道是否可用（仅开发环境）。
func (m *Manager) MockEnabled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.providers["mock"]
	return ok && !m.cfg.IsProduction()
}

// Register 预留：未来注册爱发电等渠道。
func (m *Manager) Register(p Provider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.providers[p.Name()] = p
}

// ProcessPaymentEvent 统一支付事件处理：幂等（重复事件直接跳过）。
// 具体业务（订单标记 + 会员开通）由注入的 handler 执行，保证 mock 与真实渠道同链路。
type EventHandler func(ctx context.Context, ev Provider, orderNo, payload string) error

// SimulatePayment 本地模拟支付入口（仅开发环境；生产环境由路由层拦截）。
func (m *Manager) SimulatePayment(ctx context.Context, ev Provider, orderNo string, handler EventHandler) error {
	if !m.MockEnabled() {
		return ErrProviderNotAllowed
	}
	return handler(ctx, ev, orderNo, `{"simulated":true}`)
}
