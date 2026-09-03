// Package database 负责 SQLite 初始化、迁移执行。
// 使用纯 Go 的 modernc.org/sqlite 驱动，无 CGO 依赖；
// 所有访问经 database/sql，未来可平滑迁移至 PostgreSQL。
package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // register "sqlite" driver
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Open 打开 SQLite 数据库并执行迁移。
// dsn 形如 ./data/app.db（可用 file:...? 查询参数）；启动时转为绝对路径并做可写性探测。
func Open(ctx context.Context, dsn string) (*sql.DB, error) {
	if err := ensureDir(dsn); err != nil {
		return nil, err
	}
	absDSN, absErr := filepath.Abs(dsn)
	if absErr == nil {
		dsn = absDSN
	}
	// 可写性探测：避免 SQLITE_CANTOPEN 难以诊断
	if err := probeWritable(filepath.Dir(dsn)); err != nil {
		return nil, fmt.Errorf("数据库目录不可写（%s）: %w", filepath.Dir(dsn), err)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败（%s）: %w", dsn, err)
	}
	// 并发写入安全：WAL + busy_timeout + 外键
	// WAL 在不支持共享内存的文件系统/挂载类型上可能失败：自动降级为 DELETE 模式继续启动
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
		log.Printf("[database] journal_mode=WAL 不可用（%v），降级为 DELETE 模式", err)
		if _, err2 := db.ExecContext(ctx, "PRAGMA journal_mode=DELETE"); err2 != nil {
			db.Close()
			return nil, fmt.Errorf("初始化 SQLite 日志模式失败（%s）: %w", dsn, err2)
		}
	}
	for _, p := range []string{"PRAGMA busy_timeout=5000", "PRAGMA foreign_keys=ON"} {
		if _, err := db.ExecContext(ctx, p); err != nil {
			db.Close()
			return nil, fmt.Errorf("初始化 SQLite PRAGMA 失败 (%s, %s): %w", p, dsn, err)
		}
	}
	db.SetMaxOpenConns(1) // SQLite 单写者；连接池串行化，避免 SQLITE_BUSY
	if err := Migrate(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// probeWritable 在目标目录创建临时文件验证可写性（不留痕迹）。
func probeWritable(dir string) error {
	tmp, err := os.CreateTemp(dir, ".vd-probe-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	tmp.Close()
	return os.Remove(name)
}

// ensureDir 保证文件型数据库所在目录存在（Docker 挂载场景）。
func ensureDir(dsn string) error {
	dsn = strings.TrimSpace(dsn)
	if strings.HasPrefix(dsn, "file:") {
		// file:path?params 形式
		if u, err := url.Parse(dsn); err == nil && u.Path != "" && u.Path != "/" {
			return os.MkdirAll(filepath.Dir(u.Path), 0755)
		}
		return nil
	}
	return os.MkdirAll(filepath.Dir(dsn), 0755)
}

// Migrate 按文件名顺序执行尚未应用的迁移文件。
func Migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("初始化 schema_migrations 失败: %w", err)
	}

	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("读取迁移目录失败: %w", err)
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	for _, name := range files {
		var applied int
		if err := db.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM schema_migrations WHERE version=?`, name).Scan(&applied); err != nil {
			return fmt.Errorf("查询迁移状态失败: %w", err)
		}
		if applied > 0 {
			continue
		}
		content, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("读取迁移 %s 失败: %w", name, err)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(content)); err != nil {
			tx.Rollback()
			return fmt.Errorf("执行迁移 %s 失败: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations(version, applied_at) VALUES(?, ?)`,
			name, time.Now().Unix()); err != nil {
			tx.Rollback()
			return fmt.Errorf("记录迁移 %s 失败: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("提交迁移 %s 失败: %w", name, err)
		}
	}
	return nil
}

// Ping 健康检查。
func Ping(ctx context.Context, db *sql.DB) error {
	return db.PingContext(ctx)
}
