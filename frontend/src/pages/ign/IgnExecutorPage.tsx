// MU frmIgnExecutor 交付管理 · 执行机操作台(AGG-IGN-03;LIST_MAINTENANCE + 审批入口)
// 注册(FUNC-13)/ 重新生成令牌(FUNC-14,明文一次性展示)/ 注销空闲(FUNC-15)/ 能力标签(FUNC-16)/
// 列表(FUNC-17,REP-05)/ 详情(FUNC-18,REP-06 令牌只出布尔)/ TLS 降级声明(FUNC-21)/
// 强制注销申请(FUNC-31 带保存草稿/提交两种形态;审批 FUNC-32/33 走审批中心)。
// 规则:INV-11/12/13/14/16/18;R-04 锁定解锁;R-07/R-08 拆除链与审批前置;R-11 降级互斥。
import { useCallback, useEffect, useState } from 'react'
import { Alert, Button, Card, Col, Descriptions, Drawer, Form, Input, Modal, Popconfirm, Row, Select, Space, Table, Tag, message } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { KeyOutlined, PlusOutlined, ReloadOutlined, SearchOutlined, StopOutlined, UserDeleteOutlined } from '@ant-design/icons'
import { ignApi, type Executor, type ForceRemoveApply } from '../../api/ign'
import TableToolbar from '../../admin/components/TableToolbar'
import { IgnApplyStatusPill, IgnExecutorStatusPill, IgnTaskStatusPill } from '../../admin/components/StatusPill'
import { usePermission } from '../../hooks/usePermission'

const EXECUTOR_STATUSES = ['ENROLLING', 'READY', 'LOCKED', 'DEGRADED', 'RENEWAL_PENDING', 'REMOVED']
const CAPABILITIES = ['DEPLOY', 'SKOPEO', 'BACKUP', 'PREFLIGHT', 'OFFLINE']

export default function IgnExecutorPage() {
  const hasPerm = usePermission()
  const [filterForm] = Form.useForm<{ status?: string; executor_type?: string; capability?: string; keyword?: string }>()
  const [form] = Form.useForm<Record<string, unknown>>()
  const [capForm] = Form.useForm<{ capability_flags?: string[] }>()
  const [forceForm] = Form.useForm<{ reason?: string }>()
  const [data, setData] = useState<Executor[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [size, setSize] = useState(10)
  const [loading, setLoading] = useState(false)
  const [createOpen, setCreateOpen] = useState(false)
  const [saving, setSaving] = useState(false)
  const [tokenView, setTokenView] = useState<{ executorNo: string; token: string; note?: string } | null>(null)
  const [capTarget, setCapTarget] = useState<Executor | null>(null)
  const [forceTarget, setForceTarget] = useState<Executor | null>(null)
  const [forceImpact, setForceImpact] = useState<ForceRemoveApply | null>(null)
  const [detail, setDetail] = useState<Executor | null>(null)
  const [detailOpen, setDetailOpen] = useState(false)

  const load = useCallback(
    async (p = page, s = size) => {
      setLoading(true)
      try {
        const v = filterForm.getFieldsValue()
        const res = await ignApi.listExecutors({ page: p, size: s, ...v })
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
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [page, size])

  const openDetail = async (r: Executor) => {
    try {
      setDetail(await ignApi.getExecutor(r.id))
      setDetailOpen(true)
    } catch {
      // 拦截器已提示
    }
  }

  const submitCreate = async () => {
    const v = await form.validateFields()
    setSaving(true)
    try {
      const created = await ignApi.createExecutor({
        name: String(v.name || ''),
        display_name: v.display_name ? String(v.display_name) : undefined,
        host: String(v.host || ''),
        port: Number(v.port),
        asset_ref: String(v.asset_ref || ''),
        capability_flags: v.capability_flags ? String(v.capability_flags) : undefined,
      })
      setCreateOpen(false)
      load()
      if (created.bootstrap_token) {
        setTokenView({ executorNo: created.executor_no, token: created.bootstrap_token, note: created.bootstrap_token_note })
      } else {
        message.success('执行机已注册')
      }
    } catch {
      // 拦截器已提示
    } finally {
      setSaving(false)
    }
  }

  const regenToken = async (r: Executor) => {
    try {
      const out = await ignApi.regenerateToken(r.id)
      if (out.bootstrap_token) {
        setTokenView({ executorNo: out.executor_no, token: out.bootstrap_token, note: out.bootstrap_token_note })
      }
      load()
    } catch {
      // 拦截器已提示
    }
  }

  const openForceRemove = async (r: Executor) => {
    setForceTarget(r)
    forceForm.resetFields()
    setForceImpact(null)
  }

  const saveForceRemove = async (mode: 'draft' | 'submit') => {
    if (!forceTarget) return
    const v = await forceForm.validateFields()
    setSaving(true)
    try {
      const out = await ignApi.saveForceRemove(forceTarget.id, String(v.reason || ''), mode)
      setForceImpact(out)
      if (mode === 'submit') {
        message.success('已提交审批（FLOW-PROC-APP-01;平台运维管理员审批）')
        setForceTarget(null)
        load()
      } else {
        message.success('草稿已保存（提交审批前可继续修改原因）')
      }
    } catch {
      // 拦截器已提示
    } finally {
      setSaving(false)
    }
  }

  const submitPendingApply = async (apply: ForceRemoveApply) => {
    try {
      await ignApi.submitForceRemove(apply.id)
      message.success('已提交审批（FLOW-PROC-APP-01）')
      setForceTarget(null)
      load()
    } catch {
      // 拦截器已提示
    }
  }

  const columns: ColumnsType<Executor> = [
    { title: '执行机编号', dataIndex: 'executor_no', key: 'executor_no', width: 165 },
    { title: '名称', dataIndex: 'name', key: 'name', width: 150 },
    {
      title: '类型',
      dataIndex: 'executor_type',
      key: 'executor_type',
      width: 90,
      render: (v: string) => (v === 'LOCAL' ? <Tag color="gold">本地</Tag> : <Tag>远程</Tag>),
    },
    { title: '资产引用（跳 O-AST）', dataIndex: 'asset_ref', key: 'asset_ref', width: 170, render: (v) => v || '-' },
    { title: '地址', key: 'addr', width: 190, render: (_, r) => `${r.host}:${r.port}` },
    {
      title: '治理状态',
      dataIndex: 'status',
      key: 'status',
      width: 110,
      render: (v: string) => <IgnExecutorStatusPill status={v} />,
    },
    {
      title: '健康（含观测时间）',
      key: 'health',
      width: 200,
      render: (_, r) => (
        <span style={{ fontSize: 12 }}>
          {r.health === 'REACHABLE' ? '可达' : r.health === 'UNREACHABLE' ? '不可达' : '未上报'}
          <br />
          {r.last_heartbeat_at || '无心跳'}
        </span>
      ),
    },
    {
      title: '当前 / 上限',
      key: 'conc',
      width: 100,
      render: (_, r) => `${r.current_tasks_count} / ${r.concurrent_tasks_max}`,
    },
    { title: '能力标签', dataIndex: 'capability_flags', key: 'capability_flags', ellipsis: true },
    {
      title: '操作',
      key: 'action',
      width: 380,
      fixed: 'right',
      render: (_, r) => {
        const removed = r.status === 'REMOVED'
        return (
          <Space size={4}>
            <Button size="small" onClick={() => openDetail(r)}>
              详情
            </Button>
            {hasPerm('ign:FUNC-14') && !removed && (
              <Popconfirm title="重新生成引导令牌？" description="旧凭据置吊销（INV-19）;锁定态一并解锁（R-04）" onConfirm={() => regenToken(r)}>
                <Button size="small" icon={<KeyOutlined />}>
                  令牌
                </Button>
              </Popconfirm>
            )}
            {hasPerm('ign:FUNC-16') && !removed && (
              <Button
                size="small"
                onClick={() => {
                  setCapTarget(r)
                  capForm.setFieldsValue({ capability_flags: (r.capability_flags || '').split(',').filter(Boolean) })
                }}
              >
                能力
              </Button>
            )}
            {hasPerm('ign:FUNC-15') && !removed && (
              <Popconfirm title="注销（空闲）该执行机？" description="要求无运行中任务;执行拆除链（R-07 次序固定）" onConfirm={async () => {
                try {
                  await ignApi.retireExecutor(r.id)
                  message.success('执行机已注销（R-07 拆除链完成）')
                  load()
                } catch {
                  // 拦截器已提示
                }
              }}>
                <Button size="small" danger icon={<StopOutlined />}>
                  注销
                </Button>
              </Popconfirm>
            )}
            {hasPerm('ign:FUNC-31') && !removed && (
              <Button size="small" danger icon={<UserDeleteOutlined />} onClick={() => openForceRemove(r)}>
                强制注销申请
              </Button>
            )}
          </Space>
        )
      },
    },
  ]

  return (
    <Card>
      <TableToolbar
        title="执行机操作台"
        total={total}
        description="执行机注册、引导与治理（AGG-IGN-03;LOCAL 由系统注册）"
        extra={
          <Space>
            <Button icon={<ReloadOutlined />} loading={loading} onClick={() => load()}>
              刷新
            </Button>
            {hasPerm('ign:FUNC-13') && (
              <Button type="primary" icon={<PlusOutlined />} onClick={() => { form.resetFields(); setCreateOpen(true) }}>
                注册远程执行机
              </Button>
            )}
          </Space>
        }
      />
      <Form form={filterForm} layout="inline" style={{ marginBottom: 12 }}>
        <Form.Item name="status" label="治理状态">
          <Select allowClear placeholder="全部" style={{ width: 150 }} options={EXECUTOR_STATUSES.map((s) => ({ value: s, label: s }))} />
        </Form.Item>
        <Form.Item name="executor_type" label="类型">
          <Select allowClear placeholder="全部" style={{ width: 120 }} options={[{ value: 'REMOTE', label: '远程' }, { value: 'LOCAL', label: '本地' }]} />
        </Form.Item>
        <Form.Item name="capability" label="能力标签">
          <Select allowClear placeholder="全部" style={{ width: 140 }} options={CAPABILITIES.map((s) => ({ value: s, label: s }))} />
        </Form.Item>
        <Form.Item name="keyword" label="关键字">
          <Input allowClear placeholder="编号 / 名称 / 地址" onPressEnter={() => load()} style={{ width: 170 }} />
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
        scroll={{ x: 1560 }}
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

      {/* 注册 */}
      <Modal title="注册远程执行机" open={createOpen} onCancel={() => setCreateOpen(false)} onOk={submitCreate} confirmLoading={saving} width={640}>
        <Alert type="info" showIcon style={{ marginBottom: 12 }} message="REMOTE 必须引用 O-AST 资产标识（INV-13,存在性核对跨中心通道未接入,本域只存标识并留痕）;人工创建 LOCAL 一律拒绝（INV-12）;引导令牌明文仅注册时一次性展示（INV-22）" />
        <Form form={form} layout="vertical">
          <Row gutter={16}>
            <Col span={12}>
              <Form.Item name="name" label="名称" rules={[{ required: true, message: '必填（INV-11 未注销范围唯一）' }]}>
                <Input placeholder="gpu-node-01" />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="display_name" label="显示名">
                <Input placeholder="GPU 节点 01" />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="host" label="服务地址" rules={[{ required: true, message: '必填（INV-11 地址+端口组合唯一）' }]}>
                <Input placeholder="192.168.3.201" />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="port" label="服务端口" rules={[{ required: true, message: '1024~65535（INV-14）' }]}>
                <Input type="number" placeholder="9801" />
              </Form.Item>
            </Col>
            <Col span={24}>
              <Form.Item name="asset_ref" label="资产标识（O-AST 引用）" rules={[{ required: true, message: 'REMOTE 必填（INV-13）' }]}>
                <Input placeholder="AST-20260101-0001" />
              </Form.Item>
            </Col>
            <Col span={24}>
              <Form.Item name="capability_flags" label="能力标签（逗号分隔,INV-18 字典校验）">
                <Select mode="multiple" placeholder="选择能力" options={CAPABILITIES.map((s) => ({ value: s, label: s }))} />
              </Form.Item>
            </Col>
          </Row>
        </Form>
      </Modal>

      {/* 令牌一次性展示 */}
      <Modal
        title={`引导令牌 · ${tokenView?.executorNo || ''}`}
        open={!!tokenView}
        onCancel={() => setTokenView(null)}
        footer={<Button type="primary" onClick={() => setTokenView(null)}>我已妥善保存</Button>}
        width={680}
      >
        <Alert type="warning" showIcon style={{ marginBottom: 12 }} message={tokenView?.note || '明文仅此一次展示（INV-22:Q-2 两处明文路径之一）,关闭后不再提供'} />
        <Input.TextArea rows={3} value={tokenView?.token || ''} readOnly style={{ fontFamily: 'monospace' }} />
      </Modal>

      {/* 能力标签调整 */}
      <Modal
        title={capTarget ? `能力标签调整 · ${capTarget.executor_no}` : '能力标签调整'}
        open={!!capTarget}
        onCancel={() => setCapTarget(null)}
        onOk={async () => {
          if (!capTarget) return
          const v = await capForm.validateFields()
          try {
            await ignApi.updateCapabilities(capTarget.id, (v.capability_flags || []).join(','))
            message.success('能力标签已调整（INV-18 字典校验通过）')
            setCapTarget(null)
            load()
          } catch {
            // 拦截器已提示
          }
        }}
      >
        <Form form={capForm} layout="vertical">
          <Form.Item name="capability_flags" label="能力标签" rules={[{ required: true, message: '至少一项（INV-18 命中 IGN_CAPABILITY）' }]}>
            <Select mode="multiple" options={CAPABILITIES.map((s) => ({ value: s, label: s }))} />
          </Form.Item>
        </Form>
      </Modal>

      {/* 强制注销申请(带保存草稿/提交) */}
      <Modal
        title={forceTarget ? `强制注销申请 · ${forceTarget.executor_no}（${forceTarget.name}）` : '强制注销申请'}
        open={!!forceTarget}
        onCancel={() => setForceTarget(null)}
        footer={
          <Space>
            <Button onClick={() => setForceTarget(null)}>关闭</Button>
            <Button loading={saving} onClick={() => saveForceRemove('draft')}>
              保存草稿
            </Button>
            <Button type="primary" danger loading={saving} onClick={() => saveForceRemove('submit')}>
              提交审批
            </Button>
          </Space>
        }
        width={680}
      >
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          message={`影响面（R-08 强制展示）:当前运行中任务 ${forceTarget?.current_tasks_count ?? 0} 个;提交后由平台运维管理员单级审批（FLOW-PROC-APP-01）,通过即取消运行任务并执行拆除链（R-07）`}
        />
        {forceTarget?.status === 'DEGRADED' && (
          <Alert type="info" showIcon style={{ marginBottom: 12 }} message="该执行机处于降级态（R-10）;取消其运行中任务属中断上游域工作,须审批通过后执行" />
        )}
        <Form form={forceForm} layout="vertical">
          <Form.Item name="reason" label="强制原因（必填,R-08 留痕）" rules={[{ required: true, message: '强制原因为必填项' }]}>
            <Input.TextArea rows={3} placeholder="执行机需要重装系统 / 证书不可恢复丢失 等" />
          </Form.Item>
        </Form>
        {forceImpact && (
          <Card size="small" title={`草稿/申请状态:${forceImpact.status}`}>
            <Button size="small" onClick={() => submitPendingApply(forceImpact)} disabled={forceImpact.status !== '草稿' && forceImpact.status !== '已驳回'}>
              提交审批
            </Button>
            <span style={{ marginLeft: 8, fontSize: 12, color: '#888' }}>申请编号 {forceImpact.apply_no}</span>
          </Card>
        )}
      </Modal>

      {/* 详情抽屉(令牌只出布尔,INV-22) */}
      <Drawer title={detail ? `执行机详情 · ${detail.executor_no}` : '执行机详情'} width={760} open={detailOpen} onClose={() => setDetailOpen(false)}>
        {detail && (
          <>
            <Descriptions column={2} size="small" bordered style={{ marginBottom: 12 }}>
              <Descriptions.Item label="执行机编号">{detail.executor_no}</Descriptions.Item>
              <Descriptions.Item label="名称">{detail.name}</Descriptions.Item>
              <Descriptions.Item label="类型">{detail.executor_type === 'LOCAL' ? '本地' : '远程'}</Descriptions.Item>
              <Descriptions.Item label="资产引用">{detail.asset_ref || '-'}</Descriptions.Item>
              <Descriptions.Item label="服务地址">{`${detail.host}:${detail.port}`}</Descriptions.Item>
              <Descriptions.Item label="治理状态">
                <IgnExecutorStatusPill status={detail.status} />
              </Descriptions.Item>
              <Descriptions.Item label="健康">
                {detail.health === 'REACHABLE' ? '可达' : detail.health === 'UNREACHABLE' ? '不可达' : '未上报'}（{detail.last_heartbeat_at || '无心跳'}）
              </Descriptions.Item>
              <Descriptions.Item label="并发额度">{`${detail.current_tasks_count} / ${detail.concurrent_tasks_max}`}</Descriptions.Item>
              <Descriptions.Item label="引导信息（令牌只出布尔,INV-22）" span={2}>
                {detail.has_active_credential ? '存在生效引导凭据' : '无生效引导凭据'}
                {detail.bootstrap_credential ? `（${detail.bootstrap_credential.credential_no} · ${detail.bootstrap_credential.cred_status} · 失败计数 ${detail.bootstrap_credential.fail_count}）` : ''}
              </Descriptions.Item>
              <Descriptions.Item label="TLS 降级声明" span={2}>
                {detail.tls_declined ? <Tag color="red">TLS 已禁用（R-11 红标）</Tag> : '正常（未降级）'}
              </Descriptions.Item>
              <Descriptions.Item label="能力标签" span={2}>{detail.capability_flags || '-'}</Descriptions.Item>
            </Descriptions>
            <Card size="small" title="证书绑定摘要（跳 O-CRT）" style={{ marginBottom: 12 }}>
              <Table
                rowKey="binding_no"
                size="small"
                pagination={false}
                columns={[
                  { title: '绑定编号', dataIndex: 'binding_no', key: 'binding_no' },
                  { title: '用途', dataIndex: 'cert_role', key: 'cert_role', width: 90 },
                  { title: '通用名称', dataIndex: 'common_name', key: 'common_name', ellipsis: true },
                  { title: '生效止', dataIndex: 'not_after', key: 'not_after', width: 165 },
                  { title: '剩余天数', dataIndex: 'days_left', key: 'days_left', width: 90 },
                  { title: '续期', dataIndex: 'renewal_count', key: 'renewal_count', width: 60 },
                  { title: '状态', dataIndex: 'bind_status', key: 'bind_status', width: 100 },
                ]}
                dataSource={(detail.tls_bindings || []) as never[]}
              />
            </Card>
            <Card size="small" title="运行中任务">
              <Table
                rowKey="task_no"
                size="small"
                pagination={false}
                columns={[
                  { title: '任务编号', dataIndex: 'task_no', key: 'task_no' },
                  { title: '动作', dataIndex: 'action', key: 'action', width: 160 },
                  { title: '状态', dataIndex: 'task_status', key: 'task_status', width: 110, render: (v: string) => <IgnTaskStatusPill status={v} /> },
                  { title: '提交人', dataIndex: 'submitted_by', key: 'submitted_by', width: 120 },
                ]}
                dataSource={(detail.running_tasks || []) as never[]}
              />
            </Card>
            {hasPerm('ign:FUNC-21') && detail.status !== 'REMOVED' && (
              <div style={{ marginTop: 12 }}>
                <Popconfirm
                  title={detail.tls_declined ? '撤销 TLS 降级声明？' : '声明 TLS 显式降级？'}
                  description="降级声明与证书绑定互斥（INV-27/R-11）;降级必须登记在案（声明 + 审计 + 界面红标）"
                  onConfirm={async () => {
                    try {
                      await ignApi.declineTls(detail.id)
                      message.success('TLS 降级声明已变更')
                      openDetail(detail)
                      load()
                    } catch {
                      // 拦截器已提示
                    }
                  }}
                >
                  <Button danger={!detail.tls_declined}>{detail.tls_declined ? '撤销降级声明' : 'TLS 显式降级声明（实验环境）'}</Button>
                </Popconfirm>
              </div>
            )}
          </>
        )}
      </Drawer>
    </Card>
  )
}
