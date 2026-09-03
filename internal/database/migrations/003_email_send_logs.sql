-- 003_email_send_logs.sql
-- 邮件发送日志：记录每次验证码/通知邮件的发送结果与服务器返回的错误，
-- 便于管理员在后台直接诊断「收不到验证码」问题（不含邮件正文/验证码/授权码）。
CREATE TABLE IF NOT EXISTS email_send_logs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    email_hash TEXT    NOT NULL,   -- 收件人邮箱 SHA-256（不存明文）
    purpose    TEXT    NOT NULL,   -- register / resend_verification / reset_password
    ok         INTEGER NOT NULL,   -- 1 成功；0 失败
    err_msg    TEXT    NOT NULL DEFAULT '', -- 失败原因（已脱敏，不含凭据）
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_email_send_logs_created ON email_send_logs(created_at);
