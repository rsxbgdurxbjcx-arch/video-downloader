// Package models 定义领域模型。
package models

import "time"

// 为便于 JSON 输出使用的时间指针工具。
func TimePtr(t time.Time) *time.Time { return &t }

// 用户角色与状态常量
const (
	RoleUser  = "user"
	RoleAdmin = "admin"

	StatusPending  = "pending"
	StatusActive   = "active"
	StatusDisabled = "disabled"
)

// 邮件用途
const (
	EmailPurposeRegister         = "register"
	EmailPurposeResendVerification = "resend_verification"
	EmailPurposeResetPassword    = "reset_password"
)

// 订单状态
const (
	OrderPending  = "pending"
	OrderPaid     = "paid"
	OrderClosed   = "closed"
	OrderRefunded = "refunded"
	OrderFailed   = "failed"
)

// 会员记录状态
const (
	MembershipActive  = "active"
	MembershipExpired = "expired"
	MembershipRevoked = "revoked"
)

// 下载任务状态
const (
	TaskQueued     = "queued"
	TaskProcessing = "processing"
	TaskCompleted  = "completed"
	TaskFailed     = "failed"
	TaskCancelled  = "cancelled"
)

// User 用户。
type User struct {
	ID              int64
	Email           string
	PasswordHash    string
	EmailVerifiedAt *time.Time
	Role            string
	Status          string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	LastLoginAt     *time.Time
}

// IsVerified 邮箱是否已验证。
func (u *User) IsVerified() bool { return u.EmailVerifiedAt != nil }

// IsAdmin 是否为管理员。
func (u *User) IsAdmin() bool { return u.Role == RoleAdmin }

// IsActive 是否可用（active 且已验证）。
func (u *User) IsActive() bool { return u.Status == StatusActive && u.IsVerified() }

// VerifyToken 邮箱验证令牌（仅哈希；验证码方式为 6 位数字的哈希）。
type VerifyToken struct {
	ID              int64
	UserID          int64
	TokenHash       string
	ExpiresAt       time.Time
	UsedAt          *time.Time
	CreatedAt       time.Time
	RequestedIPHash string
	Attempts        int // 验证码错误尝试次数（超过上限作废）
}

// EmailSendRecord 邮件发送记录（脱敏）。
type EmailSendRecord struct {
	ID            int64
	EmailHash     string
	UserID        *int64
	Purpose       string
	RequestIPHash string
	CreatedAt     time.Time
}

// EmailSendLog 邮件发送日志（管理员诊断；不含正文/凭据）。
type EmailSendLog struct {
	ID        int64
	EmailHash string
	Purpose   string
	OK        bool
	ErrMsg    string
	CreatedAt time.Time
}

// Session 会话。
type Session struct {
	ID         int64
	UserID     int64
	TokenHash  string
	ExpiresAt  time.Time
	CreatedAt  time.Time
	LastSeenAt time.Time
	UserAgent  string
	IPHash     string
}

// Plan 会员套餐。
type Plan struct {
	ID                  int64
	Name                string
	Description         string
	PriceCents          int64
	DurationDays        int
	DownloadLimit       int // -1 不限
	DailyDownloadLimit  int // -1 不限
	MaxConcurrentTasks  int
	MaxFileSize         int64 // 0 不限
	AllowedQuality      string
	Enabled             bool
	SortOrder           int
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// QualitySet 解析允许的清晰度集合。
func (p *Plan) QualitySet() map[string]bool {
	set := map[string]bool{}
	cur := ""
	for _, ch := range p.AllowedQuality {
		if ch == ',' {
			if cur != "" {
				set[cur] = true
			}
			cur = ""
			continue
		}
		cur += string(ch)
	}
	if cur != "" {
		set[cur] = true
	}
	return set
}

// Membership 用户会员记录。
type Membership struct {
	ID            int64
	UserID        int64
	PlanID        int64
	StartsAt      time.Time
	ExpiresAt     time.Time
	Status        string
	SourceOrderID *int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Order 订单。
type Order struct {
	ID              int64
	OrderNo         string
	UserID          int64
	PlanID          int64
	AmountCents     int64
	Currency        string
	Provider        string
	ProviderTradeNo string
	Status          string
	Subject         string
	PaidAt          *time.Time
	ExpiresAt       time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// PaymentEvent 支付事件。
type PaymentEvent struct {
	ID          int64
	Provider    string
	EventID     string
	OrderNo     string
	Payload     string
	Processed   bool
	CreatedAt   time.Time
	ProcessedAt *time.Time
}

// DownloadTask 下载任务（持久化）。
type DownloadTask struct {
	ID           int64
	TaskID       string
	UserID       int64
	SourceURL    string
	Platform     string
	Status       string
	Progress     float64
	TaskDir      string
	OutputPath   string
	Filename     string
	FileSize     int64
	ErrorMessage string
	Title        string
	CreatedAt    time.Time
	StartedAt    *time.Time
	CompletedAt  *time.Time
	ExpiresAt    time.Time
}

// AuditLog 审计日志。
type AuditLog struct {
	ID        int64
	UserID    *int64
	Action    string
	Detail    string
	IPHash    string
	CreatedAt time.Time
}
