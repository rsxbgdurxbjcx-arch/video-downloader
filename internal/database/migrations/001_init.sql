-- 001_init.sql 初始化全部业务表
-- SQLite 方言；如需迁移 PostgreSQL，仅需替换自增/时间戳写法。

-- 用户表
CREATE TABLE IF NOT EXISTS users (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    email             TEXT    NOT NULL UNIQUE,
    password_hash     TEXT    NOT NULL,
    email_verified_at INTEGER,
    role              TEXT    NOT NULL DEFAULT 'user' CHECK (role IN ('user','admin')),
    status            TEXT    NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','active','disabled')),
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL,
    last_login_at     INTEGER
);
CREATE INDEX IF NOT EXISTS idx_users_status ON users(status);

-- 邮箱验证令牌（仅存哈希；单次使用；可被新令牌作废）
CREATE TABLE IF NOT EXISTS email_verification_tokens (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   TEXT    NOT NULL UNIQUE,
    expires_at   INTEGER NOT NULL,
    used_at      INTEGER,
    created_at   INTEGER NOT NULL,
    requested_ip_hash TEXT
);
CREATE INDEX IF NOT EXISTS idx_verify_tokens_user ON email_verification_tokens(user_id);

-- 邮件发送记录（只存邮箱哈希与 IP 哈希，禁止保存完整邮箱与邮件正文）
CREATE TABLE IF NOT EXISTS email_send_records (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    email_hash      TEXT    NOT NULL,
    user_id         INTEGER REFERENCES users(id) ON DELETE SET NULL,
    purpose         TEXT    NOT NULL CHECK (purpose IN ('register','resend_verification','reset_password')),
    request_ip_hash TEXT    NOT NULL,
    created_at      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_email_records_hash ON email_send_records(email_hash, created_at);
CREATE INDEX IF NOT EXISTS idx_email_records_ip ON email_send_records(request_ip_hash, created_at);

-- 会话（仅存 Token 哈希）
CREATE TABLE IF NOT EXISTS sessions (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   TEXT    NOT NULL UNIQUE,
    expires_at   INTEGER NOT NULL,
    created_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    user_agent   TEXT,
    ip_hash      TEXT
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);

-- 会员套餐（金额整数分，禁止 float）
CREATE TABLE IF NOT EXISTS membership_plans (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    name                TEXT    NOT NULL,
    description         TEXT    NOT NULL DEFAULT '',
    price_cents         INTEGER NOT NULL CHECK (price_cents >= 0),
    duration_days       INTEGER NOT NULL CHECK (duration_days > 0),
    download_limit      INTEGER NOT NULL DEFAULT -1, -- -1 表示不限制
    daily_download_limit INTEGER NOT NULL DEFAULT -1,
    max_concurrent_tasks INTEGER NOT NULL DEFAULT 1,
    max_file_size       INTEGER NOT NULL DEFAULT 0,  -- 字节；0 表示不限制
    allowed_quality     TEXT    NOT NULL DEFAULT '', -- 逗号分隔: 720p,1080p,4k；空=全部
    enabled             INTEGER NOT NULL DEFAULT 1,
    sort_order          INTEGER NOT NULL DEFAULT 0,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
);

-- 用户会员记录（新购与续费）
CREATE TABLE IF NOT EXISTS user_memberships (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    plan_id         INTEGER NOT NULL REFERENCES membership_plans(id),
    starts_at       INTEGER NOT NULL,
    expires_at      INTEGER NOT NULL,
    status          TEXT    NOT NULL DEFAULT 'active' CHECK (status IN ('active','expired','revoked')),
    source_order_id INTEGER,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_memberships_user ON user_memberships(user_id, expires_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_memberships_order ON user_memberships(source_order_id) WHERE source_order_id IS NOT NULL;

-- 订单
CREATE TABLE IF NOT EXISTS orders (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    order_no        TEXT    NOT NULL UNIQUE,
    user_id         INTEGER NOT NULL REFERENCES users(id),
    plan_id         INTEGER NOT NULL REFERENCES membership_plans(id),
    amount_cents    INTEGER NOT NULL,
    currency        TEXT    NOT NULL DEFAULT 'CNY',
    provider        TEXT    NOT NULL DEFAULT 'mock',
    provider_trade_no TEXT,
    status          TEXT    NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','paid','closed','refunded','failed')),
    subject         TEXT    NOT NULL DEFAULT '',
    paid_at         INTEGER,
    expires_at      INTEGER NOT NULL,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_orders_user ON orders(user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_orders_status ON orders(status, expires_at);

-- 支付事件（Mock 与未来真实回调；防止重复处理）
CREATE TABLE IF NOT EXISTS payment_events (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    provider     TEXT    NOT NULL,
    event_id     TEXT    NOT NULL,
    order_no     TEXT    NOT NULL,
    payload      TEXT    NOT NULL DEFAULT '',
    processed    INTEGER NOT NULL DEFAULT 0,
    created_at   INTEGER NOT NULL,
    processed_at INTEGER
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_payment_events_unique ON payment_events(provider, event_id);

-- 下载任务
CREATE TABLE IF NOT EXISTS download_tasks (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id       TEXT    NOT NULL UNIQUE,
    user_id       INTEGER NOT NULL REFERENCES users(id),
    source_url    TEXT    NOT NULL,
    platform      TEXT    NOT NULL,
    status        TEXT    NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','processing','completed','failed','cancelled')),
    progress      REAL    NOT NULL DEFAULT 0,
    task_dir      TEXT    NOT NULL DEFAULT '',
    output_path   TEXT    NOT NULL DEFAULT '',
    filename      TEXT    NOT NULL DEFAULT '',
    filesize      INTEGER NOT NULL DEFAULT 0,
    error_message TEXT    NOT NULL DEFAULT '',
    title         TEXT    NOT NULL DEFAULT '',
    created_at    INTEGER NOT NULL,
    started_at    INTEGER,
    completed_at  INTEGER,
    expires_at    INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tasks_user ON download_tasks(user_id, created_at);
CREATE INDEX IF NOT EXISTS idx_tasks_status ON download_tasks(status);

-- 审计日志（禁止记录密码/Cookie/令牌正文）
CREATE TABLE IF NOT EXISTS audit_logs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER,
    action     TEXT    NOT NULL,
    detail     TEXT    NOT NULL DEFAULT '',
    ip_hash    TEXT,
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_created ON audit_logs(created_at);
CREATE INDEX IF NOT EXISTS idx_audit_user ON audit_logs(user_id);
