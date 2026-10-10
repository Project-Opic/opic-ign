// MU frmIgnClusterConsole 集群交付台(MASTER_DETAIL_FORM;v1.5 新增宿主屏，无菜单叶)
// 由 frmIgnClusterIndex 行操作「进入集群交付台」经深链 /ign/console/:clusterRef 进入。
// 主 = 这一个集群在本域的头部徽章条（内容只允许是 REP-19 的派生摘要 + 标识本身），从 = 三页签组
//（配置｜任务｜日志与对账 —— 本域三种视角的并列，不是流程节点）。
// 宿主屏零新增语义（9.11.1 / 8.6 第 21 项）：本屏不新增功能点 / 报表 / 权限码 / 字典 / 规则，
// v1.5 新增的 FUNC-52 / REP-19 / PERM-FUNC-52 落在索引屏；页签主体一律渲染既有屏本体（回指 9.9 / 9.5 / 9.6），不抄第二份。
// 台内绝不出现步进器 / 步骤条 /「第 N 步」（Q-69 裁定 ②：步进权威在 O-CLC，本域连状态机都没有）；
// 也不出现上游集群名称 / 生命周期状态 / 健康（INV-33，本域没有那份数据可抄）。
import { useCallback, useEffect, useMemo, useState } from 'react'
import { Alert, Button, Card, Space, Spin, Tabs, Tag, Tooltip, Typography } from 'antd'
import { ArrowLeftOutlined, ExportOutlined, ReloadOutlined } from '@ant-design/icons'
import { useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { dictItems, ignApi, type ClusterIndexRow } from '../../api/ign'
import { IgnTaskStatusPill } from '../../admin/components/StatusPill'
import { usePermission } from '../../hooks/usePermission'
import IgnClusterConfigPage from './IgnClusterConfigPage'
import IgnTaskPage from './IgnTaskPage'
import IgnLogPage from './IgnLogPage'

const TAB_KEYS = ['config', 'task', 'log'] as const
type TabKey = (typeof TAB_KEYS)[number]

const TAB_LABELS: Record<TabKey, string> = { config: '配置', task: '任务', log: '日志与对账' }

/** 需要看集群本身（名称 / 状态 / 节点）一律跳 O-CLC，不留副本、不做内嵌卡片（9.11.2 末行）。
 *  子域部署下 ign.* 与 clc.* 同构，就地换主机名；本地开发没有同构主机名时落回部署域地址。 */
function clcClusterLedgerUrl() {
  const { protocol, hostname } = window.location
  const host = hostname.startsWith('ign.') ? `clc.${hostname.slice('ign.'.length)}` : 'clc.opic-ai.ccoe.tech'
  return `${protocol}//${host}/clc/cluster`
}

export default function IgnClusterConsolePage() {
  const { clusterRef = '' } = useParams()
  const nav = useNavigate()
  const hasPerm = usePermission()
  const [searchParams, setSearchParams] = useSearchParams()
  const [dict, setDict] = useState<Record<string, unknown>>({})

  // 徽章条数据 = REP-19 按标识精确取一行（与索引清单同一查询面，不是第二份报表）
  const [row, setRow] = useState<ClusterIndexRow | null>(null)
  const [missing, setMissing] = useState(false)
  const [degraded, setDegraded] = useState(false)
  const [loading, setLoading] = useState(true)

  // 台级广播：页签内写动作成功后自增 ⇒ 徽章条重取 + 配置签落盘列刷新（9.11.7 ①②③）
  const [tick, setTick] = useState(0)

  const reloadBadges = useCallback((quiet = false) => {
    if (!quiet) setLoading(true)
    return ignApi
      .listDeliveryClusters({ page: 1, size: 1, cluster_ref: clusterRef })
      .then((res) => {
        const hit = (res.list || []).find((r) => r.cluster_ref === clusterRef) || null
        setRow(hit)
        setMissing(!hit)
        setDegraded(false)
      })
      .catch(() => {
        // 取数失败不等于"本域没服务过"：那是查询结论，不是错误结论（口径 11 计数与观测可分）
        setDegraded(true)
      })
      .finally(() => setLoading(false))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [clusterRef])

  useEffect(() => {
    setLoading(true)
    setRow(null)
    setMissing(false)
    reloadBadges(true)
    ignApi.dictionaries().then(setDict).catch(() => {})
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [clusterRef])

  const onChanged = useCallback(() => {
    setTick((t) => t + 1)
    reloadBadges(true)
  }, [reloadBadges])

  const verdictLabel = useCallback(
    (code?: string | null) => dictItems(dict, 'IGN_VERDICT_SOURCE').find((d) => d.code === code)?.label || code || '-',
    [dict],
  )

  // 页签可见性按各自主数据权限码判（8.3.2 ① / 9.11.2），缺位页签不进序列；接口侧仍由各权限码把门
  const tabVisible: Record<TabKey, boolean> = {
    config: hasPerm('ign:FUNC-46'),
    task: hasPerm(['ign:FUNC-23', 'ign:FUNC-25']),
    log: hasPerm('ign:FUNC-25'),
  }
  const visibleTabs = TAB_KEYS.filter((k) => tabVisible[k])

  // 9.11.6 缺省页签派生：有未决偏差 ⇒ 日志与对账；有在跑 ⇒ 任务；无在跑有生效覆写 ⇒ 配置；其余 ⇒ 配置
  const defaultTab: TabKey | undefined = useMemo(() => {
    if (!row) return undefined
    const derived: TabKey =
      row.pending_deviation_count > 0 ? 'log' : row.running_task_count > 0 ? 'task' : 'config'
    return tabVisible[derived] ? derived : visibleTabs[0]
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [row, visibleTabs.join(',')])

  const urlTab = searchParams.get('tab') as TabKey | null
  const activeTab = urlTab && tabVisible[urlTab] ? urlTab : defaultTab

  // 8.3.2 ②：深链落进无权页签 ⇒ 静默落回第一个有权签，地址栏同步改写（否则刷新还是那条越权深链）
  useEffect(() => {
    if (!urlTab || tabVisible[urlTab]) return
    setSearchParams(activeTab ? { tab: activeTab } : {}, { replace: true })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [urlTab, clusterRef, visibleTabs.join(',')])

  if (loading && !row && !missing) {
    return (
      <Card>
        <Spin />
      </Card>
    )
  }

  // 9.11.6：标识在本域从未出现过（不在 REP-19 键集）⇒ 提示后返回索引，不给空白台
  if (missing) {
    return (
      <Card>
        <Alert
          type="info"
          showIcon
          message={`本域没有该集群的交付记录：${clusterRef}`}
          description="本域只登记自己服务过的标识（REP-19 键集 = 交付记录与覆写登记的并集）。集群是否存在、叫什么、状态如何，是 O-CLC 的事实（INV-33）。"
          action={<Button size="small" onClick={() => nav('/ign/cluster')}>返回索引</Button>}
        />
      </Card>
    )
  }

  if (visibleTabs.length === 0) {
    return (
      <Card>
        <Alert
          type="warning"
          showIcon
          message="当前身份在本域没有任何台内页签的主数据牌，故不进台（9.11.4：不给「进得来、什么都看不了」的空壳态）"
          action={<Button size="small" onClick={() => nav('/ign/cluster')}>返回索引</Button>}
        />
      </Card>
    )
  }

  // 徽章条：只允许 REP-19 的派生值 + 标识本身；页签切换不重载本条（9.11.6）
  const badges = (
    <Card size="small" style={{ marginBottom: 12 }}>
      <Space wrap size={6} style={{ width: '100%', justifyContent: 'space-between' }}>
        <Space wrap size={6}>
          <Typography.Text strong copyable={{ text: clusterRef }} style={{ fontSize: 15 }}>
            {clusterRef}
          </Typography.Text>
          {row?.last_task_no ? (
            <Space size={4}>
              <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                最近一次交付
              </Typography.Text>
              <Typography.Link onClick={() => nav(`/ign/console/${encodeURIComponent(clusterRef)}?tab=log`)}>
                {row.last_task_status ? <IgnTaskStatusPill status={row.last_task_status} /> : '—'}
              </Typography.Link>
              <Typography.Text style={{ fontSize: 12 }}>
                {row.last_task_no}（{row.last_ended_at || '尚无结束时间'}）· 结论依据 {verdictLabel(row.last_verdict_source)}
              </Typography.Text>
            </Space>
          ) : (
            <Tag>本域尚未替该集群执行过交付任务（仅有覆写登记）</Tag>
          )}
          <Tag color={row?.running_task_count ? 'processing' : 'default'}>在跑任务 {row?.running_task_count ?? 0}</Tag>
          <Tooltip title="生效态覆写字段计数；已回落的不计入但台内「配置」页签可见（REP-19 口径）">
            <Tag color={row?.effective_override_count ? 'purple' : 'default'}>
              生效覆写 {row?.effective_override_count ?? 0} 字段
              {row?.rolled_back_override_count ? `（另 ${row.rolled_back_override_count} 项已回落）` : ''}
            </Tag>
          </Tooltip>
          <Tooltip title="处于 LOST 或已发生状态修正的任务计数（LOST 单列不并入失败）；逐笔纠偏明细在「日志与对账」页签">
            <Tag color={row?.pending_deviation_count ? 'warning' : 'default'}>未决偏差 {row?.pending_deviation_count ?? 0}</Tag>
          </Tooltip>
          {degraded && (
            <Typography.Text type="warning" style={{ fontSize: 12 }}>
              徽章条取数失败（页签内数据照常按各自查询面呈现；此处不做"没有记录"的推断）
            </Typography.Text>
          )}
        </Space>
        <Space size={6}>
          <Button size="small" icon={<ReloadOutlined />} onClick={() => reloadBadges()}>
            刷新徽章
          </Button>
          {/* el08：跨域内容一律跳转，不留副本 */}
          <Button size="small" icon={<ExportOutlined />} href={clcClusterLedgerUrl()} target="_blank" rel="noreferrer">
            在集群中心看该集群
          </Button>
          <Button size="small" icon={<ArrowLeftOutlined />} onClick={() => nav('/ign/cluster')}>
            返回索引
          </Button>
        </Space>
      </Space>
    </Card>
  )

  // 页签主体一律带 clusterRef 作 key：路由参数换成另一个集群时整棵子树重挂载，
  // 否则子屏 useState 的初值会残留上一个集群的上下文。
  const items = visibleTabs.map((k) => ({
    key: k,
    label: TAB_LABELS[k],
    children:
      k === 'config' ? (
        <IgnClusterConfigPage key={`config-${clusterRef}`} clusterRef={clusterRef} onSaved={onChanged} refreshToken={tick} />
      ) : k === 'task' ? (
        <IgnTaskPage key={`task-${clusterRef}`} clusterRef={clusterRef} onChanged={onChanged} />
      ) : (
        <IgnLogPage key={`log-${clusterRef}`} clusterRef={clusterRef} />
      ),
  }))

  return (
    <Card styles={{ body: { paddingTop: 12 } }}>
      {badges}
      <Tabs
        activeKey={activeTab}
        onChange={(k) => setSearchParams({ tab: k }, { replace: true })}
        items={items}
        destroyOnHidden
      />
    </Card>
  )
}
