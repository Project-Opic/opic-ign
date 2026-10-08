import { useEffect, useState } from 'react'
import { dictItems, ignApi, type DictItem, type ParamFaceField } from '../api/ign'

// 输入提示语是纯界面文案（不参与校验、也不是取值域），所以留在界面侧；未命中的键返回空串。
const PLACEHOLDER_BY_KEY: Record<string, string> = {
  deploy_container_runtime: 'containerd / docker',
  deploy_dns_domain: '例 cluster.local',
  deploy_image_mirror: '一行一条 host[:port]，如 mirror.internal:5000',
  deploy_install_source: 'online / offline',
  deploy_network_plugin: 'flannel / calico / cilium',
  deploy_node_port_range: '例 30000-32767',
  deploy_pod_cidr: '例 10.200.0.0/16',
  deploy_proxy_mode: 'iptables / ipvs / nftables',
  deploy_service_cidr: '例 10.68.0.0/16',
}

/**
 * 参数面目录：字段键合法域取 /api/meta/ign-param-face（后端面定义表的只读投影），
 * 文件面 / 值形态 / 模板态 / 覆写态的中文名取 /api/v1/ign/dictionaries 的 M1 字典。
 * 界面不保留任何副本 —— 面定义或字典改动即随接口生效，不需要重新发版（MU el09 口径）。
 */
export function useParamFace() {
  const [fields, setFields] = useState<ParamFaceField[]>([])
  const [dicts, setDicts] = useState<Record<string, unknown>>({})
  const [loaded, setLoaded] = useState(false)

  useEffect(() => {
    let alive = true
    Promise.all([ignApi.paramFaceCatalog(), ignApi.dictionaries()])
      .then(([cat, all]) => {
        if (!alive) return
        setFields(Array.isArray(cat) ? cat : [])
        setDicts(all || {})
        setLoaded(true)
      })
      .catch(() => {
        // 拦截器已提示；目录取不到时下拉为空，比按本地旧副本给出被 INV-41 拒绝的键更安全
      })
    return () => {
      alive = false
    }
  }, [])

  const faces = dictItems(dicts, 'IGN_CONFIG_FILE_FACE')
  // 只列真有键的文件面：出现一个空轴下拉等于邀请用户切过去看空表
  const faceOptions = faces
    .filter((f) => fields.some((x) => x.file_face === f.code))
    .map((f) => ({ value: f.code, label: f.label }))
  const faceLabel = (code: string) => faces.find((f) => f.code === code)?.label || code

  return {
    fields,
    loaded,
    faceOptions,
    faceLabel,
    shapeOptions: dictItems(dicts, 'IGN_VALUE_SHAPE').map((s) => ({ value: s.code, label: s.label })),
    templateStateOptions: dictItems(dicts, 'IGN_PARAM_TEMPLATE_STATE').map((s) => ({ value: s.code, label: s.label })),
    overrideStateLabel: (code: string) => dictItems(dicts, 'IGN_OVERRIDE_STATE').find((s) => s.code === code)?.label || code,
    fieldsOfFace: (code: string) => fields.filter((f) => f.file_face === code),
    fieldOf: (key: string) => fields.find((f) => f.key === key),
    placeholderOf: (key: string) => PLACEHOLDER_BY_KEY[key] || '',
  }
}
