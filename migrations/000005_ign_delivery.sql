-- OPIC 点火中心(ignition)v1.1 交付门面与本机执行位 DDL——goose 迁移 000005
-- 依据 models/m1-object-model.yaml(AGG-IGN-06 属性增补 + 子实体 TaskLogLine)与需求文档 2.6/2.7。
-- 约定同 000004:五默认字段、时间列 TEXT、逻辑删除 flag。
--   步骤编码/来源标识/结论依据三件是门面与判定网关的落点(INV-34~INV-37);
--   日志行由「中继回流视图」升格为聚合内子实体表 ign_task_log:子实体不独立编号,
--   以「所属任务 + 行游标」定位,游标任务内单调只增(INV-38),写前不可逆脱敏后置 masked(R-16/INV-40)。

-- +goose Up

-- ============ AGG-IGN-06 执行任务:门面与判定网关三件(v1.1) ============
ALTER TABLE exec_task ADD COLUMN IF NOT EXISTS step_code VARCHAR(4);          -- IGN_INSTALL_STEP 01~07;仅 ezctl.setup 有值,其余动作必空(INV-36)
ALTER TABLE exec_task ADD COLUMN IF NOT EXISTS source_task_no VARCHAR(64);    -- 来源任务标识(O-CLC 任务编号);门面受理必填、人工提交必空(INV-34)
ALTER TABLE exec_task ADD COLUMN IF NOT EXISTS source_step_index INTEGER;     -- 来源步骤序号(≥1);与来源任务标识共同构成幂等键(INV-35)
ALTER TABLE exec_task ADD COLUMN IF NOT EXISTS verdict_source VARCHAR(20);    -- 结论依据(IGN_VERDICT_SOURCE);终态必填,LOST 专用 TRACE_LOST(INV-37)
COMMENT ON COLUMN exec_task.step_code IS '安装类步骤参数合法域 01~07;步骤状态机权威在 O-CLC(INV-36)';
COMMENT ON COLUMN exec_task.source_task_no IS '跨域只存标识(INV-34);REP-14 固定过滤该列非空';
COMMENT ON COLUMN exec_task.verdict_source IS '终态结论的可信来源(报表口径 7 的对象层落点);LOST 必为 TRACE_LOST 且无退出码(INV-37)';

-- 门面路径不携带业务请求令牌 ⇒ 该列改条件必填(INV-31 摘要唯一性只对非空值生效)
ALTER TABLE exec_task ALTER COLUMN request_token_digest DROP NOT NULL;
DROP INDEX IF EXISTS uq_task_digest_per_executor;
CREATE UNIQUE INDEX IF NOT EXISTS uq_task_digest_per_executor
    ON exec_task (executor_id, request_token_digest)
    WHERE flag = 1 AND request_token_digest IS NOT NULL;

-- INV-35:同一「来源任务标识 + 来源步骤序号」至多一个非终态任务(命中即复用原任务)
CREATE UNIQUE INDEX IF NOT EXISTS uq_task_source_open ON exec_task (source_task_no, source_step_index)
    WHERE flag = 1 AND task_status IN ('RUNNING', 'CANCELLING');
-- R-17 同集群在途互斥与排队(读同源集群非终态计数);REP-14 受理台账检索
CREATE INDEX IF NOT EXISTS idx_task_cluster_open ON exec_task (cluster_ref) WHERE flag = 1 AND task_status IN ('RUNNING', 'CANCELLING');
CREATE INDEX IF NOT EXISTS idx_task_source ON exec_task (source_task_no, source_step_index);

-- ============ 子实体 TaskLogLine:task_log → ign_task_log(v1.1) ============
ALTER TABLE IF EXISTS task_log RENAME TO ign_task_log;
ALTER TABLE ign_task_log ADD COLUMN IF NOT EXISTS masked BOOLEAN NOT NULL DEFAULT false;
COMMENT ON COLUMN ign_task_log.direction IS '流向(IGN_LOG_DIRECTION):TOOL 执行位子进程逐行输出 / PLATFORM 平台注记 / RELAY 中继回流 / AGENT 代理回报';
COMMENT ON COLUMN ign_task_log.masked IS 'v1.1;命中掩码规则后被改写 ⇒ true(写前不可逆脱敏,R-16/INV-40)——「日志被动过」可见而非静默替换';
-- INV-38:任务内行游标唯一(乱序或重复到达的行直接丢弃不入库)
CREATE UNIQUE INDEX IF NOT EXISTS uq_ign_log_seq ON ign_task_log (task_id, seq) WHERE flag = 1;
-- REF-16 保留期清理(数据生命周期,非业务改写)
CREATE INDEX IF NOT EXISTS idx_ign_log_logged_at ON ign_task_log (logged_at);

-- ============ v1.1 运行参数(REF-14~16;FUNC-30 可维护) ============
INSERT INTO ign_platform_config (config_key, config_value, description, created_by)
SELECT k.v, d.v, m.v, 1
FROM (VALUES ('receipt_log_batch'), ('kill_grace_seconds'), ('task_log_retention_days')) AS k(v)
JOIN LATERAL (
    SELECT CASE k.v
        WHEN 'receipt_log_batch' THEN '500'
        WHEN 'kill_grace_seconds' THEN '30'
        ELSE '14'
    END AS v
) d ON true
JOIN LATERAL (
    SELECT CASE k.v
        WHEN 'receipt_log_batch' THEN 'REF-14 门面单次回吐日志行数上限（缺省 500 行/次，超出携带续传游标）'
        WHEN 'kill_grace_seconds' THEN 'REF-15 取消与超时的终止宽限（秒，SIGTERM 后再 SIGKILL，缺省 30）'
        ELSE 'REF-16 任务日志保留天数（缺省 14；超期行由平台侧清理）'
    END AS v
) m ON true
WHERE NOT EXISTS (SELECT 1 FROM ign_platform_config p WHERE p.config_key = k.v);

-- ============ 对账留痕事件类型扩充（v1.1；REP-13 c8/c9 的分列可数口径） ============
COMMENT ON COLUMN ign_reconcile_log.event_type IS
    'COUNT_CORRECT 计数纠偏 / STATUS_FIX 状态对账修正 / TASK_LOST 失联判定（三类=纠偏，REP-13 c9）'
    '/ GOVERNANCE_REFUSE 治理拒绝（R-12/R-13）/ TOOL_UNAVAILABLE 执行位不可用即刻拒绝（R-15）'
    '（两类=受理侧拒绝，REP-13 c8，且不建任务行）—— 纠偏、拒绝、执行结论三条语义分列，不可合并计数';

-- +goose Down
COMMENT ON COLUMN ign_reconcile_log.event_type IS '事件类型(COUNT_CORRECT/STATUS_FIX/TASK_LOST)';
DELETE FROM ign_platform_config WHERE config_key IN ('receipt_log_batch', 'kill_grace_seconds', 'task_log_retention_days');
DROP INDEX IF EXISTS idx_ign_log_logged_at;
DROP INDEX IF EXISTS uq_ign_log_seq;
ALTER TABLE ign_task_log DROP COLUMN IF EXISTS masked;
ALTER TABLE IF EXISTS ign_task_log RENAME TO task_log;
DROP INDEX IF EXISTS idx_task_source;
DROP INDEX IF EXISTS idx_task_cluster_open;
DROP INDEX IF EXISTS uq_task_source_open;
DROP INDEX IF EXISTS uq_task_digest_per_executor;
CREATE UNIQUE INDEX IF NOT EXISTS uq_task_digest_per_executor ON exec_task (executor_id, request_token_digest) WHERE flag = 1;
ALTER TABLE exec_task ALTER COLUMN request_token_digest SET NOT NULL;
ALTER TABLE exec_task DROP COLUMN IF EXISTS verdict_source;
ALTER TABLE exec_task DROP COLUMN IF EXISTS source_step_index;
ALTER TABLE exec_task DROP COLUMN IF EXISTS source_task_no;
ALTER TABLE exec_task DROP COLUMN IF EXISTS step_code;
