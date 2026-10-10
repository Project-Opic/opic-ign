import { useEffect, useState, type ReactNode } from 'react'
import { Navigate, Route, Routes, useLocation, useParams } from 'react-router-dom'
import { Spin } from 'antd'
import { useDispatch, useSelector } from 'react-redux'
import { authApi } from '../api/auth'
import { getToken, clearToken } from '../api/request'
import { setAuth, clearAuth } from '../store/slices/authSlice'
import type { AppDispatch, RootState } from '../store'
import UserLayout from '../layouts/UserLayout'
import AdminLayout from '../admin/layout/AdminLayout'
import Login from '../pages/login'
import GlassForbidden from '../admin/pages/Forbidden'
import AdminDashboard from '../admin/pages/Dashboard'
import OperationLogs from '../admin/pages/logs/OperationLogs'
import LoginLogs from '../admin/pages/logs/LoginLogs'
import AuditLogs from '../admin/pages/logs/AuditLogs'
import OnlineUsers from '../admin/pages/OnlineUsers'
import NoticeManage from '../admin/pages/NoticeManage'
import ErrCodes from '../admin/pages/ErrCodes'
import ServerMonitor from '../admin/pages/ServerMonitor'
import { isAdminPermissions } from '../admin/access'
import IgnClusterIndexPage from '../pages/ign/IgnClusterIndexPage'
import IgnClusterConsolePage from '../pages/ign/IgnClusterConsolePage'
import { IgnExecutorHost, IgnTaskHost, IgnVersionHost } from '../pages/ign/IgnHostTabs'
import IgnExecutorPage from '../pages/ign/IgnExecutorPage'
import IgnOpsPage from '../pages/ign/IgnOpsPage'
import Todo from '../pages/workbench/Todo'
import Done from '../pages/workbench/Done'
import Requested from '../pages/workbench/Requested'
import FlowDefinitions from '../pages/flow/FlowDefinitions'
import FlowDesigner from '../pages/flow/FlowDesigner'
import TemporalFlowDesignerPage, { TemporalFlowListPage } from '../pages/flow/TemporalFlowDesigner'
import AIModelConfigPage from '../admin/ai/AIModelConfig'
import IsolationPage from '../admin/ai/IsolationInfo'
import FlowInstances from '../pages/flow/FlowInstances'
import FlowTasks from '../pages/flow/FlowTasks'
import UserManage from '../pages/system/UserManage'
import RoleManage from '../pages/system/RoleManage'
import PermissionManage from '../pages/system/PermissionManage'
import ResourceManage from '../pages/system/ResourceManage'

/** 登录成功 / 恢复会话后的首页:一律进用户工作台(管理员经工作台 Header「管理控制台」入口或直接输 /admin 进控制台) */
export function resolveHomePath(_permissions?: string[] | undefined | null): string {
  return '/ign/cluster'
}

/** 有 token 无 user 时先调 /auth/info 恢复登录态,失败清 token 回 /login */
function RequireAuth({ children }: { children: ReactNode }) {
  const token = getToken()
  const user = useSelector((s: RootState) => s.auth.user)
  const dispatch = useDispatch<AppDispatch>()
  const [failed, setFailed] = useState(false)

  useEffect(() => {
    if (!token || user) return
    let cancelled = false
    authApi
      .info()
      .then((info) => {
        if (!cancelled) dispatch(setAuth({ info }))
      })
      .catch(() => {
        if (!cancelled) {
          clearToken()
          dispatch(clearAuth())
          setFailed(true)
        }
      })
    return () => {
      cancelled = true
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  if (!token || failed) return <Navigate to="/login" replace />
  if (!user) {
    return (
      <div style={{ display: 'flex', justifyContent: 'center', alignItems: 'center', height: '100vh' }}>
        <Spin size="large" tip="加载中…" />
      </div>
    )
  }
  return <>{children}</>
}

/** 根路径:一律重定向到用户工作台 */
function RootRedirect() {
  return <Navigate to="/ign/cluster" replace />
}

/** 旧设计器路径重定向:需要在运行时读取 :id 参数(Navigate.to 不会做参数插值) */
function LegacyDesignerRedirect() {
  const { id } = useParams()
  return <Navigate to={`/admin/flow/designer/${id ?? ''}`} replace />
}

/** v1.5 旧路由重定向：把书签里的"菜单叶 = 一个屏"翻译成"宿主页签 = 同一个屏"（MU routeRetirements）*/
function useLegacyQuery() {
  return new URLSearchParams(useLocation().search)
}

/** /ign/log → /ign/task?tab=log；屏内三子签的深链值域由 ?tab= 迁到 ?log_tab=（9.6 载体注记），其余参数原样带走 */
function LegacyLogRedirect() {
  const q = useLegacyQuery()
  const sub = q.get('tab') || ''
  q.delete('tab')
  if (['task', 'reconcile', 'facade'].includes(sub)) q.set('log_tab', sub)
  q.set('tab', 'log')
  return <Navigate to={`/ign/task?${q.toString()}`} replace />
}

/** /ign/config → 集群交付台的「配置」页签；无集群标识时退到索引屏（配置屏只有一台一轴的读法，跨集群无从选起） */
function LegacyConfigRedirect() {
  const q = useLegacyQuery()
  const ref = q.get('cluster_ref') || ''
  q.delete('cluster_ref')
  if (!ref) return <Navigate to="/ign/cluster" replace />
  q.set('tab', 'config')
  return <Navigate to={`/ign/console/${encodeURIComponent(ref)}?${q.toString()}`} replace />
}

/** 管理控制台守卫:无管理权限 → 玻璃风 403 */
function AdminGuard({ children }: { children: ReactNode }) {
  const permissions = useSelector((s: RootState) => s.auth.permissions)
  if (!isAdminPermissions(permissions)) return <Navigate to="/admin/403" replace />
  return <>{children}</>
}

export default function AppRoutes() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />

      {/* 根路径:一律重定向到用户工作台 */}
      <Route
        path="/"
        element={
          <RequireAuth>
            <RootRedirect />
          </RequireAuth>
        }
      />

      {/* 用户工作台(玻璃风):/ign/* 五叶 + 集群交付台深链、/workbench/* */}
      <Route
        path="/"
        element={
          <RequireAuth>
            <UserLayout />
          </RequireAuth>
        }
      >
        <Route path="ign/cluster" element={<IgnClusterIndexPage />} />
        <Route path="ign/console/:clusterRef" element={<IgnClusterConsolePage />} />
        <Route path="ign/version" element={<IgnVersionHost />} />
        <Route path="ign/executor" element={<IgnExecutorHost />} />
        <Route path="ign/task" element={<IgnTaskHost />} />
        <Route path="ign/ops" element={<IgnOpsPage />} />
        {/* v1.5 载体置换：五条旧叶路由重定向到落点页签，兼容一版（收藏夹 / 上游跳转不得变 404） */}
        <Route path="ign/image" element={<Navigate to="/ign/version?tab=image" replace />} />
        <Route path="ign/template" element={<Navigate to="/ign/version?tab=template" replace />} />
        <Route path="ign/tls" element={<Navigate to="/ign/executor?tab=tls" replace />} />
        <Route path="ign/log" element={<LegacyLogRedirect />} />
        <Route path="ign/config" element={<LegacyConfigRedirect />} />
        <Route path="workbench/todo" element={<Todo />} />
        <Route path="workbench/done" element={<Done />} />
        <Route path="workbench/requested" element={<Requested />} />
      </Route>

      {/* 管理控制台(玻璃主题):/admin/* */}
      <Route path="/admin/403" element={<RequireAuth><GlassForbidden /></RequireAuth>} />
      <Route
        path="/admin"
        element={
          <RequireAuth>
            <AdminGuard>
              <AdminLayout />
            </AdminGuard>
          </RequireAuth>
        }
      >
        <Route index element={<Navigate to="/admin/dashboard" replace />} />
        <Route path="dashboard" element={<AdminDashboard />} />
        <Route path="system/users" element={<UserManage />} />
        <Route path="system/roles" element={<RoleManage />} />
        <Route path="system/permissions" element={<PermissionManage />} />
        <Route path="system/resources" element={<ResourceManage />} />
        <Route path="flow/definitions" element={<FlowDefinitions />} />
        <Route path="flow/designer/:id" element={<FlowDesigner />} />
        <Route path="temporalflow" element={<TemporalFlowListPage />} />
        <Route path="temporalflow/designer/:id" element={<TemporalFlowDesignerPage />} />
        <Route path="temporalflow/designer/new" element={<TemporalFlowDesignerPage />} />
        <Route path="ai-model-config" element={<AIModelConfigPage />} />
        <Route path="isolation" element={<IsolationPage />} />
        <Route path="flow/instances" element={<FlowInstances />} />
        <Route path="flow/tasks" element={<FlowTasks />} />
        <Route path="logs/operation" element={<OperationLogs />} />
        <Route path="logs/login" element={<LoginLogs />} />
        <Route path="logs/audit" element={<AuditLogs />} />
        <Route path="online-users" element={<OnlineUsers />} />
        <Route path="notice" element={<NoticeManage />} />
        <Route path="errcodes" element={<ErrCodes />} />
        <Route path="monitor" element={<ServerMonitor />} />
        <Route path="biz/executor" element={<IgnExecutorPage />} />
        <Route path="biz/approval" element={<Todo />} />
      </Route>

      {/* 旧路径书签兼容:重定向到 /admin 对应页面 */}
      <Route path="/system/users" element={<Navigate to="/admin/system/users" replace />} />
      <Route path="/system/roles" element={<Navigate to="/admin/system/roles" replace />} />
      <Route path="/system/permissions" element={<Navigate to="/admin/system/permissions" replace />} />
      <Route path="/system/resources" element={<Navigate to="/admin/system/resources" replace />} />
      <Route path="/flow/definitions" element={<Navigate to="/admin/flow/definitions" replace />} />
      <Route path="/flow/instances" element={<Navigate to="/admin/flow/instances" replace />} />
      <Route path="/flow/tasks" element={<Navigate to="/admin/flow/tasks" replace />} />
      <Route path="/flow/designer/:id" element={<LegacyDesignerRedirect />} />

      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}
