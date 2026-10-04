// MU frmIgnOps 运行观测 · 统计与参数(QUERY_LIST)
// 统计总览查询(FUNC-29,REP-13)/ 运行参数维护(FUNC-30,REF-* 平台配置)。
import { useCallback, useEffect, useState } from 'react'
import { Button, Card, Col, Input, Row, Statistic, Table, message } from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { ReloadOutlined, SaveOutlined } from '@ant-design/icons'
import { ignApi, type IgnParam, type IgnStats } from '../../api/ign'
import TableToolbar from '../../admin/components/TableToolbar'
import { usePermission } from '../../hooks/usePermission'

export default function IgnOpsPage() {
  const hasPerm = usePermission()
  const [stats, setStats] = useState<IgnStats | null>(null)
  const [params, setParams] = useState<IgnParam[]>([])
  const [draft, setDraft] = useState<Record<string, string>>({})
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const [s, p] = await Promise.all([ignApi.stats(), ignApi.getParams()])
      setStats(s)
      setParams(p || [])
      setDraft(Object.fromEntries((p || []).map((x) => [x.config_key, x.config_value])))
    } catch {
      // 拦截器已提示
    } finally {
      setLoading(false)
    }
  }, [])

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

  return (
    <Card>
      <TableToolbar
        title="运行观测"
        total={params.length}
        description="统计总览与运行参数（REP-13 / FUNC-30;REF-* 平台配置）"
        extra={
          <Button icon={<ReloadOutlined />} loading={loading} onClick={load}>
            刷新
          </Button>
        }
      />
      <Row gutter={16} style={{ marginBottom: 16 }}>
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
            <Statistic title="执行机就绪" value={stats?.executor_ready ?? '-'} />
          </Card>
        </Col>
        <Col span={4}>
          <Card size="small">
            <Statistic title="不可达" value={stats?.executor_unreachable ?? '-'} />
          </Card>
        </Col>
        <Col span={4}>
          <Card size="small">
            <Statistic title="任务 7 日成功" value={stats?.task_success_7d ?? '-'} />
          </Card>
        </Col>
        <Col span={4}>
          <Card size="small">
            <Statistic title="纠偏次数" value={stats?.reconcile_count ?? '-'} />
          </Card>
        </Col>
      </Row>
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
      />
    </Card>
  )
}
