// MU frmIgnTemplate 交付管理 · 参数面模板(AGG-IGN-07;MASTER_DETAIL_FORM)
// 列表(FUNC-38,REP-15)/ 详情三段分区(FUNC-39,REP-16 模板头+字段行整组+被引用出处)/
// 登记(FUNC-40)/ 字段行整组替换(FUNC-41,R-22 无单行 CUD)/ 启用停用(FUNC-42)/ 设为默认(FUNC-43,仅 IGN-ROLE-02)/ 软删除(FUNC-44,R-25 出处清单)。
// 不变量(默认须启用 INV-43 / 启用空模板即拒 / 面外键治理拒绝 INV-41 / 删除被引用即拒 R-25)的中文提示一律由后端给出、
// 经响应拦截器原样透出;本屏不重写规则文案,成功态短提示仅做动作确认。
import { useCallback, useEffect, useState } from 'react'
import {
  Alert,
  Badge,
  Button,
  Card,
  Col,
  Descriptions,
  Drawer,
  Form,
  Input,
  Modal,
  Row,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
  message,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import {
  DeleteOutlined,
  EditOutlined,
  ExportOutlined,
  PauseCircleOutlined,
  PlusOutlined,
  ProfileOutlined,
  ReloadOutlined,
  RightOutlined,
  SaveOutlined,
  SearchOutlined,
  StarOutlined,
} from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import { ignApi, type IgnParamTemplate, type ParamTemplateItemRow, type ParamTemplateReference } from '../../api/ign'
import TableToolbar from '../../admin/components/TableToolbar'
import { IgnTemplateStatePill } from '../../admin/components/StatusPill'
import { usePermission } from '../../hooks/usePermission'
import { useParamFace } from '../../hooks/useParamFace'

/** 治理动作类型（四类动作同口径必须携带事由——后端强校验，本屏表单 required 只是先行拦截） */
type GovernAction = 'enable' | 'disable' | 'default' | 'delete'

const GOVERN_META: Record<GovernAction, { title: string; ok: string }> = {
  enable: { title: '启用模板', ok: '模板已启用' },
  disable: { title: '停用模板', ok: '模板已停用（已生效取值不作废，R-25）' },
  default: { title: '设为默认', ok: '已设为默认（同面原默认行自动取消标记，INV-43）' },
  delete: { title: '软删除模板', ok: '模板已软删除（字段行随聚合级联）' },
}

export default function IgnParamTemplatePage() {
  const hasPerm = usePermission()
  const paramFace = useParamFace()
  const navigate = useNavigate()
  const [filterForm] = Form.useForm<{ file_face?: string; status?: string; keyword?: string }>()
  const [showDisabled, setShowDisabled] = useState(false)
  const [data, setData] = useState<IgnParamTemplate[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [size, setSize] = useState(10)
  const [loading, setLoading] = useState(false)

  const [createOpen, setCreateOpen] = useState(false)
  const [createForm] = Form.useForm<{ template_name: string; file_face: string; description?: string }>()
  const [saving, setSaving] = useState(false)

  const [detail, setDetail] = useState<IgnParamTemplate | null>(null)
  const [detailOpen, setDetailOpen] = useState(false)
  const [editingItems, setEditingItems] = useState(false)
  const [editRows, setEditRows] = useState<{ field_key: string; field_value: string }[]>([])
  const [itemsForm] = Form.useForm<{ reason: string }>()
  const [itemsSaving, setItemsSaving] = useState(false)

  const [gov, setGov] = useState<{ action: GovernAction; row: IgnParamTemplate } | null>(null)
  const [govForm] = Form.useForm<{ reason: string }>()

  const load = useCallback(
    async (p = page, s = size) => {
      setLoading(true)
      try {
        const v = filterForm.getFieldsValue()
        const res = await ignApi.listParamTemplates({
          page: p,
          size: s,
          file_face: v.file_face,
          status: v.status,
          keyword: v.keyword,
          show_disabled: showDisabled && !v.status ? '1' : undefined,
        })
        setData(res.list || [])
        setTotal(res.total || 0)
      } catch {
        // 拦截器已提示
      } finally {
        setLoading(false)
      }
    },
    [page, size, filterForm, showDisabled],
  )

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [page, size, showDisabled])

  const openCreate = () => {
    createForm.resetFields()
    setCreateOpen(true)
  }

  const submitCreate = async () => {
    const v = await createForm.validateFields()
    setSaving(true)
    try {
      await ignApi.createParamTemplate(v)
      message.success('模板已登记（编号生成、状态草稿；字段行可为空——保存松、提交严）')
      setCreateOpen(false)
      load()
    } catch {
      // 拦截器已透出后端中文拒绝（重名即拒绝等）
    } finally {
      setSaving(false)
    }
  }

  const openDetail = async (r: IgnParamTemplate) => {
    try {
      setDetail(await ignApi.getParamTemplate(r.id))
      setEditingItems(false)
      setDetailOpen(true)
    } catch {
      // 拦截器已提示
    }
  }

  const startItemsEdit = () => {
    if (!detail) return
    // 整组替换语义（R-22）：编辑态以当前整组为底稿，保存提交的是整组，未提交的键即不存在
    setEditRows((detail.items || []).map((it) => ({ field_key: it.field_key, field_value: it.field_value })))
    itemsForm.resetFields()
    setEditingItems(true)
  }

  const saveItems = async () => {
    if (!detail) return
    const { reason } = await itemsForm.validateFields()
    const rows = editRows.filter((r) => r.field_key)
    if (rows.length !== editRows.length) {
      message.warning('存在未选择字段键的空行，请先补齐或移除')
      return
    }
    setItemsSaving(true)
    try {
      const next = await ignApi.saveParamTemplateItems(detail.id, reason, rows)
      setDetail(next)
      setEditingItems(false)
      message.success('字段行已整组替换（未提交的行即不存在，R-22）')
      load()
    } catch {
      // 拦截器已透出后端中文拒绝（面外键治理拒绝 / 形态校验等）
    } finally {
      setItemsSaving(false)
    }
  }

  const openGovern = (action: GovernAction, row: IgnParamTemplate) => {
    govForm.resetFields()
    setGov({ action, row })
  }

  const submitGovern = async () => {
    if (!gov) return
    const { reason } = await govForm.validateFields()
    const { action, row } = gov
    setSaving(true)
    try {
      if (action === 'enable') await ignApi.enableParamTemplate(row.id, reason)
      else if (action === 'disable') await ignApi.disableParamTemplate(row.id, reason)
      else if (action === 'default') await ignApi.setParamTemplateDefault(row.id, reason)
      else await ignApi.deleteParamTemplate(row.id, reason)
      message.success(GOVERN_META[action].ok)
      setGov(null)
      if (detailOpen && detail?.id === row.id) setDetailOpen(false)
      load()
    } catch {
      // 拦截器已透出后端中文拒绝（停用默认模板即拒 / 启用空模板即拒 / 被引用不可删并列出全部出处）
    } finally {
      setSaving(false)
    }
  }

  // 「出处即下一步动作的位置」——MU :902/:941 的联动口径；frmIgnConfig 屏已落地于 /ign/config
  //（IgnClusterConfigPage 消费这里携带的 file_face / param_template_ref / cluster_ref 三个 query，
  // 进入即定位该集群 / 该轴 / 该模板取值上下文的单轴当前态视图）。
  const gotoConfig = (clusterRef?: string) => {
    const q = new URLSearchParams({ file_face: detail?.file_face || '', param_template_ref: detail?.template_no || '' })
    if (clusterRef) q.set('cluster_ref', clusterRef)
    navigate(`/ign/config?${q.toString()}`)
  }

  const columns: ColumnsType<IgnParamTemplate> = [
    { title: '模板编号', dataIndex: 'template_no', key: 'template_no', width: 160 },
    {
      title: '模板名',
      dataIndex: 'template_name',
      key: 'template_name',
      width: 200,
      render: (_, r) => (
        <Space size={6}>
          {r.template_name}
          {r.default_badge &&
            (hasPerm('ign:FUNC-43') ? (
              <Tag color="gold">默认</Tag>
            ) : (
              <Tooltip title="跨集群影响面，由平台运维管理员操作（本屏设为默认仅其岗位可见）">
                <Tag color="gold">默认</Tag>
              </Tooltip>
            ))}
          {r.deleted && <Tag>已删除</Tag>}
        </Space>
      ),
    },
    { title: '文件面', dataIndex: 'file_face', key: 'file_face', width: 110, render: (v: string) => paramFace.faceLabel(v) },
    { title: '状态', dataIndex: 'template_status', key: 'template_status', width: 90, render: (v: string) => <IgnTemplateStatePill status={v} /> },
    {
      title: '被引用集群数',
      dataIndex: 'referenced_cluster_count',
      key: 'referenced_cluster_count',
      width: 130,
      render: (_, r) => (
        <Space size={4}>
          <Badge count={r.referenced_cluster_count} showZero color={r.referenced_cluster_count ? 'processing' : undefined} />
          {r.referenced_cluster_count > 0 && (
            <Tooltip title={<>出处集群：{r.referenced_clusters.join('、')}（出处即下一步动作的位置）</>}>
              <Button size="small" type="link" icon={<ExportOutlined />} onClick={() => navigate(`/ign/config?file_face=${r.file_face}&param_template_ref=${r.template_no}&cluster_ref=${encodeURIComponent(r.referenced_clusters[0] || '')}`)}>
                跳配置
              </Button>
            </Tooltip>
          )}
        </Space>
      ),
    },
    { title: '用途说明', dataIndex: 'description', key: 'description', ellipsis: true, render: (v) => v || '-' },
    {
      title: '启用时间 / 登记时间',
      key: 'times',
      width: 190,
      render: (_, r) => (
        <span style={{ fontSize: 12 }}>
          {r.enabled_at || '未启用'}
          <br />
          <span style={{ opacity: 0.65 }}>{r.created_at}</span>
        </span>
      ),
    },
    {
      title: '操作',
      key: 'action',
      width: 300,
      fixed: 'right',
      render: (_, r) => {
        const dead = r.deleted
        return (
          <Space size={4}>
            <Button size="small" icon={<ProfileOutlined />} onClick={() => openDetail(r)}>
              详情
            </Button>
            {!dead && hasPerm('ign:FUNC-42') && (r.template_status === 'DRAFT' || r.template_status === 'DISABLED') && (
              <Button size="small" icon={<RightOutlined />} onClick={() => openGovern('enable', r)}>
                启用
              </Button>
            )}
            {!dead && hasPerm('ign:FUNC-42') && r.template_status === 'ENABLED' && (
              <Button size="small" icon={<PauseCircleOutlined />} onClick={() => openGovern('disable', r)}>
                停用
              </Button>
            )}
            {/* MU el17：设为默认仅 IGN-ROLE-02 渲染（ign:FUNC-43 只在平台运维管理员发牌集内） */}
            {!dead && hasPerm('ign:FUNC-43') && r.template_status === 'ENABLED' && !r.is_default && (
              <Button size="small" icon={<StarOutlined />} onClick={() => openGovern('default', r)}>
                设为默认
              </Button>
            )}
            {!dead && hasPerm('ign:FUNC-44') && (
              <Button size="small" danger icon={<DeleteOutlined />} onClick={() => openGovern('delete', r)}>
                删除
              </Button>
            )}
          </Space>
        )
      },
    },
  ]

  const detailItemsColumns: ColumnsType<ParamTemplateItemRow> = [
    {
      title: '字段键',
      key: 'field_key',
      width: 240,
      render: (_, it) => (
        <span style={{ fontSize: 12 }}>
          {it.label || it.field_key}
          <br />
          <span style={{ opacity: 0.65 }}>
            {it.field_key}
            {it.ref_id ? ` · ${it.ref_id}` : ''}
          </span>
        </span>
      ),
    },
    { title: '值形态', dataIndex: 'value_shape', key: 'value_shape', width: 90 },
    {
      title: '值（按口径 1 脱敏显示）',
      key: 'value',
      render: (_, it) => {
        if (it.display_note) return <span style={{ opacity: 0.75 }}>{it.display_note}</span>
        const shown = it.field_value_masked ?? it.field_value
        return it.value_shape === 'LIST' ? <span style={{ whiteSpace: 'pre-wrap', fontSize: 12 }}>{shown || '-'}</span> : shown || '-'
      },
    },
  ]

  return (
    <Card>
      <TableToolbar
        title="参数面模板"
        total={total}
        description="参数面治理资产（AGG-IGN-07；跨集群取值口径，字段行整组编辑 R-22，默认标记仅平台运维可动 INV-43）"
        extra={
          <Space>
            <Button icon={<ReloadOutlined />} loading={loading} onClick={() => load()}>
              刷新
            </Button>
            {hasPerm('ign:FUNC-40') && (
              <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
                模板登记
              </Button>
            )}
          </Space>
        }
      />
      <Form form={filterForm} layout="inline" style={{ marginBottom: 12 }}>
        <Form.Item name="file_face" label="文件面">
          <Select allowClear placeholder="两轴全部" style={{ width: 190 }} options={paramFace.faceOptions} />
        </Form.Item>
        <Form.Item name="status" label="状态">
          <Select allowClear placeholder="缺省筛除停用" style={{ width: 140 }} options={paramFace.templateStateOptions} />
        </Form.Item>
        <Form.Item name="keyword" label="关键字">
          <Input allowClear placeholder="模板名 / 编号（不检索字段值）" onPressEnter={() => load()} style={{ width: 220 }} />
        </Form.Item>
        <Form.Item label="显示停用项" tooltip="状态筛选项为空时生效：缺省筛除 DISABLED，打开后连同停用一并列出；已软删行默认可见（deleted 列区分）">
          <Switch checked={showDisabled} onChange={setShowDisabled} />
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

      {/* FUNC-40 登记：保存松、提交严——只保证编号与命名可用，键域合法性由整组保存(FUNC-41)与启用(FUNC-42)把关 */}
      <Modal title="参数面模板登记" open={createOpen} onCancel={() => setCreateOpen(false)} onOk={submitCreate} confirmLoading={saving} width={560}>
        <Alert type="info" showIcon style={{ marginBottom: 12 }} message="登记即分配编号 TMP-YYYYMMDD-NNNN（生成后只读），状态草稿；允许字段行为空，重名在未删除范围内即拒绝（软删除释放名称）" />
        <Form form={createForm} layout="vertical">
          <Form.Item name="template_name" label="模板名" rules={[{ required: true, message: '必填（同一范围内不重名）' }]}>
            <Input placeholder="生产主机清单口径" />
          </Form.Item>
          <Form.Item name="file_face" label="文件面（一张模板只属一轴）" rules={[{ required: true, message: '必填且只属一轴' }]}>
            <Select placeholder="选择文件面" options={paramFace.faceOptions} />
          </Form.Item>
          <Form.Item name="description" label="用途说明（≤500 字）">
            <Input.TextArea rows={3} maxLength={500} showCount placeholder="不给「看起来一样」的两张模板做区分就只有靠编号猜" />
          </Form.Item>
        </Form>
      </Modal>

      {/* FUNC-39 详情三段分区：模板头 / 字段行整组 / 被引用出处 */}
      <Drawer title={detail ? `模板详情 · ${detail.template_no}` : '模板详情'} width={860} open={detailOpen} onClose={() => setDetailOpen(false)}>
        {detail && (
          <>
            <Descriptions column={2} size="small" bordered style={{ marginBottom: 12 }}>
              <Descriptions.Item label="模板编号">{detail.template_no}</Descriptions.Item>
              <Descriptions.Item label="模板名">
                <Space size={6}>
                  {detail.template_name}
                  {detail.default_badge && <Tag color="gold">默认</Tag>}
                  {detail.deleted && <Tag>已删除</Tag>}
                </Space>
              </Descriptions.Item>
              <Descriptions.Item label="文件面">{paramFace.faceLabel(detail.file_face)}</Descriptions.Item>
              <Descriptions.Item label="状态">
                <IgnTemplateStatePill status={detail.template_status} />
              </Descriptions.Item>
              <Descriptions.Item label="启用时间">{detail.enabled_at || '未启用（草稿态为空）'}</Descriptions.Item>
              <Descriptions.Item label="登记时间">{detail.created_at}</Descriptions.Item>
              <Descriptions.Item label="用途说明" span={2}>
                {detail.description || '-'}
              </Descriptions.Item>
            </Descriptions>

            <Card
              size="small"
              title="模板字段行（整组编辑，保存提交的是整组 · R-22；无单行增删入口）"
              style={{ marginBottom: 12 }}
              extra={
                !detail.deleted &&
                hasPerm('ign:FUNC-41') &&
                (editingItems ? (
                  <Space>
                    <Button size="small" onClick={() => setEditingItems(false)}>
                      取消
                    </Button>
                    <Button size="small" type="primary" icon={<SaveOutlined />} loading={itemsSaving} onClick={saveItems}>
                      保存整组
                    </Button>
                  </Space>
                ) : (
                  <Button size="small" icon={<EditOutlined />} onClick={startItemsEdit}>
                    编辑整组
                  </Button>
                ))
              }
            >
              {editingItems ? (
                <Form form={itemsForm} layout="vertical">
                  {editRows.map((row, idx) => {
                    const def = paramFace.fieldOf(row.field_key)
                    const placeholder = paramFace.placeholderOf(row.field_key)
                    return (
                      <Row gutter={8} key={idx} align="middle" style={{ marginBottom: 4 }}>
                        <Col span={7}>
                          <Select
                            style={{ width: '100%' }}
                            placeholder="字段键（合法域=REF-17~25）"
                            value={row.field_key || undefined}
                            options={paramFace.fieldsOfFace(detail.file_face).map((f) => ({
                              value: f.key,
                              label: `${f.label}（${f.key}）`,
                              disabled: editRows.some((o, i) => i !== idx && o.field_key === f.key),
                            }))}
                            onChange={(k) => setEditRows(editRows.map((o, i) => (i === idx ? { ...o, field_key: k, field_value: '' } : o)))}
                          />
                        </Col>
                        <Col span={3}>
                          <span style={{ fontSize: 12 }}>{def ? `形态 ${def.value_shape}` : '随键带出'}</span>
                        </Col>
                        <Col span={10}>
                          {def?.value_shape === 'LIST' ? (
                            <Input.TextArea rows={2} placeholder={placeholder} value={row.field_value} onChange={(e) => setEditRows(editRows.map((o, i) => (i === idx ? { ...o, field_value: e.target.value } : o)))} />
                          ) : (
                            <Input placeholder={placeholder || '值（空值即让位给下层）'} value={row.field_value} onChange={(e) => setEditRows(editRows.map((o, i) => (i === idx ? { ...o, field_value: e.target.value } : o)))} />
                          )}
                        </Col>
                        <Col span={4}>
                          <Button size="small" danger icon={<DeleteOutlined />} onClick={() => setEditRows(editRows.filter((_, i) => i !== idx))}>
                            移除行
                          </Button>
                        </Col>
                      </Row>
                    )
                  })}
                  <Space style={{ marginBottom: 8 }}>
                    <Button size="small" icon={<PlusOutlined />} onClick={() => setEditRows([...editRows, { field_key: '', field_value: '' }])}>
                      新增行（仅整组编辑态可用）
                    </Button>
                  </Space>
                  <Form.Item name="reason" label="变更事由（整组替换必填）" rules={[{ required: true, message: '治理动作必须携带事由' }]}>
                    <Input.TextArea rows={2} maxLength={500} placeholder="为什么改这套口径" />
                  </Form.Item>
                </Form>
              ) : (
                <Table
                  rowKey={(r) => `${r.id}-${r.field_key}`}
                  size="small"
                  pagination={false}
                  columns={detailItemsColumns}
                  dataSource={detail.items || []}
                  locale={{ emptyText: '字段行为空（草稿态正常——启用前须整组保存字段行，启用空模板会被治理拒绝）' }}
                />
              )}
            </Card>

            <Card size="small" title={`被引用出处（反查本域覆写行 + 上游面标识声明；共 ${detail.referenced_cluster_count} 个集群）`}>
              <Table
                rowKey={(r) => `${r.cluster_ref}|${r.source_type}|${r.source_label}`}
                size="small"
                pagination={false}
                columns={[
                  { title: '集群标识', dataIndex: 'cluster_ref', key: 'cluster_ref', width: 200 },
                  { title: '出处', dataIndex: 'source_label', key: 'source_label' },
                  {
                    title: '下一步',
                    key: 'goto',
                    width: 140,
                    render: (_, r: ParamTemplateReference) => (
                      <Button size="small" type="link" icon={<ExportOutlined />} onClick={() => gotoConfig(r.cluster_ref)}>
                        跳集群交付配置
                      </Button>
                    ),
                  },
                ]}
                dataSource={detail.references || []}
                locale={{ emptyText: '暂无引用（可安全软删除；被引用时删除会被治理拒绝并列出全部出处，R-25）' }}
              />
            </Card>
          </>
        )}
      </Drawer>

      {/* FUNC-42/43/44 治理确认：事由必填；软删前置展示出处（出处即下一步动作的位置） */}
      <Modal
        title={gov ? `${GOVERN_META[gov.action].title} · ${gov.row.template_no}` : ''}
        open={!!gov}
        onCancel={() => setGov(null)}
        onOk={submitGovern}
        confirmLoading={saving}
        okButtonProps={{ danger: gov?.action === 'delete' }}
        width={560}
      >
        {gov && (
          <>
            {gov.action === 'enable' && (
              <Alert type="info" showIcon style={{ marginBottom: 12 }} message="启用前置：字段行非空且逐键合法（键域 + 形态，与受理侧同一套校验器）；首次启用回填启用时间" />
            )}
            {gov.action === 'disable' && (
              <Alert type="warning" showIcon style={{ marginBottom: 12 }} message="停用默认模板会被治理拒绝（INV-43）——先对同面另一张启用态模板「设为默认」；停用只表示不再被新集群选中，已生效取值不作废（R-25）" />
            )}
            {gov.action === 'default' && (
              <Alert type="warning" showIcon style={{ marginBottom: 12 }} message="设为默认后同面原默认行自动取消标记（同面至多一条默认）；默认只作用于上游选择器缺省值，不参与渲染兜底（R-20）" />
            )}
            {gov.action === 'delete' && (
              <Alert
                type={gov.row.referenced_cluster_count > 0 ? 'error' : 'info'}
                showIcon
                style={{ marginBottom: 12 }}
                message={
                  gov.row.referenced_cluster_count > 0
                    ? `正被 ${gov.row.referenced_cluster_count} 个集群引用：${gov.row.referenced_clusters.join('、')}——提交将被治理拒绝并列出全部出处（R-25）`
                    : '软删除是标记不是第四个状态值；字段行随聚合级联，名称随删除释放'
                }
              />
            )}
            <Form form={govForm} layout="vertical">
              <Form.Item name="reason" label="变更事由（治理动作留痕，必填）" rules={[{ required: true, message: '必须携带事由' }]}>
                <Input.TextArea rows={3} maxLength={500} placeholder="为什么执行该治理动作" />
              </Form.Item>
            </Form>
          </>
        )}
      </Modal>
    </Card>
  )
}
