// MU frmIgnLog 交付管理 · 日志与对账台(QUERY_LIST)
// 任务台账查询(FUNC-25)/ 任务详情(FUNC-26)/ 任务日志(FUNC-27,中继回流视图)/ 对账与纠偏台账(FUNC-28,REP-12)。
// 规则:R-06 计数纠偏留痕;R-09 状态修正/任务丢失显式留痕;R-12 治理拒绝记录可查。
import { useCallback, useEffect, useState } from 'react'
import { Button, Card, Form, Input, Select, Space, Table, Tabs, message } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { ProfileOutlined, ReloadOutlined, SearchOutlined } from '@ant-design/icons'
import { ignApi, type ExecTask, type PortableRow } from '../../api/ign'
import TableToolbar from '../../admin/components/TableToolbar'
import { IgnTaskStatusPill } from '../../admin/components/StatusPill'
import { usePermission } from '../../hooks/usePermission'

const EVENT_TYPES = [
  { value: 'COUNT_CORRECT', label: '计数纠偏（R-06）' },
  { value: 'STATUS_FIX', label: '状态修正（R-09/R-10/R-12）' },
  { value: 'TASK_LOST', label: '任务丢失（R-09 显式置败）' },
]

export default function IgnLogPage() {
  const hasPerm = usePermission()
  const [taskForm] = Form.useForm<{ keyword?: string; task_status?: string }>()
  const [recForm] = Form.useForm<{ event_type?: string; keyword?: string }>()
  const [tasks, setTasks] = useState<ExecTask[]>([])
  const [taskTotal, setTaskTotal] = useState(0)
  const [taskPage, setTaskPage] = useState(1)
  const [taskLoading, setTaskLoading] = useState(false)
  const [recRows, setRecRows] = useState<PortableRow[]>([])
  const [recTotal, setRecTotal] = useState(0)
  const [recPage, setRecPage] = useState(1)
  const [recLoading, setRecLoading] = useState(false)

  const loadTasks = useCallback(
    async (p = taskPage, s = 10) => {
      setTaskLoading(true)
      try {
        const v = taskForm.getFieldsValue()
        const res = await ignApi.listTasks({ page: p, size: s, ...v })
        setTasks(res.list || [])
        setTaskTotal(res.total || 0)
      } catch {
        // 拦截器已提示
      } finally {
        setTaskLoading(false)
      }
    },
    [taskPage, taskForm],
  )

  const loadRec = useCallback(
    async (p = recPage, s = 10) => {
      setRecLoading(true)
      try {
        const v = recForm.getFieldsValue()
        const res = await ignApi.listReconcile({ page: p, size: s, ...v })
        setRecRows(res.list || [])
        setRecTotal(res.total || 0)
      } catch {
        // 拦截器已提示
      } finally {
        setRecLoading(false)
      }
    },
    [recPage, recForm],
  )

  useEffect(() => {
    loadTasks()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [taskPage])
  useEffect(() => {
    loadRec()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [recPage])

  const openLog = async (r: ExecTask) => {
    try {
      const detail = await ignApi.getTask(r.task_no)
      const logs = (detail.logs || []) as { logged_at?: string; direction?: string; log_line?: string }[]
      message.info(
        logs.length > 0
          ? `任务 ${r.task_no} 日志 ${logs.length} 行:\n` + logs.map((l) => `[${l.logged_at || ''}]${l.direction ? `(${l.direction})` : ''} ${l.log_line}`).join('\n')
          : `任务 ${r.task_no} 暂无日志回流（日志中继经跨域通道,代理回执后可见）`,
        8,
      )
    } catch {
      // 拦截器已提示
    }
  }

  const taskColumns: ColumnsType<ExecTask> = [
    { title: '任务编号', dataIndex: 'task_no', key: 'task_no', width: 165 },
    { title: '动作', dataIndex: 'action', key: 'action', width: 170 },
    { title: '执行机', dataIndex: 'executor_no', key: 'executor_no', width: 165 },
    { title: '关联集群', dataIndex: 'cluster_ref', key: 'cluster_ref', width: 150, ellipsis: true },
    {
      title: '状态（含来源标注）',
      dataIndex: 'task_status',
      key: 'task_status',
      width: 130,
      render: (v: string) => <IgnTaskStatusPill status={v} />,
    },
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

  return (
    <Card>
      <Tabs
        defaultActiveKey="task"
        items={[
          {
            key: 'task',
            label: '任务台账与日志',
            children: (
              <>
                <TableToolbar
                  title="任务台账"
                  total={taskTotal}
                  description="任务日志为执行机侧中继回流视图（FUNC-27;以执行机为准 R-09）"
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
                    <Select allowClear style={{ width: 140 }} options={['RUNNING', 'CANCELLING', 'COMPLETED', 'FAILED', 'CANCELLED'].map((s) => ({ value: s, label: s }))} />
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
                  scroll={{ x: 1180 }}
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
                  description="全部例外路径必须留痕（R-06 计数纠偏 / R-09 状态修正与任务丢失 / R-12 治理拒绝）"
                  extra={
                    <Button icon={<ReloadOutlined />} loading={recLoading} onClick={() => loadRec()}>
                      刷新
                    </Button>
                  }
                />
                <Form form={recForm} layout="inline" style={{ marginBottom: 12 }}>
                  <Form.Item name="event_type" label="类型">
                    <Select allowClear style={{ width: 220 }} options={EVENT_TYPES} />
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
        ]}
      />
    </Card>
  )
}
