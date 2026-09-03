// video-downloader 服务入口：配置加载 → 数据库迁移 → 服务组装 → 优雅启停。
package main

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"video-downloader/internal/auth"
	"video-downloader/internal/config"
	"video-downloader/internal/database"
	"video-downloader/internal/downloader"
	"video-downloader/internal/email"
	"video-downloader/internal/handlers"
	"video-downloader/internal/models"
	"video-downloader/internal/payment"
	"video-downloader/internal/repository"
	"video-downloader/internal/services"
)

//go:embed static/*
var staticFS embed.FS

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("配置错误: %v", err)
	}

	// 旧版部署兼容：PORT 环境变量存在且未显式指定 APP_ADDR
	if os.Getenv("PORT") != "" && os.Getenv("APP_ADDR") == "" {
		cfg.AppAddr = ":" + os.Getenv("PORT")
	}

	// 数据目录
	if err := downloader.EnsureDirs(
		filepath.Dir(cfg.DatabaseURL), cfg.DownloadDir, cfg.CookieDir); err != nil {
		log.Fatalf("初始化目录失败: %v", err)
	}

	// 数据库 + 迁移
	ctx := context.Background()
	db, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("数据库初始化失败: %v", err)
	}
	defer db.Close()

	store := repository.NewStore(db)

	// 邮件（Spug 推送助手：https://push.spug.cc/guide/mail）
	mailer := email.NewSender(cfg)

	// Cookie 加密存储
	cookies, err := downloader.NewCookieStore(cfg.CookieDir, cfg.CookieEncryptionKey)
	if err != nil {
		log.Fatalf("Cookie 存储初始化失败: %v", err)
	}

	// 下载引擎
	engine := downloader.NewEngine(store, cfg, cookies)
	downloader.ResolveBinaries(cfg.YTDLPPath, cfg.FFmpegPath, cfg.FFprobePath)

	// 认证
	authSvc := auth.NewService(store, cfg, mailer)

	// 会员/订单
	entitle := services.NewEntitlementService(store)
	orderSvc := services.NewOrderService(store, cfg)

	// 支付渠道
	payMgr, err := payment.NewManager(cfg)
	if err != nil {
		log.Fatalf("支付渠道初始化失败: %v", err)
	}

	// 初始化默认管理员（如果不存在）
	if err := ensureAdmin(ctx, store, authSvc, cfg); err != nil {
		log.Fatalf("初始化管理员失败: %v", err)
	}

	// 后台任务
	engine.StartCleanupTimer()
	go func() {
		ticker := time.NewTicker(20 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			_ = store.DeleteExpiredSessions(ctx)
			_ = store.CloseExpiredOrders(ctx)
			_ = store.ExpireStaleMemberships(ctx)
		}
	}()

	app := &handlers.App{
		Cfg:     cfg,
		Store:   store,
		Auth:    authSvc,
		Entitle: entitle,
		Order:   orderSvc,
		Engine:  engine,
		Mailer:  mailer,
		PayMgr:  payMgr,
	}

	staticContent, _ := fs.Sub(staticFS, "static")
	handler := app.Routes(staticContent)

	srv := &http.Server{
		Addr:         cfg.AppAddr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // 大文件下载不受写超时限制
		IdleTimeout:  120 * time.Second,
	}

	// 优雅关闭
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		log.Printf("收到退出信号，正在优雅关闭...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("关闭异常: %v", err)
		}
	}()

	// 邮件服务状态（不打印模板编码/凭证）
	if cfg.MailEnabled && cfg.MailTemplateCode != "" {
		log.Printf("📧 邮件推送已启用: spug mail api=%s（官方模板验证码；模板编码为调用凭证，不落日志）", cfg.MailAPIBase)
		if cfg.RedisURL != "" {
			log.Printf("📧 邮件限流: Redis 计数（REDIS_URL 已配置）")
		} else {
			log.Printf("📧 邮件限流: 数据库统计（未配置 REDIS_URL）")
		}
	} else if cfg.IsDevelopment() {
		log.Printf("📧 邮件推送未启用（SPUG_MAIL_ENABLED=false）：验证码仅输出到控制台，不会发送邮件！生产部署请按 README 接入 Spug 推送助手")
	} else {
		log.Printf("📧 邮件推送未启用（SPUG_MAIL_ENABLED=false）：注册将拒绝发送验证码")
	}

	log.Printf("🚀 video-downloader 启动: addr=%s env=%s base=%s", cfg.AppAddr, cfg.AppEnv, cfg.AppBaseURL)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("服务启动失败: %v", err)
	}
	log.Printf("服务已停止")
}

// ensureAdmin 保证管理员账户就绪（自举）。
//   - 账户不存在：按 ADMIN_INITIAL_EMAIL/ADMIN_INITIAL_PASSWORD 创建（pending，需邮箱验证）；
//   - 账户已存在且已验证：仅确保角色为 admin；
//   - 账户已存在但【未验证】（pending）：自举未完成——重发验证码，并在未验证期间
//     以 .env 的 ADMIN_INITIAL_PASSWORD 为准重置密码。
//     （若把 ADMIN_INITIAL_EMAIL 改为新的邮箱，重启后会按新邮箱创建管理员并发送验证码，
//      即可解决"admin@example.com 占位邮箱收不到验证码导致永久卡死"的问题。）
// 生产环境：使用默认密码 change-me 时 config.Validate 已拒绝启动。
func ensureAdmin(ctx context.Context, store *repository.Store, authSvc *auth.Service, cfg *config.Config) error {
	emailNorm := auth.NormalizeEmail(cfg.AdminInitialEmail)
	if emailNorm == "" {
		return nil
	}
	exists, err := store.GetUserByEmail(ctx, emailNorm)
	if err == nil {
		// 已有账户：确保角色为 admin
		if !exists.IsAdmin() {
			if err := store.SetUserRole(ctx, exists.ID, "admin"); err != nil {
				return err
			}
		}
		if !exists.IsVerified() {
			return bootstrapPendingAdmin(ctx, store, authSvc, cfg, exists)
		}
		return nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	hash, err := auth.HashPassword(cfg.AdminInitialPassword)
	if err != nil {
		return err
	}
	user, err := store.CreateUser(ctx, emailNorm, hash, "admin", "pending")
	if err != nil {
		return err
	}
	log.Printf("[admin] 已创建管理员账户: %s（pending，请完成邮箱验证）", emailNorm)
	return resendAdminVerification(ctx, store, authSvc, cfg, user)
}

// bootstrapPendingAdmin 未验证管理员的自举补全：未验证期间以 .env 密码为准并重发验证码。
func bootstrapPendingAdmin(ctx context.Context, store *repository.Store, authSvc *auth.Service, cfg *config.Config, admin *models.User) error {
	// 密码同步（pending 期间以 .env 配置为准；change-me 等默认值不重置，验证通过后不再触发）
	if cfg.AdminInitialPassword != "" && cfg.AdminInitialPassword != config.DefaultAdminPassword {
		if h, hErr := auth.HashPassword(cfg.AdminInitialPassword); hErr == nil {
			if uErr := store.UpdateUserPassword(ctx, admin.ID, h); uErr != nil {
				log.Printf("[admin] 重置管理员密码失败: %v", uErr)
			}
		}
	}
	log.Printf("[admin] 管理员尚未验证，已重发验证码并同步 .env 密码（完成邮箱验证后即可登录）")
	return resendAdminVerification(ctx, store, authSvc, cfg, admin)
}

// resendAdminVerification 发送管理员验证码邮件（开发环境未配置邮件推送时输出到控制台）。
func resendAdminVerification(ctx context.Context, store *repository.Store, authSvc *auth.Service, cfg *config.Config, user *models.User) error {
	if err := authSvc.SendVerification(ctx, user, "register", "127.0.0.1"); err != nil {
		if cfg.IsDevelopment() && !cfg.MailEnabled {
			log.Printf("[admin][dev] Spug 邮件推送未配置，管理员验证码已输出到上述/dev 日志")
		}
		log.Printf("[admin] 验证邮件发送提醒: %v", err)
		return nil // 发送失败不阻塞启动（可稍后重发）
	}
	return nil
}
