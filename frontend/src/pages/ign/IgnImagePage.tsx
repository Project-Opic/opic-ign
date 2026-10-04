// MU frmIgnImage 交付管理 · 发行包与镜像(AGG-IGN-02;LIST_MAINTENANCE)
// 触发下载(FUNC-03)/ 触发预推(FUNC-09)/ 清单覆写(FUNC-10)/ 断点续推(FUNC-11)/ 预推明细查询(FUNC-12,REP-04)。
// 规则:INV-08 同版本仓库+标签唯一、明细由清单派生;INV-09/10 状态-原因口径;REF-04 并发上限热生效。
import { useCallback, useEffect, useState } from 'react'
import { Button, Card, Form, Input, Modal, Select, Space, Table, message } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { CloudDownloadOutlined, EditOutlined, ReloadOutlined, RocketOutlined, SearchOutlined, ThunderboltOutlined } from '@ant-design/icons'
import { ignApi, type DeliveryVersion, type PrepushItem } from '../../api/ign'
import TableToolbar from '../../admin/components/TableToolbar'
import { IgnPrepushStatusPill, IgnVersionStatusPill } from '../../admin/components/StatusPill'
import { usePermission } from '../../hooks/usePermission'

const PREPUSH_STATUSES = ['PENDING', 'SUCCESS', 'FAILED', 'SKIPPED']

export default function IgnImagePage() {
  const hasPerm = usePermission()
  const [filterForm] = Form.useForm<{ version_no?: string; prepush_status?: string }>()
  const [versions, setVersions] = useState<DeliveryVersion[]>([])
  const [data, setData] = useState<PrepushItem[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [size, setSize] = useState(10)
  const [loading, setLoading] = useState(false)
  const [manifestOpen, setManifestOpen] = useState(false)
  const [manifestText, setManifestText] = useState('[]')
  const [manifestTarget, setManifestTarget] = useState<DeliveryVersion | null>(null)
  const [saving, setSaving] = useState(false)

  const loadVersions = useCallback(async () => {
    try {
      const res = await ignApi.listVersions({ page: 1, size: 100 })
      setVersions(res.list || [])
    } catch {
      // 拦截器已提示
    }
  }, [])

  const load = useCallback(
    async (p = page, s = size) => {
      setLoading(true)
      try {
        const v = filterForm.getFieldsValue()
        const res = await ignApi.listPrepush({ page: p, size: s, ...v })
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
    loadVersions()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [page, size])

  const actOnVersion = async (versionNo: string | undefined, fn: (v: DeliveryVersion) => Promise<unknown>, ok: string) => {
    if (!versionNo) {
      message.warning('请先在筛选栏选择版本')
      return
    }
    const target = versions.find((x) => x.version_no === versionNo)
    if (!target) {
      message.warning('未找到对应版本')
      return
    }
    try {
      await fn(target)
      message.success(ok)
      load()
    } catch {
      // 拦截器已提示
    }
  }

  const openManifest = async (versionNo: string | undefined) => {
    if (!versionNo) {
      message.warning('请先在筛选栏选择版本')
      return
    }
    const target = versions.find((x) => x.version_no === versionNo)
    if (!target) return
    try {
      const full = await ignApi.getVersion(target.id)
      setManifestTarget(full)
      setManifestText(JSON.stringify(full.manifest ?? [], null, 2))
      setManifestOpen(true)
    } catch {
      // 拦截器已提示
    }
  }

  const saveManifest = async () => {
    if (!manifestTarget) return
    setSaving(true)
    try {
      await ignApi.overwriteManifest(manifestTarget.id, manifestText)
      message.success('镜像清单已覆写（预推明细同步对齐,INV-08）')
      setManifestOpen(false)
      load()
    } catch {
      // 拦截器已提示
    } finally {
      setSaving(false)
    }
  }

  const versionSelect = (
    <Form.Item name="version_no" label="版本">
      <Select
        allowClear
        showSearch
        optionFilterProp="label"
        placeholder="选择版本"
        style={{ width: 240 }}
        options={versions.map((v) => ({ value: v.version_no, label: `${v.version_no}（${v.name}·${v.version_status}）` }))}
      />
    </Form.Item>
  )

  const columns: ColumnsType<PrepushItem> = [
    { title: '版本', dataIndex: 'version_no', key: 'version_no', width: 165 },
    {
      title: '镜像坐标',
      key: 'coord',
      width: 320,
      render: (_, r) => `${r.repo}:${r.tag}`,
    },
    { title: '摘要', dataIndex: 'digest', key: 'digest', ellipsis: true, render: (v) => v || '-' },
    { title: '体积', dataIndex: 'size_bytes', key: 'size_bytes', width: 110, render: (v) => (v ? `${(Number(v) / 1048576).toFixed(1)} MB` : '-') },
    {
      title: '预推状态',
      dataIndex: 'prepush_status',
      key: 'prepush_status',
      width: 110,
      render: (v: string) => <IgnPrepushStatusPill status={v} />,
    },
    { title: '失败原因 / 跳过依据', dataIndex: 'fail_reason', key: 'fail_reason', ellipsis: true, render: (v) => v || '-' },
    { title: '最近预推时间', dataIndex: 'prepushed_at', key: 'prepushed_at', width: 170, render: (v) => v || '-' },
  ]

  return (
    <Card>
      <TableToolbar
        title="发行包与镜像"
        total={total}
        description="发行包下载与镜像预推台（AGG-IGN-01/02;预推明细由镜像清单派生）"
        extra={
          <Space wrap>
            <Button icon={<ReloadOutlined />} loading={loading} onClick={() => load()}>
              刷新
            </Button>
            {hasPerm('ign:FUNC-03') && (
              <Button
                icon={<CloudDownloadOutlined />}
                onClick={() =>
                  actOnVersion(
                    filterForm.getFieldValue('version_no'),
                    (v) => ignApi.downloadVersion(v.id),
                    '下载已触发（R-02:摘要提供即强校验;校验通过才置就绪）',
                  )
                }
              >
                触发下载
              </Button>
            )}
            {hasPerm('ign:FUNC-09') && (
              <Button
                type="primary"
                icon={<RocketOutlined />}
                onClick={() =>
                  actOnVersion(
                    filterForm.getFieldValue('version_no'),
                    (v) => ignApi.startPrepush(v.id),
                    '镜像预推已触发（REF-04 并发上限热生效）',
                  )
                }
              >
                触发预推
              </Button>
            )}
            {hasPerm('ign:FUNC-11') && (
              <Button
                icon={<ThunderboltOutlined />}
                onClick={() =>
                  actOnVersion(filterForm.getFieldValue('version_no'), (v) => ignApi.retryPrepush(v.id), '断点续推已触发（只补失败明细）')
                }
              >
                断点续推
              </Button>
            )}
            {hasPerm('ign:FUNC-10') && (
              <Button icon={<EditOutlined />} onClick={() => openManifest(filterForm.getFieldValue('version_no'))}>
                镜像清单覆写
              </Button>
            )}
          </Space>
        }
      />
      <Form form={filterForm} layout="inline" style={{ marginBottom: 12 }}>
        {versionSelect}
        <Form.Item name="prepush_status" label="预推状态">
          <Select allowClear placeholder="全部" style={{ width: 140 }} options={PREPUSH_STATUSES.map((s) => ({ value: s, label: s }))} />
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
        scroll={{ x: 1250 }}
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
        title={manifestTarget ? `镜像清单覆写 · ${manifestTarget.version_no}（${manifestTarget.name}）` : '镜像清单覆写'}
        open={manifestOpen}
        onCancel={() => setManifestOpen(false)}
        onOk={saveManifest}
        confirmLoading={saving}
        width={720}
      >
        <p style={{ color: '#888', fontSize: 12 }}>
          以整份清单为准,覆写后预推明细同步对齐（INV-08:同版本内仓库+标签联合唯一）;元素含 repo / tag / digest / size_bytes。
          {manifestTarget && <> 当前状态:<IgnVersionStatusPill status={manifestTarget.version_status} /></>}
        </p>
        <Input.TextArea rows={14} value={manifestText} onChange={(e) => setManifestText(e.target.value)} style={{ fontFamily: 'monospace' }} />
      </Modal>
    </Card>
  )
}
