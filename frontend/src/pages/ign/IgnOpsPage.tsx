// MU frmIgnOps 运行观测 · 统计与参数(QUERY_LIST)
// 统计总览查询(FUNC-29,REP-13 九块 c1~c9)/ 运行参数维护(FUNC-30,REF-* 平台配置)。
// v1.1 口径：计数类（台账事实）与观测类（系统探测）视觉分列且观测类各带扫描时间；
//
// LOST 独立成卡、不并入失败计数（INV-37/B-CONF-13）；门面受理量与人工提交量分列（INV-34）；
// 拒绝（Z1 治理拒绝 / Z2 执行位不可用）与纠偏分列，不与执行结论混计；
// 执行位命令面挂载路径只读呈现——不在可维护参数内（§1.6.1，改它走部署不走界面）。
import { useCallback, useEffect, useState } from 'react'
import { Alert, Button, Card, Col, Descriptions, Input, Row, Statistic, Table, Tag, message } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { ReloadOutlined, SaveOutlined } from '@ant-design/icons'
import { ignApi, type IgnParam, type IgnStats, type StatBucket } from '../../api/ign'
import TableToolbar from '../../admin/components/TableToolbar'
import { usePermission } from '../../hooks/usePermission'

/** 分布块（c1/c2/c3/c6）的统一小表 */
function DistCard({ title, rows, empty }: { title: string; rows?: StatBucket[]; empty: string }) {
  return (
    <Card size="small" title={title} style={{ marginBottom: 12 }}>
      <Table
        rowKey="code"
        size="small"
        columns={[
          { title: '取值', dataIndex: 'code', key: 'code' },
          { title: '数量', dataIndex: 'count', key: 'count', width: 90 },
        ]}
        dataSource={rows || []}
        pagination={false}
        locale={{ emptyText: empty }}
      />
    </Card>
  )
}

export default function IgnOpsPage() {
  const hasPerm = usePermission()
  const [stats, setStats] = useState<IgnStats | null>(null)
  const [params, setParams] = useState<IgnParam[]>([])
  const [draft, setDraft] = useState<Record<string, string>>({})
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)

  // 运行参数读权限(FUNC-30)与统计读权限(FUNC-29)分属不同岗位:缺前者只影响参数块,不得连带统计卡清空
  const canParams = hasPerm('ign:FUNC-30')

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const [settled] = await Promise.allSettled([ignApi.stats()])
      if (settled.status === 'fulfilled') setStats(settled.value)
      if (canParams) {
        const p = await ignApi.getParams().catch(() => null)
        setParams(p || [])
        setDraft(Object.fromEntries((p || []).map((x) => [x.config_key, x.config_value])))
      }
    } finally {
      setLoading(false)
    }
  }, [canParams])

  useEffect(() => {
    load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const save = async () => {
    setSaving(true)
    try {
      await ignApi.saveParams(params.map((p) => ({ config_key: p.config_key, config_value: draft[p.config_key] ?? p.config_value })))
      message.success('运行参数已保存（REF-* 缺省口径可覆盖,热生效）')
      load()
    } catch {
      // 拦截器已提示
    } finally {
      setSaving(false)
    }
  }

  const columns: ColumnsType<IgnParam> = [
    { title: '配置键', dataIndex: 'config_key', key: 'config_key', width: 240 },
    {
      title: '配置值',
      dataIndex: 'config_value',
      key: 'config_value',
      width: 200,
      render: (_, r) =>
        hasPerm('ign:FUNC-30') ? (
          <Input value={draft[r.config_key]} onChange={(e) => setDraft({ ...draft, [r.config_key]: e.target.value })} style={{ width: 160 }} />
        ) : (
          r.config_value
        ),
    },
    { title: '说明', dataIndex: 'description', key: 'description', ellipsis: true },
    { title: '最近更新', dataIndex: 'updated_at', key: 'updated_at', width: 170, render: (v) => v || '-' },
  ]

  const obs = stats?.local_exec_observation
  const health = stats?.health_snapshot

  return (
    <Card>
      <TableToolbar
        title="运行观测"
        total={params.length}
        description="统计总览（REP-13 九块：台账事实与系统观测分列，观测类各带扫描时间）与运行参数（FUNC-30;REF-*）"
        extra={
          <Button icon={<ReloadOutlined />} loading={loading} onClick={load}>
            刷新
          </Button>
        }
      />

      {/* 台账事实（计数类）*/}
      <Row gutter={16}>
        <Col span={4}>
          <Card size="small">
            <Statistic title="版本数" value={stats?.version_total ?? '-'} />
          </Card>
        </Col>
        <Col span={4}>
          <Card size="small">
            <Statistic title="可用版本" value={stats?.version_available ?? '-'} suffix="/ 总数" />
          </Card>
        </Col>
        <Col span={4}>
          <Card size="small">
            <Statistic title="门面受理（累计 / 7 日）" value={stats?.facade_accept_count ?? '-'} suffix={`/ ${stats?.facade_accept_7d ?? '-'}`} />
          </Card>
        </Col>
        <Col span={4}>
          <Card size="small">
            <Statistic title="人工提交（分列,INV-34）" value={stats?.manual_submit_count ?? '-'} />
          </Card>
        </Col>
        <Col span={4}>
          <Card size="small">
            <Statistic title="7 日完成" value={stats?.task_completed_7d ?? '-'} />
          </Card>
        </Col>
        <Col span={4}>
          <Card size="small">
            <Statistic title="7 日失败" value={stats?.task_failed_7d ?? '-'} />
          </Card>
        </Col>
        <Col span={4}>
          <Card size="small">
            <Statistic title="7 日已取消" value={stats?.task_cancelled_7d ?? '-'} />
          </Card>
        </Col>
        {/* el12：LOST 独立成卡、不并入失败计数（INV-37）*/}
        <Col span={4}>
          <Card size="small" style={{ borderStyle: 'dashed' }}>
            <Statistic title="痕迹不可存活 LOST（独立终态）" value={stats?.task_lost ?? '-'} suffix={`/ 7 日 ${stats?.task_lost_7d ?? '-'}`} />
          </Card>
        </Col>
        <Col span={4}>
          <Card size="small">
            <Statistic title="在途（执行中 / 取消中）" value={stats?.task_open ?? '-'} />
          </Card>
        </Col>
        <Col span={4}>
          <Card size="small">
            <Statistic title="留痕总条数" value={stats?.reconcile_count ?? '-'} />
          </Card>
        </Col>
        <Col span={8}>
          <Card size="small">
            <Statistic
              title="受理侧拒绝（Z1 治理 / Z2 执行位，分列）"
              value={`${stats?.refuse_governance_count ?? 0} / ${stats?.refuse_tool_unavailable_count ?? 0}`}
            />
          </Card>
        </Col>
        <Col span={8}>
          <Card size="small">
            <Statistic
              title="纠偏计数（计数纠偏 / 状态修正 / 失联判定）"
              value={`${stats?.correct_count?.COUNT_CORRECT ?? 0} / ${stats?.correct_count?.STATUS_FIX ?? 0} / ${stats?.correct_count?.TASK_LOST ?? 0}`}
            />
          </Card>
        </Col>
      </Row>

      {/* 观测类：系统探测结论，不是台账 —— 各带扫描时间与口径说明 */}
      <Row gutter={16} style={{ margin: '12px 0' }}>
        <Col span={12}>
          <Card size="small" title="健康快照（观测类）" style={{ borderStyle: 'dashed' }}>
            <Descriptions column={2} size="small">
              <Descriptions.Item label="就绪执行机">{health?.ready ?? '-'}</Descriptions.Item>
              <Descriptions.Item label="心跳不可达">{health?.unreachable ?? '-'}</Descriptions.Item>
              <Descriptions.Item label="超时窗口(秒)">{health?.timeout_seconds ?? '-'}</Descriptions.Item>
              <Descriptions.Item label="扫描时间">{health?.scanned_at || '-'}</Descriptions.Item>
              <Descriptions.Item label="口径" span={2}>
                <span style={{ fontSize: 12, color: '#888' }}>{health?.note || '观测类：不可达是即时结论，不落治理状态（7.2）'}</span>
              </Descriptions.Item>
            </Descriptions>
          </Card>
        </Col>
        <Col span={12}>
          <Card size="small" title="执行位可用（观测类,R-15）" style={{ borderStyle: 'dashed' }}>
            <Descriptions column={2} size="small">
              <Descriptions.Item label="本机执行位">
                {stats?.local_exec_available ? <Tag color="green">可用</Tag> : <Tag color="orange">不可用</Tag>}
              </Descriptions.Item>
              <Descriptions.Item label="扫描时间">{obs?.scanned_at || '-'}</Descriptions.Item>
              <Descriptions.Item label="不可用原因" span={2}>
                {obs?.reason || '命令面挂载与可执行探测均通过'}
              </Descriptions.Item>
              {/* el17：挂载路径与命令面只读呈现，不在可维护参数内（§1.6.1）*/}
              <Descriptions.Item label="命令面可执行（部署配置,只读）" span={2}>
                <Tag bordered={false}>{obs?.tool_bin || '未配置'}</Tag>
              </Descriptions.Item>
              <Descriptions.Item label="工作根目录（部署配置,只读）" span={2}>
                <Tag bordered={false}>{obs?.work_root || '未配置'}</Tag>
                <span style={{ fontSize: 12, color: '#888', marginLeft: 8 }}>改它走部署、不走界面</span>
              </Descriptions.Item>
            </Descriptions>
          </Card>
        </Col>
      </Row>

      <Row gutter={16}>
        <Col span={6}>
          <DistCard title="c1 版本状态分布" rows={stats?.version_status_dist} empty="暂无版本" />
        </Col>
        <Col span={6}>
          <DistCard title="c2 镜像预推完成度" rows={stats?.prepush_status_dist} empty="暂无预推明细" />
        </Col>
        <Col span={6}>
          <DistCard title="c3 执行机治理分布" rows={stats?.executor_status_dist} empty="暂无执行机" />
        </Col>
        <Col span={6}>
          <DistCard title="c6 门面受理结论依据分布" rows={stats?.verdict_source_dist} empty="暂无门面受理记录" />
        </Col>
      </Row>

      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 12 }}
        message="日志保留与清理由系统执行（REF-16），界面不提供手工清理位;门面单次回吐行数上限为 REF-14，超出携带续传游标;终止宽限 REF-15 作用于进程组终止链（SIGTERM→SIGKILL）。"
      />
      <TableToolbar
        title="运行参数（REF-* 平台配置,缺省见说明）"
        total={params.length}
        extra={
          hasPerm('ign:FUNC-30') && (
            <Button type="primary" icon={<SaveOutlined />} loading={saving} onClick={save}>
              保存运行参数
            </Button>
          )
        }
      />
      <Table
        rowKey="config_key"
        columns={columns}
        dataSource={params}
        loading={loading}
        pagination={false}
        scroll={{ x: 900 }}
        locale={{ emptyText: canParams ? '暂无数据' : '运行参数读权限属平台运维管理员（ign:FUNC-30），当前岗位不可见' }}
      />
    </Card>
  )
}
