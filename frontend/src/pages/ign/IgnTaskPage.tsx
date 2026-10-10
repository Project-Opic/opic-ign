// MU frmIgnTask 交付管理 · 任务提交与台账(AGG-IGN-06;MASTER_DETAIL_FORM)
// 上半提交表单(FUNC-23)/ 下半台账(FUNC-25,REP-09)/ 详情与只读日志子表(FUNC-26/27,REP-11)。
// v1.1 增补：步骤编码条件必填(INV-36)、来源两列只读反查(FUNC-37/INV-34)。
// v1.5 侧栏收口：本屏仍是一级叶，同时是集群交付台的「任务」页签载体（9.11.2）——
// 带 clusterRef 入参即台内挂点：关联集群由上下文给定（提交表单预填只读、台账不出集群筛选器），
// 屏本体与查询面不改一份，只是同一报表的第二个呈现入口。
//
// 动作下拉只列「交付动作层」——白名单枚举 ≠ 可提交集合(需求 7.3 三层可达面)：
// 内部前置层 ezctl.new 由 B-AUTO-15 使用，平台运维层 scripts.* 由 B-AUTO-02/06~08 发起，
// 二者都不对人工提交开放(后端 SubmitTask 同口径拒绝)。
// 规则:INV-28 白名单;INV-31 防重放;INV-33 必须归属集群;R-05 容量原子;R-12 治理拒绝;R-15 执行位不可用给原因。
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Alert, Button, Card, Col, Descriptions, Drawer, Form, Input, Popconfirm, Row, Select, Space, Table, Tag, message } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { FileSearchOutlined, ProfileOutlined, ReloadOutlined, SearchOutlined, SendOutlined, StopOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import { ignApi, dictItems, type DictItem, type ExecTask, type Executor, type IgnStats, type TaskLogLine } from '../../api/ign'
import TableToolbar from '../../admin/components/TableToolbar'
import { IgnTaskStatusPill } from '../../admin/components/StatusPill'
import { usePermission } from '../../hooks/usePermission'

// 交付动作层之外的动作码（7.3：可达面由后端 ignActionPlane 判定，界面据同一口径不出选项）
const NON_DELIVERY_ACTIONS = ['ezctl.new', 'scripts.image_prepush', 'scripts.health_check', 'scripts.tls_renew']

// 兜底枚举：字典未回吐 IGN_ACTION 时仍可提交（与 M1 DICT-IGN_ACTION 的交付动作层一致）
const FALLBACK_DELIVERY_ACTIONS = [
  'ezctl.setup',
  'ezctl.start',
  'ezctl.stop',
  'ezctl.add-node',
  'ezctl.del-node',
  'ezctl.upgrade',
  'ezctl.backup',
  'ezctl.restore',
  'ezctl.destroy',
  'ezctl.ca-renew',
  'ezctl.kcfg-adm',
]

const FALLBACK_TASK_STATUS = ['RUNNING', 'CANCELLING', 'COMPLETED', 'FAILED', 'CANCELLED', 'LOST']

export default function IgnTaskPage({
  clusterRef = '',
  onChanged,
}: {
  clusterRef?: string
  /** 台内挂点：派发 / 撤销等状态变更后回调（9.11.7 ①③：徽章条重取、配置页签落盘列刷新） */
  onChanged?: () => void
}) {
  const hasPerm = usePermission()
  const nav = useNavigate()
  const [filterForm] = Form.useForm<{ action?: string; task_status?: string; cluster_ref?: string; keyword?: string }>()
  const [form] = Form.useForm<Record<string, unknown>>()
  const action = Form.useWatch('action', form)
  const [executors, setExecutors] = useState<Executor[]>([])
  const [dict, setDict] = useState<Record<string, unknown>>({})
  const [stats, setStats] = useState<IgnStats | null>(null)
  const [data, setData] = useState<ExecTask[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [size, setSize] = useState(10)
  const [loading, setLoading] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [detail, setDetail] = useState<ExecTask | null>(null)
  const [detailOpen, setDetailOpen] = useState(false)
  const [logs, setLogs] = useState<TaskLogLine[]>([])
  const [logInfo, setLogInfo] = useState<{ log_cursor?: number; gap_lines?: number; gap_note?: string; masked_count?: number; total?: number }>({})
  const [logLoading, setLogLoading] = useState(false)

  const deliveryActions = useMemo(() => {
    const fromDict = dictItems(dict, 'IGN_ACTION')
      .map((d) => d.code)
      .filter((c) => c && !NON_DELIVERY_ACTIONS.includes(c))
    return (fromDict.length > 0 ? fromDict : FALLBACK_DELIVERY_ACTIONS).map((c) => ({ value: c, label: c }))
  }, [dict])

  const stepOptions = useMemo(
    () => dictItems(dict, 'IGN_INSTALL_STEP').map((d) => ({ value: d.code, label: `${d.code}｜${d.label}` })),
    [dict],
  )

  const statusOptions = useMemo(() => {
    const fromDict = dictItems(dict, 'IGN_TASK_STATUS')
      .map((d) => d.code)
      .filter(Boolean)
    return (fromDict.length > 0 ? fromDict : FALLBACK_TASK_STATUS).map((c) => ({ value: c, label: c }))
  }, [dict])

  const verdictLabel = useCallback(
    (code?: string | null) => (code ? dictItems(dict, 'IGN_VERDICT_SOURCE').find((d) => d.code === code)?.label || code : ''),
    [dict],
  )

  const loadExecutors = useCallback(async () => {
    try {
      const res = await ignApi.listExecutors({ page: 1, size: 200, status: 'READY' })
      // el01：只列就绪项，且把「未超载」的排在前面；LOCAL（本机执行位）置顶
      const list = (res.list || []).slice().sort((a, b) => {
        const local = (e: Executor) => (e.executor_type === 'LOCAL' ? 0 : 1)
        const free = (e: Executor) => (e.current_tasks_count < e.concurrent_tasks_max ? 0 : 1)
        return local(a) - local(b) || free(a) - free(b)
      })
      setExecutors(list)
    } catch {
      // 拦截器已提示
    }
  }, [])

  const load = useCallback(
    async (p = page, s = size) => {
      setLoading(true)
      try {
        const v = filterForm.getFieldsValue()
        // 9.11.2：集群台挂点下 clusterRef 由上下文注入为只读过滤条件，不是屏上那个可改的筛选器
        const res = await ignApi.listTasks({ page: p, size: s, ...v, ...(clusterRef ? { cluster_ref: clusterRef } : {}) })
        setData(res.list || [])
        setTotal(res.total || 0)
      } catch {
        // 拦截器已提示
      } finally {
        setLoading(false)
      }
    },
    [page, size, filterForm, clusterRef],
  )

  useEffect(() => {
    loadExecutors()
    ignApi
      .dictionaries()
      .then(setDict)
      .catch(() => {
        // 拦截器已提示；字典缺失时上面有兜底枚举
      })
    if (hasPerm('ign:FUNC-29')) {
      // R-15：执行位不可用要在原因位给出原因，而不是静默置灰
      ignApi
        .stats()
        .then(setStats)
        .catch(() => {})
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [page, size])

  // 动作离开展装族 ⇒ 步骤编码置空（INV-36：仅 ezctl.setup 携带，其余动作该列必须为空）
  useEffect(() => {
    if (action !== 'ezctl.setup' && form.getFieldValue('step_code')) {
      form.setFieldValue('step_code', undefined)
    }
  }, [action, form])

  // 台内挂点：关联集群由上下文给定（只读、不可改），提交表单不再要求人再填一遍
  useEffect(() => {
    if (clusterRef) form.setFieldValue('cluster_ref', clusterRef)
  }, [clusterRef, form])

  const loadLogs = useCallback(async (taskNo: string, sinceSeq?: number) => {
    setLogLoading(true)
    try {
      const res = await ignApi.taskLogs(taskNo, sinceSeq ? { since_seq: sinceSeq } : undefined)
      setLogs((prev) => (sinceSeq ? [...prev, ...(res.list || [])] : res.list || []))
      setLogInfo({
        log_cursor: res.log_cursor,
        gap_lines: res.gap_lines,
        gap_note: res.gap_note,
        masked_count: res.masked_count,
        total: res.total,
      })
    } catch {
      // 拦截器已提示
    } finally {
      setLogLoading(false)
    }
  }, [])

  const submitTask = async () => {
    const v = await form.validateFields()
    setSubmitting(true)
    try {
      const res = await ignApi.submitTask({
        executor_id: Number(v.executor_id),
        action: String(v.action),
        step_code: v.step_code ? String(v.step_code) : undefined,
        args: v.args ? String(v.args) : '',
        env: v.env ? String(v.env) : undefined,
        cluster_ref: String(v.cluster_ref),
        timeout_seconds: v.timeout_seconds ? Number(v.timeout_seconds) : undefined,
        request_token: v.request_token ? String(v.request_token) : undefined,
      })
      message.success(String(res?.dispatch_note || '任务已提交（R-05 计数已递增）'))
      form.resetFields()
      if (clusterRef) form.setFieldValue('cluster_ref', clusterRef)
      load()
      // 9.11.7 ③：台内派发成功 ⇒ 徽章条"最近一次交付 / 在跑任务数"重取
      onChanged?.()
    } catch {
      // 拦截器已提示
    } finally {
      setSubmitting(false)
    }
  }

  const cancelTask = async (r: ExecTask) => {
    try {
      await ignApi.cancelTask(r.task_no)
      message.success('撤销请求已登记（在途任务由执行事实收口；无在途协程则就地收口为 CANCELLED）')
      load()
      onChanged?.()
    } catch {
      // 拦截器已提示
    }
  }

  const openDetail = async (r: ExecTask) => {
    try {
      const d = await ignApi.getTask(r.task_no)
      setDetail(d)
      setDetailOpen(true)
      setLogs([])
      setLogInfo({})
      loadLogs(r.task_no)
    } catch {
      // 拦截器已提示
    }
  }

  const columns: ColumnsType<ExecTask> = [
    { title: '任务编号', dataIndex: 'task_no', key: 'task_no', width: 165 },
    { title: '动作', dataIndex: 'action', key: 'action', width: 165 },
    {
      title: '步骤编码',
      dataIndex: 'step_code',
      key: 'step_code',
      width: 110,
      render: (v: string | null) => (v ? `${v}` : '-'),
    },
    { title: '执行机', dataIndex: 'executor_no', key: 'executor_no', width: 165 },
    { title: '关联集群', dataIndex: 'cluster_ref', key: 'cluster_ref', width: 160, ellipsis: true },
    {
      // el09：状态 + 结论依据同现；LOST 退出码位为空（INV-37）
      title: '状态 / 结论依据',
      dataIndex: 'task_status',
      key: 'task_status',
      width: 190,
      render: (v: string, r) => (
        <Space size={4}>
          <IgnTaskStatusPill status={v} />
          {r.verdict_source && <Tag bordered={false}>{verdictLabel(r.verdict_source)}</Tag>}
        </Space>
      ),
    },
    {
      // el11：只读列、无输入位；非空即「由门面受理」，点击进入门面受理台账反查（FUNC-37/INV-34）
      title: '来源（只读反查）',
      key: 'source',
      width: 200,
      render: (_, r) =>
        r.source_task_no ? (
          <Button
            type="link"
            size="small"
            style={{ padding: 0, height: 'auto' }}
            // el13：反查落点是 frmIgnLog 的「门面受理台账」子签。v1.5 起本屏两处挂点（横看叶 / 台内「任务」签），
            // 跳转保持在当前挂点上：台内 ⇒ 台内日志签（clusterRef 上下文不丢），横看 ⇒ 交付任务叶的日志签。
            onClick={() =>
              nav(
                `${clusterRef ? `/ign/console/${encodeURIComponent(clusterRef)}` : '/ign/task'}?tab=log&log_tab=facade&source_task_no=${encodeURIComponent(
                  String(r.source_task_no),
                )}`,
              )
            }
          >
            门面受理 · {r.source_task_no} / 第 {r.source_step_index ?? '-'} 步
          </Button>
        ) : (
          '人工提交'
        ),
    },
    {
      title: '退出码 / 失败原因',
      key: 'result',
      width: 150,
      render: (_, r) =>
        r.task_status === 'LOST' ? (
          '退出码无（痕迹不可存活，INV-37）'
        ) : r.fail_reason ? (
          r.fail_reason
        ) : r.exit_code !== null && r.exit_code !== undefined ? (
          `exit ${r.exit_code}`
        ) : (
          '-'
        ),
    },
    {
      title: '起止时间',
      key: 'time',
      width: 260,
      render: (_, r) => `${r.started_at || '-'} ~ ${r.ended_at || (r.task_status === 'RUNNING' ? '…' : '-')}`,
    },
    { title: '提交人', dataIndex: 'submitted_by', key: 'submitted_by', width: 110 },
    {
      title: '操作',
      key: 'op',
      width: 150,
      fixed: 'right',
      render: (_, r) => (
        <Space size={4}>
          <Button size="small" icon={<ProfileOutlined />} onClick={() => openDetail(r)}>
            详情
          </Button>
          {hasPerm('ign:FUNC-24') && (r.task_status === 'RUNNING' || r.task_status === 'CANCELLING') && (
            <Popconfirm title="撤销该任务？" description="人为中止终态为 CANCELLED（与 FAILED 分列,INV-30）" onConfirm={() => cancelTask(r)}>
              <Button size="small" danger icon={<StopOutlined />}>
                撤销
              </Button>
            </Popconfirm>
          )}
        </Space>
      ),
    },
  ]

  const logColumns: ColumnsType<TaskLogLine> = [
    { title: '行游标', dataIndex: 'seq', key: 'seq', width: 90 },
    { title: '时间', dataIndex: 'logged_at', key: 'logged_at', width: 170 },
    { title: '流向', dataIndex: 'direction', key: 'direction', width: 100 },
    {
      title: '日志行',
      dataIndex: 'log_line',
      key: 'log_line',
      render: (v: string) => <span style={{ fontSize: 12, whiteSpace: 'pre-wrap' }}>{v}</span>,
    },
    {
      // R-16：命中掩码规则的行标出「日志被动过」，而非静默替换
      title: '已脱敏',
      dataIndex: 'masked',
      key: 'masked',
      width: 90,
      render: (v: boolean) => (v ? <Tag color="orange">已掩码</Tag> : '-'),
    },
  ]

  return (
    <Card>
      {/* 上半:提交表单(MASTER 区) */}
      <Card size="small" title="提交执行任务" style={{ marginBottom: 16 }}>
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 12 }}
          message="动作必须命中 IGN_ACTION 白名单（INV-28）且属交付动作层（7.3：内部前置与平台运维动作不对界面开放）;参数不得含 shell 元字符（R-12）;任务必须归属关联集群（INV-33）;超上限即容量拒绝（R-05）"
        />
        {stats && stats.local_exec_available === false && (
          <Alert
            type="warning"
            showIcon
            style={{ marginBottom: 12 }}
            message="本机执行位不可用"
            description={`命令面挂载缺失或不可执行：${stats.local_exec_observation?.reason || '未探测到原因'}（R-15 降级口径；派发将即刻拒绝，不是静默置灰）`}
          />
        )}
        <Form form={form} layout="vertical">
          <Row gutter={16}>
            <Col span={8}>
              <Form.Item name="executor_id" label="执行机（就绪;本机执行位置顶）" rules={[{ required: true, message: '必选' }]}>
                <Select
                  showSearch
                  optionFilterProp="label"
                  placeholder="选择执行机"
                  options={executors.map((e) => ({
                    value: e.id,
                    label: `${e.executor_no}（${e.name} · ${e.executor_type === 'LOCAL' ? '本机执行位' : '远端代理'} · ${e.current_tasks_count}/${e.concurrent_tasks_max}）`,
                  }))}
                />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="action" label="执行动作（交付动作层）" rules={[{ required: true, message: '必选（INV-28 白名单）' }]}>
                <Select showSearch placeholder="选择动作" options={deliveryActions} />
              </Form.Item>
            </Col>
            {/* el10：条件必填——仅 ezctl.setup 可见且必填，其余动作隐藏并置空（INV-36） */}
            {action === 'ezctl.setup' && (
              <Col span={8}>
                <Form.Item name="step_code" label="安装步骤编码（01~07）" rules={[{ required: true, message: 'ezctl.setup 必须携带步骤编码（INV-36）' }]}>
                  <Select placeholder="选择步骤" options={stepOptions} />
                </Form.Item>
              </Col>
            )}
            <Col span={8}>
              <Form.Item name="cluster_ref" label="关联集群（跨域引用只存标识）" rules={[{ required: true, message: '必填（INV-33）' }]}>
                {/* 台内挂点由上下文给定，人不必也不该再填一遍（9.11.3：宿主屏只注入 clusterRef，不新增写口） */}
                <Input placeholder="CLUSTER-xxx / 集群标识" readOnly={!!clusterRef} />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="request_token" label="请求令牌（防重放,INV-31）" rules={[{ required: true, message: '必填（人工路径以「执行机 + 令牌摘要」唯一）' }]}>
                <Input placeholder="一次性令牌，窗口内重复提交返回原任务" />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="timeout_seconds" label="超时秒数（空 = 平台缺省,REF-02）">
                <Input type="number" placeholder="7200" />
              </Form.Item>
            </Col>
            <Col span={24}>
              <Form.Item name="args" label="动作参数（独立 argv 无 shell;人工路径保留输入位,门面路径无此位）">
                <Input placeholder="--cluster mycluster --node node1" />
              </Form.Item>
            </Col>
            <Col span={24}>
              <Form.Item name="env" label="环境变量（每行 KEY=VALUE;值脱敏展示）">
                <Input.TextArea rows={2} placeholder={'KUBECONFIG=/etc/kube/conf\nIMAGE_MIRROR=harbor.local'} />
              </Form.Item>
            </Col>
            {/* §1.6.1：命令面挂载路径与工作根目录只以部署配置值存在，界面只读、不可维护（改它走部署） */}
            <Col span={24}>
              <Space size={8} style={{ fontSize: 12, color: '#888' }}>
                <span>执行位命令面（部署配置，界面只读）：</span>
                <Tag bordered={false}>{stats?.local_exec_observation?.tool_bin || '未探测'}</Tag>
                <span>工作根目录：</span>
                <Tag bordered={false}>{stats?.local_exec_observation?.work_root || '未探测'}</Tag>
                {stats?.local_exec_observation?.scanned_at && <span>扫描时间 {String(stats.local_exec_observation.scanned_at)}</span>}
              </Space>
            </Col>
          </Row>
          {hasPerm('ign:FUNC-23') && (
            <Button type="primary" icon={<SendOutlined />} loading={submitting} onClick={submitTask}>
              提交
            </Button>
          )}
        </Form>
      </Card>

      {/* 下半:台账(DETAIL 区,FUNC-25 / REP-09) */}
      <TableToolbar
        title="任务台账"
        total={total}
        description="执行任务台账与撤销（AGG-IGN-06;状态来源:执行事实回填 R-09;LOST 为独立终态 INV-37）"
        extra={
          <Button icon={<ReloadOutlined />} loading={loading} onClick={() => load()}>
            刷新
          </Button>
        }
      />
      <Form form={filterForm} layout="inline" style={{ marginBottom: 12 }}>
        <Form.Item name="action" label="动作">
          <Select allowClear showSearch style={{ width: 190 }} options={deliveryActions} />
        </Form.Item>
        <Form.Item name="task_status" label="状态">
          <Select allowClear style={{ width: 150 }} options={statusOptions} />
        </Form.Item>
        {/* 台内挂点的集群由上下文给定，不再出一份同义筛选器（9.11.1：挂点只换过滤上下文） */}
        {!clusterRef && (
          <Form.Item name="cluster_ref" label="集群">
            <Input allowClear placeholder="集群标识" onPressEnter={() => load()} style={{ width: 150 }} />
          </Form.Item>
        )}
        <Form.Item name="keyword" label="关键字">
          <Input allowClear placeholder="任务编号 / 提交人" onPressEnter={() => load()} style={{ width: 160 }} />
        </Form.Item>
        <Form.Item>
          <Button icon={<SearchOutlined />} onClick={() => load()}>
            查询
          </Button>
        </Form.Item>
      </Form>
      <Table
        rowKey="id"
        columns={columns}
        dataSource={data}
        loading={loading}
        scroll={{ x: 1760 }}
        pagination={{
          current: page,
          pageSize: size,
          total,
          showSizeChanger: true,
          pageSizeOptions: [10, 20, 50],
          showTotal: (t) => `共 ${t} 条`,
          onChange: (p, s) => {
            setPage(p)
            setSize(s)
          },
        }}
      />

      <Drawer title={detail ? `任务详情 · ${detail.task_no}` : '任务详情'} width={860} open={detailOpen} onClose={() => setDetailOpen(false)}>
        {detail && (
          <>
            <Descriptions column={2} size="small" bordered style={{ marginBottom: 12 }}>
              <Descriptions.Item label="任务编号">{detail.task_no}</Descriptions.Item>
              <Descriptions.Item label="状态 / 结论依据">
                <Space size={4}>
                  <IgnTaskStatusPill status={detail.task_status} />
                  {detail.verdict_source && <Tag bordered={false}>{verdictLabel(detail.verdict_source)}</Tag>}
                </Space>
              </Descriptions.Item>
              <Descriptions.Item label="执行机">{`${detail.executor_no || ''}（${detail.executor_name || ''}）`}</Descriptions.Item>
              <Descriptions.Item label="关联集群">{detail.cluster_ref}</Descriptions.Item>
              <Descriptions.Item label="动作">{detail.action}</Descriptions.Item>
              <Descriptions.Item label="步骤编码">{detail.step_code || '-'}</Descriptions.Item>
              <Descriptions.Item label="来源（只读，INV-34）" span={2}>
                {detail.source_note || (detail.source_task_no ? `门面受理 · ${detail.source_task_no} / 第 ${detail.source_step_index ?? '-'} 步` : '人工提交')}
              </Descriptions.Item>
              <Descriptions.Item label="动作参数" span={2}>
                {detail.args || '-'}
              </Descriptions.Item>
              <Descriptions.Item label="环境变量（值脱敏）" span={2}>
                <pre style={{ margin: 0, fontSize: 12, whiteSpace: 'pre-wrap' }}>{detail.env_masked || '-'}</pre>
              </Descriptions.Item>
              <Descriptions.Item label="退出码">{detail.task_status === 'LOST' ? '无（痕迹不可存活）' : (detail.exit_code ?? '-')}</Descriptions.Item>
              <Descriptions.Item label="失败原因">{detail.fail_reason || '-'}</Descriptions.Item>
              <Descriptions.Item label="起止时间" span={2}>
                {`${detail.started_at || '-'} ~ ${detail.ended_at || '-'}${detail.duration_ms != null ? `（${detail.duration_ms} ms）` : ''}`}
              </Descriptions.Item>
              <Descriptions.Item label="状态来源标注" span={2}>
                {detail.status_source || '执行事实回填（R-09）'}
              </Descriptions.Item>
              <Descriptions.Item label="对账注记" span={2}>
                {detail.reconciled_note || '-'}
              </Descriptions.Item>
              <Descriptions.Item label="提交人">{detail.submitted_by}</Descriptions.Item>
              <Descriptions.Item label="超时秒数">{detail.timeout_seconds}</Descriptions.Item>
            </Descriptions>

            {/* el12：从表｜任务日志行（只读，REP-11；游标空洞显式标注 INV-38；不提供导出） */}
            <Card
              size="small"
              title={`任务日志行（只读子表 · 游标右界 ${logInfo.log_cursor ?? detail.log_cursor ?? 0} · 共 ${logInfo.total ?? 0} 行）`}
              extra={
                <Space size={6}>
                  <Button
                    size="small"
                    icon={<FileSearchOutlined />}
                    loading={logLoading}
                    onClick={() => detail && loadLogs(detail.task_no, logInfo.log_cursor)}
                  >
                    按游标增量续传
                  </Button>
                  <Button size="small" icon={<ReloadOutlined />} loading={logLoading} onClick={() => detail && loadLogs(detail.task_no)}>
                    重载
                  </Button>
                </Space>
              }
            >
              {logInfo.gap_note && (
                <Alert type="warning" showIcon style={{ marginBottom: 8 }} message={logInfo.gap_note} />
              )}
              {(logInfo.masked_count ?? 0) > 0 && (
                <Alert type="info" showIcon style={{ marginBottom: 8 }} message={`本批 ${logInfo.masked_count} 行命中掩码规则（R-16：写前不可逆脱敏，界面与门面回读同源同脱敏 INV-40）`} />
              )}
              <Table
                rowKey="seq"
                size="small"
                columns={logColumns}
                dataSource={logs}
                loading={logLoading}
                pagination={false}
                scroll={{ y: 300 }}
              />
            </Card>
          </>
        )}
      </Drawer>
    </Card>
  )
}
