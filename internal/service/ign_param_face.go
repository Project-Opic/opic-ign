// ign_param_face.go —— 参数面四层取值（R-20 的**唯一**实现）。
//
// 本文件是 v1.3 易用性增补的地基，两件事只做一次：
//
//	① 面定义（REF-17~REF-25）= 参数面字段键的唯一合法键源（INV-41）。键集合、文件面、值形态、
//	   落盘键名、单键形态校验器全部只在 `ignParamFaceFields()` 一张表里；缺省值取自
//	   `defaultIgnConfigs()`（同一份登记键清单），**不再写第二份键清单**（5.20 ⑲）。
//	   字段键字典刻意不建（需求文档 7.3 第三处"不建字典"：权威在这张面定义表）。
//
//	② 四层取值 `resolveParamFace`：生效覆写（EFFECTIVE）> 交付版本派生 > 参数面模板（启用态且
//	   面标识命中）> 平台缺省面。面标识为空 ⇒ 第 3 层**整体跳过**，绝不回落到默认模板（R-20 分支 H：
//	   默认标记只服务上游选择器的缺省值，回落它会让存量集群下次装机参数静默漂移）。
//	   逐字段取最高优先命中，不做层间合并（R-22：列表型是整项替换）。
//	   FUNC-46 显示、FUNC-48 预检、B-AUTO-15 渲染三处**必须调这同一个函数**——预检与受理各写
//	   一套逻辑必然漂移，漂移后的预检就是误导源（R-23）。
//
// 每个字段都给出来源（IGN_PARAM_SOURCE 四值之一，R-20"必现不得为空"）：只有最终值、没有来源的
// 配置视图不解决问题——运维要判断的是"我该改哪一层"。
package service

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"gorm.io/gorm"

	"gitcode.com/opic-ontology/opic-techbase/internal/store"
	"gitcode.com/opic-ontology/opic-techbase/pkg/errcode"
)

// ---------- 字典码（IGN_CONFIG_FILE_FACE / IGN_VALUE_SHAPE / IGN_PARAM_SOURCE） ----------

const (
	ignFaceHosts  = "HOSTS"  // 主机清单面（hosts 的 [all:vars]）
	ignFaceConfig = "CONFIG" // 集群配置面（config.yml）
)

const (
	ignShapeScalar = "SCALAR" // 标量：单值替换
	ignShapeList   = "LIST"   // 列表：整项替换 + 一键回落（R-22，无元素级增删）
)

const (
	ignSrcOverride = "OVERRIDE"         // 集群覆写（第 1 层）
	ignSrcVersion  = "VERSION_DERIVED"  // 交付版本派生（第 2 层）
	ignSrcTemplate = "PARAM_TEMPLATE"   // 参数面模板（第 3 层）
	ignSrcPlatform = "PLATFORM_DEFAULT" // 平台缺省面（第 4 层地板）
)

// IGN_OVERRIDE_STATE（覆写两态，可往复：回落不是删除，是"这一次人工决定不再作数"）。
const (
	ignOverrideEffective  = "EFFECTIVE"
	ignOverrideRolledBack = "ROLLED_BACK"
)

// IGN_PARAM_TEMPLATE_STATE（软删除是标记，不是第四个取值）。
const (
	ignTplDraft    = "DRAFT"
	ignTplEnabled  = "ENABLED"
	ignTplDisabled = "DISABLED"
)

// ignSourceLabels 来源徽章的中文名（REP-17 c6；界面直接显示，不再二次翻译）。
var ignSourceLabels = map[string]string{
	ignSrcOverride: "集群覆写",
	ignSrcVersion:  "交付版本派生",
	ignSrcTemplate: "参数面模板",
	ignSrcPlatform: "平台缺省面",
}

// R-20 各层的"本层跳过"原因文案（REP-17 c3/c4 整列显示；显示与判定同源，界面不自行推断）。
const (
	ignFaceSkipNoRef = "未携带面标识，本层跳过（R-20：默认模板不参与渲染兜底）"
	ignFaceSkipRef   = "未携带面标识，本层跳过"
	ignFaceOtherAxis = "本张模板只属另一文件面（R-22：一张模板只属一轴）"
	ignVerSkipNoRef  = "未携带交付版本引用，本层跳过（沿用发行包自身版本，本域不猜版本号）"
)

// R-18 三态（只读探测与 dry-run 共用同一套结论口径，R-23/R-24）。
const (
	ignStateAbsent   = "ABSENT"   // 无清单 ⇒ 接管
	ignStateManaged  = "MANAGED"  // 有本域标记 ⇒ 幂等改写 / 沿用
	ignStateConflict = "CONFLICT" // 有清单而无本域标记 ⇒ 拒绝改写
)

// ignConflictUnsaved R-24 的回吐文案：受理侧判定、界面照抄显示（"已保存 · 未生效"必须是结论而不是推测）。
const ignConflictUnsaved = "已保存 · 未生效（清单归属冲突）"

// ignListSep 列表值的存储分隔符（整项替换：一行台账 = 落盘后的一项取值）。
const ignListSep = "\n"

// ---------- 面定义：字段键的唯一合法键源（INV-41） ----------

// ignParamField 参数面的一个字段键定义。
type ignParamField struct {
	Key      string // 字段键 = 平台缺省面登记键（REF-17~25；不另建键字典）
	RefID    string // 规则依据（REF-17 … REF-25）
	Label    string // 中文名（REP-17 行首列的可读名）
	FileFace string // IGN_CONFIG_FILE_FACE：该键落在哪一轴
	Shape    string // IGN_VALUE_SHAPE：SCALAR / LIST
	AxisFile string // 落盘文件名（hosts / config.yml）——"当前落盘"列的取数位置
	DiskKey  string // 落盘键名（[all:vars] 的 INI 键 / config.yml 的顶层键）
	Get      func(ignDeployParams) string
	Set      func(*ignDeployParams, string)
	Check    func(ignParamField, string) error // 单键形态校验（受理/预检/保存三处同一实现，R-23）
}

var (
	ignParamFieldsOnce  sync.Once
	ignParamFieldsTable []ignParamField
	ignParamFieldsIndex map[string]ignParamField
)

// ignCheckOneOf 枚举形态校验（枚举外值 ⇒ 明确报错，不改用另一取值继续，REF-21/22/24 同口径）。
func ignCheckOneOf(allow []string) func(ignParamField, string) error {
	return func(f ignParamField, v string) error {
		for _, a := range allow {
			if a == v {
				return nil
			}
		}
		return Fail(errcode.ErrConfig, "交付参数 %s=%q 不在 %s 支持的枚举内（%s）", f.Key, v, f.Label, f.RefID)
	}
}

// ignCheckCIDR 只校验 CIDR 形态：与节点网络是否重叠属部署侧事实，本域不校验（REF-17/18）。
func ignCheckCIDR(f ignParamField, v string) error {
	if !ignCIDRRe.MatchString(v) {
		return Fail(errcode.ErrConfig, "交付参数 %s=%q 不是 CIDR 形态（%s）", f.Key, v, f.RefID)
	}
	return nil
}

// ignCheckDNSDomain 集群 DNS 后缀形态（REF-19：它同时决定服务域名与访问证书域名形态）。
func ignCheckDNSDomain(f ignParamField, v string) error {
	if !ignDNSDomainRe.MatchString(v) {
		return Fail(errcode.ErrConfig, "交付参数 %s=%q 不是合法域名后缀形态（%s：小写字母数字与点/连字符，例 cluster.local）", f.Key, v, f.RefID)
	}
	return nil
}

// ignCheckPortRange NodePort 区间"起始-结束"形态（REF-23）。
func ignCheckPortRange(f ignParamField, v string) error {
	if !ignPortRangeRe.MatchString(v) {
		return Fail(errcode.ErrConfig, "交付参数 %s=%q 不是 起始-结束 端口区间形态（%s）", f.Key, v, f.RefID)
	}
	return nil
}

// ignCheckCNIPlugin CNI 插件枚举（REF-20"须为发行包支持的插件枚举"）：
// 优先按 M1 字典 IGN_CNI_PLUGIN（大小写不敏感，落盘值是小写形态），注册表不可用时退回发行包插件枚举。
func ignCheckCNIPlugin(f ignParamField, v string) error {
	codes := ignDictCodes("IGN_CNI_PLUGIN")
	if len(codes) > 0 {
		if codes[strings.ToUpper(v)] {
			return nil
		}
		return Fail(errcode.ErrConfig, "交付参数 %s=%q 不在 IGN_CNI_PLUGIN 字典内（%s：本域字典为平台预置枚举，不允许运行期新增）", f.Key, v, f.RefID)
	}
	for _, a := range []string{"flannel", "calico", "cilium", "kube-ovn", "kube-router"} {
		if a == v {
			return nil
		}
	}
	return Fail(errcode.ErrConfig, "交付参数 %s=%q 不是发行包支持的 CNI 插件（%s）", f.Key, v, f.RefID)
}

// ignCheckMirrorHost 内网镜像仓库地址形态（REF-25：host[:port]，可带 scheme；列表型逐条目校验）。
func ignCheckMirrorHost(f ignParamField, v string) error {
	items := ignListItems(v)
	if len(items) == 0 {
		return nil // 空即不改写仓库键（REF-25 明文口径），不是形态错误
	}
	for _, it := range items {
		if !ignMirrorHostRe.MatchString(ignMirrorHostOf(it)) {
			return Fail(errcode.ErrConfig, "交付参数 %s 的条目 %q 不是 host[:port] 形态（%s）", f.Key, it, f.RefID)
		}
	}
	return nil
}

// ignParamFaceFields 面定义表（REF-17~REF-25 九键；顺序即 REP-17 的字段键字典序轴内稳定输出）。
func ignParamFaceFields() []ignParamField {
	ignParamFieldsOnce.Do(func() {
		ignParamFieldsTable = []ignParamField{
			{Key: ignCfgDeployServiceCIDR, RefID: "REF-17", Label: "Service 网段", FileFace: ignFaceHosts,
				AxisFile: "hosts", DiskKey: "SERVICE_CIDR", Check: ignCheckCIDR,
				Get: func(p ignDeployParams) string { return p.ServiceCIDR },
				Set: func(p *ignDeployParams, v string) { p.ServiceCIDR = v }},
			{Key: ignCfgDeployPodCIDR, RefID: "REF-18", Label: "Pod 网段", FileFace: ignFaceHosts,
				AxisFile: "hosts", DiskKey: "CLUSTER_CIDR", Check: ignCheckCIDR,
				Get: func(p ignDeployParams) string { return p.PodCIDR },
				Set: func(p *ignDeployParams, v string) { p.PodCIDR = v }},
			{Key: ignCfgDeployDNSDomain, RefID: "REF-19", Label: "集群 DNS 后缀", FileFace: ignFaceHosts,
				AxisFile: "hosts", DiskKey: "CLUSTER_DNS_DOMAIN", Check: ignCheckDNSDomain,
				Get: func(p ignDeployParams) string { return p.DNSDomain },
				Set: func(p *ignDeployParams, v string) { p.DNSDomain = v }},
			{Key: ignCfgDeployNetworkPlugin, RefID: "REF-20", Label: "CNI 插件", FileFace: ignFaceHosts,
				AxisFile: "hosts", DiskKey: "CLUSTER_NETWORK", Check: ignCheckCNIPlugin,
				Get: func(p ignDeployParams) string { return p.NetworkPlugin },
				Set: func(p *ignDeployParams, v string) { p.NetworkPlugin = v }},
			{Key: ignCfgDeployRuntime, RefID: "REF-21", Label: "容器运行时", FileFace: ignFaceHosts,
				AxisFile: "hosts", DiskKey: "CONTAINER_RUNTIME", Check: ignCheckOneOf([]string{"containerd", "docker"}),
				Get: func(p ignDeployParams) string { return p.Runtime },
				Set: func(p *ignDeployParams, v string) { p.Runtime = v }},
			{Key: ignCfgDeployProxyMode, RefID: "REF-22", Label: "kube-proxy 模式", FileFace: ignFaceHosts,
				AxisFile: "hosts", DiskKey: "PROXY_MODE", Check: ignCheckOneOf([]string{"iptables", "ipvs", "nftables"}),
				Get: func(p ignDeployParams) string { return p.ProxyMode },
				Set: func(p *ignDeployParams, v string) { p.ProxyMode = v }},
			{Key: ignCfgDeployNodePortRange, RefID: "REF-23", Label: "NodePort 区间", FileFace: ignFaceHosts,
				AxisFile: "hosts", DiskKey: "NODE_PORT_RANGE", Check: ignCheckPortRange,
				Get: func(p ignDeployParams) string { return p.NodePortRange },
				Set: func(p *ignDeployParams, v string) { p.NodePortRange = v }},
			{Key: ignCfgDeployInstallSource, RefID: "REF-24", Label: "取源方式", FileFace: ignFaceConfig,
				AxisFile: "config.yml", DiskKey: "INSTALL_SOURCE", Check: ignCheckOneOf([]string{"online", "offline"}),
				Get: func(p ignDeployParams) string { return p.InstallSource },
				Set: func(p *ignDeployParams, v string) { p.InstallSource = v }},
			{Key: ignCfgDeployImageMirror, RefID: "REF-25", Label: "内网镜像仓库地址", FileFace: ignFaceConfig,
				AxisFile: "config.yml", DiskKey: "INSECURE_REG", Shape: ignShapeList, Check: ignCheckMirrorHost,
				Get: func(p ignDeployParams) string { return strings.Join(p.MirrorHostList(), ignListSep) },
				Set: func(p *ignDeployParams, v string) { p.setMirrorHosts(v) }},
		}
		for i := range ignParamFieldsTable {
			if ignParamFieldsTable[i].Shape == "" {
				ignParamFieldsTable[i].Shape = ignShapeScalar
			}
		}
		sort.Slice(ignParamFieldsTable, func(i, j int) bool { return ignParamFieldsTable[i].Key < ignParamFieldsTable[i].Key })
		ignParamFieldsIndex = map[string]ignParamField{}
		for _, f := range ignParamFieldsTable {
			ignParamFieldsIndex[f.Key] = f
		}
	})
	return ignParamFieldsTable
}

// ignParamFieldOf 按字段键取面定义（INV-41：面外键在这里判定，模板行/覆写行/落盘核对共用一套）。
func ignParamFieldOf(key string) (ignParamField, bool) {
	f, ok := ignParamFieldsIndex[key]
	if !ok {
		ignParamFaceFields() // 触发一次初始化（首次调用方可能只查键）
		f, ok = ignParamFieldsIndex[key]
	}
	return f, ok
}

// ignParamFaceKeysOfAxis 某文件面的全部字段键（空 fileFace ⇒ 全部键，渲染/预检/只读通道用）。
func ignParamFaceKeysOfAxis(fileFace string) []ignParamField {
	out := []ignParamField{}
	for _, f := range ignParamFaceFields() {
		if fileFace == "" || f.FileFace == fileFace {
			out = append(out, f)
		}
	}
	return out
}

// ParamFaceCatalog 面定义表的对外只读投影（GET /api/meta/ign-param-face）。
// 界面下拉的合法域就是这张表本身（MU el09「刻意不建键字典、面定义改动不需重启界面」）：
// 只出键、中文名、所属文件面、值形态、规则依据与落盘文件名，**不出任何取值、落盘键与校验器**
// —— 值与凭据零出口（INV-08），前端若持有值即等于在本域四层之外自己算了一层取值。
func ParamFaceCatalog() []map[string]any {
	fields := ignParamFaceFields()
	out := make([]map[string]any, 0, len(fields))
	for _, f := range fields {
		out = append(out, map[string]any{
			"key":         f.Key,
			"label":       f.Label,
			"file_face":   f.FileFace,
			"value_shape": f.Shape,
			"ref_id":      f.RefID,
			"axis_file":   f.AxisFile,
		})
	}
	return out
}

// ignCfgFieldDefault 平台缺省面的登记值（取值源仍是 defaultIgnConfigs，面定义不复制第二份缺省清单）。
func ignCfgFieldDefault(key string) string {
	for _, c := range defaultIgnConfigs() {
		if c.Key == key {
			return c.Value
		}
	}
	return ""
}

// ignCheckFieldValue 单键形态校验：空值即"让位"（该键交由下层/现场决定），不判为形态错误。
func ignCheckFieldValue(f ignParamField, v string) error {
	v = strings.TrimSpace(v)
	if v == "" || f.Check == nil {
		return nil
	}
	return f.Check(f, v)
}

// ---------- 列表值与镜像仓库地址的取值辅助 ----------

// ignListItems 列表值文本 → 条目（换行或逗号分隔；去空去重保序——整项替换后仍保持台账顺序）。
func ignListItems(text string) []string {
	var out []string
	seen := map[string]bool{}
	for _, seg := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == ',' || r == '\r' }) {
		s := strings.TrimSpace(seg)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// ignListText 条目 → 存储文本（整项替换的唯一序列化形态）。
func ignListText(items []string) string {
	return strings.Join(items, ignListSep)
}

// ignMirrorHostOf 去掉 scheme 前缀，取仓库地址的 host[:port] 本体。
func ignMirrorHostOf(v string) string {
	h := strings.TrimSpace(v)
	for _, p := range []string{"http://", "https://", "//"} {
		h = strings.TrimPrefix(h, p)
	}
	return strings.TrimSuffix(strings.TrimSpace(h), "/")
}

// MirrorHostList 全部仓库条目（host[:port] 形态；INSECURE_REG 整项替换的落盘输入）。
func (p ignDeployParams) MirrorHostList() []string {
	if len(p.ImageMirrors) > 0 {
		return p.ImageMirrors
	}
	if strings.TrimSpace(p.ImageMirror) == "" {
		return nil
	}
	return []string{strings.TrimSpace(p.ImageMirror)}
}

// setMirrorHosts 写入仓库面：整项列表 + 首个条目（首个条目沿用 v1.1.1 的单值语义，
// 代理例外清单与 SANDBOX_IMAGE 拼接因此逐字节不变）。
func (p *ignDeployParams) setMirrorHosts(text string) {
	items := []string{}
	for _, it := range ignListItems(text) {
		if h := ignMirrorHostOf(it); h != "" {
			items = append(items, h)
		}
	}
	p.ImageMirrors = items
	p.ImageMirror = ""
	if len(items) > 0 {
		p.ImageMirror = items[0]
	}
}

// ignMirrorHosts NO_PROXY 用的仓库主机清单（全部条目；单条目时与 v1.1.1 同值）。
func (p ignDeployParams) MirrorHosts() []string {
	out := []string{}
	for _, it := range p.MirrorHostList() {
		if h := ignMirrorHostOf(it); h != "" {
			out = append(out, h)
		}
	}
	return out
}

// ---------- 四层取值（R-20 唯一实现） ----------

// paramLayerCell 单字段在某一层的取值结果（未命中时 hit=false，note 说明为什么没命中）。
type paramLayerCell struct {
	Value        string `json:"value"`
	Hit          bool   `json:"hit"`
	Note         string `json:"note,omitempty"`
	Status       string `json:"status,omitempty"` // 覆写层：IGN_OVERRIDE_STATE
	Reason       string `json:"reason,omitempty"` // 覆写层：事由（治理事实必须说为什么改）
	TaskNo       string `json:"applied_task_no,omitempty"`
	AppliedAt    string `json:"applied_at,omitempty"`
	RolledBackAt string `json:"rolled_back_at,omitempty"`
	SourceRef    string `json:"source_ref,omitempty"` // 该层的事实来源（模板编号 / 版本引用 / 覆写坐标）
}

// paramFieldTrace 单字段的四层取值轨迹（REP-17 的一行；R-20 要求逐字段可给出生效来源）。
type paramFieldTrace struct {
	FieldKey             string         `json:"field_key"`
	RefID                string         `json:"ref_id"`
	Label                string         `json:"label"`
	FileFace             string         `json:"file_face"`
	ValueShape           string         `json:"value_shape"`
	PlatformDefault      paramLayerCell `json:"platform_default"`
	TemplateValue        paramLayerCell `json:"param_template"`
	VersionValue         paramLayerCell `json:"version_derived"`
	OverrideValue        paramLayerCell `json:"override"`
	EffectiveValue       string         `json:"effective_value"`
	EffectiveSource      string         `json:"effective_source"`
	EffectiveSourceLabel string         `json:"effective_source_label"`
}

// paramFaceView 一次四层取值的完整结果。
type paramFaceView struct {
	Params           ignDeployParams   `json:"-"`
	Traces           []paramFieldTrace `json:"fields"`
	ClusterRef       string            `json:"cluster_ref"`
	FileFace         string            `json:"file_face,omitempty"`
	ParamTemplateRef string            `json:"param_template_ref,omitempty"` // 命中的模板编号（空 ⇒ 第 3 层整体跳过）
	TemplateNotice   string            `json:"param_template_notice"`        // 整列级结论（REP-17 c3 列头提示）
	TemplateFileFace string            `json:"param_template_face,omitempty"`
	TemplateStatus   string            `json:"param_template_status,omitempty"`
	VersionRef       string            `json:"version_ref,omitempty"`
	VersionNotice    string            `json:"version_notice,omitempty"` // 第 2 层整列级结论
	VersionStatus    string            `json:"version_status,omitempty"`
	OverrideKeys     []string          `json:"-"` // 真实参与取值的覆写坐标（B-AUTO-15 追溯回填的判据）
	DriftKeys        []string          `json:"-"` // 与平台缺省面不同的键（任务注记用）
	templateHits     map[string]bool   `json:"-"` // 第 3 层逐字段命中的键（"有行且非空"）
	templateErr      error             `json:"-"` // 面标识不可解析 / 非启用态（受理拒绝，显示不拒绝）
	versionErr       error             `json:"-"` // 版本引用不可解析 / 不可交付（INV-33 / R-19）
}

// NewParamFaceView 空的命中标记集初始化（在 resolveParamFace 内按需建立）。
func (v *paramFaceView) markTemplateHit(key string) {
	if v.templateHits == nil {
		v.templateHits = map[string]bool{}
	}
	v.templateHits[key] = true
}

// ignResolveVersionRow 解析交付版本引用（INV-33 可解析 + R-19 可交付）。
//
// 引用为空 ⇒ (nil, nil)：本层不适用，不是错误。
// 引用非空但不可解析 / 不可交付 ⇒ 返回治理拒绝错误，但**不在此处中断取值**：错误随视图交给
// 受理侧门槛（ignFaceGateUsable），显示侧（FUNC-46 / FUNC-50）据此在 c4 列给出处置说明。
// 判定只有一份实现 —— 预检、受理、显示读的是同一个结论（R-23）。
func ignResolveVersionRow(db *gorm.DB, versionRef string) (map[string]any, error) {
	versionRef = strings.TrimSpace(versionRef)
	if versionRef == "" {
		return nil, nil
	}
	row, err := store.One(db, `SELECT * FROM delivery_version
		WHERE flag = 1
		  AND (version_no = ? OR LOWER(name) = LOWER(?))
		ORDER BY id DESC`, versionRef, versionRef)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, ignGovernanceRefuse(fmt.Sprintf(
			"交付版本引用 %q 在本域无登记（INV-33 跨域只存标识：标识必须可解析，未登记即治理拒绝）", versionRef))
	}
	status := Str(row["version_status"])
	if !ignVersionDeliverable[status] {
		return nil, ignGovernanceRefuse(fmt.Sprintf(
			"交付版本引用 %q 的状态为「%s」（%s），不在可交付范围（R-19：须未归档且就绪及之后 —— 就绪由发行包下载执行达成）",
			versionRef, ignVersionStatusLabel(status), status))
	}
	return row, nil
}

// ignVersionFaceHits 版本登记事实里落在参数面键域内的取值（第 2 层的逐字段命中集）。
// REF-20 明文：交付版本登记了插件时以版本登记为准。其余组件版本（编排引擎/CNI 版本/CoreDNS/
// pause 镜像/运行时版本）不在 REF-17~25 键域内 ⇒ 只进渲染、不进 REP-17 的行集（模型缺口已登记上报）。
func ignVersionFaceHits(row map[string]any) map[string]string {
	out := map[string]string{}
	if row == nil {
		return out
	}
	if cni := strings.ToLower(Str(row["cni_plugin"])); cni != "" {
		out[ignCfgDeployNetworkPlugin] = cni
	}
	return out
}

// resolveParamFace 四层取值的唯一入口（R-20）。
//
// clusterRef 为空 ⇒ 不看覆写层（不属于任何集群的全局面，如 FUNC-30 缺省面预览）；
// fileFace 为空 ⇒ 返回两轴全部键（渲染 / 预检 / 只读通道都要看整张渲染面）；
// paramTemplateRef 为空 ⇒ 第 3 层整体跳过（R-20：绝不回落到默认模板）；
// 版本引用不可解析（INV-33）/ 不可交付（R-19）、面标识不可解析 / 非启用态（R-13 增补口径 + R-20）
// ⇒ 不中断取值，而是把治理拒绝结论与列级说明带回视图里，由受理侧门槛 ignFaceGateUsable 决定是否拒绝。
func resolveParamFace(db *gorm.DB, clusterRef, fileFace, paramTemplateRef, versionRef string) (*paramFaceView, error) {
	if db == nil {
		db = store.DB
	}
	clusterRef = strings.TrimSpace(clusterRef)
	fileFace = strings.TrimSpace(fileFace)
	paramTemplateRef = strings.TrimSpace(paramTemplateRef)
	versionRef = strings.TrimSpace(versionRef)
	fields := ignParamFaceKeysOfAxis(fileFace)

	// 第 4 层：平台缺省面（REF-17~25 登记键，热读）
	base := ignDeployProfileOf(db)

	view := &paramFaceView{
		ClusterRef: clusterRef, FileFace: fileFace, VersionRef: versionRef,
		ParamTemplateRef: paramTemplateRef,
	}

	// 第 3 层：参数面模板（启用态 + 面标识命中 + 一张模板只属一轴）
	tplParams := base
	if paramTemplateRef == "" {
		view.TemplateNotice = ignFaceSkipNoRef
	} else {
		tpl, items, refuse, notice := ignFaceTemplateItems(db, paramTemplateRef, fileFace)
		if refuse != nil {
			view.templateErr = refuse
			view.TemplateNotice = Str(AsBizErr(refuse).Msg)
		} else if tpl != nil {
			view.ParamTemplateRef = Str(tpl["template_no"])
			view.TemplateFileFace = Str(tpl["file_face"])
			view.TemplateStatus = Str(tpl["template_status"])
			view.TemplateNotice = notice
			for k, v := range items {
				f, ok := ignParamFieldOf(k)
				if !ok || strings.TrimSpace(v) == "" {
					continue // 面外键不可能入库（FUNC-41 已拒）；空值即"让位"，本层不命中
				}
				f.Set(&tplParams, v)
				view.markTemplateHit(k)
			}
		}
	}

	// 第 2 层：交付版本派生（组件版本面；落在面定义键域内的键才进 REP-17 行集）
	verParams := base
	versionRow, verr := ignResolveVersionRow(db, versionRef)
	switch {
	case verr != nil:
		view.versionErr = verr
		view.VersionNotice = Str(AsBizErr(verr).Msg)
		versionRow = nil // 引用不可用 ⇒ 本层不命中（取值仍可由其余三层给出）
	case versionRow == nil:
		view.VersionNotice = ignVerSkipNoRef
	default:
		if err := ignApplyVersionRow(versionRow, &verParams); err != nil {
			return nil, err
		}
		view.VersionStatus = Str(versionRow["version_status"])
		view.VersionNotice = "交付版本 " + Str(versionRow["version_no"]) + "（" +
			ignVersionStatusLabel(view.VersionStatus) + "）登记的组件面参与派生"
	}
	hits := ignVersionFaceHits(versionRow)

	// 第 1 层：集群交付配置生效覆写（IGN_OVERRIDE_STATE=EFFECTIVE）
	var ovrRows []map[string]any
	if clusterRef != "" {
		rows, err := store.List(db, `SELECT * FROM ign_cluster_config_override
			WHERE flag = 1 AND cluster_ref = ? ORDER BY file_face, field_key`, clusterRef)
		if err != nil {
			return nil, err
		}
		ovrRows = rows
	}
	ovrIndex := map[string]map[string]any{}
	for _, r := range ovrRows {
		ovrIndex[Str(r["field_key"])] = r
	}

	view.Traces = make([]paramFieldTrace, 0, len(fields))
	// 渲染面以"版本派生面"为底：参数面键逐字段被下面的循环改写，非参数面键（SSH 免密、
	// 编排引擎/CNI/CoreDNS 版本、pause 镜像）不进 REP-17 行集，但必须随渲染面一起交付
	// —— 取值源仍是版本派生面 + 缺省面兜底（v1.1.1 行为逐字节不变）。
	p := verParams
	for _, f := range fields {
		cellPlatform := paramLayerCell{Value: f.Get(base), Hit: true, SourceRef: "平台配置项 " + f.Key}
		cellTpl := paramLayerCell{}
		switch {
		case view.ParamTemplateRef == "":
			cellTpl.Note = view.TemplateNotice // 未携带面标识 / 本层未命中（原因即列级结论）
		case view.templateErr != nil:
			cellTpl.Note = cellNote(view.TemplateNotice)
		case f.FileFace != view.TemplateFileFace:
			cellTpl.Note = ignFaceOtherAxis
		default:
			val := f.Get(tplParams)
			cellTpl = paramLayerCell{
				Value: val, Hit: view.templateHits[f.Key], SourceRef: view.ParamTemplateRef,
			}
			if !cellTpl.Hit {
				cellTpl.Note = "该键在此模板内无字段行或值为空（R-22：空值即让位，本层不命中）"
			}
		}
		cellVer := paramLayerCell{Note: view.VersionNotice}
		if v, ok := hits[f.Key]; ok {
			cellVer = paramLayerCell{Value: v, Hit: true, SourceRef: versionRef}
		} else if versionRef != "" && view.versionErr == nil {
			cellVer.Note = "交付版本登记事实不在此键的参数面键域内（R-20：本层不命中）"
		}
		cellOvr := paramLayerCell{}
		if row, has := ovrIndex[f.Key]; has && (fileFace == "" || Str(row["file_face"]) == f.FileFace) {
			status := Str(row["override_status"])
			value := Str(row["override_value"])
			if f.Shape == ignShapeList {
				value = ignListText(ignListItems(value))
			}
			cellOvr = paramLayerCell{
				Value: value, Reason: Str(row["reason"]), Status: status,
				TaskNo: Str(row["applied_task_no"]), AppliedAt: Str(row["applied_at"]),
				RolledBackAt: Str(row["rolled_back_at"]),
				SourceRef:    fmt.Sprintf("%s/%s/%s", Str(row["cluster_ref"]), Str(row["file_face"]), Str(row["field_key"])),
			}
			if status == ignOverrideEffective {
				cellOvr.Hit = true
			} else {
				cellOvr.Note = "已回落，不参与取值（R-20：取值回落到下层；行保留以可查曾改过什么）"
			}
		}

		tr := paramFieldTrace{
			FieldKey: f.Key, RefID: f.RefID, Label: f.Label,
			FileFace: f.FileFace, ValueShape: f.Shape,
			PlatformDefault: cellPlatform, TemplateValue: cellTpl,
			VersionValue: cellVer, OverrideValue: cellOvr,
		}
		// 优先级次序固定（R-20）：覆写 > 版本派生 > 模板 > 缺省面；逐字段取最高优先命中，不做层间合并。
		for _, l := range []struct {
			cell paramLayerCell
			src  string
		}{
			{cellOvr, ignSrcOverride},
			{cellVer, ignSrcVersion},
			{cellTpl, ignSrcTemplate},
			{cellPlatform, ignSrcPlatform},
		} {
			if !l.cell.Hit {
				continue
			}
			tr.EffectiveValue = l.cell.Value
			tr.EffectiveSource = l.src
			tr.EffectiveSourceLabel = ignSourceLabels[l.src]
			break
		}
		if tr.EffectiveSource == "" { // R-20"必现不得为空"：地板层恒命中，不产生空徽章
			tr.EffectiveValue = cellPlatform.Value
			tr.EffectiveSource = ignSrcPlatform
			tr.EffectiveSourceLabel = ignSourceLabels[ignSrcPlatform]
		}
		f.Set(&p, tr.EffectiveValue)
		view.Traces = append(view.Traces, tr)
		if tr.EffectiveSource == ignSrcOverride {
			view.OverrideKeys = append(view.OverrideKeys, f.FileFace+"|"+f.Key)
		}
		if tr.EffectiveValue != f.Get(base) {
			view.DriftKeys = append(view.DriftKeys, tr.FieldKey)
		}
	}
	view.Params = p
	return view, nil
}

// cellNote 把列级结论复制到未命中单元（显示与判定同源，界面不自行推断）。
func cellNote(s string) string {
	if strings.TrimSpace(s) == "" {
		return "本层未命中"
	}
	return s
}

// ignFaceGateUsable 受理侧 / 预检侧的取值层门槛（R-13 增补口径 + R-19）：
// 携带了版本引用或面标识却不是一条可用事实 ⇒ 治理拒绝，绝不静默用下层取值继续装机。
// 显示侧（FUNC-46 / FUNC-50）不调本门槛 —— 它们要呈现"为什么不命中"，而不是拒绝回答。
func ignFaceGateUsable(view *paramFaceView) error {
	if view == nil {
		return nil
	}
	if view.versionErr != nil {
		return view.versionErr
	}
	if view.templateErr != nil {
		return view.templateErr
	}
	return nil
}

// ignFaceEffectiveUsable 生效面能否被受理/预检放行的合一点：取值层门槛（R-13 增补口径 + R-19）
// 加一次形态收口（ignValidateDeployParams）。受理侧（ignProfileForAction）与 dry-run（FUNC-48）
// 都调它 —— 门槛与校验分两处、两侧各挑一半，就会出现"预检绿、受理走旧路径"的分歧（R-23）。
// p 允许是生效面叠加待保存覆写候选后的面（FUNC-48 的 overrides），故与 view 分开传。
func ignFaceEffectiveUsable(view *paramFaceView, p ignDeployParams) error {
	if err := ignFaceGateUsable(view); err != nil {
		return err
	}
	return ignValidateDeployParams(p)
}

// ignFaceTemplateItems 第 3 层的模板解析：返回模板行 + 字段值集 + 列级说明 + 治理拒绝结论。
// 不可解析 / 非启用态 ⇒ refuse（受理即拒）；面不符 ⇒ 只回列级说明（一张模板只属一轴，另一轴本层不命中）。
func ignFaceTemplateItems(db *gorm.DB, ref, fileFace string) (map[string]any, map[string]string, error, string) {
	row, err := ignFaceTemplateOf(db, ref)
	if err != nil {
		return nil, nil, err, ""
	}
	if row == nil {
		return nil, nil, ignGovernanceRefuse(fmt.Sprintf(
			"参数面模板标识 %q 在本域无登记（R-13 本批增补口径：递交的是登记标识，标识必须可解析；已软删或未登记即治理拒绝）", ref)), ""
	}
	status := Str(row["template_status"])
	if status != "ENABLED" {
		return nil, nil, ignGovernanceRefuse(fmt.Sprintf(
			"参数面模板标识 %q 指向的模板状态为「%s」，非启用态不参与取值（R-20：第 3 层只认启用态；停用是「不再被新集群选中」，不是「已生效的取值作废」）",
			ref, ignTemplateStateLabel(status))), ""
	}
	if fileFace != "" && Str(row["file_face"]) != fileFace {
		return row, map[string]string{}, nil, ignFaceOtherAxis
	}
	items, err := store.List(db, `SELECT field_key, field_value FROM ign_param_template_item
		WHERE flag = 1 AND template_id = ? ORDER BY field_key`, toInt(row["id"]))
	if err != nil {
		return nil, nil, err, ""
	}
	out := map[string]string{}
	for _, it := range items {
		out[Str(it["field_key"])] = Str(it["field_value"])
	}
	notice := "面标识命中启用态模板 " + Str(row["template_no"])
	if len(out) == 0 {
		notice = "面标识命中的模板无字段行（R-22：整组替换后未提交的键即不存在，本层不命中）"
	}
	return row, out, nil, notice
}

// ignFaceTemplateOf 按登记标识解析模板行（编号优先，其次名称；与版本引用同口径）。
func ignFaceTemplateOf(db *gorm.DB, ref string) (map[string]any, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, nil
	}
	return store.One(db, `SELECT * FROM ign_param_template
		WHERE flag = 1 AND (template_no = ? OR LOWER(template_name) = LOWER(?))
		ORDER BY id DESC`, ref, ref)
}

// ignTemplateStateLabel 模板状态的中文名（拒绝理由要能读懂）。
func ignTemplateStateLabel(code string) string {
	switch code {
	case "DRAFT":
		return "草稿"
	case "ENABLED":
		return "启用"
	case "DISABLED":
		return "停用"
	}
	return code
}

// ignOverrideStateLabel 覆写状态中文名。
func ignOverrideStateLabel(code string) string {
	if code == "ROLLED_BACK" {
		return "已回落"
	}
	return "生效"
}

// ignValidateParamFace 逐字段形态校验（R-23：模板行、覆写行、预检、受理共用这一处）。
func ignValidateParamFace(fields []ignParamField, vals map[string]string) error {
	for _, f := range fields {
		v, ok := vals[f.Key]
		if !ok {
			continue
		}
		if err := ignCheckFieldValue(f, v); err != nil {
			return err
		}
	}
	return nil
}

// ---------- 清单/配置文件的"当前落盘"回读（REP-17 c7；报表口径 2） ----------

var (
	ignDNSDomainRe   = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]{0,61}[a-z0-9])?$`)
	ignMirrorHostRe  = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]{0,62})(:\d{2,5})?$`)
	ignYAMLTopItemRe = regexp.MustCompile(`^\s*-\s*(.*)$`)
)

// ignFieldOnDisk 读 clusters/<ref> 里该字段键的当前落盘值。
// 取数失败回"不可用 + 原因"（报表口径 2：不以空值或"与生效值相同"代替）。
func ignFieldOnDisk(clusterRef string, f ignParamField) (string, string) {
	if !ignClusterIDRe.MatchString(clusterRef) {
		return "", "集群标识形态非法，无法定位工作目录（INV-33）"
	}
	dir := ignClusterDir(clusterRef)
	if f.FileFace == ignFaceHosts {
		text, err := ignReadClusterFile(dir, "hosts")
		if err != nil {
			return "", "不可用：clusters/" + clusterRef + "/hosts 不可读（交付配置尚未生成）"
		}
		_, sections := ignSplitINI(text)
		for _, s := range sections {
			if s.name != "all:vars" {
				continue
			}
			if idx := ignFindINIKey(s.body, f.DiskKey); idx >= 0 {
				m := ignINIKeyRe.FindStringSubmatch(s.body[idx])
				return strings.Trim(strings.TrimSpace(m[3]), `"`), ""
			}
		}
		return "", "不可用：hosts 的 [all:vars] 内无 " + f.DiskKey + " 键（发行包模板形态异常，R-15）"
	}
	text, err := ignReadClusterFile(dir, "config.yml")
	if err != nil {
		return "", "不可用：clusters/" + clusterRef + "/config.yml 不可读（交付配置尚未生成）"
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		m := ignYAMLKeyRe.FindStringSubmatch(l)
		if m == nil || m[2] != f.DiskKey {
			continue
		}
		if f.Shape == ignShapeList {
			var items []string
			for j := i + 1; j < len(lines); j++ {
				im := ignYAMLTopItemRe.FindStringSubmatch(lines[j])
				if im == nil {
					break
				}
				items = append(items, strings.Trim(strings.TrimSpace(im[1]), `"`))
			}
			if len(items) == 0 {
				return "", "" // 键在册而条目为空：这是落盘事实，不是取数失败（报表口径 2 只管取数失败）
			}
			return ignListText(items), ""
		}
		return strings.Trim(strings.TrimSpace(m[3]), `"`), ""
	}
	return "", "不可用：config.yml 内无 " + f.DiskKey + " 键（发行包模板形态异常，R-15）"
}

// ---------- R-18 三态只读探测（FUNC-46 冲突标注 / FUNC-47 回吐 / FUNC-48 预判共用） ----------

// ignInventoryState 只读判定该集群清单的归属三态（不写盘、不建任务、不改任何行）。
func ignInventoryState(clusterRef string) (string, string, error) {
	if !ignClusterIDRe.MatchString(clusterRef) {
		return "", "", Fail(errcode.ErrParam, "关联集群标识 %q 不符合格式白名单（INV-33）", clusterRef)
	}
	dir := ignClusterDir(clusterRef)
	hostsText, hostsErr := ignReadClusterFile(dir, "hosts")
	confText, confErr := ignReadClusterFile(dir, "config.yml")
	if hostsErr != nil || confErr != nil {
		return ignStateAbsent, "工作目录无清单 ⇒ 下次受理按登记事实接管（R-18 ①）", nil
	}
	m, err := ignLoadManifest(dir)
	if err != nil {
		return "", "", err
	}
	if m == nil {
		return ignStateConflict, "清单在册但无本域派生标记 ⇒ 非本域产物，受理即刻拒绝改写（R-18 ③）", nil
	}
	if m.HostsSHA256 != ignSHA256(hostsText) || m.ConfigSHA256 != ignSHA256(confText) {
		return ignStateConflict, "清单在册但内容与本域派生指纹不符 ⇒ 接管后被改动，受理即刻拒绝（R-15）", nil
	}
	return ignStateManaged, "清单由本域派生在册 ⇒ 下次受理逐字段跟随事实改写（R-18 ②）", nil
}

// ignFaceRequestContext 本次递交的取值上下文：请求体携带的标识优先，为空即回落到该集群
// 接管标记里的面标识快照（上游上一次递交的那张牌）。回落的是"快照"而不是"默认模板" ——
// 后者会让未指定模板的存量集群下次装机参数静默漂移（R-20 分支 H）。
// 受理侧（ignProfileForAction）与显示/预检侧（FUNC-46/48/50）都读这一处，两侧不得各写一套。
func ignFaceRequestContext(clusterRef, paramTemplateRef, versionRef string) (string, string) {
	paramTemplateRef = strings.TrimSpace(paramTemplateRef)
	versionRef = strings.TrimSpace(versionRef)
	if paramTemplateRef == "" || versionRef == "" {
		mplRef, mVerRef := ignClusterFaceContext(clusterRef)
		if paramTemplateRef == "" {
			paramTemplateRef = mplRef
		}
		if versionRef == "" {
			versionRef = mVerRef
		}
	}
	return paramTemplateRef, versionRef
}

// ignClusterFaceContext 该集群当前的取值上下文（上游面标识声明与版本引用的本域留存事实）。
// 权威落点是接管标记快照：R-18 明文"标记同时快照当次落盘的派生事实，单一事实源、不另设第二套清单"。
func ignClusterFaceContext(clusterRef string) (paramTemplateRef, versionRef string) {
	if !ignClusterIDRe.MatchString(clusterRef) {
		return "", ""
	}
	m, err := ignLoadManifest(ignClusterDir(clusterRef))
	if err != nil || m == nil {
		return "", ""
	}
	return strings.TrimSpace(m.ParamTemplateRef), strings.TrimSpace(m.VersionRef)
}

// ignClustersDeclaringFace 反查"哪些集群的上游面标识声明指向这张模板"（R-25 引用来源 ①）。
// 本域不直查他域表（INV-33 同族）：依据是受理时快照在接管标记里的登记标识，单一事实源。
func ignClustersDeclaringFace(templateRef string) []string {
	templateRef = strings.TrimSpace(templateRef)
	out := []string{}
	root := strings.TrimSpace(ignWorkRoot())
	if root == "" || templateRef == "" {
		return out
	}
	entries, err := os.ReadDir(filepath.Join(root, "clusters"))
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m, err := ignLoadManifest(filepath.Join(root, "clusters", e.Name()))
		if err != nil || m == nil {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(m.ParamTemplateRef), templateRef) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// ignTemplateReferences 一张模板的被引用出处（R-25 两类来源 + FUNC-44 拒绝时必须列出的清单）。
func ignTemplateReferences(db *gorm.DB, tpl map[string]any) ([]map[string]any, error) {
	out := []map[string]any{}
	if tpl == nil {
		return out, nil
	}
	templateNo := Str(tpl["template_no"])
	seen := map[string]bool{}
	// ① 上游 O-CLC 集群模板登记的参数面模板标识（跨域只存标识，由本域受理快照留存）
	for _, ref := range ignClustersDeclaringFace(templateNo) {
		seen[ref] = true
		out = append(out, map[string]any{
			"cluster_ref": ref, "source_type": "UPSTREAM_FACE_DECLARATION",
			"source_label":       "上游集群模板的面标识声明（R-25 ①，本域接管标记快照留存）",
			"param_template_ref": templateNo,
		})
	}
	// ② 本域生效态覆写所依据的模板行（同域可直接查，R-25 ②）
	rows, err := store.List(db, `SELECT o.cluster_ref, o.file_face, o.field_key, o.override_value, o.reason
		FROM ign_cluster_config_override o
		JOIN ign_param_template_item i ON i.template_id = ? AND i.flag = 1 AND i.field_key = o.field_key
		WHERE o.flag = 1 AND o.override_status = 'EFFECTIVE' AND o.file_face = ?
		ORDER BY o.cluster_ref, o.field_key`, toInt(tpl["id"]), Str(tpl["file_face"]))
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		ref := Str(r["cluster_ref"])
		if !seen[ref] {
			seen[ref] = true
			out = append(out, map[string]any{
				"cluster_ref": ref, "source_type": "EFFECTIVE_OVERRIDE",
				"source_label": "本域生效态覆写依据该模板行（R-25 ②）", "param_template_ref": templateNo,
			})
		}
		out = append(out, map[string]any{
			"cluster_ref": ref, "source_type": "EFFECTIVE_OVERRIDE_DETAIL",
			"source_label": "覆写坐标 " + ref + "/" + Str(r["file_face"]) + "/" + Str(r["field_key"]),
			"field_key":    Str(r["field_key"]), "override_value": Str(r["override_value"]),
			"reason": Str(r["reason"]),
		})
	}
	return out, nil
}

// ---------- REF-26 界面日志自动跟随间隔（热生效；越界夹到边界并提示） ----------

const (
	ignCfgLogFollowIntervalSec = "log_follow_interval_sec" // REF-26 缺省 2 秒，允许 2~3 秒
	ignLogFollowMin            = 2
	ignLogFollowMax            = 3
	ignLogFollowDefault        = 2
)

// ignLogFollowIntervalSec 读取界面节奏参数：只影响刷新节奏，不影响游标语义与数据完整性（REF-26）。
// 越界值夹到边界并回吐提示 —— 不做静默回落，也不让界面拿到一个不确定的节奏。
func ignLogFollowIntervalSec(db *gorm.DB) (int64, string) {
	raw, err := ignConfigGet(db, ignCfgLogFollowIntervalSec)
	if err != nil || strings.TrimSpace(raw) == "" {
		return ignLogFollowDefault, ""
	}
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return ignLogFollowDefault, fmt.Sprintf("REF-26 配置值 %q 不是整数，按缺省 %d 秒执行", raw, ignLogFollowDefault)
	}
	if n < ignLogFollowMin || n > ignLogFollowMax {
		clamped := int64(ignLogFollowMin)
		if n > ignLogFollowMax {
			clamped = ignLogFollowMax
		}
		return clamped, fmt.Sprintf("REF-26 允许区间 2~3 秒，登记值 %d 秒越界 ⇒ 取边界 %d 秒（只影响界面刷新节奏，游标语义不变）", n, clamped)
	}
	return n, ""
}
