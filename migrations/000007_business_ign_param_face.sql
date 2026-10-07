-- OPIC 点火中心(ignition)v1.3 易用性增补「参数面治理」DDL——goose 迁移 000007
-- 依据 models/m1-object-model.yaml(AGG-IGN-07 交付参数面模板 + 子实体 ParamTemplateItem /
--   AGG-IGN-08 集群交付配置覆写)与 models/m3-rule-model.yaml(INV-41~43 / R-20~R-26 / REF-26)。
-- 约定同 000004/000005:五默认字段(created_by/created_at/updated_by/updated_at/flag)、
--   时间列 TEXT、逻辑删除 flag=0、唯一性用部分唯一索引(WHERE flag = 1)兜底。
-- 注意:goose 同号文件会 panic ⇒ 本迁移必须是 000007(000006 已被 m5 种子占用)。
-- 跨域留痕:cluster_ref / applied_task_no 为跨域或跨对象标识,只存标识不建外键(INV-33 同族);
--   清单/配置文件文本本体不入表(R-24 / Q-69 裁定②)——表里只有治理事实。

-- +goose Up

-- ============ AGG-IGN-07 交付参数面模板 IgnParamTemplate(TMP) ============
CREATE TABLE IF NOT EXISTS ign_param_template (
    id BIGSERIAL PRIMARY KEY,
    template_no VARCHAR(32) NOT NULL UNIQUE,               -- 模板编号 TMP-YYYYMMDD-NNNN(服务层事务内生成,生成后只读;m1 required=true ⇒ NOT NULL)
    template_name VARCHAR(128) NOT NULL,                   -- 模板名称(INV-41 同族:未删除范围内唯一,重名即拒绝)
    file_face VARCHAR(10) NOT NULL,                        -- 文件面(IGN_CONFIG_FILE_FACE:HOSTS/CONFIG;一张模板只属一轴)
    template_status VARCHAR(20) NOT NULL DEFAULT 'DRAFT',  -- 模板状态(IGN_PARAM_TEMPLATE_STATE:DRAFT/ENABLED/DISABLED;R-20 第 3 层只认启用态)
    is_default BOOLEAN NOT NULL DEFAULT FALSE,             -- 默认标记(只服务上游选择器缺省值,不参与渲染兜底,R-20)
    description VARCHAR(500),                              -- 用途说明(自由文本)
    enabled_at TEXT,                                       -- 首次进入启用态时间(草稿态为空)
    created_by BIGINT,
    created_at TEXT DEFAULT to_char(now(), 'YYYY-MM-DD HH24:MI:SS'),
    updated_by BIGINT,
    updated_at TEXT DEFAULT to_char(now(), 'YYYY-MM-DD HH24:MI:SS'),
    flag SMALLINT DEFAULT 1
);
-- FUNC-40:模板名在"未删除范围内"唯一(软删除释放名称)
CREATE UNIQUE INDEX IF NOT EXISTS uq_ign_template_name_live ON ign_param_template (LOWER(template_name)) WHERE flag = 1;
-- INV-43 第一句:同一文件面内 isDefault 为真的模板至多一条(谓词不带状态条件——带上 ENABLED
-- 就放行了"同面两条默认其中一条停用态"的歧义态;"默认模板必须处于启用态"由服务层校验, FUNC-42 停用默认即拒)
CREATE UNIQUE INDEX IF NOT EXISTS uq_ign_template_default_per_face
    ON ign_param_template (file_face) WHERE flag = 1 AND is_default = TRUE;
-- REF-20/REP-15:按文件面分组 + 状态过滤(停用可切换显示,软删行默认可见)
CREATE INDEX IF NOT EXISTS idx_ign_template_face_status ON ign_param_template (file_face, template_status);

-- ============ 子实体 ParamTemplateItem(聚合内从表;不独立编号,以「所属模板 + 字段键」定位) ============
CREATE TABLE IF NOT EXISTS ign_param_template_item (
    id BIGSERIAL PRIMARY KEY,
    template_id BIGINT NOT NULL,                           -- 所属模板(引用 ign_param_template.id;与字段键联合唯一)
    field_key VARCHAR(64) NOT NULL,                        -- 字段键(合法域 = REF-17~REF-25 面定义,INV-41;刻意不建字典)
    field_value TEXT NOT NULL DEFAULT '',                  -- 字段值(可空串=让位给下层;R-23 过受理侧同一套形态校验器)
    value_shape VARCHAR(10) NOT NULL,                      -- 值形态(IGN_VALUE_SHAPE:SCALAR/LIST;随字段键由面定义带出,不可人工改)
    created_by BIGINT,
    created_at TEXT DEFAULT to_char(now(), 'YYYY-MM-DD HH24:MI:SS'),
    updated_by BIGINT,
    updated_at TEXT DEFAULT to_char(now(), 'YYYY-MM-DD HH24:MI:SS'),
    flag SMALLINT DEFAULT 1
);
-- FUNC-41:同面内不重行(整组替换后仍成立);R-22 列表值整体替换、无元素级增删
CREATE UNIQUE INDEX IF NOT EXISTS uq_ign_template_item_key ON ign_param_template_item (template_id, field_key) WHERE flag = 1;
CREATE INDEX IF NOT EXISTS idx_ign_template_item_template ON ign_param_template_item (template_id);

-- ============ AGG-IGN-08 集群交付配置覆写 ClusterConfigOverride(不独立编号) ============
CREATE TABLE IF NOT EXISTS ign_cluster_config_override (
    id BIGSERIAL PRIMARY KEY,
    cluster_ref VARCHAR(128) NOT NULL,                     -- 集群标识(跨域只存标识,INV-33 同族;天然坐标之一)
    file_face VARCHAR(10) NOT NULL,                        -- 文件面(IGN_CONFIG_FILE_FACE;天然坐标之二)
    field_key VARCHAR(64) NOT NULL,                        -- 字段键(限 REF-17~25 面定义,INV-41;天然坐标之三)
    override_value TEXT NOT NULL,                          -- 覆写值(列表型为整体替换后的全量值,R-22;过 R-23 校验器)
    value_shape VARCHAR(10) NOT NULL,                      -- 值形态(IGN_VALUE_SHAPE;决定校验分支与回落粒度)
    override_status VARCHAR(20) NOT NULL DEFAULT 'EFFECTIVE', -- 覆写状态(IGN_OVERRIDE_STATE:EFFECTIVE/ROLLED_BACK,两态可往复)
    reason VARCHAR(500) NOT NULL,                          -- 覆写事由(必填:治理事实必须说为什么改)
    applied_task_no VARCHAR(32),                           -- 已应用任务号(B-AUTO-15 对齐成功后回填;回落必空,INV-42)
    applied_at TEXT,                                       -- 已应用时间(与上一列成对出现、成对清空)
    overridden_by VARCHAR(64) NOT NULL,                    -- 操作人(最后一次覆写/回落的人工身份)
    overridden_at TEXT NOT NULL,                           -- 操作时间(最后一次写入时刻)
    rolled_back_at TEXT,                                   -- 回落时间(进入已回落态时回填)
    created_by BIGINT,
    created_at TEXT DEFAULT to_char(now(), 'YYYY-MM-DD HH24:MI:SS'),
    updated_by BIGINT,
    updated_at TEXT DEFAULT to_char(now(), 'YYYY-MM-DD HH24:MI:SS'),
    flag SMALLINT DEFAULT 1,
    -- INV-42:状态为已回落时追溯对必须为空(库侧兜底,服务层先给中文提示)
    CONSTRAINT ck_ign_override_trace_pair CHECK (
        override_status <> 'ROLLED_BACK' OR (applied_task_no IS NULL AND applied_at IS NULL)
    )
);
-- INV-42:同一「集群标识 + 文件面 + 字段键」至多一条(坐标唯一;已回落行再覆写即复用该行改写)
CREATE UNIQUE INDEX IF NOT EXISTS uq_ign_override_coordinate ON ign_cluster_config_override (cluster_ref, file_face, field_key) WHERE flag = 1;
CREATE INDEX IF NOT EXISTS idx_ign_override_cluster ON ign_cluster_config_override (cluster_ref, override_status);
CREATE INDEX IF NOT EXISTS idx_ign_override_task ON ign_cluster_config_override (applied_task_no);

-- ============ v1.3 运行参数(REF-26;FUNC-30 可维护,热生效同 FUNC-30 口径) ============
-- 界面日志自动跟随的读取间隔:缺省 2 秒,允许 2~3 秒(越界由服务层夹到边界并给出提示)。
-- 是界面节奏参数、不属参数面 ⇒ 不进 INV-41 的合法键集合(不写进面定义表)。
INSERT INTO ign_platform_config (config_key, config_value, description, created_by)
SELECT 'log_follow_interval_sec', '2', 'REF-26 界面日志自动跟随的读取间隔（秒，允许 2~3；越界取边界值并提示，只影响刷新节奏不影响游标语义）', 1
WHERE NOT EXISTS (SELECT 1 FROM ign_platform_config p WHERE p.config_key = 'log_follow_interval_sec');

-- +goose Down
DELETE FROM ign_platform_config WHERE config_key = 'log_follow_interval_sec';
DROP INDEX IF EXISTS idx_ign_override_task;
DROP INDEX IF EXISTS idx_ign_override_cluster;
DROP INDEX IF EXISTS uq_ign_override_coordinate;
DROP TABLE IF EXISTS ign_cluster_config_override;
DROP INDEX IF EXISTS idx_ign_template_item_template;
DROP INDEX IF EXISTS uq_ign_template_item_key;
DROP TABLE IF EXISTS ign_param_template_item;
DROP INDEX IF EXISTS idx_ign_template_face_status;
DROP INDEX IF EXISTS uq_ign_template_default_per_face;
DROP INDEX IF EXISTS uq_ign_template_name_live;
DROP TABLE IF EXISTS ign_param_template;
