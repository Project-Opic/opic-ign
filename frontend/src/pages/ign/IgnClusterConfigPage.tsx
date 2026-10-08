// MU frmIgnConfig 交付管理 · 集群交付配置(AGG-IGN-08;LIST_MAINTENANCE,单集群单轴当前态,FUNC-45~49)
// 字段级视图(FUNC-46,REP-17:四层取值并列 + 生效来源徽章必现 + 「当前落盘」与「下次将生效」两列并列不合并 R-21)/
// 覆写保存(FUNC-47:单字段坐标、事由必填、列表值整项替换 R-22,只改「下次将生效」)/
// dry-run 预检(FUNC-48:与受理侧同一套校验器与三态判定 R-23,五不:不写盘/不建任务/不占额度/不改行/不进台账)/
// 覆写回落(FUNC-49:只对「生效」行渲染;INV-42 回落即清空追溯对,行保留)/
// 清单原文只读(FUNC-45,REP-18:独立权限码 ign:FUNC-45、逐次留痕、命中凭据形态整行掩码,无该权限不渲染入口也不报错 R-26)。
// 不变量中文结论(面外键 INV-41 / 坐标自洽 INV-42 / 冲突态「已保存 · 未生效」R-24 / 让位与各层跳过原因 R-20)一律由后端
// 回吐、经响应拦截器原样透出;本屏不重写规则文案、不自行推断(R-24:冲突标注是受理侧判定,不是界面猜测)。
import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Alert,
  Button,
  Card,
  Descriptions,
  Drawer,
  Empty,
  Form,
  Input,
  Modal,
  Popconfirm,
  Select,
  Space,
  Table,
  Tag,
  Tooltip,
  message,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import {
  CheckCircleOutlined,
  CloseCircleOutlined,
  CopyOutlined,
  EditOutlined,
  FileSearchOutlined,
  ReloadOutlined,
  RollbackOutlined,
  SaveOutlined,
  SearchOutlined,
} from '@ant-design/icons'
import { useNavigate, useSearchParams } from 'react-router-dom'
import {
  ignApi,
  type ClusterConfigFieldRow,
  type ClusterConfigValidateResult,
  type ClusterConfigView,
  type ClusterRawView,
  type ParamLayerCell,
} from '../../api/ign'
import TableToolbar from '../../admin/components/TableToolbar'
import { IgnOverrideStatePill } from '../../admin/components/StatusPill'
import { usePermission } from '../../hooks/usePermission'
import { useParamFace } from '../../hooks/useParamFace'

// 两轴一次只选一轴（切轴即整表重载——两轴键域不同，混看会得出错误的「当前值」）；
// 轴集合与中文名一律取字典 IGN_CONFIG_FILE_FACE，界面不自存副本。

/** 生效来源徽章（IGN_PARAM_SOURCE 四值）的底色是纯装饰，标签一律用后端中文不回译 */
const SOURCE_TAG_COLOR: Record<string, string> = {
  OVERRIDE: 'purple',
  VERSION_DERIVED: 'cyan',
  PARAM_TEMPLATE: 'blue',
  PLATFORM_DEFAULT: 'default',
}

/** 四层格渲染：值 + 「让位 / 本层跳过 / 已回落」结论按 valueNote 呈现为次级灰字（后端原注，不判成校验错误） */
function LayerCellText({ cell }: { cell: ParamLayerCell }) {
  if (!cell || (!cell.hit && !cell.value && !cell.note)) return <span style={{ opacity: 0.45 }}>—</span>
  return (
    <div style={{ fontSize: 12 }}>
      {cell.value ? (
        <span style={{ whiteSpace: 'pre-wrap' }}>{cell.value}</span>
      ) : (
        <span style={{ opacity: 0.45 }}>—</span>
      )}
      {cell.note && (
        <div style={{ opacity: 0.65, marginTop: 2 }}>{cell.note}</div>
      )}
    </div>
  )
}

export default function IgnClusterConfigPage() {
  const hasPerm = usePermission()
  const paramFace = useParamFace()
  const navigate = useNavigate()
  const [query] = useSearchParams()

  // 模板屏 gotoConfig 携带的三个 query：进入即定位到该集群 / 该面 / 该模板的取值上下文视图
  const [clusterRef, setClusterRef] = useState(query.get('cluster_ref') || '')
  const [fileFace, setFileFace] = useState(query.get('file_face') || '')
  const [ctxTemplateRef, setCtxTemplateRef] = useState(query.get('param_template_ref') || '')
  const [ctxVersionRef, setCtxVersionRef] = useState(query.get('version_ref') || '')

  const [clusterOptions, setClusterOptions] = useState<string[]>([])
  const [view, setView] = useState<ClusterConfigView | null>(null)
  const [viewLoading, setViewLoading] = useState(false)

  const [dry, setDry] = useState<ClusterConfigValidateResult | null>(null)
  const [dryLoading, setDryLoading] = useState(false)

  const [ovRow, setOvRow] = useState<ClusterConfigFieldRow | null>(null)
  const [ovForm] = Form.useForm<{ value: string; reason: string }>()
  const [ovSaving, setOvSaving] = useState(false)
  const [ovDry, setOvDry] = useState<ClusterConfigValidateResult | null>(null)
  const [ovDryLoading, setOvDryLoading] = useState(false)

  const [rawOpen, setRawOpen] = useState(false)
  const [raw, setRaw] = useState<ClusterRawView | null>(null)
  const [rawLoading, setRawLoading] = useState(false)

  const ready = !!clusterRef && !!fileFace

  // el01「只列本域有交付记录或已登记配置的集群」：取数走 FUNC-38 列表回吐的 referenced_clusters 并集
  //（两类来源=上游面标识声明快照 + 本域生效覆写依据，R-25 同源；无 FUNC-38 发牌则不出声，选项退化为跳转携带值）
  useEffect(() => {
    if (!hasPerm('ign:FUNC-38')) return
    ignApi
      .listParamTemplates({ page: 1, size: 200 })
      .then((res) => {
        const set = new Set<string>()
        for (const t of res.list || []) (t.referenced_clusters || []).forEach((c) => set.add(c))
        setClusterOptions([...set].sort())
      })
      .catch(() => {
        // 拦截器已提示；选项取数失败不阻塞跳转携带的集群标识定位
      })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const loadView = useCallback(async () => {
    if (!ready) return
    setViewLoading(true)
    try {
      setView(
        await ignApi.getClusterConfig({
          cluster_ref: clusterRef,
          file_face: fileFace,
          param_template_ref: ctxTemplateRef || undefined,
          version_ref: ctxVersionRef || undefined,
        }),
      )
    } catch {
      // 拦截器已透出后端中文结论（如集群标识形态非法 INV-33）
      setView(null)
    } finally {
      setViewLoading(false)
    }
  }, [ready, clusterRef, fileFace, ctxTemplateRef, ctxVersionRef])

  // 一次一轴：切换集群或文件面即整表重载（valueNote el02）
  useEffect(() => {
    loadView()
  }, [loadView])

  const selectOptions = useMemo(() => {
    const opts = [...clusterOptions]
    if (clusterRef && !opts.includes(clusterRef)) opts.unshift(clusterRef)
    return opts.map((c) => ({ value: c, label: c }))
  }, [clusterOptions, clusterRef])

  /* ---------- FUNC-48 dry-run 预检（当前轴整面预检；候选预检在覆写浮层内） ---------- */
  const runDryRun = async () => {
    setDryLoading(true)
    try {
      setDry(
        await ignApi.validateClusterConfig({
          cluster_ref: clusterRef,
          file_face: fileFace,
          param_template_ref: ctxTemplateRef || undefined,
          version_ref: ctxVersionRef || undefined,
        }),
      )
    } catch {
      // 拦截器已提示
    } finally {
      setDryLoading(false)
    }
  }

  /* ---------- FUNC-47 覆写保存（初值 = 生效值，不是缺省面值，避免误以为是空值覆盖） ---------- */
  const openOverride = (r: ClusterConfigFieldRow) => {
    ovForm.setFieldsValue({ value: r.next_effective_value || '', reason: r.override.reason || '' })
    setOvDry(null)
    setOvRow(r)
  }

  const candidateDryRun = async () => {
    if (!ovRow) return
    setOvDryLoading(true)
    try {
      setOvDry(
        await ignApi.validateClusterConfig({
          cluster_ref: clusterRef,
          file_face: ovRow.file_face,
          param_template_ref: ctxTemplateRef || undefined,
          version_ref: ctxVersionRef || undefined,
          overrides: [{ field_key: ovRow.field_key, value: ovForm.getFieldValue('value') ?? '' }],
        }),
      )
    } catch {
      // 拦截器已提示
    } finally {
      setOvDryLoading(false)
    }
  }

  const saveOverride = async () => {
    if (!ovRow) return
    const v = await ovForm.validateFields()
    setOvSaving(true)
    try {
      const res = await ignApi.saveClusterOverride({
        cluster_ref: clusterRef,
        file_face: ovRow.file_face,
        field_key: ovRow.field_key,
        value: v.value,
        reason: v.reason,
      })
      message.success(res.notice)
      if (res.save_effective_note) message.warning(res.save_effective_note)
      setOvRow(null)
      await loadView()
    } catch {
      // 拦截器已透出后端中文拒绝（事由必填 / 空值不是回落入口 / 面外键治理拒绝 / 形态校验）
    } finally {
      setOvSaving(false)
    }
  }

  /* ---------- FUNC-49 覆写回落（INV-42：状态翻面同时清空追溯对；行保留） ---------- */
  const rollback = async (r: ClusterConfigFieldRow) => {
    try {
      const res = await ignApi.rollbackClusterOverride({ cluster_ref: clusterRef, file_face: r.file_face, field_key: r.field_key })
      message.success(res.notice)
      await loadView()
    } catch {
      // 拦截器已透出（如重复回落的 ErrState 结论：回落按钮只对「生效」行渲染）
    }
  }

  /* ---------- FUNC-45 原文只读：每次打开都重新读、重新审计，关闭不做缓存复用（R-26） ---------- */
  const openRaw = async () => {
    setRaw(null)
    setRawOpen(true)
    setRawLoading(true)
    try {
      setRaw(await ignApi.getClusterRaw({ cluster_ref: clusterRef, file_face: fileFace }))
    } catch {
      // 拦截器已透出「clusters/<ref> 不可读（交付配置尚未生成）」等 ErrState 结论
    } finally {
      setRawLoading(false)
    }
  }

  const conflict = view?.inventory_state === 'CONFLICT'

  const columns: ColumnsType<ClusterConfigFieldRow> = [
    {
      title: '字段键',
      key: 'field_key',
      width: 210,
      render: (_, r) => (
        <div style={{ fontSize: 12 }}>
          {r.label}
          <br />
          <span style={{ opacity: 0.65 }}>
            {r.field_key} · {r.ref_id} · {r.value_shape}
          </span>
        </div>
      ),
    },
    {
      title: '平台缺省面值（第 4 层地板）',
      key: 'platform_default',
      width: 170,
      render: (_, r) => <LayerCellText cell={r.platform_default} />,
    },
    {
      title: (
        <Tooltip title={view?.param_template_notice || ''}>
          <span>参数面模板值（第 3 层）</span>
        </Tooltip>
      ),
      key: 'param_template',
      width: 200,
      render: (_, r) => <LayerCellText cell={r.param_template} />,
    },
    {
      title: (
        <Tooltip title={view?.version_notice || ''}>
          <span>交付版本派生值（第 2 层）</span>
        </Tooltip>
      ),
      key: 'version_derived',
      width: 180,
      render: (_, r) => <LayerCellText cell={r.version_derived} />,
    },
    {
      title: '覆写值 + 状态 + 事由（第 1 层）',
      key: 'override',
      width: 240,
      render: (_, r) => {
        const o = r.override
        if (!o.status && !o.value) return <span style={{ opacity: 0.45 }}>无覆写</span>
        return (
          <div style={{ fontSize: 12 }}>
            <Space size={6} wrap>
              <span style={{ whiteSpace: 'pre-wrap' }}>{o.value || '—'}</span>
              <IgnOverrideStatePill status={o.status || ''} />
            </Space>
            {o.reason && (
              <div style={{ opacity: 0.75, marginTop: 2 }}>事由：{o.reason}</div>
            )}
            {o.rolled_back_at && (
              <div style={{ opacity: 0.6, marginTop: 2 }}>回落于 {o.rolled_back_at}</div>
            )}
            {o.note && (
              <div style={{ opacity: 0.65, marginTop: 2 }}>{o.note}</div>
            )}
          </div>
        )
      },
    },
    {
      title: '生效值 · 下次将生效（含生效来源徽章）',
      key: 'next_effective',
      width: 210,
      render: (_, r) => {
        // 清单归属冲突行显示「不渲染」，不给出一个不会用到的值（valueNote el09）
        if (conflict && r.override.hit) {
          return (
            <Tooltip title={view?.save_effective_note || ''}>
              <span style={{ opacity: 0.65 }}>不渲染</span>
            </Tooltip>
          )
        }
        return (
          <div style={{ fontSize: 12 }}>
            <span style={{ whiteSpace: 'pre-wrap' }}>{r.next_effective_value}</span>
            <div style={{ marginTop: 4 }}>
              <Tag color={SOURCE_TAG_COLOR[r.effective_source] || 'default'}>{r.effective_source_label}</Tag>
              {r.source_ref && <span style={{ opacity: 0.6, fontSize: 11 }}>{r.source_ref}</span>}
            </div>
          </div>
        )
      },
    },
    {
      title: '当前落盘',
      key: 'on_disk',
      width: 160,
      render: (_, r) =>
        r.on_disk_value ? (
          <span style={{ whiteSpace: 'pre-wrap', fontSize: 12 }}>{r.on_disk_value}</span>
        ) : (
          // 取数失败显示「不可用 + 原因」（报表口径 2），不以空值或「与生效值相同」代替
          <span style={{ opacity: 0.65, fontSize: 12 }}>{r.on_disk_note || '—'}</span>
        ),
    },
    {
      title: '应用追溯',
      key: 'applied',
      width: 150,
      render: (_, r) =>
        r.applied_task_no ? (
          <Button size="small" type="link" style={{ padding: 0 }} onClick={() => navigate(`/ign/task?keyword=${encodeURIComponent(r.applied_task_no as string)}`)}>
            {r.applied_task_no}
            <br />
            <span style={{ opacity: 0.65, fontSize: 11 }}>{r.applied_at}</span>
          </Button>
        ) : (
          // 无值必须显示「未应用」，不给空单元格（valueNote el10）；覆写保存成功时「当前落盘」不刷新即未派发
          <span style={{ opacity: 0.75 }}>{r.applied_display}</span>
        ),
    },
    {
      title: '冲突标注',
      key: 'conflict',
      width: 170,
      render: (_, r) =>
        conflict && r.override.hit ? (
          <span style={{ fontSize: 12, color: '#d4380d' }}>{view?.save_effective_note || '已保存 · 未生效（清单归属冲突）'}</span>
        ) : (
          <span style={{ opacity: 0.45 }}>—</span>
        ),
    },
    {
      title: '操作',
      key: 'ops',
      width: 150,
      fixed: 'right',
      render: (_, r) => (
        <Space size={4}>
          {hasPerm('ign:FUNC-47') && (
            <Button size="small" icon={<EditOutlined />} onClick={() => openOverride(r)}>
              覆写
            </Button>
          )}
          {/* el15：回落按钮只对「生效」行渲染 —— 已回落的坐标已不在任何层上 */}
          {hasPerm('ign:FUNC-49') && r.override.status === 'EFFECTIVE' && (
            <Popconfirm
              title="回落为上层取值"
              description="状态翻面为已回落并同步清空追溯对（INV-42）；行保留以可查「这个集群曾经改过什么」，取值回落到下层（R-20 次序不破）。"
              okText="回落"
              onConfirm={() => rollback(r)}
            >
              <Button size="small" icon={<RollbackOutlined />}>
                回落
              </Button>
            </Popconfirm>
          )}
        </Space>
      ),
    },
  ]

  return (
    <Card>
      <TableToolbar
        title="集群交付配置"
        total={view?.fields?.length || 0}
        description="集群交付配置台（AGG-IGN-08；单集群单轴当前态。覆写只改「下次将生效」R-21，四层取值逐字段带来源徽章 R-20）"
        extra={
          <Space>
            <Button icon={<ReloadOutlined />} loading={viewLoading} disabled={!ready} onClick={() => loadView()}>
              重载本轴
            </Button>
            {hasPerm('ign:FUNC-48') && (
              <Button icon={<SearchOutlined />} loading={dryLoading} disabled={!ready} onClick={runDryRun}>
                配置校验 dry-run
              </Button>
            )}
            {/* el17：原文视图独立发牌（R-26 判据是内容敏感度）——无 ign:FUNC-45 权限时不渲染入口、也不报错 */}
            {hasPerm('ign:FUNC-45') && (
              <Button icon={<FileSearchOutlined />} disabled={!ready} onClick={openRaw}>
                查看原文
              </Button>
            )}
          </Space>
        }
      />

      <Form layout="inline" style={{ marginBottom: 12 }}>
        <Form.Item label="集群标识" tooltip="跨域引用 O-CLC（INV-33）；只列本域有交付记录或已登记配置的集群；未选定不出表">
          <Select
            showSearch
            style={{ width: 240 }}
            placeholder="选择集群（跳选框）"
            value={clusterRef || undefined}
            options={selectOptions}
            onChange={(v) => {
              setClusterRef(v || '')
              setView(null)
              setDry(null)
            }}
          />
        </Form.Item>
        <Form.Item label="文件面" tooltip="字典 IGN_CONFIG_FILE_FACE；一次只看一轴，切换轴即整表重载（两轴键域不同，混看会得出错误的「当前值」）">
          <Select
            style={{ width: 200 }}
            placeholder="选择文件面（一次一轴）"
            value={fileFace || undefined}
            options={paramFace.faceOptions}
            onChange={(v) => {
              setFileFace(v || '')
              setView(null)
              setDry(null)
            }}
          />
        </Form.Item>
        {(ctxTemplateRef || ctxVersionRef) && (
          <Form.Item
            label={`取值上下文携带：${ctxTemplateRef || '—'}${ctxVersionRef ? ` / 版本 ${ctxVersionRef}` : ''}`}
            tooltip="模板屏跳转带来的面标识/版本引用；清空后回落到该集群接管标记快照（与受理侧同处解析，R-20）"
          >
            <Button
              size="small"
              onClick={() => {
                setCtxTemplateRef('')
                setCtxVersionRef('')
              }}
            >
              清除携带
            </Button>
          </Form.Item>
        )}
      </Form>

      {!ready && (
        <Empty description="未选定集群 + 文件面不出表（FUNC-46 前置：一次只看一轴；集群从「参数面模板」屏的引用出处跳转而来，或本域已有交付记录/覆写登记）" />
      )}

      {ready && view && (
        <>
          <Descriptions size="small" column={2} bordered style={{ marginBottom: 12 }}>
            <Descriptions.Item label="集群标识">{view.cluster_ref}</Descriptions.Item>
            <Descriptions.Item label="文件面">{paramFace.faceLabel(view.file_face)}</Descriptions.Item>
            <Descriptions.Item label="面标识（请求 → 命中）">
              {view.requested_param_template_ref || '未携带（回落接管标记快照）'} → {view.hit_param_template_ref || '未命中'}
            </Descriptions.Item>
            <Descriptions.Item label="版本引用（请求 → 状态）">
              {view.requested_version_ref || '未携带'} {view.version_status ? `（${view.version_status}）` : ''}
            </Descriptions.Item>
            <Descriptions.Item label="第 3 层整列结论" span={2}>
              {view.param_template_notice || '—'}
            </Descriptions.Item>
            <Descriptions.Item label="第 2 层整列结论" span={2}>
              {view.version_notice || '—'}
            </Descriptions.Item>
            <Descriptions.Item label="清单三态（R-18 受理侧判定）" span={2}>
              <Space>
                <span>{view.inventory_state || '探测不可用'}</span>
                <span style={{ opacity: 0.75, fontSize: 12 }}>{view.inventory_note}</span>
              </Space>
            </Descriptions.Item>
          </Descriptions>

          {/* el11 表头原因位：冲突态由受理侧判定回吐，界面照抄显示不自行推断（R-24） */}
          {conflict && (
            <Alert
              type="warning"
              showIcon
              style={{ marginBottom: 12 }}
              message={`覆写已登记、本次不参与渲染：${view.save_effective_note || '已保存 · 未生效（清单归属冲突）'}`}
              description={view.inventory_note}
            />
          )}

          <Table
            rowKey="field_key"
            size="small"
            columns={columns}
            dataSource={view.fields}
            loading={viewLoading}
            pagination={false}
            scroll={{ x: 1700 }}
          />
        </>
      )}

      {/* el19 dry-run 结论区：文案与受理侧同源（同一套校验器 R-23）；结论区不出现任务编号、不新增台账行（五不） */}
      {dry && (
        <Card
          size="small"
          title={
            <Space>
              dry-run 预检结论 · {dry.cluster_ref}
              {dry.file_face ? ` / ${dry.file_face}` : '（整张渲染面）'}
              {dry.passed ? (
                <Tag icon={<CheckCircleOutlined />} color="success">通过</Tag>
              ) : (
                <Tag icon={<CloseCircleOutlined />} color="error">不通过（判定停在：{dry.stage}）</Tag>
              )}
            </Space>
          }
          style={{ marginTop: 12 }}
          extra={
            <Button size="small" onClick={() => setDry(null)}>
              收起
            </Button>
          }
        >
          <Alert type="info" showIcon style={{ marginBottom: 8 }} message={dry.notice} />
          {dry.reasons.length > 0 && (
            <Alert
              type="error"
              showIcon
              style={{ marginBottom: 8 }}
              message="逐字段原因"
              description={
                <ul style={{ margin: 0, paddingLeft: 18 }}>
                  {dry.reasons.map((rs, i) => (
                    <li key={i}>{rs}</li>
                  ))}
                </ul>
              }
            />
          )}
          <Space style={{ marginBottom: 8 }} wrap>
            <Tag>清单三态预判：{dry.inventory_state || '探测不可用'}</Tag>
            {dry.would_reject_on_rewrite && <Tag color="volcano">冲突态：带清单的重写受理即刻拒绝</Tag>}
            {dry.pending_overrides?.length ? <Tag color="purple">候选覆写 {dry.pending_overrides.length} 项（未落库）</Tag> : null}
          </Space>
          <Table
            rowKey="field_key"
            size="small"
            pagination={false}
            dataSource={dry.fields}
            columns={[
              { title: '字段键', key: 'k', width: 220, render: (_, r) => `${r.label}（${r.field_key}）` },
              { title: '文件面', dataIndex: 'file_face', key: 'file_face', width: 90 },
              {
                title: '生效值（命中层）',
                key: 'ev',
                render: (_, r) => (
                  <Space size={6} wrap>
                    <span style={{ whiteSpace: 'pre-wrap', fontSize: 12 }}>{r.effective_value}</span>
                    <Tag color={SOURCE_TAG_COLOR[r.effective_source] || 'default'}>{r.effective_source_label}</Tag>
                  </Space>
                ),
              },
            ]}
          />
        </Card>
      )}

      {/* el12/el13/el14 覆写浮层：初值 = 生效值；列表值整体替换、无元素级增删控件（R-22）；事由必填并落库（覆写事由是这条覆写的组成） */}
      <Modal
        title={ovRow ? `覆写 · ${ovRow.label}（${ovRow.field_key}）` : ''}
        open={!!ovRow}
        onCancel={() => setOvRow(null)}
        width={640}
        footer={[
          hasPerm('ign:FUNC-48') && (
            <Button key="dry" icon={<SearchOutlined />} loading={ovDryLoading} onClick={candidateDryRun}>
              预检该覆写
            </Button>
          ),
          <Button key="cancel" onClick={() => setOvRow(null)}>
            取消
          </Button>,
          <Button key="save" type="primary" icon={<SaveOutlined />} loading={ovSaving} onClick={saveOverride}>
            保存覆写
          </Button>,
        ]}
      >
        <Alert
          type="info"
          showIcon
          style={{ marginBottom: 12 }}
          message="保存只改「下次将生效」列——不重写已落盘文件、不触发派发（R-21）；列表值为整体替换后的全量值，一行台账 = 落盘后的一项取值（R-22）"
        />
        <Form form={ovForm} layout="vertical">
          <Form.Item name="value" label={`新值（初值 = 生效值；形态 ${ovRow?.value_shape === 'LIST' ? '列表值（整体替换）' : '单值'}）`} rules={[{ required: true, message: '必填（保存空值不是回落入口，请使用「回落为上层取值」）' }]}>
            {ovRow?.value_shape === 'LIST' ? (
              <Input.TextArea rows={4} placeholder="一行一条，整体替换全量值" />
            ) : (
              <Input placeholder="单值替换" />
            )}
          </Form.Item>
          <Form.Item name="reason" label="覆写事由（必填，随覆写行落库）" rules={[{ required: true, message: '治理事实必须说为什么改' }]}>
            <Input.TextArea rows={2} maxLength={500} placeholder="为什么这个集群要偏离上层取值" />
          </Form.Item>
        </Form>
        {ovDry && (
          <Alert
            type={ovDry.passed ? 'success' : 'error'}
            showIcon
            style={{ marginBottom: 8 }}
            message={ovDry.passed ? '预检通过（该候选覆写可保存且下次受理将生效）' : `预检不通过（判定停在：${ovDry.stage}）`}
            description={
              <>
                <ul style={{ margin: 0, paddingLeft: 18 }}>
                  {ovDry.reasons.map((rs, i) => (
                    <li key={i}>{rs}</li>
                  ))}
                </ul>
                <div style={{ marginTop: 6, opacity: 0.75 }}>{ovDry.notice}</div>
              </>
            }
          />
        )}
      </Modal>

      {/* el18 原文只读浮层：带行号整份文本、可复制、不提供编辑态与导出控件；每次打开重新读、重新审计（R-26） */}
      <Drawer
        title={`清单原文只读 · ${clusterRef} / ${fileFace}（独立权限码，读也留痕）`}
        width={760}
        open={rawOpen}
        onClose={() => {
          setRawOpen(false)
          setRaw(null)
        }}
        extra={
          raw && (
            <Button
              icon={<CopyOutlined />}
              onClick={() => {
                navigator.clipboard
                  .writeText(raw.lines.map((l) => `${l.no}  ${l.text}`).join('\n'))
                  .then(() => message.success('已复制（可复制、不提供导出控件，R-26）'))
                  .catch(() => message.error('剪贴板不可用'))
              }}
            >
              复制
            </Button>
          )
        }
      >
        {rawLoading && <Alert type="info" showIcon message="读取中——每次打开都重新读盘、重新写审计（关闭不做缓存复用，R-26）" />}
        {!rawLoading && raw && (
          <>
            <Alert type="info" showIcon style={{ marginBottom: 8 }} message={raw.notice} description={`来源：${raw.read_origin} · ${raw.line_count} 行 · 摘要 ${raw.content_sha256}`} />
            {raw.masked_line_count > 0 && (
              <Alert type="warning" showIcon style={{ marginBottom: 8 }} message={`命中 R-16 凭据形态 ${raw.masked_line_count} 行，已整行掩码（片段掩码仍可拼回，故整行处置）`} />
            )}
            <pre style={{ fontSize: 12, whiteSpace: 'pre-wrap', background: 'rgba(127,127,127,0.08)', padding: 12, margin: 0 }}>
              {raw.lines.map((l) => `${String(l.no).padStart(4, ' ')}  ${l.text}`).join('\n')}
            </pre>
          </>
        )}
      </Drawer>
    </Card>
  )
}
