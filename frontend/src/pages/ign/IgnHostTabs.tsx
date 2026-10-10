// MU v1.5 载体置换（Q-129 / Q-133 / Q-134）：三个横看屏就地升格为宿主屏的页签装配壳。
// 本文件零新增语义 —— 不新增功能点 / 报表 / 权限码 / 字典 / 规则，页签主体一律渲染既有屏组件本体
//（9.1 / 9.3 / 9.5 的载体注记：被承载屏的元素表一字未动，变的只是"从哪儿进"）。
// 页签可见性沿用该屏退出侧栏前菜单叶的同一张主数据牌（8.1.1 ③：载体置换不改变任何权限判定）；
// 缺位页签不进序列、深链落进无权页签静默落回首签（8.3.2 ①②）；接口侧仍由各权限码把门，界面隐藏不是安全边界。
import { useEffect, type ReactNode } from 'react'
import { Tabs } from 'antd'
import { useSearchParams } from 'react-router-dom'
import { usePermission } from '../../hooks/usePermission'
import IgnVersionPage from './IgnVersionPage'
import IgnImagePage from './IgnImagePage'
import IgnParamTemplatePage from './IgnParamTemplatePage'
import IgnExecutorPage from './IgnExecutorPage'
import IgnTlsPage from './IgnTlsPage'
import IgnTaskPage from './IgnTaskPage'
import IgnLogPage from './IgnLogPage'

export interface IgnHostTab {
  /** 深链值：?tab=<key>（MU routeRetirements 的四条重定向即按这套值落点） */
  key: string
  label: string
  /** 该页签自己的主数据权限码（任一命中即渲染；缺位不渲染） */
  perms: string[]
  body: ReactNode
}

export function IgnHostTabs({ tabs }: { tabs: IgnHostTab[] }) {
  const hasPerm = usePermission()
  const [searchParams, setSearchParams] = useSearchParams()
  const visible = tabs.filter((t) => hasPerm(t.perms))
  const urlTab = searchParams.get('tab')
  const active = visible.find((t) => t.key === urlTab)?.key ?? visible[0]?.key

  // 8.3.2 ②：地址栏里那条越权深链要同步改写掉，否则刷新还是同一条
  useEffect(() => {
    if (!urlTab || visible.some((t) => t.key === urlTab)) return
    setSearchParams(active ? { tab: active } : {}, { replace: true })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [urlTab, active])

  return (
    <Tabs
      activeKey={active}
      onChange={(k) => setSearchParams({ tab: k }, { replace: true })}
      destroyOnHidden
      items={visible.map((t) => ({ key: t.key, label: t.label, children: t.body }))}
    />
  )
}

/** 「交付资产」叶：版本目录 ｜ 发行包与镜像 ｜ 参数面模板（frmIgnImage / frmIgnTemplate 的落点页签） */
export function IgnVersionHost() {
  return (
    <IgnHostTabs
      tabs={[
        { key: 'version', label: '版本目录', perms: ['ign:FUNC-06'], body: <IgnVersionPage /> },
        { key: 'image', label: '发行包与镜像', perms: ['ign:FUNC-12'], body: <IgnImagePage /> },
        { key: 'template', label: '参数面模板', perms: ['ign:FUNC-38'], body: <IgnParamTemplatePage /> },
      ]}
    />
  )
}

/** 「执行位」叶：执行机 ｜ 通道安全（强制注销审批弹窗位置不变、不升格为页签 —— 9.3 载体注记） */
export function IgnExecutorHost() {
  return (
    <IgnHostTabs
      tabs={[
        { key: 'executor', label: '执行机', perms: ['ign:FUNC-17'], body: <IgnExecutorPage /> },
        { key: 'tls', label: '通道安全', perms: ['ign:FUNC-22'], body: <IgnTlsPage /> },
      ]}
    />
  )
}

/** 「交付任务」叶：任务台账 ｜ 日志与对账（横看形态，不带 clusterRef —— 台内挂点才是纵看） */
export function IgnTaskHost() {
  return (
    <IgnHostTabs
      tabs={[
        { key: 'ledger', label: '任务台账', perms: ['ign:FUNC-23', 'ign:FUNC-25'], body: <IgnTaskPage /> },
        { key: 'log', label: '日志与对账', perms: ['ign:FUNC-25'], body: <IgnLogPage /> },
      ]}
    />
  )
}
