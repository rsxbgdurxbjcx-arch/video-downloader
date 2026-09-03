-- 002_verify_code_attempts.sql
-- 邮箱验证码（6 位数字）防暴力枚举：记录错误尝试次数，超过上限作废。
-- 其他列（token_hash/expires_at/used_at）沿用 001_init.sql 结构。
ALTER TABLE email_verification_tokens ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;
