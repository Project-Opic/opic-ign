-- ignition-center 审计修复 DDL —— goose 迁移 000008
-- 会话代际:本地 HS256 令牌签发时写入 epoch,禁用/重置口令时递增,
-- 使既有令牌在账号重新启用后仍保持失效(逐请求与当前值比对,fail-closed)。

-- +goose Up
ALTER TABLE sys_user ADD COLUMN IF NOT EXISTS session_epoch BIGINT NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE sys_user DROP COLUMN IF EXISTS session_epoch;
