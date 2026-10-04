// MU frmIgnTls 交付管理 · 通道安全台(AGG-IGN-05;LIST_MAINTENANCE)
// 手动续期(FUNC-20)/ 降级声明(FUNC-21,入口在执行机详情)/ 清单查询(FUNC-22,REP-08 含临期视图)。
// 规则:INV-23 同机同用途唯一生效;INV-24 起止;INV-25 REVOKED 终态;INV-26 续期只增;REF-05/06/07。
import { useCallback, useEffect, useState } from 'react'
import { Alert, Button, Card, Form, Input, Popconfirm, Select, Space, Table, Tag, message } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { ReloadOutlined, SearchOutlined } from '@ant-design/icons'
import { ignApi, type TlsBinding } from '../../api/ign'
import TableToolbar from '../../admin/components/TableToolbar'
import { IgnTlsStatusPill } from '../../admin/components/StatusPill'
import { usePermission } from '../../hooks/usePermission'

export default function IgnTlsPage() {
  const hasPerm = usePermission()
  const [filterForm] = Form.useForm<{ cert_role?: string; bind_status?: string; expiring?: string; keyword?: string }>()
  const [data, setData] = useState<TlsBinding[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [size, setSize] = useState(10)
  const [loading, setLoading] = useState(false)
  const [expiringOnly, setExpiringOnly] = useState(false)

  const load = useCallback(
    async (p = page, s = size) => {
      setLoading(true)
      try {
        const v = filterForm.getFieldsValue()
        const res = await ignApi.listTls({ page: p, size: s, ...v, expiring: expiringOnly ? 'true' : '' })
        setData(res.list || [])
        setTotal(res.total || 0)
      } catch {
        // 拦截器已提示
      } finally {
        setLoading(false)
      }
    },
    [page, size, filterForm, expiringOnly],
  )

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [page, size, expiringOnly])

  const renew = async (r: TlsBinding) => {
    try {
      await ignApi.renewTls(r.id)
      message.success('续期已执行（续期计数 +1,INV-26）')
      load()
    } catch {
      // 拦截器已提示
    }
  }

  const columns: ColumnsType<TlsBinding> = [
    { title: '绑定编号', dataIndex: 'binding_no', key: 'binding_no', width: 165 },
    {
      title: '执行机',
      key: 'executor',
      width: 200,
      render: (_, r) => `${r.executor_no || ''}（${r.executor_name || ''}）`,
    },
    {
      title: '用途',
      dataIndex: 'cert_role',
      key: 'cert_role',
      width: 90,
      render: (v: string) => (v === 'SERVER' ? <Tag color="blue">服务端</Tag> : <Tag>客户端</Tag>),
    },
    { title: '通用名称', dataIndex: 'common_name', key: 'common_name', ellipsis: true },
    { title: '凭据标识（跳 O-CRT）', dataIndex: 'cert_ref', key: 'cert_ref', width: 190, ellipsis: true, render: (v) => v || '-' },
    {
      title: '生效起止',
      key: 'validity',
      width: 300,
      render: (_, r) => `${r.not_before} ~ ${r.not_after}`,
    },
    {
      title: '剩余天数（即时计算）',
      dataIndex: 'days_left',
      key: 'days_left',
      width: 140,
      render: (v: number | undefined, r) => {
        if (v === undefined) return '-'
        const urgent = v <= 14
        return <span style={{ color: urgent ? '#cf1322' : undefined }}>{v} 天{urgent ? '（临期,REF-07）' : ''}</span>
      },
    },
    { title: '续期次数', dataIndex: 'renewal_count', key: 'renewal_count', width: 90 },
    {
      title: '状态',
      dataIndex: 'bind_status',
      key: 'bind_status',
      width: 110,
      render: (v: string) => <IgnTlsStatusPill status={v} />,
    },
    {
      title: '操作',
      key: 'action',
      width: 120,
      fixed: 'right',
      render: (_, r) =>
        hasPerm('ign:FUNC-20') && r.bind_status !== 'REVOKED' ? (
          <Popconfirm title="手动触发续期？" description="重签请求登记 + 事实快照更新;证书热加载推送经 O-CRT 通道（未接入,留痕）" onConfirm={() => renew(r)}>
            <Button size="small">续期</Button>
          </Popconfirm>
        ) : (
          '-'
        ),
    },
  ]

  return (
    <Card>
      <TableToolbar
        title="通道安全台"
        total={total}
        description="执行机通道证书绑定（AGG-IGN-05;剩余天数即时计算不落库 REF-06）"
        extra={
          <Space>
            <Button icon={<ReloadOutlined />} loading={loading} onClick={() => load()}>
              刷新
            </Button>
            <Button type={expiringOnly ? 'primary' : 'default'} onClick={() => setExpiringOnly(!expiringOnly)}>
              临期视图（{expiringOnly ? '已开启' : '未开启'}）
            </Button>
          </Space>
        }
      />
      {expiringOnly && (
        <Alert type="warning" showIcon style={{ marginBottom: 12 }} message="临期视图:展示生效止落在 REF-07 提前天数（缺省 14 天,平台配置可调）内的绑定" />
      )}
      <Form form={filterForm} layout="inline" style={{ marginBottom: 12 }}>
        <Form.Item name="cert_role" label="证书用途">
          <Select allowClear placeholder="全部" style={{ width: 130 }} options={[{ value: 'SERVER', label: '服务端' }, { value: 'CLIENT', label: '客户端' }]} />
        </Form.Item>
        <Form.Item name="bind_status" label="状态">
          <Select
            allowClear
            placeholder="全部"
            style={{ width: 140 }}
            options={['PENDING', 'ACTIVE', 'RENEWING', 'REVOKED', 'FAILED'].map((s) => ({ value: s, label: s }))}
          />
        </Form.Item>
        <Form.Item name="keyword" label="执行机">
          <Input allowClear placeholder="编号 / 名称 / CN" onPressEnter={() => load()} style={{ width: 180 }} />
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
        scroll={{ x: 1480 }}
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
