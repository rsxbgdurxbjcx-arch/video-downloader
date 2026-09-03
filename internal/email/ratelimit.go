package email

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"video-downloader/internal/config"
	"video-downloader/internal/redisx"
	"video-downloader/internal/repository"
)

// ErrMailRateLimited 邮件发送被限流（冷却期/小时上限/IP 上限）。
var ErrMailRateLimited = errors.New("邮件发送过于频繁")

// RateLimiter 邮件发送限流器：
//   - 配置了 REDIS_URL 且可达时使用 Redis 计数（跨实例持久、原子）；
//   - Redis 不可用或未配置时自动降级为数据库 email_send_records 统计（行为等价）。
//
// 规则：
//   - 同一邮箱冷却期内（默认 60 秒）只能发送一次；
//   - 同一邮箱每小时最多发送 EMAIL_HOURLY_LIMIT（默认 6）次；
//   - 同一 IP 每小时最多发送 EMAIL_IP_HOURLY_LIMIT（默认 30）次（防止更换邮箱刷邮件）。
type RateLimiter struct {
	redis *redisx.Client
	store *repository.Store
	cfg   *config.Config

	mu            sync.Mutex
	redisDownTill time.Time // Redis 故障后 30 秒内直接走数据库，避免每次请求都等连接超时
}

// NewRateLimiter 创建限流器。REDIS_URL 无效时返回 nil（视为未配置，走数据库降级）。
func NewRateLimiter(cfg *config.Config, store *repository.Store) *RateLimiter {
	r := &RateLimiter{cfg: cfg, store: store}
	if cfg.RedisURL != "" {
		if c, err := redisx.ParseURL(cfg.RedisURL); err == nil {
			r.redis = c
		} else {
			r.redis = nil
		}
	}
	return r
}

// redisReady Redis 是否可用（故障冷却期内视为不可用）。
func (r *RateLimiter) redisReady() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return time.Now().After(r.redisDownTill)
}

// markRedisDown 记录 Redis 故障时间并重新调度重试。
func (r *RateLimiter) markRedisDown() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.redisDownTill = time.Now().Add(30 * time.Second)
}

// Check 检查是否允许发送一次邮件；允许返回 nil，否则返回 ErrMailRateLimited 或底层错误。
func (r *RateLimiter) Check(ctx context.Context, emailHash, ipHash string) error {
	if r.redis != nil && r.redisReady() {
		if err := r.checkRedis(ctx, emailHash, ipHash); err == nil {
			return nil
		} else if !errors.Is(err, ErrMailRateLimited) {
			// Redis 不可达/异常：标记故障并降级到数据库统计（保证限流不因 Redis 故障而失效）
			r.markRedisDown()
			return r.checkDB(ctx, emailHash, ipHash)
		} else {
			return err
		}
	}
	return r.checkDB(ctx, emailHash, ipHash)
}

func (r *RateLimiter) checkRedis(ctx context.Context, emailHash, ipHash string) error {
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	cooldown := r.cfg.EmailSendCooldownSeconds
	hourly := r.cfg.EmailHourlyLimit
	ipHourly := r.cfg.EmailIPHourlyLimit
	hour := int64(3600)

	// 1. 同邮箱冷却期（SET NX EX：key 已存在说明冷却中）
	if cooldown > 0 {
		v, err := r.redis.Do(cctx, "SET", "vd:mail:cooldown:"+emailHash, "1", "NX", "EX", strconv.Itoa(cooldown))
		if err != nil {
			return err
		}
		if v == "" {
			return ErrMailRateLimited
		}
	}
	// 2. 同邮箱每小时上限
	if hourly > 0 {
		v, err := r.redis.Do(cctx, "INCR", "vd:mail:hourly:"+emailHash)
		if err != nil {
			return err
		}
		n, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil {
			return perr
		}
		if n == 1 {
			_, _ = r.redis.Do(cctx, "EXPIRE", "vd:mail:hourly:"+emailHash, strconv.FormatInt(hour, 10))
		}
		if n > int64(hourly) {
			return ErrMailRateLimited
		}
	}
	// 3. 同 IP 每小时上限（防更换邮箱刷邮件）
	if ipHourly > 0 {
		v, err := r.redis.Do(cctx, "INCR", "vd:mail:ip:"+ipHash)
		if err != nil {
			return err
		}
		n, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil {
			return perr
		}
		if n == 1 {
			_, _ = r.redis.Do(cctx, "EXPIRE", "vd:mail:ip:"+ipHash, strconv.FormatInt(hour, 10))
		}
		if n > int64(ipHourly) {
			return ErrMailRateLimited
		}
	}
	return nil
}

func (r *RateLimiter) checkDB(ctx context.Context, emailHash, ipHash string) error {
	now := time.Now()
	cooldown := r.cfg.EmailSendCooldownSeconds
	hourly := r.cfg.EmailHourlyLimit
	ipHourly := r.cfg.EmailIPHourlyLimit

	if cooldown > 0 {
		last, err := r.store.LatestEmailSend(ctx, emailHash)
		if err == nil && last != nil && now.Sub(*last) < time.Duration(cooldown)*time.Second {
			return ErrMailRateLimited
		}
	}
	if hourly > 0 {
		n, err := r.store.CountEmailSince(ctx, emailHash, now.Add(-time.Hour))
		if err != nil {
			return err
		}
		if n >= int64(hourly) {
			return ErrMailRateLimited
		}
	}
	if ipHourly > 0 {
		n, err := r.store.CountIPSince(ctx, ipHash, now.Add(-time.Hour))
		if err != nil {
			return err
		}
		if n >= int64(ipHourly) {
			return ErrMailRateLimited
		}
	}
	return nil
}

// CooldownRemaining 返回该邮箱距下次允许发送的剩余秒数（0 表示当前可发送）。
// 用于把「60 秒重发冷却」交给后端计时：无论 Redis 还是数据库，均返回服务器视角的准确剩余时间。
func (r *RateLimiter) CooldownRemaining(ctx context.Context, emailHash string) int {
	cooldown := r.cfg.EmailSendCooldownSeconds
	if cooldown <= 0 {
		return 0
	}
	if r.redis != nil && r.redisReady() {
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if v, err := r.redis.Do(cctx, "TTL", "vd:mail:cooldown:"+emailHash); err == nil {
			if n, perr := strconv.ParseInt(v, 10, 64); perr == nil {
				if n <= 0 {
					return 0 // key 不存在（-2）或未设置过期：无冷却
				}
				return int(n)
			}
		}
	}
	// 数据库降级：根据最近发送时间推算
	last, err := r.store.LatestEmailSend(ctx, emailHash)
	if err != nil || last == nil {
		return 0
	}
	remaining := cooldown - int(time.Since(*last).Seconds())
	if remaining < 0 {
		return 0
	}
	return remaining
}
