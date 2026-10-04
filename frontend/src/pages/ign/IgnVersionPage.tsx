// MU frmIgnVersion 交付管理 · 版本目录(AGG-IGN-01;LIST_MAINTENANCE)
// 登记(FUNC-01)/ 修改(FUNC-02)/ 下载(FUNC-03)/ 归档(FUNC-04)/ 软删(FUNC-05)/
// 列表(FUNC-06,REP-01)/ 详情(FUNC-07,REP-02 含镜像清单与预推状态)。
// 规则:INV-01 名称未归档唯一;INV-04 组件版本组契约冻结;INV-05 归档冻结;INV-07 状态单向;REF-08 软删时限。
import { useCallback, useEffect, useState } from 'react'
import { Alert, Button, Card, Col, Descriptions, Drawer, Form, Input, Modal, Popconfirm, Row, Select, Space, Table, message } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { FileDoneOutlined, CloudDownloadOutlined, DeleteOutlined, EditOutlined, PlusOutlined, ProfileOutlined, ReloadOutlined, SearchOutlined } from '@ant-design/icons'
import { ignApi, type DeliveryVersion, type PortableRow } from '../../api/ign'
import TableToolbar from '../../admin/components/TableToolbar'
import { IgnVersionStatusPill } from '../../admin/components/StatusPill'
import { usePermission } from '../../hooks/usePermission'

const VERSION_STATUSES = ['DRAFT', 'DOWNLOADING', 'READY', 'PREPUSHING', 'PREPUSHED', 'PARTIALLY_PREPUSHED', 'ARCHIVED', 'FAILED']
const CNI_PLUGINS = ['CALICO', 'CILIUM', 'FLANNEL']

export default function IgnVersionPage() {
  const hasPerm = usePermission()
  const [filterForm] = Form.useForm<{ status?: string; orchestrator_version?: string; cni_plugin?: string; keyword?: string }>()
  const [form] = Form.useForm<Record<string, unknown>>()
  const [data, setData] = useState<DeliveryVersion[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [size, setSize] = useState(10)
  const [loading, setLoading] = useState(false)
  const [editingId, setEditingId] = useState<number | null>(null)
  const [modalOpen, setModalOpen] = useState(false)
  const [saving, setSaving] = useState(false)
  const [detail, setDetail] = useState<DeliveryVersion | null>(null)
  const [detailOpen, setDetailOpen] = useState(false)

  const load = useCallback(
    async (p = page, s = size) => {
      setLoading(true)
      try {
        const v = filterForm.getFieldsValue()
        const res = await ignApi.listVersions({ page: p, size: s, ...v })
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

  const openCreate = () => {
    setEditingId(null)
    form.resetFields()
    setModalOpen(true)
  }

  const openEdit = (r: DeliveryVersion) => {
    setEditingId(r.id)
    form.setFieldsValue({
      name: r.name,
      display_name: r.display_name,
      tool_version: r.tool_version,
      orchestrator_version: r.orchestrator_version,
      runtime_version: r.runtime_version,
      etcd_version: r.etcd_version,
      cni_plugin: r.cni_plugin,
      cni_version: r.cni_version,
      coredns_version: r.coredns_version,
      tarball_url: r.tarball_url,
      tarball_sha256: r.tarball_sha256,
      default_registry_ref: r.default_registry_ref,
    })
    setModalOpen(true)
  }

  const openDetail = async (r: DeliveryVersion) => {
    try {
      setDetail(await ignApi.getVersion(r.id))
      setDetailOpen(true)
    } catch {
      // 拦截器已提示
    }
  }

  const submit = async () => {
    const v = await form.validateFields()
    setSaving(true)
    try {
      if (editingId) {
        await ignApi.updateVersion(editingId, v)
        message.success('版本登记信息已修改（组件版本组契约字段不可改,INV-04）')
      } else {
        await ignApi.createVersion(v)
        message.success('版本已登记，状态置草稿')
      }
      setModalOpen(false)
      load()
    } catch {
      // 拦截器已提示
    } finally {
      setSaving(false)
    }
  }

  const act = async (fn: () => Promise<unknown>, ok: string) => {
    try {
      await fn()
      message.success(ok)
      load()
    } catch {
      // 拦截器已提示
    }
  }

  const columns: ColumnsType<DeliveryVersion> = [
    { title: '版本编号', dataIndex: 'version_no', key: 'version_no', width: 165 },
    { title: '版本名', dataIndex: 'name', key: 'name', width: 160 },
    {
      title: '组件版本组（契约,上架后冻结）',
      key: 'components',
      width: 330,
      render: (_, r) => (
        <span style={{ fontSize: 12 }}>
          {r.tool_version} / {r.orchestrator_version} / {r.runtime_version} / etcd {r.etcd_version} / {r.cni_plugin} {r.cni_version} / coredns {r.coredns_version}
        </span>
      ),
    },
    { title: '发行包地址', dataIndex: 'tarball_url', key: 'tarball_url', ellipsis: true, width: 220 },
    {
      title: '状态',
      dataIndex: 'version_status',
      key: 'version_status',
      width: 110,
      render: (v: string) => <IgnVersionStatusPill status={v} />,
    },
    { title: '归档时间', dataIndex: 'archived_at', key: 'archived_at', width: 170, render: (v) => v || '-' },
    {
      title: '操作',
      key: 'action',
      width: 330,
      fixed: 'right',
      render: (_, r) => {
        const archived = r.version_status === 'ARCHIVED'
        return (
          <Space size={4}>
            <Button size="small" icon={<ProfileOutlined />} onClick={() => openDetail(r)}>
              详情
            </Button>
            {hasPerm('ign:FUNC-02') && !archived && (
              <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(r)}>
                修改
              </Button>
            )}
            {hasPerm('ign:FUNC-03') && (r.version_status === 'DRAFT' || r.version_status === 'FAILED') && (
              <Button size="small" icon={<CloudDownloadOutlined />} onClick={() => act(() => ignApi.downloadVersion(r.id), '下载已触发（R-02:摘要提供即强校验）')}>
                下载
              </Button>
            )}
            {hasPerm('ign:FUNC-04') && ['READY', 'PREPUSHED', 'PARTIALLY_PREPUSHED'].includes(r.version_status) && (
              <Popconfirm title="归档该版本？" description="归档后登记字段冻结（INV-05,终态冻结）" onConfirm={() => act(() => ignApi.archiveVersion(r.id), '版本已归档')}>
                <Button size="small" icon={<FileDoneOutlined />}>
                  归档
                </Button>
              </Popconfirm>
            )}
            {hasPerm('ign:FUNC-05') && archived && (
              <Popconfirm
                title="软删除该版本？"
                description="仅已归档且超过平台配置天数（REF-08 缺省 30 天）;删除提示仓库侧镜像清理清单（R-01）"
                onConfirm={() => act(() => ignApi.deleteVersion(r.id), '版本已软删除')}
              >
                <Button size="small" danger icon={<DeleteOutlined />}>
                  删除
                </Button>
              </Popconfirm>
            )}
          </Space>
        )
      },
    },
  ]

  return (
    <Card>
      <TableToolbar
        title="版本目录"
        total={total}
        description="交付版本编目与上架（AGG-IGN-01;组件版本组为集群契约字段）"
        extra={
          <Space>
            <Button icon={<ReloadOutlined />} loading={loading} onClick={() => load()}>
              刷新
            </Button>
            {hasPerm('ign:FUNC-01') && (
              <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
                版本登记
              </Button>
            )}
          </Space>
        }
      />
      <Form form={filterForm} layout="inline" style={{ marginBottom: 12 }}>
        <Form.Item name="status" label="版本状态">
          <Select allowClear placeholder="全部" style={{ width: 150 }} options={VERSION_STATUSES.map((s) => ({ value: s, label: s }))} />
        </Form.Item>
        <Form.Item name="orchestrator_version" label="编排引擎版本">
          <Input allowClear placeholder="如 v1.30" onPressEnter={() => load()} style={{ width: 140 }} />
        </Form.Item>
        <Form.Item name="cni_plugin" label="CNI">
          <Select allowClear placeholder="全部" style={{ width: 130 }} options={CNI_PLUGINS.map((s) => ({ value: s, label: s }))} />
        </Form.Item>
        <Form.Item name="keyword" label="关键字">
          <Input allowClear placeholder="编号 / 名称" onPressEnter={() => load()} style={{ width: 160 }} />
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

      <Modal
        title={editingId ? '修改版本登记信息（组件版本组只读）' : '版本登记'}
        open={modalOpen}
        onCancel={() => setModalOpen(false)}
        onOk={submit}
        confirmLoading={saving}
        width={760}
      >
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 12 }}
          message="组件版本组六项 + CoreDNS 为集群契约字段,创建后不可修改（INV-04）;版本名在未归档范围内唯一（INV-01,保存即校验）"
        />
        <Form form={form} layout="vertical">
          <Row gutter={16}>
            <Col span={12}>
              <Form.Item name="name" label="版本名" rules={[{ required: true, message: '必填（INV-01 未归档范围唯一）' }]}>
                <Input placeholder="k8s-1.30.4-ezctl" />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="display_name" label="显示名">
                <Input placeholder="Kubernetes 1.30.4 交付版" />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="tool_version" label="交付工具版本" rules={[{ required: true, message: '语义化版本 x.y.z（INV-02）' }]}>
                <Input disabled={!!editingId} placeholder="2.24.0" />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="orchestrator_version" label="编排引擎版本" rules={[{ required: true, message: 'v1.x.y（INV-02）' }]}>
                <Input disabled={!!editingId} placeholder="v1.30.4" />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="runtime_version" label="容器运行时版本" rules={[{ required: true, message: '必填（INV-02）' }]}>
                <Input disabled={!!editingId} placeholder="1.7.24" />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="etcd_version" label="etcd 版本" rules={[{ required: true, message: '必填（INV-02）' }]}>
                <Input disabled={!!editingId} placeholder="3.5.12" />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="cni_plugin" label="CNI 插件" rules={[{ required: true, message: '命中 IGN_CNI_PLUGIN 字典（INV-03）' }]}>
                <Select disabled={!!editingId} placeholder="选择插件" options={CNI_PLUGINS.map((s) => ({ value: s, label: s }))} />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="cni_version" label="CNI 版本" rules={[{ required: true, message: '必填（INV-02）' }]}>
                <Input disabled={!!editingId} placeholder="3.28.2" />
              </Form.Item>
            </Col>
            <Col span={8}>
              <Form.Item name="coredns_version" label="CoreDNS 版本" rules={[{ required: true, message: '必填（INV-02）' }]}>
                <Input disabled={!!editingId} placeholder="1.11.1" />
              </Form.Item>
            </Col>
            <Col span={24}>
              <Form.Item name="tarball_url" label="发行包地址" rules={[{ required: true, message: '合法 URL（INV-02）' }]}>
                <Input placeholder="https://repo.example.com/opic/ezctl-2.24.0.tar.gz" />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="tarball_sha256" label="发行包摘要（可选,提供即强校验 R-02）">
                <Input placeholder="64 位十六进制" />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item name="default_registry_ref" label="默认仓库项目（跨中心引用只存标识）">
                <Input placeholder="opic-release" />
              </Form.Item>
            </Col>
          </Row>
        </Form>
      </Modal>

      <Drawer title={detail ? `版本详情 · ${detail.version_no}` : '版本详情'} width={760} open={detailOpen} onClose={() => setDetailOpen(false)}>
        {detail && (
          <>
            <Descriptions column={2} size="small" bordered style={{ marginBottom: 12 }}>
              <Descriptions.Item label="版本编号">{detail.version_no}</Descriptions.Item>
              <Descriptions.Item label="状态">
                <IgnVersionStatusPill status={detail.version_status} />
              </Descriptions.Item>
              <Descriptions.Item label="组件版本组" span={2}>
                {detail.tool_version} / {detail.orchestrator_version} / {detail.runtime_version} / etcd {detail.etcd_version} / {detail.cni_plugin} {detail.cni_version} / coredns {detail.coredns_version}
              </Descriptions.Item>
              <Descriptions.Item label="发行包地址" span={2}>
                {detail.tarball_url}
              </Descriptions.Item>
              <Descriptions.Item label="缓存路径">{detail.cache_path || '尚未就绪'}</Descriptions.Item>
              <Descriptions.Item label="默认仓库项目">{detail.default_registry_ref || '-'}</Descriptions.Item>
              <Descriptions.Item label="归档时间">{detail.archived_at || '-'}</Descriptions.Item>
            </Descriptions>
            <Card size="small" title="镜像清单与预推状态" style={{ marginBottom: 12 }}>
              <Table
                rowKey={(r: PortableRow) => `${r.repo}:${r.tag}`}
                size="small"
                pagination={false}
                columns={[
                  { title: '镜像坐标', key: 'coord', render: (_, r: PortableRow) => `${r.repo}:${r.tag}` },
                  { title: '摘要', dataIndex: 'digest', key: 'digest', ellipsis: true, render: (v) => v || '-' },
                  { title: '体积', dataIndex: 'size_bytes', key: 'size_bytes', width: 110, render: (v) => (v ? `${(Number(v) / 1048576).toFixed(1)} MB` : '-') },
                  { title: '预推状态', dataIndex: 'prepush_status', key: 'prepush_status', width: 110 },
                  { title: '失败原因', dataIndex: 'fail_reason', key: 'fail_reason', ellipsis: true, render: (v) => v || '-' },
                  { title: '最近预推', dataIndex: 'prepushed_at', key: 'prepushed_at', width: 165, render: (v) => v || '-' },
                ]}
                dataSource={(detail.prepush_items || []) as never[]}
              />
            </Card>
            <Card size="small" title="镜像清单原文(JSON)">
              <pre style={{ maxHeight: 220, overflow: 'auto', fontSize: 12 }}>{JSON.stringify(detail.manifest ?? [], null, 2)}</pre>
            </Card>
          </>
        )}
      </Drawer>
    </Card>
  )
}
