// MU frmIgnTask 交付管理 · 任务桥接(AGG-IGN-06;MASTER_DETAIL_FORM)
// 上半提交表单(执行机/动作/参数/集群/超时,FUNC-23)/ 下半台账(FUNC-25,REP-09);
// 取消(FUNC-24)/ 详情(FUNC-26,REP-10 环境变量脱敏)。
// 规则:INV-28 白名单;INV-31 防重放;INV-33 必须归属集群;R-05 容量原子;R-12 治理拒绝。
import { useCallback, useEffect, useState } from 'react'
import { Alert, Button, Card, Col, Descriptions, Drawer, Form, Input, Popconfirm, Row, Select, Space, Table, message } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { ProfileOutlined, ReloadOutlined, SearchOutlined, SendOutlined, StopOutlined } from '@ant-design/icons'
import { ignApi, type ExecTask, type Executor } from '../../api/ign'
import TableToolbar from '../../admin/components/TableToolbar'
import { IgnTaskStatusPill } from '../../admin/components/StatusPill'
import { usePermission } from '../../hooks/usePermission'

const ACTIONS = [
  'ezctl.new',
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
  'scripts.image_prepush',
  'scripts.health_check',
  'scripts.tls_renew',
]

export default function IgnTaskPage() {
  const hasPerm = usePermission()
  const [filterForm] = Form.useForm<{ action?: string; task_status?: string; cluster_ref?: string; keyword?: string }>()
  const [form] = Form.useForm<Record<string, unknown>>()
  const [executors, setExecutors] = useState<Executor[]>([])
  const [data, setData] = useState<ExecTask[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [size, setSize] = useState(10)
  const [loading, setLoading] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [detail, setDetail] = useState<ExecTask | null>(null)
  const [detailOpen, setDetailOpen] = useState(false)

  const loadExecutors = useCallback(async () => {
    try {
      const res = await ignApi.listExecutors({ page: 1, size: 200, status: 'READY' })
      setExecutors(res.list || [])
    } catch {
      // 拦截器已提示
    }
  }, [])

  const load = useCallback(
    async (p = page, s = size) => {
      setLoading(true)
      try {
        const v = filterForm.getFieldsValue()
        const res = await ignApi.listTasks({ page: p, size: s, ...v })
        setData(res.list || [])
        setTotal(res.total || 0)
      } catch {
        // 拦截器已提示
      } finally {
        setLoading(false)
      }
    },
    [page, size, filterForm],
  )

  useEffect(() => {
    loadExecutors()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [page, size])

  const submitTask = async () => {
    const v = await form.validateFields()
    setSubmitting(true)
    try {
      await ignApi.submitTask({
        executor_id: Number(v.executor_id),
        action: String(v.action),
        args: v.args ? String(v.args) : '',
        env: v.env ? String(v.env) : undefined,
        working_dir: v.working_dir ? String(v.working_dir) : undefined,
        cluster_ref: String(v.cluster_ref),
        timeout_seconds: v.timeout_seconds ? Number(v.timeout_seconds) : undefined,
      })
      message.success('任务已提交（R-05 计数已递增;结果经代理回执回流）')
      form.resetFields()
      load()
    } catch {
      // 拦截器已提示
    } finally {
      setSubmitting(false)
    }
  }

  const cancelTask = async (r: ExecTask) => {
    try {
      await ignApi.cancelTask(r.task_no)
      message.success('取消指令已登记（CANCELLING;代理确认后终态 CANCELLED）')
      load()
    } catch {
      // 拦截器已提示
    }
  }

  const openDetail = async (r: ExecTask) => {
    try {
      setDetail(await ignApi.getTask(r.task_no))
      setDetailOpen(true)
    } catch {
      // 拦截器已提示
    }
  }

  const columns: ColumnsType<ExecTask> = [
    { title: '任务编号', dataIndex: 'task_no', key: 'task_no', width: 165 },
    { title: '动作', dataIndex: 'action', key: 'action', width: 180 },
    { title: '执行机', dataIndex: 'executor_no', key: 'executor_no', width: 165 },
    { title: '关联集群', dataIndex: 'cluster_ref', key: 'cluster_ref', width: 170, ellipsis: true },
    {
      title: '状态',
      dataIndex: 'task_status',
      key: 'task_status',
      width: 110,
      render: (v: string) => <IgnTaskStatusPill status={v} />,
    },
    {
      title: '退出码 / 失败原因',
      key: 'result',
      width: 150,
      render: (_, r) => (r.fail_reason ? r.fail_reason : r.exit_code !== null && r.exit_code !== undefined ? `exit ${r.exit_code}` : '-'),
    },
    {
      title: '起止时间',
      key: 'time',
      width: 300,
      render: (_, r) => `${r.started_at || '-'} ~ ${r.ended_at || (r.task_status === 'RUNNING' ? '…' : '-')}`,
    },
    { title: '提交人', dataIndex: 'submitted_by', key: 'submitted_by', width: 110 },
    {
      title: '操作',
      key: 'action',
      width: 150,
      fixed: 'right',
      render: (_, r) => (
        <Space size={4}>
          <Button size="small" icon={<ProfileOutlined />} onClick={() => openDetail(r)}>
            详情
          </Button>
          {hasPerm('ign:FUNC-24') && r.task_status === 'RUNNING' && (
            <Popconfirm title="取消该任务？" description="人为中止终态为 CANCELLED（与 FAILED 分列,INV-30）" onConfirm={() => cancelTask(r)}>
              <Button size="small" danger icon={<StopOutlined />}>
                取消
              </Button>
            </Popconfirm>
          )}
        </Space>
      ),
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
          message="动作必须命中 IGN_ACTION 白名单（INV-28）;参数不得含 shell 元字符（R-12 治理拒绝）;任务必须归属关联集群（INV-33）;超上限即容量拒绝（R-05）"
        />
        <Form form={form} layout="vertical">
          <Row gutter={16}>
            <Col span={8}>
              <Form.Item name="executor_id" label="执行机（就绪）" rules={[{ required: true, message: '必选' }]}>
                <Select
                  showSearch
                  optionFilterProp="label"
                  placeholder="选择执行机"
                  options={executors.map((e) => ({ value: e.id, label: `${e.executor_no}（${e.name} · ${e.current_tasks_count}/${e.concurrent_tasks_max}）` }))}
                />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="action" label="执行动作" rules={[{ required: true, message: '必选（INV-28 白名单）' }]}>
                <Select showSearch placeholder="选择动作" options={ACTIONS.map((a) => ({ value: a, label: a }))} />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="cluster_ref" label="关联集群（跨域引用只存标识）" rules={[{ required: true, message: '必填（INV-33）' }]}>
                <Input placeholder="CLUSTER-xxx / 集群标识" />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="args" label="动作参数（独立 argv 无 shell）">
                <Input placeholder="--cluster mycluster --node node1" />
              </Form.Item>
            </Col>
            <Col span={6}>
              <Form.Item name="timeout_seconds" label="超时秒数（空 = 平台缺省 7200,REF-02）">
                <Input type="number" placeholder="7200" />
              </Form.Item>
            </Col>
            <Col span={6}>
              <Form.Item name="working_dir" label="工作目录（可选）">
                <Input placeholder="/opt/ezop" />
              </Form.Item>
            </Col>
            <Col span={24}>
              <Form.Item name="env" label="环境变量（每行 KEY=VALUE;值脱敏展示）">
                <Input.TextArea rows={2} placeholder={'KUBECONFIG=/etc/kube/conf\nIMAGE_MIRROR=harbor.local'} />
              </Form.Item>
            </Col>
          </Row>
          {hasPerm('ign:FUNC-23') && (
            <Button type="primary" icon={<SendOutlined />} loading={submitting} onClick={submitTask}>
              提交
            </Button>
          )}
        </Form>
      </Card>

      {/* 下半:台账(DETAIL 区,FUNC-25) */}
      <TableToolbar
        title="任务台账"
        total={total}
        description="执行任务台账与取消（AGG-IGN-06;状态来源:执行机事实回填 R-09）"
        extra={
          <Button icon={<ReloadOutlined />} loading={loading} onClick={() => load()}>
            刷新
          </Button>
        }
      />
      <Form form={filterForm} layout="inline" style={{ marginBottom: 12 }}>
        <Form.Item name="action" label="动作">
          <Select allowClear showSearch style={{ width: 190 }} options={ACTIONS.map((a) => ({ value: a, label: a }))} />
        </Form.Item>
        <Form.Item name="task_status" label="状态">
          <Select allowClear style={{ width: 140 }} options={['RUNNING', 'CANCELLING', 'COMPLETED', 'FAILED', 'CANCELLED'].map((s) => ({ value: s, label: s }))} />
        </Form.Item>
        <Form.Item name="cluster_ref" label="集群">
          <Input allowClear placeholder="集群标识" onPressEnter={() => load()} style={{ width: 150 }} />
        </Form.Item>
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
        scroll={{ x: 1420 }}
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

      <Drawer title={detail ? `任务详情 · ${detail.task_no}` : '任务详情'} width={720} open={detailOpen} onClose={() => setDetailOpen(false)}>
        {detail && (
          <>
            <Descriptions column={2} size="small" bordered style={{ marginBottom: 12 }}>
              <Descriptions.Item label="任务编号">{detail.task_no}</Descriptions.Item>
              <Descriptions.Item label="状态">
                <IgnTaskStatusPill status={detail.task_status} />
              </Descriptions.Item>
              <Descriptions.Item label="执行机">{`${detail.executor_no || ''}（${detail.executor_name || ''}）`}</Descriptions.Item>
              <Descriptions.Item label="关联集群">{detail.cluster_ref}</Descriptions.Item>
              <Descriptions.Item label="动作" span={2}>{detail.action}</Descriptions.Item>
              <Descriptions.Item label="动作参数" span={2}>
                {detail.args || '-'}
              </Descriptions.Item>
              <Descriptions.Item label="环境变量（值脱敏）" span={2}>
                <pre style={{ margin: 0, fontSize: 12, whiteSpace: 'pre-wrap' }}>{detail.env_masked || '-'}</pre>
              </Descriptions.Item>
              <Descriptions.Item label="退出码">{detail.exit_code ?? '-'}</Descriptions.Item>
              <Descriptions.Item label="失败原因">{detail.fail_reason || '-'}</Descriptions.Item>
              <Descriptions.Item label="起止时间" span={2}>
                {`${detail.started_at || '-'} ~ ${detail.ended_at || '-'}${detail.duration_ms != null ? `（${detail.duration_ms} ms）` : ''}`}
              </Descriptions.Item>
              <Descriptions.Item label="状态来源标注" span={2}>
                {detail.status_source || '执行机事实回填（R-09）'}
              </Descriptions.Item>
              <Descriptions.Item label="提交人">{detail.submitted_by}</Descriptions.Item>
              <Descriptions.Item label="超时秒数">{detail.timeout_seconds}</Descriptions.Item>
            </Descriptions>
            <Card size="small" title="任务日志（中继回流视图）">
              {detail.logs && detail.logs.length > 0 ? (
                <pre style={{ maxHeight: 300, overflow: 'auto', fontSize: 12 }}>
                  {detail.logs.map((l) => `[${l.logged_at || ''}] ${l.log_line}`).join('\n')}
                </pre>
              ) : (
                <span style={{ color: '#888', fontSize: 12 }}>暂无日志回流（日志中继经跨域通道,代理回执后可见）</span>
              )}
            </Card>
          </>
        )}
      </Drawer>
    </Card>
  )
}
