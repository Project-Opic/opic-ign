// 待办(流程引擎维度):执行机强制注销审批(FLOW-PROC-APP-01,单级审批);
// 摘要取 flow_instance.business_key(申请编号)与 ign_force_remove/executor(申请台账)。
// 通过 → FUNC-32(取消运行任务 + 拆除链 R-07 次序固定);驳回 → FUNC-33(状态不变,意见留痕)。
import { useEffect, useState } from 'react'
import { Button, Card, Descriptions, Input, Modal, Space, Table, message } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { ReloadOutlined } from '@ant-design/icons'
import { workbenchApi, type TodoItem } from '../../api/flow'
import TableToolbar from '../../admin/components/TableToolbar'
import { IgnApplyStatusPill, IgnExecutorStatusPill } from '../../admin/components/StatusPill'

type PendingAction = 'approve' | 'reject' | 'return'

export default function Todo() {
  const [data, setData] = useState<TodoItem[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [size, setSize] = useState(10)
  const [loading, setLoading] = useState(false)
  const [current, setCurrent] = useState<TodoItem | null>(null)
  const [comment, setComment] = useState('')
  const [acting, setActing] = useState(false)

  const load = async (p = page, s = size) => {
    setLoading(true)
    try {
      const res = await workbenchApi.todo({ page: p, size: s })
      setData(res.list || [])
      setTotal(res.total || 0)
    } catch {
      // 拦截器已提示
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [page, size])

  const doAction = async (action: PendingAction) => {
    if (!current) return
    setActing(true)
    try {
      if (action === 'approve') await workbenchApi.approve(current.id, comment)
      else if (action === 'reject') await workbenchApi.reject(current.id, comment)
      else await workbenchApi.returnTask(current.id, comment)
      message.success(
        action === 'approve'
          ? '审批通过:已取消运行中任务并执行拆除链（R-07 次序固定）'
          : action === 'reject'
            ? '已驳回:执行机状态不变,意见留痕'
            : '已退回',
      )
      setCurrent(null)
      setComment('')
      load()
    } catch {
      // 拦截器已提示
    } finally {
      setActing(false)
    }
  }

  const columns: ColumnsType<TodoItem> = [
    {
      title: '申请编号',
      dataIndex: 'apply_no',
      key: 'apply_no',
      width: 165,
      render: (v: string | undefined, r) => v || r.business_key || '-',
    },
    { title: '执行机', dataIndex: 'executor_no', key: 'executor_no', width: 165, render: (v: string | undefined, r) => v || '-' },
    { title: '名称', dataIndex: 'executor_name', key: 'executor_name', width: 140, render: (v) => v || '-' },
    {
      title: '执行机状态',
      dataIndex: 'executor_status',
      key: 'executor_status',
      width: 110,
      render: (v: string | undefined) => (v ? <IgnExecutorStatusPill status={v} /> : '-'),
    },
    { title: '强制原因', dataIndex: 'reason', key: 'reason', ellipsis: true },
    {
      title: '申请单状态',
      dataIndex: 'apply_status',
      key: 'apply_status',
      width: 150,
      render: (v: string | undefined) => (v ? <IgnApplyStatusPill status={v} /> : '-'),
    },
    { title: '审批节点', dataIndex: 'activity_name', key: 'activity_name', width: 180 },
    { title: '提交人', dataIndex: 'creator_name', key: 'creator_name', width: 110, render: (v) => v || '-' },
    { title: '提交时间', dataIndex: 'started_at', key: 'started_at', width: 170, render: (v) => v || '-' },
    {
      title: '操作',
      key: 'action',
      width: 100,
      render: (_, record) => (
        <Button
          type="primary"
          size="small"
          onClick={() => {
            setCurrent(record)
            setComment('')
          }}
        >
          处理
        </Button>
      ),
    },
  ]

  return (
    <Card>
      <TableToolbar
        title="我的待办"
        total={total}
        extra={
          <Button icon={<ReloadOutlined />} onClick={() => load()}>
            刷新
          </Button>
        }
      />
      <Table
        rowKey="id"
        columns={columns}
        dataSource={data}
        loading={loading}
        scroll={{ x: 1300 }}
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

      <Modal
        title="执行机强制注销审批（FLOW-PROC-APP-01，单级审批）"
        open={!!current}
        onCancel={() => setCurrent(null)}
        footer={
          <Space>
            <Button danger loading={acting} onClick={() => doAction('reject')}>
              驳回
            </Button>
            <Button loading={acting} onClick={() => doAction('return')}>
              退回
            </Button>
            <Button type="primary" loading={acting} onClick={() => doAction('approve')}>
              通过
            </Button>
          </Space>
        }
        width={640}
      >
        <Descriptions column={2} size="small" style={{ marginBottom: 12 }}>
          <Descriptions.Item label="申请编号">{current?.apply_no || current?.business_key || '-'}</Descriptions.Item>
          <Descriptions.Item label="执行机">{current?.executor_no || '-'}（{current?.executor_name || '-'}）</Descriptions.Item>
          <Descriptions.Item label="执行机状态" span={2}>
            {current?.executor_status ? <IgnExecutorStatusPill status={current.executor_status} /> : '-'}
          </Descriptions.Item>
          <Descriptions.Item label="强制原因" span={2}>
            {current?.reason || '-'}
          </Descriptions.Item>
          <Descriptions.Item label="影响面（运行中任务,R-08 强制展示）" span={2}>
            {(() => {
              const snap = current?.impact_snapshot as { running_count?: number; running_tasks?: { task_no?: string; action?: string; cluster_ref?: string }[] } | undefined
              if (!snap || !snap.running_tasks?.length) return '无运行中任务'
              return `${snap.running_count ?? snap.running_tasks.length} 个：` + snap.running_tasks.map((t) => `${t.task_no}（${t.action} @ ${t.cluster_ref || '-'}）`).join('、')
            })()}
          </Descriptions.Item>
          <Descriptions.Item label="审批节点" span={2}>
            {current?.activity_name}（平台运维管理员 · 通过时先取消运行任务再执行拆除链 R-07）
          </Descriptions.Item>
        </Descriptions>
        <p style={{ color: '#888', fontSize: 12 }}>
          通过 → FUNC-32 取消运行中任务并执行拆除链（吊销双证书 → 凭据置已吊销 → 状态置已注销,R-07 次序固定）；驳回 → FUNC-33 执行机状态不变、意见留痕。
        </p>
        <Input.TextArea
          rows={3}
          value={comment}
          onChange={(e) => setComment(e.target.value)}
          placeholder="审批意见（驳回建议填写）"
        />
      </Modal>
    </Card>
  )
}
