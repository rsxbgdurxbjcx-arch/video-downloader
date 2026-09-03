// Package services 实现会员权益与订单业务（后端统一权限判断）。
package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"video-downloader/internal/repository"
)

// 免费用户默认权益（内置，不由前端控制）：
// 每个账号共 2 次下载额度（总额度，非每日）；用尽后需开通会员。
const (
	FreeDownloadLimit        = 2
	FreeDailyDownloadLimit   = -1 // 每日不限；由总额度控制（每个账号 2 次）
	FreeMaxConcurrentTasks   = 2
	FreeMaxFileSize    int64 = 2 << 30 // 2GB
	FreeQuality               = "720p"

	// AdminMaxConcurrentTasks 管理员（永久会员）允许的并发下载数
	AdminMaxConcurrentTasks = 10
)

// ErrQuotaExceeded 配额不足。
var ErrQuotaExceeded = errors.New("下载额度已用尽")

// ErrConcurrencyExceeded 并发超限。
var ErrConcurrencyExceeded = errors.New("同时进行的下载任务数已达上限")

// ErrQualityNotAllowed 清晰度受限。
var ErrQualityNotAllowed = errors.New("该视频清晰度超出当前会员等级")

// ErrPlanDisabled 套餐已停用。
var ErrPlanDisabled = errors.New("套餐已停用")

// ErrPlanNotFound 套餐不存在。
var ErrPlanNotFound = errors.New("套餐不存在")

// Entitlement 用户当前权益快照。
type Entitlement struct {
	IsMember            bool
	PlanID              int64
	PlanName            string
	DownloadLimit       int   // -1 不限；否则为周期（30 天）限额
	DailyDownloadLimit  int   // -1 不限
	MaxConcurrentTasks  int
	MaxFileSize         int64
	AllowedQuality      []string
	ExpiresAt           *time.Time
}

// EntitlementService 权益服务。
type EntitlementService struct {
	store *repository.Store
}

// NewEntitlementService 创建服务。
func NewEntitlementService(store *repository.Store) *EntitlementService {
	return &EntitlementService{store: store}
}

// GetUserEntitlement 计算用户当前权益（会员到期自动排除；未验证用户只有受限权益）。
// 管理员账号：等价于【永久会员】（不限次数/每日/文件大小/清晰度），不随会员记录到期。
func (s *EntitlementService) GetUserEntitlement(ctx context.Context, userID int64, verified bool) (*Entitlement, error) {
	// 管理员永久会员：无需验证邮箱也可使用管理后台；权益取最大值
	if admin, err := s.store.GetUserByID(ctx, userID); err == nil && admin.IsAdmin() {
		return &Entitlement{
			IsMember:           true,
			PlanID:             0,
			PlanName:           "管理员（永久会员）",
			DownloadLimit:      -1,
			DailyDownloadLimit: -1,
			MaxConcurrentTasks: AdminMaxConcurrentTasks,
			MaxFileSize:        0, // 不限
			AllowedQuality:     []string{}, // 空 = 全部画质
		}, nil
	}
	if !verified {
		return &Entitlement{
			IsMember: false, PlanName: "未验证",
			DownloadLimit: 0, DailyDownloadLimit: 0,
			MaxConcurrentTasks: 0, MaxFileSize: 0,
			AllowedQuality: []string{},
		}, nil
	}
	membership, err := s.store.GetActiveMembership(ctx, userID)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}
	if err == nil && membership != nil {
		plan, perr := s.store.GetPlan(ctx, membership.PlanID)
		if perr == nil && plan != nil {
			quality := []string{}
			for q := range plan.QualitySet() {
				quality = append(quality, q)
			}
			return &Entitlement{
				IsMember: true,
				PlanID:   plan.ID,
				PlanName: plan.Name,
				DownloadLimit: plan.DownloadLimit,
				DailyDownloadLimit: plan.DailyDownloadLimit,
				MaxConcurrentTasks: plan.MaxConcurrentTasks,
				MaxFileSize: plan.MaxFileSize,
				AllowedQuality: quality,
				ExpiresAt: &membership.ExpiresAt,
			}, nil
		}
	}
	// 免费权益
	return &Entitlement{
		IsMember: false, PlanName: "免费用户",
		DownloadLimit: FreeDownloadLimit,
		DailyDownloadLimit: FreeDailyDownloadLimit,
		MaxConcurrentTasks: FreeMaxConcurrentTasks,
		MaxFileSize: FreeMaxFileSize,
		AllowedQuality: []string{FreeQuality},
	}, nil
}

// CanUseQuality 检查清晰度是否被允许（默认为空集合=全部允许）。
func (e *Entitlement) CanUseQuality(height int) error {
	if len(e.AllowedQuality) == 0 {
		return nil
	}
	need := "720p"
	switch {
	case height > 2160:
		need = "8k"
	case height > 1440:
		need = "4k"
	case height > 1080:
		need = "2k"
	case height > 720:
		need = "1080p"
	case height > 0:
		need = "720p"
	}
	if need == "720p" {
		return nil
	}
	for _, q := range e.AllowedQuality {
		if q == need {
			return nil
		}
	}
	return fmt.Errorf("%w: 需要 %s 及以上权益", ErrQualityNotAllowed, need)
}

// CheckCreateDownload 下载任务创建前置检查（每日额度/总额/并发）。
func (s *EntitlementService) CheckCreateDownload(ctx context.Context, userID int64, ent *Entitlement) error {
	// 每日额度
	if ent.DailyDownloadLimit >= 0 {
		dayStart := startOfDay(time.Now())
		n, err := s.store.CountUserTasksSince(ctx, userID, dayStart)
		if err != nil {
			return err
		}
		if n >= int64(ent.DailyDownloadLimit) {
			return ErrQuotaExceeded
		}
	}
	// 周期总额度（30 天窗口）
	if ent.DownloadLimit >= 0 {
		n, err := s.store.CountUserTasksSince(ctx, userID, time.Now().Add(-30*24*time.Hour))
		if err != nil {
			return err
		}
		if n >= int64(ent.DownloadLimit) {
			return ErrQuotaExceeded
		}
	}
	// 并发
	if ent.MaxConcurrentTasks >= 0 {
		n, err := s.store.CountUserActiveTasks(ctx, userID)
		if err != nil {
			return err
		}
		if n >= int64(ent.MaxConcurrentTasks) {
			return ErrConcurrencyExceeded
		}
	}
	return nil
}

// RemainingQuota 剩余额摘要（用户中心展示）。
func (s *EntitlementService) RemainingQuota(ctx context.Context, userID int64, ent *Entitlement) (map[string]int64, error) {
	dayStart := startOfDay(time.Now())
	today, err := s.store.CountUserTasksSince(ctx, userID, dayStart)
	if err != nil {
		return nil, err
	}
	total, err := s.store.CountUserTasksSince(ctx, userID, time.Now().Add(-30*24*time.Hour))
	if err != nil {
		return nil, err
	}
	active, err := s.store.CountUserActiveTasks(ctx, userID)
	if err != nil {
		return nil, err
	}
	return map[string]int64{
		"used_today": today,
		"used_total": total,
		"active":     active,
	}, nil
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}
