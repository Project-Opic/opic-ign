// 我的申请(本中心=我提交的执行机强制注销申请;FLOW-PROC-APP-01)。
// 撤回入口:仅待审批且审批人未处理时可撤回(无写入,执行机状态不变)。
import { useEffect, useState } from 'react'
import { Button, Card, Popconfirm, Table, message } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { ReloadOutlined } from '@ant-design/icons'
import { workbenchApi, type RequestedItem } from '../../api/flow'
import { ignApi } from '../../api/ign'
import TableToolbar from '../../admin/components/TableToolbar'
import { IgnApplyStatusPill, IgnExecutorStatusPill, InstanceStatusPill } from '../../admin/components/StatusPill'

export default function Requested() {
  const [data, setData] = useState<RequestedItem[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [size, setSize] = useState(10)
  const [loading, setLoading] = useState(false)

  const load = async (p = page, s = size) => {
    setLoading(true)
    try {
      const res = await workbenchApi.requested({ page: p, size: s })
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

  const withdraw = async (r: RequestedItem) => {
    try {
      const applies = await ignApi.listForceRemove({ page: 1, size: 100, status: '待平台运维审批' })
      const target = (applies.list || []).find((c) => c.instance_id === r.instance_id)
      if (!target) {
        message.warning('未找到对应的待审申请（可能已被处理）')
        return
      }
      await ignApi.withdrawForceRemove(target.id)
      message.success('已撤回（无写入；执行机状态不变）')
      load()
    } catch {
      // 拦截器已提示
    }
  }

  const columns: ColumnsType<RequestedItem> = [
    { title: '申请编号', dataIndex: 'apply_no', key: 'apply_no', width: 165 },
    { title: '执行机', dataIndex: 'executor_no', key: 'executor_no', width: 165, render: (v) => v || '-' },
    {
      title: '执行机状态',
      dataIndex: 'executor_status',
      key: 'executor_status',
      width: 110,
      render: (v: string | null) => (v ? <IgnExecutorStatusPill status={v} /> : '-'),
    },
    { title: '强制原因', dataIndex: 'reason', key: 'reason', ellipsis: true },
    {
      title: '申请单状态',
      dataIndex: 'apply_status',
      key: 'apply_status',
      width: 150,
      render: (v: string | null) => (v ? <IgnApplyStatusPill status={v} /> : '-'),
    },
    {
      title: '流程状态',
      dataIndex: 'flow_status',
      key: 'flow_status',
      width: 120,
      render: (v: string | undefined) => (v ? <InstanceStatusPill status={v} /> : '-'),
    },
    { title: '审批意见', dataIndex: 'approve_comment', key: 'approve_comment', width: 160, render: (v) => v || '-' },
    {
      title: '审批人 / 时间',
      key: 'approved',
      width: 220,
      render: (_, r) => (r.approved_name ? `${r.approved_name} · ${r.approved_at || ''}` : '-'),
    },
    { title: '提交时间', dataIndex: 'started_at', key: 'started_at', width: 170, render: (v) => v || '-' },
    {
      title: '操作',
      key: 'action',
      width: 100,
      render: (_, r) =>
        r.apply_status === '待平台运维审批' ? (
          <Popconfirm title={`确认撤回申请「${r.apply_no}」？`} description="撤回无写入，执行机状态不变" onConfirm={() => withdraw(r)}>
            <Button type="link" size="small" danger>
              撤回
            </Button>
          </Popconfirm>
        ) : (
          '-'
        ),
    },
  ]

  return (
    <Card>
      <TableToolbar
        title="我的申请"
        total={total}
        description="执行机强制注销申请（唯一审批流 FLOW-PROC-APP-01）"
        extra={
          <Button icon={<ReloadOutlined />} onClick={() => load()}>
            刷新
          </Button>
        }
      />
      <Table
        rowKey="instance_id"
        columns={columns}
        dataSource={data}
        loading={loading}
        scroll={{ x: 1400 }}
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
    </Card>
  )
}
