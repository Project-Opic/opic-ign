// MU frmIgnLog 日志与对账台(QUERY_LIST;v1.5 起退出侧栏，两处挂点：「交付任务」叶的「日志与对账」签 + 集群交付台的「日志与对账」签)
// 三页签：任务台账与日志(FUNC-25/26/27,REP-11)/ 对账与纠偏(FUNC-28,REP-12)/ 门面受理台账(FUNC-37,REP-14)。
// 挂点差的是过滤上下文(9.6 载体注记)：传入 clusterRef 即本集群 scope，三页签共用同一套 REP-11~REP-14 查询面、不复制第二份报表绑定。
// 规则:R-06 计数纠偏留痕;R-09 状态修正/任务丢失显式留痕;R-12 治理拒绝与 R-15 执行位不可用记录可查;
//       INV-38 行游标空洞显式标注;R-16/INV-40 已脱敏标记与门面回读同源。
import { useCallback, useEffect, useState } from 'react'
import { Alert, Button, Card, Descriptions, Drawer, Form, Input, Select, Space, Table, Tabs, Tag } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { ProfileOutlined, ReloadOutlined, SearchOutlined } from '@ant-design/icons'
import { useSearchParams } from 'react-router-dom'
import { dictItems, ignApi, type ExecTask, type FacadeTask, type PortableRow, type TaskLogLine } from '../../api/ign'
import TableToolbar from '../../admin/components/TableToolbar'
import { IgnTaskStatusPill } from '../../admin/components/StatusPill'
import { usePermission } from '../../hooks/usePermission'

// REP-12 的「类型」维度：拒绝类（受理侧即刻拒绝，不产生任务行）与纠偏类（在途/终态后的修正）分列
const EVENT_TYPES = [
  { value: 'GOVERNANCE_REFUSE', label: '治理拒绝（R-12/R-13：白名单外、可达面外、拼接风险）' },
  { value: 'TOOL_UNAVAILABLE', label: '执行位不可用（R-15：命令面挂载缺失，受理即刻拒绝）' },
  { value: 'COUNT_CORRECT', label: '计数纠偏（R-06）' },
  { value: 'STATUS_FIX', label: '状态修正（R-09/R-10/R-12）' },
  { value: 'TASK_LOST', label: '痕迹不可存活判定（INV-37：崩溃痕迹无法验真）' },
]

const TASK_STATUS = ['RUNNING', 'CANCELLING', 'COMPLETED', 'FAILED', 'CANCELLED', 'LOST']

// 屏内三页签的自有值域（9.6 载体注记：内部结构未变）。外层宿主深链占用 ?tab=（/ign/task?tab=log），
// 本屏的子签改由 ?log_tab= 承载；旧链接的 ?tab=facade 只在值命中自有值域时兼容识别一次。
const SUB_TABS = ['task', 'reconcile', 'facade']

export default function IgnLogPage({ clusterRef = '' }: { clusterRef?: string }) {
  const hasPerm = usePermission()
  const [query] = useSearchParams()
  // 任务详情「来源」列反查跳转：/ign/task?tab=log&log_tab=facade&source_task_no=xxx
  const [tab, setTab] = useState(() => {
    const v = query.get('log_tab') || query.get('tab') || 'task'
    return SUB_TABS.includes(v) ? v : 'task'
  })
  const [taskForm] = Form.useForm<{ keyword?: string; task_status?: string }>()
  const [recForm] = Form.useForm<{ event_type?: string; keyword?: string }>()
  const [facadeForm] = Form.useForm<{ source_task_no?: string; source_step_index?: string; cluster_ref?: string; step_code?: string; verdict_source?: string; task_status?: string }>()
  const [tasks, setTasks] = useState<ExecTask[]>([])
  const [taskTotal, setTaskTotal] = useState(0)
  const [taskPage, setTaskPage] = useState(1)
  const [taskLoading, setTaskLoading] = useState(false)
  const [recRows, setRecRows] = useState<PortableRow[]>([])
  const [recTotal, setRecTotal] = useState(0)
  const [recPage, setRecPage] = useState(1)
  const [recLoading, setRecLoading] = useState(false)
  const [facadeRows, setFacadeRows] = useState<FacadeTask[]>([])
  const [facadeTotal, setFacadeTotal] = useState(0)
  const [facadePage, setFacadePage] = useState(1)
  const [facadeLoading, setFacadeLoading] = useState(false)
  const [dict, setDict] = useState<Record<string, unknown>>({})
  const [logTask, setLogTask] = useState<ExecTask | null>(null)
  const [logOpen, setLogOpen] = useState(false)
  const [logRows, setLogRows] = useState<TaskLogLine[]>([])
  const [logMeta, setLogMeta] = useState<{ log_cursor?: number; gap_lines?: number; gap_note?: string; masked_count?: number; total?: number }>({})
  const [logLoading, setLogLoading] = useState(false)

  const verdictLabel = useCallback(
    (code?: string | null) => {
      if (!code) return '-'
      return dictItems(dict, 'IGN_VERDICT_SOURCE').find((d) => d.code === code)?.label || code
    },
    [dict],
  )

  const loadTasks = useCallback(
    async (p = taskPage, s = 10) => {
      setTaskLoading(true)
      try {
        const v = taskForm.getFieldsValue()
        // 载体注记（v1.5）：台内挂点带 clusterRef（本集群 scope），横看挂点不带 —— 同一套查询面
        const res = await ignApi.listTasks({ page: p, size: s, ...(clusterRef ? { cluster_ref: clusterRef } : {}), ...v })
        setTasks(res.list || [])
        setTaskTotal(res.total || 0)
      } catch {
        // 拦截器已提示
      } finally {
        setTaskLoading(false)
      }
    },
    [taskPage, taskForm, clusterRef],
  )

  const loadRec = useCallback(
    async (p = recPage, s = 10) => {
      setRecLoading(true)
      try {
        const v = recForm.getFieldsValue()
        const res = await ignApi.listReconcile({ page: p, size: s, ...(clusterRef ? { cluster_ref: clusterRef } : {}), ...v })
        setRecRows(res.list || [])
        setRecTotal(res.total || 0)
      } catch {
        // 拦截器已提示
      } finally {
        setRecLoading(false)
      }
    },
    [recPage, recForm, clusterRef],
  )

  const loadFacade = useCallback(
    async (p = facadePage, s = 20) => {
      if (!hasPerm('ign:FUNC-37')) return
      setFacadeLoading(true)
      try {
        // 台内挂点由上下文给定集群，屏上那个可改的集群筛选器不渲染，这里也就不再取表值
        const v = facadeForm.getFieldsValue()
        if (clusterRef) v.cluster_ref = clusterRef
        const res = await ignApi.listFacadeTasks({ page: p, size: s, ...v })
        setFacadeRows(res.list || [])
        setFacadeTotal(res.total || 0)
      } catch {
        // 拦截器已提示
      } finally {
        setFacadeLoading(false)
      }
    },
    [facadePage, facadeForm, hasPerm, clusterRef],
  )

  const openLog = async (r: ExecTask) => {
    try {
      const detail = await ignApi.getTask(r.task_no)
      setLogTask(detail)
      setLogOpen(true)
      await loadLogLines(r.task_no)
    } catch {
      // 拦截器已提示
    }
  }

  // FUNC-27 / REP-11：日志子表与门面回执读的是同一份已脱敏数据（INV-40），界面不提供导出位
  const loadLogLines = useCallback(async (taskNo: string, sinceSeq?: number) => {
    setLogLoading(true)
    try {
      const res = await ignApi.taskLogs(taskNo, sinceSeq ? { since_seq: sinceSeq } : undefined)
      setLogRows((prev) => (sinceSeq ? [...prev, ...(res.list || [])] : res.list || []))
      setLogMeta({ log_cursor: res.log_cursor, gap_lines: res.gap_lines, gap_note: res.gap_note, masked_count: res.masked_count, total: res.total })
    } catch {
      // 拦截器已提示
    } finally {
      setLogLoading(false)
    }
  }, [])

  useEffect(() => {
    loadTasks()
    loadRec()
    ignApi
      .dictionaries()
      .then(setDict)
      .catch(() => {})
    const src = query.get('source_task_no')
    if (src) facadeForm.setFieldValue('source_task_no', src)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  useEffect(() => {
    loadTasks()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [taskPage])
  useEffect(() => {
    loadRec()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [recPage])
  useEffect(() => {
    loadFacade()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [facadePage])

  const taskColumns: ColumnsType<ExecTask> = [
    { title: '任务编号', dataIndex: 'task_no', key: 'task_no', width: 165 },
    { title: '动作', dataIndex: 'action', key: 'action', width: 170 },
    { title: '执行机', dataIndex: 'executor_no', key: 'executor_no', width: 165 },
    { title: '关联集群', dataIndex: 'cluster_ref', key: 'cluster_ref', width: 150, ellipsis: true },
    {
      // el06：状态 + 结论依据 + 对账修正三件同现；LOST 落「痕迹不可存活」且退出码位为空（INV-37）
      title: '状态 / 结论依据',
      dataIndex: 'task_status',
      key: 'task_status',
      width: 200,
      render: (v: string, r) => (
        <Space size={4}>
          <IgnTaskStatusPill status={v} />
          {r.verdict_source && <Tag bordered={false}>{verdictLabel(r.verdict_source)}</Tag>}
        </Space>
      ),
    },
    {
      title: '来源（INV-34）',
      key: 'source',
      width: 170,
      render: (_, r) => (r.source_task_no ? `门面 · ${r.source_task_no} / ${r.source_step_index ?? '-'}` : '人工提交'),
    },
    { title: '退出码', key: 'exit', width: 90, render: (_, r) => (r.task_status === 'LOST' ? '无' : (r.exit_code ?? '-')) },
    { title: '起止时间', key: 'time', width: 300, render: (_, r) => `${r.started_at || '-'} ~ ${r.ended_at || '…'}` },
    {
      title: '操作',
      key: 'action',
      width: 160,
      render: (_, r) => (
        <Space size={4}>
          <Button size="small" icon={<ProfileOutlined />} onClick={() => openLog(r)}>
            日志
          </Button>
        </Space>
      ),
    },
  ]

  const recColumns: ColumnsType<PortableRow> = [
    {
      title: '事件类型',
      dataIndex: 'event_type',
      key: 'event_type',
      width: 150,
      render: (v: string) => EVENT_TYPES.find((e) => e.value === v)?.label || v,
    },
    { title: '对象', key: 'object', width: 200, render: (_, r) => `${r.object_type || ''} ${r.object_no || ''}` },
    {
      title: '本地值 → 对端值',
      key: 'values',
      width: 220,
      render: (_, r) => `${r.local_value ?? '-'} → ${r.remote_value ?? '-'}`,
    },
    { title: '触发原因', dataIndex: 'trigger_reason', key: 'trigger_reason', ellipsis: true },
    { title: '留痕（谁 / 何时）', key: 'trace', width: 240, render: (_, r) => `${r.operator_name || r.operator_id || '系统'} · ${r.created_at || ''}` },
  ]

  const logColumns: ColumnsType<TaskLogLine> = [
    { title: '行游标', dataIndex: 'seq', key: 'seq', width: 90 },
    { title: '时间', dataIndex: 'logged_at', key: 'logged_at', width: 170 },
    { title: '流向', dataIndex: 'direction', key: 'direction', width: 100 },
    { title: '日志行', dataIndex: 'log_line', key: 'log_line', render: (v: string) => <span style={{ fontSize: 12, whiteSpace: 'pre-wrap' }}>{v}</span> },
    {
      // el08 / R-16：命中掩码规则的行标出「日志被动过」，而非静默替换
      title: '已脱敏',
      dataIndex: 'masked',
      key: 'masked',
      width: 90,
      render: (v: boolean) => (v ? <Tag color="orange">已掩码</Tag> : '-'),
    },
  ]

  // c1~c12（REP-14）：本域任务号 / 来源两列 / 动作 / 步骤编码 / 状态 / 结论依据 / 退出码 / 集群 / 耗时 / 提交人 / 日志行数 / 游标右界
  const facadeColumns: ColumnsType<FacadeTask> = [
    { title: '本域任务编号', dataIndex: 'task_no', key: 'task_no', width: 165 },
    {
      title: '来源任务标识 + 序号',
      key: 'source',
      width: 230,
      render: (_, r) => `${r.source_task_no} / 第 ${r.source_step_index ?? '-'} 步`,
    },
    { title: '动作', dataIndex: 'action', key: 'action', width: 160 },
    { title: '步骤编码', dataIndex: 'step_code', key: 'step_code', width: 100, render: (v) => v || '-' },
    { title: '集群标识', dataIndex: 'cluster_ref', key: 'cluster_ref', width: 150, ellipsis: true },
    {
      title: '状态 / 结论依据',
      key: 'status',
      width: 200,
      render: (_, r) => (
        <Space size={4}>
          <IgnTaskStatusPill status={r.task_status} />
          {r.verdict_source && <Tag bordered={false}>{verdictLabel(r.verdict_source)}</Tag>}
        </Space>
      ),
    },
    { title: '退出码', dataIndex: 'exit_code', key: 'exit_code', width: 80, render: (v, r) => (r.task_status === 'LOST' ? '无' : (v ?? '-')) },
    { title: '失败原因', dataIndex: 'fail_reason', key: 'fail_reason', width: 180, ellipsis: true, render: (v) => v || '-' },
    { title: '耗时(ms)', dataIndex: 'duration_ms', key: 'duration_ms', width: 100, render: (v) => v ?? '-' },
    { title: '受理身份', dataIndex: 'submitted_by', key: 'submitted_by', width: 130 },
    { title: '日志行数', dataIndex: 'log_line_count', key: 'log_line_count', width: 100 },
    { title: '游标右界', dataIndex: 'log_cursor', key: 'log_cursor', width: 100 },
    { title: '受理时间', dataIndex: 'created_at', key: 'created_at', width: 170 },
  ]

  return (
    <Card>
      <Tabs
        activeKey={tab}
        onChange={setTab}
        items={[
          {
            key: 'task',
            label: '任务台账与日志',
            children: (
              <>
                <TableToolbar
                  title="任务台账"
                  total={taskTotal}
                  description="任务日志取自本域唯一日志存储（FUNC-27;写前脱敏 R-16，与门面回读同源 INV-40）"
                  extra={
                    <Button icon={<ReloadOutlined />} loading={taskLoading} onClick={() => loadTasks()}>
                      刷新
                    </Button>
                  }
                />
                <Form form={taskForm} layout="inline" style={{ marginBottom: 12 }}>
                  <Form.Item name="keyword" label="任务">
                    <Input allowClear placeholder="任务编号 / 提交人" onPressEnter={() => loadTasks(1)} style={{ width: 180 }} />
                  </Form.Item>
                  <Form.Item name="task_status" label="状态">
                    <Select allowClear style={{ width: 150 }} options={TASK_STATUS.map((s) => ({ value: s, label: s }))} />
                  </Form.Item>
                  <Form.Item>
                    <Button icon={<SearchOutlined />} onClick={() => loadTasks(1)}>
                      查询
                    </Button>
                  </Form.Item>
                </Form>
                <Table
                  rowKey="id"
                  columns={taskColumns}
                  dataSource={tasks}
                  loading={taskLoading}
                  scroll={{ x: 1580 }}
                  pagination={{
                    current: taskPage,
                    pageSize: 10,
                    total: taskTotal,
                    showTotal: (t) => `共 ${t} 条`,
                    onChange: (p) => setTaskPage(p),
                  }}
                />
              </>
            ),
          },
          {
            key: 'reconcile',
            label: '对账与纠偏',
            children: (
              <>
                <TableToolbar
                  title="对账与纠偏台账"
                  total={recTotal}
                  description="全部例外路径必须留痕；拒绝类（R-12 治理拒绝 / R-15 执行位不可用）与纠偏类（R-06 / R-09 / INV-37）分列，不与执行结论混计"
                  extra={
                    <Button icon={<ReloadOutlined />} loading={recLoading} onClick={() => loadRec()}>
                      刷新
                    </Button>
                  }
                />
                <Form form={recForm} layout="inline" style={{ marginBottom: 12 }}>
                  <Form.Item name="event_type" label="类型">
                    <Select allowClear style={{ width: 300 }} options={EVENT_TYPES} />
                  </Form.Item>
                  <Form.Item name="keyword" label="关键字">
                    <Input allowClear placeholder="对象 / 原因" onPressEnter={() => loadRec(1)} style={{ width: 180 }} />
                  </Form.Item>
                  <Form.Item>
                    <Button icon={<SearchOutlined />} onClick={() => loadRec(1)}>
                      查询
                    </Button>
                  </Form.Item>
                </Form>
                <Table
                  rowKey="id"
                  columns={recColumns}
                  dataSource={recRows}
                  loading={recLoading}
                  scroll={{ x: 1000 }}
                  pagination={{
                    current: recPage,
                    pageSize: 10,
                    total: recTotal,
                    showTotal: (t) => `共 ${t} 条`,
                    onChange: (p) => setRecPage(p),
                  }}
                />
              </>
            ),
          },
          {
            key: 'facade',
            label: '门面受理台账',
            children: (
              <>
                {!hasPerm('ign:FUNC-37') ? (
                  <Alert type="warning" showIcon message="无「门面受理台账查询」权限（ign:FUNC-37）" description="该台账回答「上游这次交付的这一步由谁受理、执行了几次、结论凭什么」。" />
                ) : (
                  <>
                    <TableToolbar
                      title="门面受理台账（REP-14）"
                      total={facadeTotal}
                      description="固定过滤「来源任务标识非空」；与任务台账共用同一 ExecTask 查询面，只差列集（不复制事实）"
                      extra={
                        <Button icon={<ReloadOutlined />} loading={facadeLoading} onClick={() => loadFacade()}>
                          刷新
                        </Button>
                      }
                    />
                    <Form form={facadeForm} layout="inline" style={{ marginBottom: 12 }}>
                      <Form.Item name="source_task_no" label="来源任务标识">
                        <Input allowClear placeholder="上游中心任务号" onPressEnter={() => loadFacade(1)} style={{ width: 180 }} />
                      </Form.Item>
                      <Form.Item name="source_step_index" label="来源步骤序号">
                        <Input allowClear type="number" onPressEnter={() => loadFacade(1)} style={{ width: 120 }} />
                      </Form.Item>
                      {!clusterRef && (
                        <Form.Item name="cluster_ref" label="集群标识">
                          <Input allowClear onPressEnter={() => loadFacade(1)} style={{ width: 150 }} />
                        </Form.Item>
                      )}
                      <Form.Item name="step_code" label="步骤编码">
                        <Select allowClear style={{ width: 120 }} options={dictItems(dict, 'IGN_INSTALL_STEP').map((d) => ({ value: d.code, label: d.code }))} />
                      </Form.Item>
                      <Form.Item name="verdict_source" label="结论依据">
                        <Select allowClear style={{ width: 160 }} options={dictItems(dict, 'IGN_VERDICT_SOURCE').map((d) => ({ value: d.code, label: d.label }))} />
                      </Form.Item>
                      <Form.Item name="task_status" label="状态">
                        <Select allowClear style={{ width: 140 }} options={TASK_STATUS.map((s) => ({ value: s, label: s }))} />
                      </Form.Item>
                      <Form.Item>
                        <Button icon={<SearchOutlined />} onClick={() => loadFacade(1)}>
                          查询
                        </Button>
                      </Form.Item>
                    </Form>
                    <Table
                      rowKey="task_no"
                      columns={facadeColumns}
                      dataSource={facadeRows}
                      loading={facadeLoading}
                      scroll={{ x: 1700 }}
                      pagination={{
                        current: facadePage,
                        pageSize: 20,
                        total: facadeTotal,
                        showTotal: (t) => `共 ${t} 条`,
                        onChange: (p) => setFacadePage(p),
                      }}
                    />
                  </>
                )}
              </>
            ),
          },
        ]}
      />

      <Drawer title={logTask ? `任务日志行 · ${logTask.task_no}` : '任务日志行'} width={820} open={logOpen} onClose={() => setLogOpen(false)}>
        {logTask && (
          <>
            <Descriptions column={2} size="small" bordered style={{ marginBottom: 12 }}>
              <Descriptions.Item label="任务编号">{logTask.task_no}</Descriptions.Item>
              <Descriptions.Item label="状态 / 结论依据">
                <Space size={4}>
                  <IgnTaskStatusPill status={logTask.task_status} />
                  {logTask.verdict_source && <Tag bordered={false}>{verdictLabel(logTask.verdict_source)}</Tag>}
                </Space>
              </Descriptions.Item>
              <Descriptions.Item label="动作 / 步骤编码">{`${logTask.action}${logTask.step_code ? ` · ${logTask.step_code}` : ''}`}</Descriptions.Item>
              <Descriptions.Item label="关联集群">{logTask.cluster_ref}</Descriptions.Item>
              <Descriptions.Item label="来源（只读）" span={2}>
                {logTask.source_note || (logTask.source_task_no ? `门面受理 · ${logTask.source_task_no} / 第 ${logTask.source_step_index ?? '-'} 步` : '人工提交（来源两列为空，INV-34）')}
              </Descriptions.Item>
              <Descriptions.Item label="对账注记" span={2}>
                {logTask.reconciled_note || '-'}
              </Descriptions.Item>
            </Descriptions>
            <Card
              size="small"
              title={`日志行（游标右界 ${logMeta.log_cursor ?? 0} · 台账 ${logMeta.total ?? 0} 行 · 本视图 ${logRows.length} 行）`}
              extra={
                <Space size={6}>
                  <Button size="small" loading={logLoading} onClick={() => logTask && loadLogLines(logTask.task_no, logMeta.log_cursor)}>
                    按游标增量续传
                  </Button>
                  <Button size="small" icon={<ReloadOutlined />} loading={logLoading} onClick={() => logTask && loadLogLines(logTask.task_no)}>
                    重载
                  </Button>
                </Space>
              }
            >
              {!!logMeta.gap_note && <Alert type="warning" showIcon style={{ marginBottom: 8 }} message={logMeta.gap_note} />}
              {(logMeta.masked_count ?? 0) > 0 && (
                <Alert type="info" showIcon style={{ marginBottom: 8 }} message={`本批 ${logMeta.masked_count} 行命中掩码规则（R-16 写前不可逆脱敏；INV-40 界面与门面回读同源同脱敏）`} />
              )}
              <Table rowKey="seq" size="small" columns={logColumns} dataSource={logRows} loading={logLoading} pagination={false} scroll={{ y: 320 }} />
            </Card>
          </>
        )}
      </Drawer>
    </Card>
  )
}
