// O-IGN 点火中心接口封装(MU 七屏 → M2 行为 FUNC-01..33 → OPIC 统一信封)。
// 路由前缀 /api/v1/ign;权限码 ign:FUNC-xx(与 M5 PERM-FUNC-xx 一一对应)。
import { http, type PageResult } from './request'

export interface PortableRow {
  [key: string]: unknown
}

/** M1 DICT-* 的启用项：界面下拉与状态映射的唯一取值域来源 */
export interface DictItem {
  code: string
  label: string
}

/** 从 /api/v1/ign/dictionaries 的响应里取某个字典的项（该响应同时带 checkpoint_note 说明位） */
export function dictItems(all: Record<string, unknown> | null | undefined, code: string): DictItem[] {
  const raw = all?.[code]
  if (!Array.isArray(raw)) return []
  return raw.map((it) => ({ code: String((it as DictItem)?.code ?? ''), label: String((it as DictItem)?.label ?? '') }))
}

/* ---------- AGG-IGN-01 交付版本 ---------- */

export interface DeliveryVersion {
  id: number
  version_no: string
  name: string
  display_name: string | null
  tool_version: string
  orchestrator_version: string
  runtime_version: string
  etcd_version: string
  cni_plugin: string
  cni_version: string
  coredns_version: string
  tarball_url: string
  tarball_sha256: string | null
  cache_path: string | null
  image_manifest: string
  manifest?: PortableRow[]
  default_registry_ref: string | null
  version_status: string
  archived_at: string | null
  prepush_items?: PortableRow[]
  prepush_summary?: Record<string, unknown>
  created_at: string
}

/* ---------- AGG-IGN-02 镜像预推明细 ---------- */

export interface PrepushItem {
  id: number
  version_id: number
  version_no?: string
  version_name?: string
  repo: string
  tag: string
  digest: string | null
  size_bytes: number | null
  prepush_status: string
  fail_reason: string | null
  prepushed_at: string | null
  created_at: string
}

/* ---------- AGG-IGN-03 执行机 ---------- */

export interface Executor {
  id: number
  executor_no: string
  name: string
  display_name: string | null
  executor_type: string
  asset_ref: string | null
  host: string
  port: number
  capability_flags: string
  status: string
  concurrent_tasks_max: number
  current_tasks_count: number
  last_heartbeat_at: string | null
  last_heartbeat_payload: string | null
  bootstrapped_at: string | null
  tls_declined: boolean
  health?: string
  health_note?: string
  has_active_credential?: boolean
  bootstrap_credential?: PortableRow | null
  tls_bindings?: PortableRow[]
  running_tasks?: PortableRow[]
  created_at: string
}

/* ---------- AGG-IGN-05 通道证书绑定 ---------- */

export interface TlsBinding {
  id: number
  binding_no: string
  executor_id: number
  executor_no?: string
  executor_name?: string
  cert_role: string
  cert_ref: string | null
  common_name: string
  not_before: string
  not_after: string
  days_left?: number
  renewal_count: number
  last_renewed_at: string | null
  bind_status: string
}

/* ---------- AGG-IGN-06 执行任务 ---------- */

export interface ExecTask {
  id: number
  task_no: string
  executor_id: number
  executor_no?: string
  executor_name?: string
  action: string
  args: string
  env: string | null
  env_masked?: string
  working_dir: string | null
  cluster_ref: string
  step_code?: string | null
  task_status: string
  exit_code: number | null
  fail_reason: string | null
  verdict_source?: string | null
  source_task_no?: string | null
  source_step_index?: number | null
  timeout_seconds: number
  started_at: string | null
  ended_at: string | null
  duration_ms: number | null
  submitted_by: string
  reconciled_note: string | null
  status_source?: string
  source_note?: string
  dispatch_note?: string
  replayed?: boolean
  replay_note?: string
  log_cursor?: number
  log_line_count?: number
  logs?: TaskLogLine[]
  created_at: string
}

/* ---------- TaskLogLine 子实体(REP-11;日志行不独立编号，只有所属任务 + 行游标) ---------- */

export interface TaskLogLine {
  seq: number
  direction: string
  log_line: string
  masked: boolean
  logged_at: string | null
}

/** REP-11：游标右界与「缺失行数」同行呈现(INV-38 的界面落点) */
export interface TaskLogPage {
  list: TaskLogLine[]
  total: number
  page: number
  size: number
  log_cursor: number
  gap_lines: number
  gap_note?: string
  masked_count: number
}

/* ---------- 门面受理台账(FUNC-37 / REP-14) ---------- */

export interface FacadeTask {
  task_no: string
  source_task_no: string
  source_step_index: number | null
  action: string
  step_code: string | null
  cluster_ref: string
  task_status: string
  verdict_source: string | null
  exit_code: number | null
  fail_reason: string | null
  started_at: string | null
  ended_at: string | null
  duration_ms: number | null
  submitted_by: string
  created_at: string
  log_line_count: number
  log_cursor: number
}

/* ---------- 强制注销申请(FUNC-31~33) ---------- */

export interface ForceRemoveApply {
  id: number
  apply_no: string
  executor_id: number
  executor_no?: string
  executor_name?: string
  executor_status?: string
  reason: string
  impact_snapshot?: { running_tasks?: PortableRow[]; running_count?: number } | string | null
  status: string
  instance_id: number | null
  approve_comment: string | null
  approved_name: string | null
  approved_at: string | null
  removal_report: string | null
  created_at: string
}

/* ---------- 运行观测(FUNC-29/30;REP-13 九块 c1~c9) ---------- */

/** REP-13 分组计数块的统一形状 */
export interface StatBucket {
  code: string
  count: number
}

/** 观测类卡片：与台账事实视觉可分，各自带扫描时间(口径 7) */
export interface ObservedCard {
  scanned_at?: string
  note?: string
  [key: string]: unknown
}

export interface IgnStats {
  // c1 版本状态分布 + 兼容位
  version_total: number
  version_available: number
  version_status_dist?: StatBucket[]
  // c2 预推完成度
  prepush_status_dist?: StatBucket[]
  // c3 执行机治理分布 + c4 健康快照(观测类)
  executor_status_dist?: StatBucket[]
  executor_ready: number
  executor_unreachable: number
  health_snapshot?: ObservedCard & { ready?: number; unreachable?: number; timeout_seconds?: number }
  // c5 任务结论计数(四终态分列；LOST 不并入失败，INV-30)
  task_completed?: number
  task_completed_7d?: number
  task_failed?: number
  task_failed_7d?: number
  task_cancelled?: number
  task_cancelled_7d?: number
  task_lost?: number
  task_lost_7d?: number
  task_open?: number
  task_success_7d: number
  // c6 门面受理量与结论依据分布(INV-34 与人工量分列)
  facade_accept_count?: number
  facade_accept_7d?: number
  manual_submit_count?: number
  verdict_source_dist?: StatBucket[]
  // c7 执行位可用(观测类，R-15)
  local_exec_available?: boolean
  local_exec_observation?: ObservedCard & {
    available?: boolean
    reason?: string
    work_root?: string
    tool_bin?: string
  }
  // c8 拒绝审计计数(Z1 治理拒绝 / Z2 执行位不可用，与执行结论分列)
  refuse_governance_count?: number
  refuse_tool_unavailable_count?: number
  // c9 纠偏计数
  correct_count?: Record<string, number>
  reconcile_count: number
}

export interface IgnParam {
  config_key: string
  config_value: string
  description: string | null
  updated_at: string | null
}

/* ---------- AGG-IGN-07 参数面模板(FUNC-38~44;MU frmIgnTemplate) ---------- */

/** 字段行(REP-16 从表;整组替换语义 R-22,无单行 CUD)。后端同时回吐原值与脱敏值 */
export interface ParamTemplateItemRow {
  id: number
  template_id: number
  field_key: string
  field_value: string
  value_shape: string
  label?: string
  ref_id?: string
  axis_file?: string
  disk_key?: string
  field_value_masked?: string
  masked?: boolean
  display_note?: string
}

/** 被引用出处(R-25 两类:上游面标识声明 / 本域生效覆写;FUNC-44 拒绝时列出的清单) */
export interface ParamTemplateReference {
  cluster_ref: string
  source_type: string
  source_label: string
  param_template_ref?: string
  field_key?: string
  override_value?: string
  reason?: string
}

export interface IgnParamTemplate {
  id: number
  template_no: string
  template_name: string
  file_face: string
  template_status: string
  is_default: boolean
  description: string | null
  enabled_at: string | null
  created_at: string
  updated_at: string | null
  /** 视图形:软删标记(后端 rowView,非状态第四个取值) */
  deleted: boolean
  /** 默认徽标只在启用态出现(INV-43 界面侧投影) */
  default_badge: boolean
  referenced_cluster_count: number
  referenced_clusters: string[]
  /** FUNC-39 详情三段:字段行整组 + 被引用出处 */
  items?: ParamTemplateItemRow[]
  references?: ParamTemplateReference[]
}

/* ---------- AGG-IGN-08 集群交付配置(FUNC-45~49;MU frmIgnConfig,单集群单轴当前态) ---------- */

/** 单层取值格(paramLayerCell):未命中层 hit=false,note 即「为什么没命中」的原结论(让位/跳过/已回落),界面不自行推断 */
export interface ParamLayerCell {
  value: string
  hit: boolean
  note?: string
  /** 覆写层:IGN_OVERRIDE_STATE(EFFECTIVE / ROLLED_BACK) */
  status?: string
  /** 覆写层:事由(治理事实必须说为什么改) */
  reason?: string
  applied_task_no?: string
  applied_at?: string
  rolled_back_at?: string
  source_ref?: string
}

/** REP-17 一行:单字段四层轨迹 + 生效来源徽章 + 两列并列 + 追溯(后端同时回吐中文徽章标签,前端不二次翻译) */
export interface ClusterConfigFieldRow {
  field_key: string
  ref_id: string
  label: string
  file_face: string
  value_shape: string
  /** 第 4 层:平台缺省面值(地板) */
  platform_default: ParamLayerCell
  /** 第 3 层:参数面模板值 */
  param_template: ParamLayerCell
  /** 第 2 层:交付版本派生值 */
  version_derived: ParamLayerCell
  /** 第 1 层:集群覆写 */
  override: ParamLayerCell
  /** 「下次将生效」列(R-21:覆写只改这一列) */
  next_effective_value: string
  effective_source: string
  effective_source_label: string
  source_ref: string
  /** 「当前落盘」列——与上一列两个字段、不合并;取数失败时 on_disk_note 给「不可用 + 原因」 */
  on_disk_value: string
  on_disk_note: string
  applied_task_no: string | null
  applied_at: string | null
  applied_display: string
}

export interface ClusterConfigView {
  cluster_ref: string
  file_face: string
  requested_param_template_ref: string
  hit_param_template_ref: string
  /** 第 3 层整列级结论(未携带面标识 ⇒「本层跳过」,不回落到默认模板,R-20 分支 H) */
  param_template_notice: string
  requested_version_ref: string
  version_notice: string
  version_status: string
  /** R-18 三态(ABSENT / MANAGED / CONFLICT),冲突态标注由受理侧判定回吐(R-24) */
  inventory_state: string
  inventory_note: string
  /** 仅冲突态回吐:「已保存 · 未生效(清单归属冲突)」 */
  save_effective_note?: string
  fields: ClusterConfigFieldRow[]
}

/** FUNC-45 原文只读(REP-18):带行号整份文本;凭据形态命中行已由后端整行掩码,前端不二次解析 */
export interface ClusterRawView {
  cluster_ref: string
  file_face: string
  file_name: string
  line_count: number
  masked_line_count: number
  content_sha256: string
  lines: { no: number; text: string }[]
  read_origin: string
  notice: string
}

/** 覆写行(ign_cluster_config_override 落库形状;回落不删行,flag=1 保留可查曾改过什么) */
export interface ClusterOverrideRow {
  id: number
  cluster_ref: string
  file_face: string
  field_key: string
  override_value: string
  value_shape: string
  override_status: string
  reason: string
  overridden_by: string
  overridden_at: string
  applied_task_no: string | null
  applied_at: string | null
  rolled_back_at: string | null
}

export interface OverrideSaveResult {
  override: ClusterOverrideRow
  notice: string
  next_effective_value?: string
  effective_source?: string
  effective_source_label?: string
  on_disk_value?: string
  on_disk_note?: string
  save_effective_note?: string
}

export interface OverrideRollbackResult {
  override: ClusterOverrideRow
  notice: string
  next_effective_value?: string
  effective_source?: string
  effective_source_label?: string
}

/** dry-run 逐字段轨迹(命中层回吐;layers 为四层格,FUNC-50 与 FUNC-46 共用形状) */
export interface ValidateFieldTrace {
  field_key: string
  ref_id: string
  label: string
  file_face: string
  value_shape: string
  effective_value: string
  effective_source: string
  effective_source_label: string
  layers: { layer: string; layer_label: string; value: string; hit: boolean; source_ref?: string; note?: string; status?: string; reason?: string }[]
}

export interface ClusterConfigValidateResult {
  cluster_ref: string
  file_face: string
  passed: boolean
  stage: string
  reasons: string[]
  fields: ValidateFieldTrace[]
  param_template_ref: string
  param_template_notice: string
  version_ref: string
  version_notice: string
  inventory_state: string
  inventory_note: string
  would_reject_on_rewrite: boolean
  pending_overrides: { field_key: string; value: string }[]
  /** 「五不」声明(不写盘 / 不建任务 / 不占额度 / 不改覆写行 / 不进台账,R-23) */
  notice: string
}

/* ---------- API ---------- */

/** 参数面字段目录项（GET /api/meta/ign-param-face；后端唯一实现在面定义表，界面不再自存副本） */
export interface ParamFaceField {
  key: string
  label: string
  file_face: string
  value_shape: string
  ref_id: string
  axis_file: string
}

export const ignApi = {
  dictionaries() {
    return http.get<Record<string, unknown>>('/api/v1/ign/dictionaries')
  },
  /** 字段键合法域（REF-17~25）：只出键的形态与归属，取值一律由业务接口按四层给出（INV-08） */
  paramFaceCatalog() {
    return http.get<ParamFaceField[]>('/api/meta/ign-param-face')
  },

  /* 交付版本(FUNC-01~08) */
  listVersions(params: { page: number; size: number; status?: string; orchestrator_version?: string; cni_plugin?: string; keyword?: string }) {
    return http.get<PageResult<DeliveryVersion>>('/api/v1/ign/version', params)
  },
  createVersion(data: Partial<DeliveryVersion>) {
    return http.post<DeliveryVersion>('/api/v1/ign/version', data)
  },
  getVersion(id: number) {
    return http.get<DeliveryVersion>(`/api/v1/ign/version/${id}`)
  },
  updateVersion(id: number, data: Partial<DeliveryVersion>) {
    return http.put<DeliveryVersion>(`/api/v1/ign/version/${id}`, data)
  },
  downloadVersion(id: number) {
    return http.post<DeliveryVersion>(`/api/v1/ign/version/${id}/download`)
  },
  archiveVersion(id: number) {
    return http.post<DeliveryVersion>(`/api/v1/ign/version/${id}/archive`)
  },
  deleteVersion(id: number) {
    return http.del<PortableRow>(`/api/v1/ign/version/${id}`)
  },
  listPublicVersions(params: { page: number; size: number }) {
    return http.get<PageResult<DeliveryVersion>>('/api/v1/ign/version/public', params)
  },

  /* 镜像预推(FUNC-09~12) */
  startPrepush(id: number) {
    return http.post<DeliveryVersion>(`/api/v1/ign/version/${id}/prepush`)
  },
  retryPrepush(id: number) {
    return http.post<DeliveryVersion>(`/api/v1/ign/version/${id}/prepush/retry`)
  },
  overwriteManifest(id: number, image_manifest: string) {
    return http.put<DeliveryVersion>(`/api/v1/ign/version/${id}/manifest`, { image_manifest })
  },
  listPrepush(params: { page: number; size: number; version_id?: number | string; prepush_status?: string; version_no?: string }) {
    return http.get<PageResult<PrepushItem>>('/api/v1/ign/prepush', params)
  },

  /* 执行机(FUNC-13~19) */
  listExecutors(params: { page: number; size: number; status?: string; executor_type?: string; capability?: string; keyword?: string }) {
    return http.get<PageResult<Executor>>('/api/v1/ign/executor', params)
  },
  createExecutor(data: { name: string; display_name?: string; host: string; port: number; asset_ref: string; capability_flags?: string }) {
    return http.post<Executor & { bootstrap_token?: string; bootstrap_token_note?: string }>('/api/v1/ign/executor', data)
  },
  getExecutor(id: number) {
    return http.get<Executor>(`/api/v1/ign/executor/${id}`)
  },
  regenerateToken(id: number) {
    return http.post<Executor & { bootstrap_token?: string; bootstrap_token_note?: string }>(`/api/v1/ign/executor/${id}/token`)
  },
  retireExecutor(id: number) {
    return http.del<Executor>(`/api/v1/ign/executor/${id}`)
  },
  updateCapabilities(id: number, capability_flags: string) {
    return http.put<Executor>(`/api/v1/ign/executor/${id}/capabilities`, { capability_flags })
  },
  listPublicExecutors(params: { page: number; size: number }) {
    return http.get<PageResult<PortableRow>>('/api/v1/ign/executor/public', params)
  },

  /* 通道证书(FUNC-20~22) */
  listTls(params: { page: number; size: number; executor_id?: number | string; cert_role?: string; bind_status?: string; expiring?: string; keyword?: string }) {
    return http.get<PageResult<TlsBinding>>('/api/v1/ign/tls', params)
  },
  renewTls(id: number) {
    return http.post<TlsBinding>(`/api/v1/ign/tls/${id}/renew`)
  },
  declineTls(executorId: number) {
    return http.post<Executor>(`/api/v1/ign/executor/${executorId}/tls-decline`)
  },

  /* 执行任务(FUNC-23~28) */
  submitTask(data: { executor_id: number; action: string; step_code?: string; args?: string; env?: string; cluster_ref: string; timeout_seconds?: number; request_token?: string }) {
    return http.post<ExecTask>('/api/v1/ign/task', data)
  },
  cancelTask(taskNo: string) {
    return http.post<ExecTask>(`/api/v1/ign/task/${taskNo}/cancel`)
  },
  listTasks(params: { page: number; size: number; executor_id?: number | string; action?: string; cluster_ref?: string; task_status?: string; keyword?: string }) {
    return http.get<PageResult<ExecTask>>('/api/v1/ign/task', params)
  },
  getTask(taskNo: string) {
    return http.get<ExecTask>(`/api/v1/ign/task/${taskNo}`)
  },
  /**
   * FUNC-27 / REP-11 任务日志明细：按行游标升序、增量续传(since_seq)、分页由服务层按模型收口 500/2000。
   * 不提供导出(口径 5)。
   */
  taskLogs(taskNo: string, params?: { since_seq?: number; page?: number; size?: number }) {
    return http.get<TaskLogPage>(`/api/v1/ign/task/${taskNo}/logs`, params)
  },
  listReconcile(params: { page: number; size: number; event_type?: string; keyword?: string }) {
    return http.get<PageResult<PortableRow>>('/api/v1/ign/reconcile', params)
  },

  /* 门面受理台账(FUNC-37 / REP-14;固定过滤「来源任务标识非空」) */
  listFacadeTasks(params: {
    page: number
    size: number
    source_task_no?: string
    source_step_index?: number | string
    cluster_ref?: string
    step_code?: string
    verdict_source?: string
    task_status?: string
  }) {
    return http.get<PageResult<FacadeTask>>('/api/v1/ign/facade/task', params)
  },

  /* 强制注销(FUNC-31~33) */
  saveForceRemove(executorId: number, reason: string, mode: 'draft' | 'submit') {
    return http.post<ForceRemoveApply>(`/api/v1/ign/executor/${executorId}/force-remove`, { reason, mode })
  },
  submitForceRemove(id: number) {
    return http.post<ForceRemoveApply>(`/api/v1/ign/force-remove/${id}/submit`)
  },
  withdrawForceRemove(id: number) {
    return http.post<ForceRemoveApply>(`/api/v1/ign/force-remove/${id}/withdraw`)
  },
  listForceRemove(params: { page: number; size: number; status?: string; executor_id?: number | string }) {
    return http.get<PageResult<ForceRemoveApply>>('/api/v1/ign/force-remove', params)
  },

  /* 运行观测(FUNC-29/30) */
  stats() {
    return http.get<IgnStats>('/api/v1/ign/stats')
  },
  getParams() {
    return http.get<IgnParam[]>('/api/v1/ign/params')
  },
  saveParams(items: { config_key: string; config_value: string }[]) {
    return http.put<PortableRow>('/api/v1/ign/params', { items })
  },

  /* 参数面模板(FUNC-38~44;MU frmIgnTemplate。启停/设默认/软删除均须事由——治理动作留痕) */
  listParamTemplates(params: {
    page: number
    size: number
    file_face?: string
    status?: string
    show_disabled?: string
    keyword?: string
  }) {
    return http.get<PageResult<IgnParamTemplate>>('/api/v1/ign/param-template', params)
  },
  getParamTemplate(id: number) {
    return http.get<IgnParamTemplate>(`/api/v1/ign/param-template/${id}`)
  },
  createParamTemplate(data: { template_name: string; file_face: string; description?: string }) {
    return http.post<IgnParamTemplate>('/api/v1/ign/param-template', data)
  },
  saveParamTemplateItems(id: number, reason: string, items: { field_key: string; field_value: string }[]) {
    return http.put<IgnParamTemplate>(`/api/v1/ign/param-template/${id}/items`, { reason, items })
  },
  enableParamTemplate(id: number, reason: string) {
    return http.post<IgnParamTemplate>(`/api/v1/ign/param-template/${id}/enable`, { reason })
  },
  disableParamTemplate(id: number, reason: string) {
    return http.post<IgnParamTemplate>(`/api/v1/ign/param-template/${id}/disable`, { reason })
  },
  setParamTemplateDefault(id: number, reason: string) {
    return http.post<IgnParamTemplate>(`/api/v1/ign/param-template/${id}/default`, { reason })
  },
  deleteParamTemplate(id: number, reason: string) {
    return http.del<IgnParamTemplate>(`/api/v1/ign/param-template/${id}`, { reason })
  },

  /* 集群交付配置(FUNC-45~49;MU frmIgnConfig。覆写/预检/回落的请求体口径 = service 层字段:
     cluster_ref / file_face / field_key / value / reason;预检另有 overrides 候选数组与取值上下文两引用) */
  getClusterConfig(params: { cluster_ref: string; file_face: string; param_template_ref?: string; version_ref?: string }) {
    return http.get<ClusterConfigView>('/api/v1/ign/cluster/config', params)
  },
  getClusterRaw(params: { cluster_ref: string; file_face: string }) {
    return http.get<ClusterRawView>('/api/v1/ign/cluster/raw', params)
  },
  saveClusterOverride(data: { cluster_ref: string; file_face: string; field_key: string; value: string; reason: string }) {
    return http.put<OverrideSaveResult>('/api/v1/ign/cluster/config/override', data)
  },
  validateClusterConfig(data: {
    cluster_ref: string
    file_face?: string
    param_template_ref?: string
    version_ref?: string
    overrides?: { field_key: string; value: string }[]
  }) {
    return http.post<ClusterConfigValidateResult>('/api/v1/ign/cluster/config/validate', data)
  },
  rollbackClusterOverride(data: { cluster_ref: string; file_face: string; field_key: string }) {
    return http.post<OverrideRollbackResult>('/api/v1/ign/cluster/config/rollback', data)
  },
}
