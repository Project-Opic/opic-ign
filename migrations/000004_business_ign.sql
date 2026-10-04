-- OPIC 点火中心(ignition)业务表 DDL——goose 迁移 000004
-- 由阶段二七模型 models/m1-object-model.yaml 生成(AGG-IGN-01..06,6 个聚合根均为独立主表,无子实体从表)。
-- 约定(开发指导书 §3.1/§3.2 + M3 REF-*):
--   业务编号 = {三位前缀}-{8 位日期段}-{4 位流水段}:VER/EXE/BCR/TLS/TSK/RMV;
--   编号由服务层在同一事务内 pg_advisory_xact_lock 防重号(事务结束自动释放),唯一索引兜底;
--   每表必带 5 默认字段 created_by/created_at/updated_by/updated_at/flag(逻辑删 flag=0 为删);
--   时间列统一 TEXT,默认 to_char(now(),'YYYY-MM-DD HH24:MI:SS'),与底座口径一致。
-- 底座示例业务表 customer_application 已自 000001 移除(交付执行域以版本/预推明细/执行机/
--   引导凭据/证书绑定/执行任务承载业务)。
-- 跨域留痕:default_registry_ref/asset_ref/cluster_ref/cert_ref 为跨中心引用(O-REG/O-AST/集群域/O-CRT),
--   只存标识不直查他域表(MCC 架构);跨中心 HTTP API 校验通道阶段三未接入,如实留痕不静默降级。

-- +goose Up

-- ============ AGG-IGN-01 交付版本 DeliveryVersion(VER) ============
CREATE TABLE IF NOT EXISTS delivery_version (
    id BIGSERIAL PRIMARY KEY,
    version_no VARCHAR(32) UNIQUE,                       -- 版本编号(VER-YYYYMMDD-NNNN,服务层生成)
    name VARCHAR(64) NOT NULL,                           -- 版本名(INV-01 未归档范围内唯一,保存即校验)
    display_name VARCHAR(128),                           -- 显示名
    tool_version VARCHAR(32) NOT NULL,                   -- 交付工具版本(INV-02 语义化版本 x.y.z)
    orchestrator_version VARCHAR(32) NOT NULL,           -- 编排引擎版本(INV-02 v1.x.y)
    runtime_version VARCHAR(32) NOT NULL,                -- 容器运行时版本(INV-02)
    etcd_version VARCHAR(32) NOT NULL,                   -- etcd 版本(INV-02)
    cni_plugin VARCHAR(20) NOT NULL,                     -- CNI 插件(INV-03 命中 IGN_CNI_PLUGIN)
    cni_version VARCHAR(32) NOT NULL,                    -- CNI 版本(INV-02)
    coredns_version VARCHAR(32) NOT NULL,                -- CoreDNS 版本(INV-02)
    tarball_url VARCHAR(500) NOT NULL,                   -- 发行包地址(INV-02 合法 URL)
    tarball_sha256 VARCHAR(64),                          -- 发行包摘要(64 位十六进制;R-02 提供即强校验)
    cache_path VARCHAR(500),                             -- 缓存解压路径(INV-06 仅就绪/预推系列状态非空)
    image_manifest TEXT NOT NULL DEFAULT '[]',           -- 镜像清单(JSON 数组;FUNC-10 覆写,INV-08 派生源)
    default_registry_ref VARCHAR(128),                   -- 默认仓库项目(跨中心引用只存标识)
    version_status VARCHAR(30) NOT NULL DEFAULT 'DRAFT', -- 版本状态(IGN_VERSION_STATUS;INV-07 单向迁移)
    archived_at TEXT,                                    -- 归档时间(INV-05 冻结基准;REF-08 软删须超 N 天)
    created_by BIGINT,
    created_at TEXT DEFAULT to_char(now(), 'YYYY-MM-DD HH24:MI:SS'),
    updated_by BIGINT,
    updated_at TEXT DEFAULT to_char(now(), 'YYYY-MM-DD HH24:MI:SS'),
    flag SMALLINT DEFAULT 1
);
-- INV-01:版本名在未归档范围内唯一(已归档名可复用;软删释放)
CREATE UNIQUE INDEX IF NOT EXISTS uq_version_name_live ON delivery_version (LOWER(name)) WHERE flag = 1 AND version_status <> 'ARCHIVED';
CREATE INDEX IF NOT EXISTS idx_version_status ON delivery_version (version_status);

-- ============ AGG-IGN-02 镜像预推明细 ImagePrepushItem(IMG;预推明细由镜像清单派生,无独立编号) ============
CREATE TABLE IF NOT EXISTS image_prepush_item (
    id BIGSERIAL PRIMARY KEY,
    version_id BIGINT NOT NULL,                          -- 所属版本(引用 delivery_version.id)
    repo VARCHAR(200) NOT NULL,                          -- 镜像仓库
    tag VARCHAR(100) NOT NULL,                           -- 镜像标签
    digest VARCHAR(128),                                 -- 内容摘要(INV-09 SUCCESS 时必填)
    size_bytes BIGINT,                                   -- 体积(字节)
    prepush_status VARCHAR(20) NOT NULL DEFAULT 'PENDING', -- 预推状态(IGN_PREPUSH_STATUS)
    fail_reason VARCHAR(500),                            -- 失败原因/跳过依据(INV-09 FAILED 必填;INV-10 SKIPPED 必填)
    prepushed_at TEXT,                                   -- 最近预推时间
    created_by BIGINT,
    created_at TEXT DEFAULT to_char(now(), 'YYYY-MM-DD HH24:MI:SS'),
    updated_by BIGINT,
    updated_at TEXT DEFAULT to_char(now(), 'YYYY-MM-DD HH24:MI:SS'),
    flag SMALLINT DEFAULT 1
);
-- INV-08:同一版本内「仓库 + 标签」联合唯一
CREATE UNIQUE INDEX IF NOT EXISTS uq_prepush_version_repo_tag ON image_prepush_item (version_id, repo, tag) WHERE flag = 1;
CREATE INDEX IF NOT EXISTS idx_prepush_status ON image_prepush_item (prepush_status);

-- ============ AGG-IGN-03 执行机 Executor(EXE) ============
CREATE TABLE IF NOT EXISTS executor (
    id BIGSERIAL PRIMARY KEY,
    executor_no VARCHAR(32) UNIQUE,                      -- 执行机编号(EXE-YYYYMMDD-NNNN,服务层生成)
    name VARCHAR(64) NOT NULL,                           -- 名称(INV-11 未注销范围内唯一)
    display_name VARCHAR(128),                           -- 显示名
    executor_type VARCHAR(10) NOT NULL DEFAULT 'REMOTE', -- 类型(IGN_EXECUTOR_TYPE;INV-12 人工创建 LOCAL 拒绝)
    asset_ref VARCHAR(128),                              -- 资产标识(INV-13 REMOTE 必填;跨中心 O-AST 只存标识)
    host VARCHAR(200) NOT NULL,                          -- 服务地址(INV-11 地址+端口组合未注销内唯一)
    port INTEGER NOT NULL,                               -- 服务端口(INV-14 1024~65535)
    capability_flags VARCHAR(200) NOT NULL DEFAULT '',   -- 能力标签(逗号分隔;INV-18 命中 IGN_CAPABILITY)
    status VARCHAR(20) NOT NULL DEFAULT 'ENROLLING',     -- 治理状态(IGN_EXECUTOR_STATUS;INV-17 REMOVED 终态)
    concurrent_tasks_max INTEGER NOT NULL DEFAULT 4,     -- 并发上限(INV-16 LOCAL 固定 4;REF-10)
    current_tasks_count INTEGER NOT NULL DEFAULT 0,      -- 当前任务数(INV-15 ≥0 且 ≤ 上限;R-05 原子增减)
    last_heartbeat_at TEXT,                              -- 最近心跳时间(REF-03 超时阈值判定)
    last_heartbeat_payload TEXT,                         -- 最近心跳载荷(健康视图)
    bootstrapped_at TEXT,                                -- 引导完成时间(R-03 双绑定 ACTIVE 时写入)
    tls_declined BOOLEAN NOT NULL DEFAULT FALSE,         -- TLS 显式降级声明(R-11/INV-27 与绑定互斥)
    created_by BIGINT,
    created_at TEXT DEFAULT to_char(now(), 'YYYY-MM-DD HH24:MI:SS'),
    updated_by BIGINT,
    updated_at TEXT DEFAULT to_char(now(), 'YYYY-MM-DD HH24:MI:SS'),
    flag SMALLINT DEFAULT 1
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_executor_name_live ON executor (LOWER(name)) WHERE flag = 1 AND status <> 'REMOVED';
CREATE UNIQUE INDEX IF NOT EXISTS uq_executor_hostport_live ON executor (host, port) WHERE flag = 1 AND status <> 'REMOVED';
-- INV-12:LOCAL 执行机全平台至多一行(由系统自动注册)
CREATE UNIQUE INDEX IF NOT EXISTS uq_executor_local_single ON executor ((executor_type)) WHERE executor_type = 'LOCAL' AND flag = 1;
CREATE INDEX IF NOT EXISTS idx_executor_status ON executor (status);

-- ============ AGG-IGN-04 引导凭据 BootstrapCredential(BCR) ============
-- INV-22 输出面约束:token_cipher/token_nonce/令牌本体不出现在任何查询响应/事件/报表/导出/审计
-- (仅 Q-2 两处明文路径:签发时一次性展示、引导消耗时校验);AI 只读 SQL 白名单不含本表。
CREATE TABLE IF NOT EXISTS bootstrap_credential (
    id BIGSERIAL PRIMARY KEY,
    credential_no VARCHAR(32) UNIQUE,                    -- 凭据编号(BCR-YYYYMMDD-NNNN,服务层生成)
    executor_id BIGINT NOT NULL,                         -- 所属执行机
    token_cipher TEXT,                                   -- 令牌密文(INV-20 与 nonce 成对或同空)
    token_nonce VARCHAR(64),                             -- 令牌 nonce(INV-20)
    issued_at TEXT NOT NULL,                             -- 生成时间
    expires_at TEXT NOT NULL,                            -- 过期时间(REF-01 平台配置缺省 24 小时)
    fail_count INTEGER NOT NULL DEFAULT 0,               -- 失败计数(R-04 达阈值执行机置锁定)
    cred_status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',   -- 凭据状态(IGN_CRED_STATUS;INV-21 仅 ACTIVE 未过期可用)
    consumed_at TEXT,                                    -- 消耗时间(消耗后密文与 nonce 同时置空,INV-20)
    revoked_at TEXT,                                     -- 吊销时间(重新生成旧凭据置 REVOKED,INV-19)
    created_by BIGINT,
    created_at TEXT DEFAULT to_char(now(), 'YYYY-MM-DD HH24:MI:SS'),
    updated_by BIGINT,
    updated_at TEXT DEFAULT to_char(now(), 'YYYY-MM-DD HH24:MI:SS'),
    flag SMALLINT DEFAULT 1
);
-- INV-19:同一执行机至多一条 ACTIVE 引导凭据
CREATE UNIQUE INDEX IF NOT EXISTS uq_bcr_active_per_executor ON bootstrap_credential (executor_id) WHERE cred_status = 'ACTIVE' AND flag = 1;
CREATE INDEX IF NOT EXISTS idx_bcr_executor ON bootstrap_credential (executor_id);

-- ============ AGG-IGN-05 通道证书绑定 ExecutorTlsBinding(TLS) ============
CREATE TABLE IF NOT EXISTS executor_tls_binding (
    id BIGSERIAL PRIMARY KEY,
    binding_no VARCHAR(32) UNIQUE,                       -- 绑定编号(TLS-YYYYMMDD-NNNN,服务层生成)
    executor_id BIGINT NOT NULL,                         -- 所属执行机
    cert_role VARCHAR(10) NOT NULL,                      -- 证书用途(IGN_CERT_ROLE:SERVER/CLIENT)
    cert_ref VARCHAR(128),                               -- 凭据标识(跨中心 O-CRT 只存标识,不直查)
    common_name VARCHAR(200) NOT NULL,                   -- 通用名称(REF-05 固定模板生成,不人工指定)
    not_before TEXT NOT NULL,                            -- 生效起
    not_after TEXT NOT NULL,                             -- 生效止(INV-24 须晚于生效起;REF-06 剩余天数即时计算不落库)
    renewal_count INTEGER NOT NULL DEFAULT 0,            -- 续期次数(INV-26 只增不减)
    last_renewed_at TEXT,                                -- 最近续期时间
    bind_status VARCHAR(20) NOT NULL DEFAULT 'PENDING',  -- 绑定状态(IGN_TLS_BIND_STATUS;INV-25 REVOKED 终态)
    created_by BIGINT,
    created_at TEXT DEFAULT to_char(now(), 'YYYY-MM-DD HH24:MI:SS'),
    updated_by BIGINT,
    updated_at TEXT DEFAULT to_char(now(), 'YYYY-MM-DD HH24:MI:SS'),
    flag SMALLINT DEFAULT 1
);
-- INV-23:同一执行机同一用途至多一条生效绑定(PENDING/ACTIVE/RENEWING)
CREATE UNIQUE INDEX IF NOT EXISTS uq_tls_active_per_role ON executor_tls_binding (executor_id, cert_role) WHERE bind_status IN ('PENDING','ACTIVE','RENEWING') AND flag = 1;
CREATE INDEX IF NOT EXISTS idx_tls_not_after ON executor_tls_binding (not_after);

-- ============ AGG-IGN-06 执行任务 ExecTask(TSK) ============
CREATE TABLE IF NOT EXISTS exec_task (
    id BIGSERIAL PRIMARY KEY,
    task_no VARCHAR(32) UNIQUE,                          -- 任务编号(TSK-YYYYMMDD-NNNN,服务层生成)
    executor_id BIGINT NOT NULL,                         -- 所属执行机
    action VARCHAR(64) NOT NULL,                         -- 执行动作(INV-28 命中 IGN_ACTION 白名单)
    args TEXT NOT NULL DEFAULT '',                       -- 动作参数(R-12 拼接风险治理拒绝)
    env TEXT,                                            -- 环境变量(展示脱敏)
    working_dir VARCHAR(300),                            -- 工作目录
    cluster_ref VARCHAR(128) NOT NULL,                   -- 关联集群(INV-33 跨域引用只存标识)
    request_token_digest VARCHAR(64) NOT NULL,           -- 请求令牌摘要(INV-31 防重放,64 位十六进制)
    remote_task_id VARCHAR(64),                          -- 远端任务标识
    task_status VARCHAR(20) NOT NULL DEFAULT 'RUNNING',  -- 任务状态(IGN_TASK_STATUS;INV-29/INV-30 口径)
    exit_code INTEGER,                                   -- 退出码(终态必有退出码或失败原因之一,INV-29)
    fail_reason VARCHAR(30),                             -- 失败原因(IGN_TASK_FAIL_REASON;INV-30 与 CANCELLED 分列)
    timeout_seconds INTEGER NOT NULL DEFAULT 7200,       -- 超时秒数(REF-02 平台配置缺省 7200)
    started_at TEXT,                                     -- 开始时间(INV-29 RUNNING 必有)
    ended_at TEXT,                                       -- 结束时间(终态必有,INV-29)
    duration_ms BIGINT,                                  -- 耗时(毫秒)
    submitted_by VARCHAR(50) NOT NULL,                   -- 提交人
    reconciled_note TEXT,                                -- 对账注记(R-09 回填留痕;REP-10 是否被修正过)
    created_by BIGINT,
    created_at TEXT DEFAULT to_char(now(), 'YYYY-MM-DD HH24:MI:SS'),
    updated_by BIGINT,
    updated_at TEXT DEFAULT to_char(now(), 'YYYY-MM-DD HH24:MI:SS'),
    flag SMALLINT DEFAULT 1
);
-- INV-31:同一执行机上请求令牌摘要至多对应一个任务(防重放兜底;重复提交返回原任务)
CREATE UNIQUE INDEX IF NOT EXISTS uq_task_digest_per_executor ON exec_task (executor_id, request_token_digest) WHERE flag = 1;
CREATE INDEX IF NOT EXISTS idx_task_status ON exec_task (task_status);
CREATE INDEX IF NOT EXISTS idx_task_executor ON exec_task (executor_id);

-- ============ 强制注销申请载体(FUNC-31 提交 / FUNC-32·33 审批 / R-07·R-08 拆除链) ============
CREATE TABLE IF NOT EXISTS ign_force_remove (
    id BIGSERIAL PRIMARY KEY,
    apply_no VARCHAR(32),                                -- 申请编号(RMV-YYYYMMDD-NNNN,服务层生成)
    executor_id BIGINT NOT NULL,                         -- 目标执行机
    reason TEXT NOT NULL,                                -- 强制原因(MU el12 必填)
    impact_snapshot TEXT,                                -- 影响面快照(运行中任务清单,R-08 强制展示)
    status VARCHAR(30) NOT NULL DEFAULT '草稿',           -- 草稿/待平台运维审批/已通过/已驳回/已撤回/已注销
    instance_id BIGINT,                                  -- 审批流实例(FLOW-PROC-APP-01)
    applicant_id BIGINT,
    applicant_name VARCHAR(50),
    approve_comment TEXT,
    approved_by BIGINT,
    approved_name VARCHAR(50),
    approved_at TEXT,
    removal_report TEXT,                                 -- 注销报告(R-07 取消结果与残留留痕)
    created_by BIGINT,
    created_at TEXT DEFAULT to_char(now(), 'YYYY-MM-DD HH24:MI:SS'),
    updated_by BIGINT,
    updated_at TEXT DEFAULT to_char(now(), 'YYYY-MM-DD HH24:MI:SS'),
    flag SMALLINT DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_force_remove_status ON ign_force_remove (status);
CREATE INDEX IF NOT EXISTS idx_force_remove_executor ON ign_force_remove (executor_id);

-- ============ 对账与纠偏台账(R-06 计数纠偏 / R-09 状态修正与任务丢失;REP-12) ============
CREATE TABLE IF NOT EXISTS ign_reconcile_log (
    id BIGSERIAL PRIMARY KEY,
    event_type VARCHAR(30) NOT NULL,                     -- 事件类型(COUNT_CORRECT/STATUS_FIX/TASK_LOST)
    object_type VARCHAR(30),                             -- 对象类型(EXECUTOR/TASK)
    object_no VARCHAR(64),                               -- 对象编号
    local_value VARCHAR(200),                            -- 本地值
    remote_value VARCHAR(200),                           -- 对端值
    trigger_reason VARCHAR(200),                         -- 触发原因
    operator_id BIGINT,                                  -- 留痕:谁
    operator_name VARCHAR(50),
    created_by BIGINT,
    created_at TEXT DEFAULT to_char(now(), 'YYYY-MM-DD HH24:MI:SS'),
    updated_by BIGINT,
    updated_at TEXT DEFAULT to_char(now(), 'YYYY-MM-DD HH24:MI:SS'),
    flag SMALLINT DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_reconcile_type ON ign_reconcile_log (event_type);

-- ============ 任务日志(中继回流视图 REP-11/FUNC-27;代理回执写入) ============
CREATE TABLE IF NOT EXISTS task_log (
    id BIGSERIAL PRIMARY KEY,
    task_id BIGINT NOT NULL,                             -- 任务(引用 exec_task.id)
    seq INTEGER NOT NULL DEFAULT 0,                      -- 行序
    direction VARCHAR(10) NOT NULL DEFAULT 'RELAY',      -- 流向(RELAY 中继回流/PLATFORM 平台注记/AGENT 代理)
    log_line TEXT NOT NULL,                              -- 日志行
    logged_at TEXT,                                      -- 时间
    created_by BIGINT,
    created_at TEXT DEFAULT to_char(now(), 'YYYY-MM-DD HH24:MI:SS'),
    updated_by BIGINT,
    updated_at TEXT DEFAULT to_char(now(), 'YYYY-MM-DD HH24:MI:SS'),
    flag SMALLINT DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_task_log_task ON task_log (task_id, seq);

-- ============ 平台配置项(REF-01~12 运行参数 FUNC-30;键值面) ============
CREATE TABLE IF NOT EXISTS ign_platform_config (
    id BIGSERIAL PRIMARY KEY,
    config_key VARCHAR(64) NOT NULL UNIQUE,
    config_value TEXT NOT NULL,
    description VARCHAR(200),
    created_by BIGINT,
    created_at TEXT DEFAULT to_char(now(), 'YYYY-MM-DD HH24:MI:SS'),
    updated_by BIGINT,
    updated_at TEXT DEFAULT to_char(now(), 'YYYY-MM-DD HH24:MI:SS'),
    flag SMALLINT DEFAULT 1
);

-- +goose Down
DROP TABLE IF EXISTS ign_platform_config;
DROP TABLE IF EXISTS task_log;
DROP TABLE IF EXISTS ign_reconcile_log;
DROP TABLE IF EXISTS ign_force_remove;
DROP TABLE IF EXISTS exec_task;
DROP TABLE IF EXISTS executor_tls_binding;
DROP TABLE IF EXISTS bootstrap_credential;
DROP TABLE IF EXISTS executor;
DROP TABLE IF EXISTS image_prepush_item;
DROP TABLE IF EXISTS delivery_version;
