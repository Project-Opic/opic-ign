// 视觉体系移植自 gopherforge (https://github.com/SuperiorChuo/gopherforge) MIT License,适配 OPIC 技术底座。
// 玻璃状态胶囊:呼吸点 + 文字,取代列表页里千篇一律的 Tag。
// tone 决定点和文字的颜色语义;on=true 时点会呼吸(活性)。

export type StatusTone = 'success' | 'muted' | 'danger' | 'info' | 'warning'

interface StatusPillProps {
  tone: StatusTone
  label: string
  /** 呼吸动画,默认 tone==='success' 时开 */
  pulse?: boolean
}

export default function StatusPill({ tone, label, pulse }: StatusPillProps) {
  const active = pulse ?? tone === 'success'
  return (
    <span className={`status-pill status-pill-${tone}`}>
      <span className={`status-pill-dot ${active ? 'status-pill-dot-pulse' : ''}`} />
      {label}
    </span>
  )
}

/** 最常见的 启用(1)/禁用(0) 二态 */
export function EnableStatusPill({ value }: { value: number }) {
  return value === 1 ? (
    <StatusPill tone="success" label="启用" />
  ) : (
    <StatusPill tone="muted" label="禁用" />
  )
}

/** 客户状态 → 胶囊(草稿=muted/待客户经理审批=info/待部门总经理审批=info/已通过=success/已驳回=danger) */
export const CUSTOMER_STATUS_TONE: Record<string, { tone: StatusTone; label: string }> = {
  草稿: { tone: 'muted', label: '草稿' },
  待客户经理审批: { tone: 'info', label: '待客户经理审批' },
  待部门总经理审批: { tone: 'info', label: '待部门总经理审批' },
  已通过: { tone: 'success', label: '已通过' },
  已驳回: { tone: 'danger', label: '已驳回' },
}

export function CustomerStatusPill({ status }: { status: string }) {
  const meta = CUSTOMER_STATUS_TONE[status]
  if (!meta) return <StatusPill tone="muted" label={status} />
  return <StatusPill tone={meta.tone} label={meta.label} />
}

/** IGN 版本状态 → 胶囊(DRAFT=muted/DOWNLOADING=info/READY=info/PREPUSHING=warning/PREPUSHED=success/PARTIALLY=warning/ARCHIVED=muted 终态/FAILED=danger) */
export const IGN_VERSION_STATUS_TONE: Record<string, { tone: StatusTone; label: string }> = {
  DRAFT: { tone: 'muted', label: '草稿' },
  DOWNLOADING: { tone: 'info', label: '下载中' },
  READY: { tone: 'info', label: '就绪' },
  PREPUSHING: { tone: 'warning', label: '预推中' },
  PREPUSHED: { tone: 'success', label: '已预推' },
  PARTIALLY_PREPUSHED: { tone: 'warning', label: '部分预推' },
  ARCHIVED: { tone: 'muted', label: '已归档' },
  FAILED: { tone: 'danger', label: '失败' },
}

export function IgnVersionStatusPill({ status }: { status: string }) {
  const meta = IGN_VERSION_STATUS_TONE[status]
  if (!meta) return <StatusPill tone="muted" label={status} />
  return <StatusPill tone={meta.tone} label={meta.label} />
}

/** IGN 执行机治理状态 → 胶囊(ENROLLING=info/READY=success/LOCKED=danger/DEGRADED=warning/RENEWAL_PENDING=warning/REMOVED=muted 终态) */
export const IGN_EXECUTOR_STATUS_TONE: Record<string, { tone: StatusTone; label: string }> = {
  ENROLLING: { tone: 'info', label: '待引导' },
  READY: { tone: 'success', label: '就绪' },
  LOCKED: { tone: 'danger', label: '已锁定' },
  DEGRADED: { tone: 'warning', label: '已降级' },
  RENEWAL_PENDING: { tone: 'warning', label: '待续期' },
  REMOVED: { tone: 'muted', label: '已注销' },
}

export function IgnExecutorStatusPill({ status }: { status: string }) {
  const meta = IGN_EXECUTOR_STATUS_TONE[status]
  if (!meta) return <StatusPill tone="muted" label={status} />
  return <StatusPill tone={meta.tone} label={meta.label} />
}

/** IGN 预推状态 → 胶囊(PENDING=muted/SUCCESS=success/FAILED=danger/SKIPPED=muted) */
export const IGN_PREPUSH_STATUS_TONE: Record<string, { tone: StatusTone; label: string }> = {
  PENDING: { tone: 'muted', label: '待预推' },
  SUCCESS: { tone: 'success', label: '成功' },
  FAILED: { tone: 'danger', label: '失败' },
  SKIPPED: { tone: 'muted', label: '已跳过' },
}

export function IgnPrepushStatusPill({ status }: { status: string }) {
  const meta = IGN_PREPUSH_STATUS_TONE[status]
  if (!meta) return <StatusPill tone="muted" label={status} />
  return <StatusPill tone={meta.tone} label={meta.label} />
}

/** IGN 证书绑定状态 → 胶囊(PENDING=info/ACTIVE=success/RENEWING=warning/REVOKED=muted 终态/FAILED=danger) */
export const IGN_TLS_STATUS_TONE: Record<string, { tone: StatusTone; label: string }> = {
  PENDING: { tone: 'info', label: '待生效' },
  ACTIVE: { tone: 'success', label: '已生效' },
  RENEWING: { tone: 'warning', label: '续期中' },
  REVOKED: { tone: 'muted', label: '已吊销' },
  FAILED: { tone: 'danger', label: '失败' },
}

export function IgnTlsStatusPill({ status }: { status: string }) {
  const meta = IGN_TLS_STATUS_TONE[status]
  if (!meta) return <StatusPill tone="muted" label={status} />
  return <StatusPill tone={meta.tone} label={meta.label} />
}

/** IGN 执行任务状态 → 胶囊(RUNNING=info/CANCELLING=warning/COMPLETED=success/FAILED=danger/CANCELLED=muted) */
export const IGN_TASK_STATUS_TONE: Record<string, { tone: StatusTone; label: string }> = {
  RUNNING: { tone: 'info', label: '执行中' },
  CANCELLING: { tone: 'warning', label: '取消中' },
  COMPLETED: { tone: 'success', label: '完成' },
  FAILED: { tone: 'danger', label: '失败' },
  CANCELLED: { tone: 'muted', label: '已取消' },
}

export function IgnTaskStatusPill({ status }: { status: string }) {
  const meta = IGN_TASK_STATUS_TONE[status]
  if (!meta) return <StatusPill tone="muted" label={status} />
  return <StatusPill tone={meta.tone} label={meta.label} />
}

/** IGN 强制注销申请单状态 → 胶囊(草稿=muted/待平台运维审批=info/已通过=success/已驳回=danger/已撤回=muted/已注销=success) */
export const IGN_APPLY_STATUS_TONE: Record<string, { tone: StatusTone; label: string }> = {
  草稿: { tone: 'muted', label: '草稿' },
  待平台运维审批: { tone: 'info', label: '待平台运维审批' },
  已通过: { tone: 'success', label: '已通过' },
  已驳回: { tone: 'danger', label: '已驳回' },
  已撤回: { tone: 'muted', label: '已撤回' },
  已注销: { tone: 'success', label: '已注销' },
}

export function IgnApplyStatusPill({ status }: { status: string }) {
  const meta = IGN_APPLY_STATUS_TONE[status]
  if (!meta) return <StatusPill tone="muted" label={status} />
  return <StatusPill tone={meta.tone} label={meta.label} />
}

/** 流程实例状态 → 胶囊(RUNNING=info/APPROVED=success/REJECTED=danger/TERMINATED=muted) */
export const INSTANCE_TONE: Record<string, { tone: StatusTone; label: string }> = {
  RUNNING: { tone: 'info', label: '运行中' },
  APPROVED: { tone: 'success', label: '已通过' },
  REJECTED: { tone: 'danger', label: '已驳回' },
  TERMINATED: { tone: 'muted', label: '已终止' },
}

export function InstanceStatusPill({ status }: { status: string }) {
  const meta = INSTANCE_TONE[status]
  if (!meta) return <StatusPill tone="muted" label={status} />
  return <StatusPill tone={meta.tone} label={meta.label} />
}

/** 任务状态 → 胶囊(TODO=info/DONE=success/CANCEL=muted) */
export const TASK_TONE: Record<string, { tone: StatusTone; label: string }> = {
  TODO: { tone: 'info', label: '待办理' },
  DONE: { tone: 'success', label: '已完成' },
  CANCEL: { tone: 'muted', label: '已取消' },
}

export function TaskStatusPill({ status }: { status: string }) {
  const meta = TASK_TONE[status]
  if (!meta) return <StatusPill tone="muted" label={status} />
  return <StatusPill tone={meta.tone} label={meta.label} />
}
