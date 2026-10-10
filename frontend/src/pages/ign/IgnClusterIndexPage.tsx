// MU frmIgnClusterIndex 本域交付集群索引(QUERY_LIST;v1.5 新增侧栏第一叶，REP-19 的唯一呈现面)
// 一行一个「本域服务过的」集群标识 + 四个即时派生摘要列（交付记录数 / 最近一次交付 / 生效覆写字段 / 未决偏差）
// + 单一动作「进入集群交付台」（9.10.2）。纯读、无写腿、无表单区（FUNC-52 是本域第 22 个查询）。
// 三条刻意的"不做"（9.10.1）：① 不显示上游集群名称 / 生命周期状态 / 健康（INV-33，本域没有那份数据）；
// ② 不显示步骤 / 进度条 / 步进器（Q-69 裁定 ②，步进权威在 O-CLC，本表连"第几步"这一列都不存在）；
// ③ 不提供导出（导航入口不是交付凭证，5.20 ⑬）。
// 摘要四列即时派生 ⇒ 统计时点标注必现、不得读作"该集群现在的状态"（口径 11 / Q-127）；
// 不做客户端缓存，从台内返回即整表重取（9.10.6 / 9.10.7 ①）。
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Button, Card, Input, Select, Space, Table, Tag, Tooltip, Typography } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { ArrowRightOutlined, ReloadOutlined, SearchOutlined } from '@ant-design/icons'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { dictItems, ignApi, type ClusterIndexResult, type ClusterIndexRow } from '../../api/ign'
import TableToolbar from '../../admin/components/TableToolbar'
import { IgnTaskStatusPill } from '../../admin/components/StatusPill'

// 待办筛项四值（el02 / REP-19 口径）：都是对本表既有派生列的收紧，不引入新查询面
const PENDING_OPTIONS = [
  { value: 'ALL', label: '全部' },
  { value: 'RUNNING', label: '有在跑任务' },
  { value: 'OVERRIDE', label: '有生效覆写' },
  { value: 'DEVIATION', label: '有未决偏差' },
]

// el10 空态：本域没有"集群"这个对象，只有"服务过谁"的事实 ⇒ 文案不得写成"没有集群"（9.10.1）
const EMPTY_NOTE =
  '本域尚未替任何集群执行过交付 —— 交付任务由上游中心派发，或在本域「交付任务」叶提交。本表不断言上游有没有集群，那是 O-CLC 的事实（INV-33）。'

const FILTERED_EMPTY_NOTE =
  '按当前筛选条件没有命中 —— 关键字只做本域标识的包含匹配，不校验集群是否存在（INV-33），换一个关键字或把待办筛项放回「全部」再看。'

/** 最近一次交付的呈现：任务号 + 结束时间 + 结果 + 结论依据四元组，不许只写结果（口径 7 / 界面口径第二条） */
function LastDeliveryCell({ row, verdictLabel, onOpen }: { row: ClusterIndexRow; verdictLabel: (c?: string | null) => string; onOpen: () => void }) {
  if (!row.last_task_no) return <Typography.Text type="secondary">—</Typography.Text>
  const running = !row.last_ended_at
  return (
    <Space direction="vertical" size={2}>
      <Space size={6} wrap>
        <Typography.Link onClick={onOpen}>{row.last_task_no}</Typography.Link>
        <Typography.Text type="secondary">{row.last_ended_at || '进行中'}</Typography.Text>
        <IgnTaskStatusPill status={row.last_task_status || ''} />
      </Space>
      <Typography.Text type="secondary" style={{ fontSize: 12 }}>
        结论依据：{verdictLabel(row.last_verdict_source)}
        {running ? '（尚无结束时间，本行按开始时间垫后排序）' : ''}
      </Typography.Text>
    </Space>
  )
}

export default function IgnClusterIndexPage() {
  const nav = useNavigate()
  const [query] = useSearchParams()
  // 深链 /ign/cluster?kw=（9.10.6）：进入即带筛选，关键字只做包含匹配
  const [keyword, setKeyword] = useState(query.get('kw') || '')
  const [pending, setPending] = useState('ALL')
  const [res, setRes] = useState<ClusterIndexResult | null>(null)
  const [rows, setRows] = useState<ClusterIndexRow[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [size, setSize] = useState(20)
  const [loading, setLoading] = useState(false)
  const [dict, setDict] = useState<Record<string, unknown>>({})

  const verdictLabel = useCallback(
    (code?: string | null) => dictItems(dict, 'IGN_VERDICT_SOURCE').find((d) => d.code === code)?.label || code || '-',
    [dict],
  )

  const load = useCallback(
    async (p = page, s = size, kw = keyword, td = pending) => {
      setLoading(true)
      try {
        const out = await ignApi.listDeliveryClusters({ page: p, size: s, keyword: kw || undefined, pending: td })
        setRes(out)
        setRows(out.list || [])
        setTotal(out.total || 0)
      } catch {
        // 拦截器已透出后端中文结论
      } finally {
        setLoading(false)
      }
    },
    [page, size, keyword, pending],
  )

  useEffect(() => {
    load(1, size)
    ignApi.dictionaries().then(setDict).catch(() => {})
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const openConsole = useCallback(
    (clusterRef: string, tab?: string) => nav(`/ign/console/${encodeURIComponent(clusterRef)}${tab ? `?tab=${tab}` : ''}`),
    [nav],
  )

  const columns = useMemo<ColumnsType<ClusterIndexRow>>(
    () => [
      {
        title: '集群标识',
        dataIndex: 'cluster_ref',
        width: 200,
        // el03：跨域引用只存标识、可复制、点击进入台；本域没有名称可渲染（INV-33）
        render: (v: string) => (
          <Space size={4}>
            <Typography.Link strong copyable={{ text: v }} onClick={() => openConsole(v)}>
              {v}
            </Typography.Link>
          </Space>
        ),
      },
      {
        title: '交付记录数',
        dataIndex: 'task_count',
        width: 130,
        // el04：含终态记录（口径 12：终态默认可见）；在跑数作次级注记，供「有在跑任务」筛项自解释
        render: (v: number, r) => (
          <Space size={4}>
            <span>{v ?? 0}</span>
            {r.running_task_count > 0 && <Tag color="processing">在跑 {r.running_task_count}</Tag>}
          </Space>
        ),
      },
      {
        title: '最近一次交付',
        dataIndex: 'last_task_no',
        width: 320,
        render: (_, r) => (
          <LastDeliveryCell row={r} verdictLabel={verdictLabel} onOpen={() => openConsole(r.cluster_ref, 'log')} />
        ),
      },
      {
        title: '已登记覆写字段',
        dataIndex: 'effective_override_count',
        width: 150,
        // el06：只计生效态；已回落的不计入但台内可见，此处给次级注记免得"数字对不上"
        render: (v: number, r) => (
          <Space size={4}>
            <span>{v ?? 0}</span>
            {r.rolled_back_override_count > 0 && (
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                （另 {r.rolled_back_override_count} 项已回落）
              </Typography.Text>
            )}
          </Space>
        ),
      },
      {
        title: '未决偏差',
        dataIndex: 'pending_deviation_count',
        width: 120,
        // el07：LOST 或已发生状态修正的计数（LOST 单列不并入失败，口径 7）；为 0 显示 0 而非留空（口径 11）
        render: (v: number) =>
          v > 0 ? (
            <Tooltip title="该集群处于 LOST 或已发生状态修正的任务计数；逐笔纠偏明细在台内「日志与对账」页签（REP-12 同源），不在本表">
              <Tag color="warning">{v}（待对账）</Tag>
            </Tooltip>
          ) : (
            <span>0</span>
          ),
      },
      {
        title: '动作',
        key: 'act',
        width: 170,
        // el09：恒可见（可见性判据只有一条 —— 该标识在本表出现过）；缺省页签由台内按 9.11.6 派生
        render: (_, r) => (
          <Button type="link" size="small" icon={<ArrowRightOutlined />} onClick={() => openConsole(r.cluster_ref)}>
            进入集群交付台
          </Button>
        ),
      },
    ],
    [openConsole, verdictLabel],
  )

  const emptyText = keyword || pending !== 'ALL' ? FILTERED_EMPTY_NOTE : EMPTY_NOTE

  return (
    <Card>
      <TableToolbar
        title="本域交付集群索引"
        total={total}
        description={
          res?.scope_note ||
          '本表只列「本域服务过的」集群：标识取自交付记录与覆写登记的并集去重（零 DDL、不落表不缓存）；上游集群的名称 / 生命周期状态 / 健康不在本域，一律不呈现（INV-33）'
        }
        extra={
          <Button icon={<ReloadOutlined />} loading={loading} onClick={() => load()}>
            刷新
          </Button>
        }
      />
      <Space wrap style={{ marginBottom: 12 }}>
        <Input
          allowClear
          style={{ width: 240 }}
          prefix={<SearchOutlined style={{ opacity: 0.45 }} />}
          placeholder="集群标识 / 关键字（CLU-…）"
          value={keyword}
          onChange={(e) => setKeyword(e.target.value)}
          onPressEnter={() => {
            setPage(1)
            load(1, size)
          }}
        />
        <Select style={{ width: 160 }} value={pending} options={PENDING_OPTIONS} onChange={setPending} />
        <Button
          type="primary"
          icon={<SearchOutlined />}
          onClick={() => {
            setPage(1)
            load(1, size)
          }}
        >
          查询
        </Button>
        {/* el08：统计时点标注必现 —— 摘要为即时派生，不是"该集群现在的状态" */}
        <Typography.Text type="secondary" style={{ fontSize: 12 }}>
          {res?.caliber_note || '摘要截至本次查询时刻（即时派生，进入台内以台内实时数据为准）'}
        </Typography.Text>
      </Space>
      <Table
        rowKey="cluster_ref"
        columns={columns}
        dataSource={rows}
        loading={loading}
        scroll={{ x: 1100 }}
        locale={{ emptyText }}
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
            load(p, s)
          },
        }}
      />
    </Card>
  )
}
