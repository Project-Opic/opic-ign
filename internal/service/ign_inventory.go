// ign_inventory.go —— 集群交付配置生成（P2：打通执行位与集群配置生成）。
//
// 命令面事实（3.200 用真 kubeasz 跑通 01~07 后固化，不是推测）：
//   - ezctl 的一切子命令都以 `clusters/<集群标识>/hosts` 与 `config.yml` 为唯一输入
//     （`-i clusters/$1/hosts -e @clusters/$1/config.yml`），且**只有 `ezctl new` 会替换
//     hosts 里的 `_cluster_name_` 占位符**；
//   - `add-node`/`del-node` 自己会把地址 sed 进 `[kube_node]`，本域若预先写入只会换来
//     "node already existed" 的远程困惑 ⇒ 安装期配置由本域派生，节点增删期由命令面维护；
//   - `add-node` 只取 `$2` 一个地址（多地址须走 `add-nodes`，见 ign_exec.go 的 argv 渲染）。
//
// 治理纪律：交付配置的**派生依据**是门面请求体里的登记事实（节点清单/版本引用/面标识）加本域
// 四层取值（R-20：集群覆写 → 交付版本派生 → 参数面模板 → 平台缺省面，权威实现只在
// resolveParamFace 一处），不是上游下发的模板文本；私钥只以路径进入 inventory，
// 绝不进 argv、不进日志、不出域（R-12 同源）。接管标记 .ign-delivery.json 让"这份配置
// 是不是本域派生的"成为可判定事实 —— 非本域产物一律拒绝改写（不静默覆盖人工配置）。
package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gorm.io/gorm"

	"gitcode.com/opic-ontology/opic-techbase/internal/config"
	"gitcode.com/opic-ontology/opic-techbase/internal/store"
	"gitcode.com/opic-ontology/opic-techbase/pkg/errcode"
)

// ignInventoryManifest 接管标记文件名（落在 clusters/<ref>/ 下，随集群目录一起被撤）。
const ignInventoryManifest = ".ign-delivery.json"

// 节点角色词汇：本域口径 master/worker/etcd，并兼容上游 O-CLC 的 CLC_NODE_ROLE 字典码
// （MASTER/WORKER/INFRA ⇒ 控制面/工作负载/基础设施即 etcd 专责）。契约词汇由被调用方定义（Q-70）。
const (
	ignRoleMaster = "master"
	ignRoleWorker = "worker"
	ignRoleEtcd   = "etcd"
)

var ignRoleAliases = map[string]string{
	"MASTER": ignRoleMaster, "CONTROL": ignRoleMaster, "MP": ignRoleMaster,
	"WORKER": ignRoleWorker, "NODE": ignRoleWorker, "MINION": ignRoleWorker,
	"ETCD": ignRoleEtcd, "INFRA": ignRoleEtcd, "INFRASTRUCTURE": ignRoleEtcd,
}

type ignNodeSpec struct {
	Addr     string
	Role     string
	Nodename string
}

// ignTopology 角色 → kubeasz 主机组的派生结果（单份事实、三个视图，不复制存储）。
type ignTopology struct {
	Etcd   []ignNodeSpec
	Master []ignNodeSpec
	Node   []ignNodeSpec
}

func (t ignTopology) IsEmpty() bool {
	return len(t.Etcd) == 0 && len(t.Master) == 0 && len(t.Node) == 0
}

// ignDeployParams 交付配置渲染面（发行包 hosts/[all:vars] 与 config.yml 的受管键）。
// 参数面取值四层（R-20 唯一实现 resolveParamFace）：集群覆写 → 交付版本派生 → 参数面模板 → 平台缺省面；
// 拓扑面（成员与角色）只来自请求体节点清单（Q-72），两者不同轴、请求体不下发参数模板。
type ignDeployParams struct {
	ServiceCIDR    string
	PodCIDR        string
	DNSDomain      string
	NetworkPlugin  string
	Runtime        string
	ProxyMode      string
	NodePortRange  string
	InstallSource  string
	ImageMirror    string // REF-25 单值语义（v1.1.1 兼容位）：多条目时为列表首项
	SSHUser        string
	SSHKey         string
	K8sVersion     string
	CNIVersion     string
	CoreDNSVersion string
	PauseImage     string // 交付版本镜像清单里的 pause 镜像 repo[:tag]（仓库地址由镜像面拼接）
	MasterAddrs    []string
	NodeAddrs      []string
	// ImageMirrors 内网镜像仓库地址的**整项列表**（R-22：列表型是整体替换，不是逐元素增删）。
	// 空 ⇒ 不改写仓库相关键（REF-25 明文口径）。落盘形态即 INSECURE_REG 的条目集。
	ImageMirrors []string
}

// ignAddrList 组内地址视图（渲染与断言共用，避免两处各自取列）。
func ignAddrList(specs []ignNodeSpec) []string {
	out := make([]string, 0, len(specs))
	for _, s := range specs {
		out = append(out, s.Addr)
	}
	return out
}

func ignHasAddr(list []string, addr string) bool {
	for _, v := range list {
		if v == addr {
			return true
		}
	}
	return false
}

// ---------- 请求体节点清单解析 ----------

var ignIPv4Re = regexp.MustCompile(`^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$`)

func ignIsIPv4(v string) bool {
	m := ignIPv4Re.FindStringSubmatch(v)
	if m == nil {
		return false
	}
	for _, part := range m[1:] {
		if len(part) > 1 && part[0] == '0' {
			return false // 前导零在命令面与 ansible 里都是歧义源
		}
		if n := mustIntPart(part); n > 255 {
			return false
		}
	}
	return true
}

func mustIntPart(s string) int {
	n := 0
	for _, r := range s {
		n = n*10 + int(r-'0')
	}
	return n
}

func ignNodeField(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s := strings.TrimSpace(Str(v)); s != "" {
				return s
			}
		}
	}
	return ""
}

// ignParseFacadeNodes 节点清单归一（Q-72：进请求体、两侧各校验一次）。
// 兼容三种形态：空白分隔串、地址串数组、对象数组（addr/ip_address + role/node_role + nodename/k8s_nodename）。
func ignParseFacadeNodes(v any) ([]ignNodeSpec, error) {
	var out []ignNodeSpec
	push := func(spec ignNodeSpec) error {
		if spec.Addr == "" {
			return Fail(errcode.ErrForbidden, "节点清单存在缺地址的条目（R-13：地址非法即治理拒绝）")
		}
		if err := ignValidateNodeAddr(spec.Addr); err != nil {
			return err
		}
		role, err := ignNormalizeRole(spec.Role)
		if err != nil {
			return err
		}
		spec.Role = role
		if spec.Nodename != "" && !ignK8sNodenameRe.MatchString(spec.Nodename) {
			return Fail(errcode.ErrForbidden,
				"节点名称 %q 不符合 kubernetes 节点命名（小写字母数字与 - .，首尾须为字母数字；kubeasz 的 k8s_nodename 同规）", spec.Nodename)
		}
		for _, prev := range out {
			if prev.Addr == spec.Addr {
				return Fail(errcode.ErrParam, "节点清单地址 %q 重复（FUNC-34：清单须是唯一地址集）", spec.Addr)
			}
		}
		out = append(out, spec)
		return nil
	}

	switch x := v.(type) {
	case nil:
		return nil, nil
	case string:
		for _, a := range ignArgvTokens(x) {
			if err := push(ignNodeSpec{Addr: a}); err != nil {
				return nil, err
			}
		}
	case []string:
		for _, a := range x {
			if err := push(ignNodeSpec{Addr: strings.TrimSpace(a)}); err != nil {
				return nil, err
			}
		}
	case []any:
		for _, it := range x {
			switch e := it.(type) {
			case map[string]any:
				spec := ignNodeSpec{
					Addr:     ignNodeField(e, "addr", "address", "ip", "ip_address"),
					Role:     ignNodeField(e, "role", "node_role"),
					Nodename: ignNodeField(e, "nodename", "node_name", "k8s_nodename"),
				}
				if err := push(spec); err != nil {
					return nil, err
				}
			case string:
				if err := push(ignNodeSpec{Addr: strings.TrimSpace(e)}); err != nil {
					return nil, err
				}
			default:
				s := strings.TrimSpace(fmt.Sprint(e))
				if s == "" {
					continue
				}
				if err := push(ignNodeSpec{Addr: s}); err != nil {
					return nil, err
				}
			}
		}
	default:
		return nil, Fail(errcode.ErrParam, "节点清单形态不符（FUNC-34：地址串数组或对象数组）")
	}
	return out, nil
}

var ignK8sNodenameRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)

// ignNormalizeRole 角色词汇归一：本域口径（master/worker/etcd）+ 上游 O-CLC 的 CLC_NODE_ROLE
// 字典码；词汇表外的角色不可静默进拓扑（它会被渲染进主机组，等于把未知语义带进交付链路）。
func ignNormalizeRole(role string) (string, error) {
	r := strings.ToUpper(strings.TrimSpace(role))
	if r == "" {
		return ignRoleWorker, nil // 只给地址 ⇒ 工作节点（O-CLC 的加节点动作正是 "add a work node"）
	}
	if v, ok := ignRoleAliases[r]; ok {
		return v, nil
	}
	switch strings.ToLower(r) {
	case ignRoleMaster, ignRoleWorker, ignRoleEtcd:
		return strings.ToLower(r), nil
	}
	return "", Fail(errcode.ErrForbidden,
		"节点角色 %q 不在本域已登记词汇（master/worker/etcd 与 CLC_NODE_ROLE）内（R-13 治理拒绝）", role)
}

// ignBuildTopology 角色 → 主机组（kubeasz 语义：控制面同机兼跑 etcd 与 kube_node；
// etcd 专责节点只进 [etcd]；奇数成员是 kubeasz 对 etcd 的明示要求，作治理规则而非注释）。
func ignBuildTopology(nodes []ignNodeSpec) (ignTopology, error) {
	var t ignTopology
	for _, n := range nodes {
		role, err := ignNormalizeRole(n.Role)
		if err != nil {
			return t, err
		}
		n.Role = role
		switch role {
		case ignRoleMaster:
			t.Etcd = append(t.Etcd, n)
			t.Master = append(t.Master, n)
			t.Node = append(t.Node, n)
		case ignRoleEtcd:
			t.Etcd = append(t.Etcd, n)
		default:
			t.Node = append(t.Node, n)
		}
	}
	if len(t.Master) == 0 {
		return t, Fail(errcode.ErrParam,
			"节点清单必须含至少一个控制面角色（FUNC-34：kubeasz 的 [kube_master] 为空则安装无从落地）")
	}
	if len(t.Etcd)%2 == 0 {
		return t, Fail(errcode.ErrForbidden,
			"etcd 成员数为偶数（%d）：Paxos 法定数不增益、分区时全数不可写（发行包明示 odd member(s)，R-13 治理拒绝）",
			len(t.Etcd))
	}
	return t, nil
}

// ---------- hosts 渲染（定点改写：受管组重写、受管键改写、其余原样保留） ----------

type ignIniSection struct {
	name string
	body []string
}

func ignSplitINI(text string) (preamble []string, sections []ignIniSection) {
	cur := -1
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			sections = append(sections, ignIniSection{name: t[1 : len(t)-1]})
			cur = len(sections) - 1
			continue
		}
		if cur < 0 {
			preamble = append(preamble, line)
			continue
		}
		sections[cur].body = append(sections[cur].body, line)
	}
	return preamble, sections
}

func ignJoinINI(preamble []string, sections []ignIniSection) string {
	var b strings.Builder
	for _, l := range preamble {
		b.WriteString(l + "\n")
	}
	for _, s := range sections {
		b.WriteString("[" + s.name + "]\n")
		for _, l := range s.body {
			b.WriteString(l + "\n")
		}
	}
	out := b.String()
	// 收尾收敛：模板末尾空行数量不参与事实判定，但同一输入必须产出同一字节串（幂等前提）
	return strings.TrimRight(out, "\n") + "\n"
}

// ignGroupLines 组内成员行：在原成员行的位置写入新成员（其余行原位保留 —— 注释是发行包给
// 运维的现场提示，也是下一个组块的抬头说明，不能被挪到本组成员之后）。
func ignGroupLines(body []string, members []ignNodeSpec) []string {
	var out []string
	replaced := false
	for _, l := range body {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") {
			out = append(out, l)
			continue
		}
		if replaced {
			continue // 原成员行整体被新事实替换（只留一份）
		}
		out = append(out, ignMemberLines(members)...)
		replaced = true
	}
	if !replaced {
		out = append(out, ignMemberLines(members)...)
	}
	return out
}

func ignMemberLines(members []ignNodeSpec) []string {
	out := make([]string, 0, len(members))
	for _, m := range members {
		line := m.Addr
		if m.Nodename != "" {
			line += " k8s_nodename=" + m.Nodename
		}
		out = append(out, line)
	}
	return out
}

var ignINIKeyRe = regexp.MustCompile(`^(\s*)([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$`)

// ignSetINIValue 改写 [all:vars] 里的键值（保留原缩进与引号风格；缺失则追加）。
func ignSetINIValue(body []string, key, value string, quote bool) []string {
	wanted := value
	if quote {
		wanted = `"` + value + `"`
	}
	for i, l := range body {
		m := ignINIKeyRe.FindStringSubmatch(l)
		if m == nil || m[2] != key {
			continue
		}
		old := strings.TrimSpace(m[3])
		styled := wanted
		if strings.HasPrefix(old, `"`) && !quote {
			styled = `"` + value + `"`
		} else if !strings.HasPrefix(old, `"`) && quote {
			styled = value
		}
		body[i] = m[1] + key + "=" + styled
		return body
	}
	// 追加位置：末尾注释块之后（保持 [all:vars] 的键面连续，ansible 不关心顺序）
	for len(body) > 0 && (strings.HasPrefix(strings.TrimSpace(body[len(body)-1]), "#") ||
		strings.TrimSpace(body[len(body)-1]) == "") {
		body = body[:len(body)-1]
	}
	return append(append(body, ""), key+"="+wanted)
}

func ignRenderHosts(tpl, clusterRef string, topo ignTopology, p ignDeployParams) (string, error) {
	if !ignClusterIDRe.MatchString(clusterRef) {
		return "", Fail(errcode.ErrParam, "关联集群标识 %q 不符合格式白名单（INV-33）", clusterRef)
	}
	preamble, sections := ignSplitINI(tpl)
	if len(sections) == 0 {
		return "", Fail(errcode.ErrConfig, "发行包 hosts 模板不可解析（clusters/%s/hosts 形态异常，R-15）", clusterRef)
	}
	want := map[string][]ignNodeSpec{
		"etcd":        topo.Etcd,
		"kube_master": topo.Master,
		"kube_node":   topo.Node,
	}
	seen := map[string]bool{}
	for i := range sections {
		s := sections[i]
		if members, ok := want[s.name]; ok {
			s.body = ignGroupLines(s.body, members)
			sections[i] = s
			seen[s.name] = true
			continue
		}
		if s.name == "all:vars" {
			s.body = ignSetHostVars(s.body, clusterRef, p)
			sections[i] = s
		}
	}
	for _, g := range []string{"etcd", "kube_master", "kube_node"} {
		if seen[g] {
			continue
		}
		idx := -1
		for i, s := range sections {
			if s.name == "all:vars" {
				idx = i
				break
			}
		}
		sec := ignIniSection{name: g, body: ignGroupLines(nil, want[g])}
		if idx < 0 {
			sections = append(sections, sec)
			continue
		}
		sections = append(sections[:idx], append([]ignIniSection{sec}, sections[idx:]...)...)
	}
	out := ignJoinINI(preamble, sections)
	if strings.Contains(out, "_cluster_name_") {
		return "", Fail(errcode.ErrConfig, "hosts 仍含 _cluster_name_ 占位符（只有 ezctl new 会替换，接管后必须自净）")
	}
	return out, nil
}

func ignSetHostVars(body []string, clusterRef string, p ignDeployParams) []string {
	set := func(k, v string) {
		if strings.TrimSpace(v) != "" {
			body = ignSetINIValue(body, k, v, true)
		}
	}
	set("SERVICE_CIDR", p.ServiceCIDR)
	set("CLUSTER_CIDR", p.PodCIDR)
	set("CLUSTER_NETWORK", p.NetworkPlugin)
	set("CONTAINER_RUNTIME", p.Runtime)
	set("PROXY_MODE", p.ProxyMode)
	set("NODE_PORT_RANGE", p.NodePortRange)
	set("CLUSTER_DNS_DOMAIN", p.DNSDomain)
	// cluster_dir：占位符替换为登记标识（真实路径）；模板缺失该键则按发行包惯例补一条
	if idx := ignFindINIKey(body, "cluster_dir"); idx >= 0 {
		m := ignINIKeyRe.FindStringSubmatch(body[idx])
		val := strings.TrimSpace(m[3])
		val = strings.ReplaceAll(val, "_cluster_name_", clusterRef)
		if !strings.Contains(val, clusterRef) { // 非常规写法（绝对路径硬编码）⇒ 整值改写
			val = `"{{ base_dir }}/clusters/` + clusterRef + `"`
		}
		body[idx] = m[1] + "cluster_dir=" + val
	} else {
		body = ignSetINIValue(body, "cluster_dir", "{{ base_dir }}/clusters/"+clusterRef, true)
	}
	// SSH 免密：只写路径（内容零出口），未配置则沿用命令面默认的 ssh-agent/known_hosts
	if d := strings.TrimSpace(p.SSHUser); d != "" {
		body = ignSetINIValue(body, "ansible_ssh_user", d, false)
	}
	if k := strings.TrimSpace(p.SSHKey); k != "" {
		body = ignSetINIValue(body, "ansible_ssh_private_key_file", k, false)
	}
	return body
}

func ignFindINIKey(body []string, key string) int {
	for i, l := range body {
		if m := ignINIKeyRe.FindStringSubmatch(l); m != nil && m[2] == key {
			return i
		}
	}
	return -1
}

// ---------- config.yml 渲染（标量定点改写 + 列表整体替换 + 其余原样保留） ----------

var ignYAMLKeyRe = regexp.MustCompile(`^(\s*)([A-Za-z_][A-Za-z0-9_]*)\s*:\s*(.*)$`)

func ignYAMLScalar(v string) string {
	if v == "" {
		return `""`
	}
	return `"` + strings.ReplaceAll(v, `"`, `\"`) + `"`
}

// ignSetYAMLScalar 改写标量键（保留缩进/引号风格），缺失则追加到文末。
func ignSetYAMLScalar(lines []string, key, value string, appendIdx int) []string {
	for i, l := range lines {
		m := ignYAMLKeyRe.FindStringSubmatch(l)
		if m == nil || m[2] != key {
			continue
		}
		lines[i] = m[1] + key + ": " + ignYAMLScalar(value)
		return lines
	}
	ins := len(lines)
	if appendIdx >= 0 && appendIdx < len(lines) {
		ins = appendIdx
	}
	add := []string{key + ": " + ignYAMLScalar(value)}
	lines = append(lines[:ins], append(add, lines[ins:]...)...)
	return lines
}

// ignListItemEnd 返回列表键的条目末行下标（key 行之后连续的 "- xxx" 行）。
func ignListItemEnd(lines []string, i int) int {
	j := i + 1
	for j < len(lines) {
		t := strings.TrimSpace(lines[j])
		if t == "" || strings.HasPrefix(t, "#") {
			break
		}
		if !strings.HasPrefix(t, "- ") && t != "-" {
			break
		}
		j++
	}
	return j - 1
}

func ignSetYAMLList(lines []string, key string, items []string, indent string) []string {
	var rendered []string
	for _, it := range items {
		rendered = append(rendered, indent+"- "+ignYAMLScalar(it))
	}
	for i, l := range lines {
		m := ignYAMLKeyRe.FindStringSubmatch(l)
		if m == nil || m[2] != key {
			continue
		}
		end := ignListItemEnd(lines, i)
		body := append([]string{l}, rendered...)
		out := append(append([]string{}, lines[:i]...), body...)
		return append(out, lines[end+1:]...)
	}
	if len(items) == 0 {
		return lines
	}
	tail := append([]string{key + ":"}, rendered...)
	return append(lines, tail...)
}

func ignMergeConfigYAML(tpl, clusterRef string, p ignDeployParams) (string, error) {
	if !ignClusterIDRe.MatchString(clusterRef) {
		return "", Fail(errcode.ErrParam, "关联集群标识 %q 不符合格式白名单（INV-33）", clusterRef)
	}
	lines := strings.Split(tpl, "\n")
	if len(lines) == 0 {
		return "", Fail(errcode.ErrConfig, "发行包 config.yml 模板为空（R-15：交付配置不可对齐）")
	}
	set := func(key, v string) {
		if strings.TrimSpace(v) != "" {
			lines = ignSetYAMLScalar(lines, key, v, -1)
		}
	}
	set("CLUSTER_NAME", clusterRef)
	set("INSTALL_SOURCE", p.InstallSource)
	set("K8S_VER", p.K8sVersion)
	set("corednsVer", p.CoreDNSVersion)
	// CNI 版本键按插件命名（发行包各插件各自的键名，写错键等于没写）
	switch p.NetworkPlugin {
	case "flannel":
		set("flannel_ver", p.CNIVersion)
	case "calico":
		set("calico_ver", p.CNIVersion)
	case "cilium":
		set("cilium_ver", p.CNIVersion)
	case "kube-ovn":
		set("kube_ovn_ver", p.CNIVersion)
	case "kube-router":
		set("kube_router_ver", p.CNIVersion)
	}
	if hosts := p.MirrorHosts(); len(hosts) > 0 {
		lines = ignSetYAMLScalar(lines, "ENABLE_MIRROR_REGISTRY", "true", -1)
		// R-22 整体替换：INSECURE_REG 的条目集就是台账里那一行的全量值（逐条目加 http:// 前缀，
		// 与 v1.1.1 的单条目渲染逐字节相同 —— dedupStringsOrdered 保序，排序会让"整项替换"
		// 的台账行与落盘顺序失去对应关系）
		reg := make([]string, 0, len(hosts))
		for _, h := range hosts {
			reg = append(reg, "http://"+h)
		}
		lines = ignSetYAMLList(lines, "INSECURE_REG", dedupStringsOrdered(reg), "  ")
		if v := strings.TrimSpace(p.SandboxImage()); v != "" {
			lines = ignSetYAMLScalar(lines, "SANDBOX_IMAGE", v, -1)
		}
	}
	// 证书 SAN：控制面地址 + api.<DNS 域>（上游按 https://api.<域>:6443 登记访问端点，
	// 签发面不含该名 ⇒ 装好的集群访问端点必然校验失败）
	san := append([]string{}, p.MasterAddrs...)
	if d := strings.TrimSpace(p.DNSDomain); d != "" {
		san = append([]string{"api." + d}, san...)
	}
	if len(san) > 0 {
		lines = ignSetYAMLList(lines, "MASTER_CERT_HOSTS", dedupStrings(san), "  ")
	}
	return strings.Join(lines, "\n"), nil
}

func dedupStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// dedupStringsOrdered 去重保序（列表型字段的整项替换用：R-22 的对应关系是
// "台账里的一行 = 落盘后的一项取值"，重排会让 REP-17 的覆写值与 config.yml 的条目顺序对不上）。
func dedupStringsOrdered(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = strings.TrimSpace(v); v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// SandboxImage pause 镜像完整坐标（<镜像仓库地址>/<清单登记的 repo:tag>）。
// 清单未登记 pause ⇒ 不改写，沿用发行包自带值（不猜 tag，猜错就是 06/07 步的 ImagePullBackOff）。
func (p ignDeployParams) SandboxImage() string {
	m := strings.TrimSuffix(strings.TrimSpace(p.ImageMirror), "/")
	if m == "" || strings.TrimSpace(p.PauseImage) == "" {
		return ""
	}
	host := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(m, "http://"), "https://"), "//")
	return host + "/" + strings.TrimSpace(p.PauseImage)
}

// ignManifest 接管标记：登记"这份配置由本域按哪些事实派生"，让后续受理可判定归属。
type ignManifest struct {
	Managed      bool   `json:"managed"`
	HostsSHA256  string `json:"hosts_sha256"`
	ConfigSHA256 string `json:"config_sha256"`
	ClusterRef   string `json:"cluster_ref"`
	UpdatedAt    string `json:"updated_at"`
	Note         string `json:"note"`
	// 派生事实快照：执行环境（NO_PROXY 内网直连清单）从这里取，不另设第二套清单
	ServiceCIDR string   `json:"service_cidr"`
	PodCIDR     string   `json:"pod_cidr"`
	DNSDomain   string   `json:"dns_domain"`
	ImageMirror string   `json:"image_mirror"`
	Nodes       []string `json:"nodes"`
	// ImageMirrors 列表型字段的整项快照（R-22：一行台账 = 落盘后的一项取值；
	// 只存首项会让"下次将生效"与"当前落盘"在多看板间对不上，形成第二套事实）
	ImageMirrors []string `json:"image_mirrors,omitempty"`
	// 上游递交的两条登记标识（R-13 本批增补口径 + R-19）：跨域只存标识、不存内容（INV-33），
	// 却是 R-25 引用来源 ①、REP-15 c7 被引用集群数与 FUNC-46 默认取值上下文的唯一本域事实源。
	ParamTemplateRef string `json:"param_template_ref,omitempty"`
	VersionRef       string `json:"version_ref,omitempty"`
}

func ignSHA256(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func ignReadClusterFile(dir, name string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func ignWriteAtomic(path, text string, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(text), mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func ignLoadManifest(dir string) (*ignManifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, ignInventoryManifest))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var m ignManifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, Fail(errcode.ErrConfig, "接管标记不可解析（%s）：交付配置归属不可判定，须清理集群目录后重新受理", err)
	}
	return &m, nil
}

// ignAlignReq 交付配置对齐请求（安装族动作在受理事务提交前落定）。
type ignAlignReq struct {
	ClusterRef string
	Action     string
	StepCode   string
	Topo       ignTopology
	Params     ignDeployParams
	// 本次生效取值所依据的两条登记标识（接管标记快照的组成部分）：下次受理不带标识时
	// 由 ignClusterFaceContext 回落读它，R-25 ① 的引用来源也只在它被快照后才可反查。
	ParamTemplateRef string
	VersionRef       string
}

// ignFaceApplied 一次对齐对覆写层的消费事实（INV-42 / R-21 回填的判据）。
// Wrote 只在 hosts/config 真被产出或刷新的那条路径为真 —— 沿用已对齐配置、节点增删只核对
// 在册事实都不算消费，回填它等于把"没动过文件的那一次"记成生效。
type ignFaceApplied struct {
	ClusterRef string
	Wrote      bool
	Face       *paramFaceView
}

// ignNewClusterSkeleton 以本域私有手段执行发行包的建目录动作（internal 动作层、不登记任务、
// 不出门面结论；7.3 第 ② 层）。返回是否成功建出 hosts/config 两份产物。
func ignNewClusterSkeleton(clusterRef string) bool {
	bin := strings.TrimSpace(config.Config.Delivery.ToolBin)
	cmd := exec.Command(bin, "new", clusterRef) // 独立 argv 直调，不经 shell（INV-28/R-12）
	cmd.Dir = ignWorkRoot()
	cmd.Env = ignToolEnv(clusterRef, ignDeployParams{})
	_ = cmd.Run()
	dir := ignClusterDir(clusterRef)
	h, errH := ignReadClusterFile(dir, "hosts")
	c, errC := ignReadClusterFile(dir, "config.yml")
	return errH == nil && h != "" && errC == nil && c != ""
}

// ignAlignClusterInventory 安装族动作的交付配置前置（B-AUTO-15 的落点，对齐三态见 R-18）。
// 只关心对齐结论说明的调用方用它；受理侧还需要"本次是否真产出/刷新了交付配置"以便按
// INV-42 回填覆写追溯对，走 ignAlignClusterInventoryOutcome。
func ignAlignClusterInventory(req ignAlignReq) (string, error) {
	_, note, err := ignAlignClusterInventoryOutcome(req)
	return note, err
}

// ignAlignClusterInventoryOutcome 对齐主体（B-AUTO-15 的落点，对齐三态见 R-18）：
// ① 接管 —— 目录缺失 ⇒ 先用发行包建目录动作产出模板形态（版本键由发行包自身决定，本域不猜版本号），再按登记事实渲染 hosts/config.yml 并写接管标记；
// ② 幂等 —— 已有本域标记 ⇒ 带清单即逐字段跟随台账与参数改写（同事实重入结论不变），不带清单即沿用已对齐配置并核对指纹（重试 / 续派场景，指纹不符即配置事实错误）；
// ③ 冲突 —— 有清单（或已对齐文件）而无本域标记 ⇒ 拒绝改写，人工或其他工具的既有事实不可被静默覆盖。
// 返回覆写消费事实（INV-42 回填判据）+ 可用于任务注记行的对齐说明。
func ignAlignClusterInventoryOutcome(req ignAlignReq) (ignFaceApplied, string, error) {
	applied := ignFaceApplied{ClusterRef: req.ClusterRef}
	if !ignClusterIDRe.MatchString(req.ClusterRef) {
		return applied, "", Fail(errcode.ErrParam, "关联集群标识 %q 不符合格式白名单（INV-33）", req.ClusterRef)
	}
	dir := ignClusterDir(req.ClusterRef)
	exists := ignDirExists(dir)
	seeded := false

	hostsText, hostsErr := ignReadClusterFile(dir, "hosts")
	confText, confErr := ignReadClusterFile(dir, "config.yml")
	if req.Topo.IsEmpty() {
		if hostsErr != nil || confErr != nil {
			return applied, "", Fail(errcode.ErrState,
				"集群交付配置尚未生成（clusters/%s/hosts|config.yml 缺失）且本次受理未带节点清单：安装族首步必须携带节点清单（FUNC-34/Q-72）",
				req.ClusterRef)
		}
		m, err := ignLoadManifest(dir)
		if err != nil {
			return applied, "", err
		}
		if m == nil {
			return applied, "", ignInventoryConflict(req.ClusterRef, "无接管标记")
		}
		if m.HostsSHA256 != ignSHA256(hostsText) || m.ConfigSHA256 != ignSHA256(confText) {
			return applied, "", Fail(errcode.ErrState,
				"集群交付配置与本域派生事实不符（clusters/%s 下的文件在接管后被改动）：R-15 受理即刻拒绝，须清理集群目录后带节点清单重新受理",
				req.ClusterRef)
		}
		return applied, "交付配置沿用（clusters/" + req.ClusterRef + " 已由本域按登记事实对齐，本次未带清单）", nil
	}

	if !exists || hostsErr != nil || confErr != nil {
		if ok, why := ignLocalExecAvailable(); !ok {
			return applied, "", Fail(errcode.ErrState, "%s（R-15）", why)
		}
		if !ignNewClusterSkeleton(req.ClusterRef) {
			return applied, "", Fail(errcode.ErrState,
				"发行包未产出 clusters/%s 的 hosts/config.yml（建目录动作不可用或模板缺失）：R-15 受理即刻拒绝",
				req.ClusterRef)
		}
		seeded = true
		hostsText, _ = ignReadClusterFile(dir, "hosts")
		confText, _ = ignReadClusterFile(dir, "config.yml")
	}
	if !seeded {
		if m, err := ignLoadManifest(dir); err != nil {
			return applied, "", err
		} else if m == nil {
			return applied, "", ignInventoryConflict(req.ClusterRef, "无接管标记")
		}
	}

	p := req.Params
	p.MasterAddrs = dedupStrings(ignAddrList(req.Topo.Master))
	p.NodeAddrs = dedupStrings(append(append([]string{}, ignAddrList(req.Topo.Node)...), p.MasterAddrs...))
	newHosts, err := ignRenderHosts(hostsText, req.ClusterRef, req.Topo, p)
	if err != nil {
		return applied, "", err
	}
	newConf, err := ignMergeConfigYAML(confText, req.ClusterRef, p)
	if err != nil {
		return applied, "", err
	}
	if err := ignWriteAtomic(filepath.Join(dir, "hosts"), newHosts, 0o644); err != nil {
		return applied, "", Fail(errcode.ErrState, "hosts 写入失败：%v（R-15）", err)
	}
	if err := ignWriteAtomic(filepath.Join(dir, "config.yml"), newConf, 0o600); err != nil {
		return applied, "", Fail(errcode.ErrState, "config.yml 写入失败：%v（R-15）", err)
	}
	m := ignManifest{
		Managed:      true,
		HostsSHA256:  ignSHA256(newHosts),
		ConfigSHA256: ignSHA256(newConf),
		ClusterRef:   req.ClusterRef,
		UpdatedAt:    ignNow(),
		Note: fmt.Sprintf("按门面登记事实派生（第 %s 步；etcd %d/控制面 %d/工作 %d）", req.StepCode,
			len(req.Topo.Etcd), len(req.Topo.Master), len(req.Topo.Node)),
		ServiceCIDR: p.ServiceCIDR,
		PodCIDR:     p.PodCIDR,
		DNSDomain:   p.DNSDomain,
		ImageMirror: p.ImageMirror,
		// 列表型字段的整项快照（R-22）：只存首项会让"下次将生效"与"当前落盘"在多看板间对不上
		ImageMirrors: p.MirrorHostList(),
		Nodes:        p.NodeAddrs,
		// 当次递交的两条登记标识：下次受理不带标识时的默认取值上下文（R-20）与 R-25 ① 的反查依据
		ParamTemplateRef: strings.TrimSpace(req.ParamTemplateRef),
		VersionRef:       strings.TrimSpace(req.VersionRef),
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := ignWriteAtomic(filepath.Join(dir, ignInventoryManifest), string(b)+"\n", 0o644); err != nil {
		return applied, "", Fail(errcode.ErrState, "接管标记写入失败：%v（R-15）", err)
	}
	verb := "按登记事实生成"
	if seeded {
		verb = "以发行包模板打底后" + verb
	}
	note := fmt.Sprintf("交付配置%s：hosts（etcd %d / 控制面 %d / 工作 %d 台）、config.yml（集群名 %s，网络 %s，Service %s，Pod %s）",
		verb, len(req.Topo.Etcd), len(req.Topo.Master), len(req.Topo.Node),
		req.ClusterRef, p.NetworkPlugin, p.ServiceCIDR, p.PodCIDR)
	if len(m.ParamTemplateRef) > 0 {
		note += "（参数面模板 " + m.ParamTemplateRef + "）"
	}
	// 文件已按四层取值落盘 ⇒ 命中覆写层的坐标即被本次消费（INV-42 追溯对的成对回填判据）；
	// 生效取值轨迹由调用方（受理侧 ignAlignPrecondition）挂到 applied.Face。
	applied.Wrote = true
	return applied, note, nil
}

func ignInventoryConflict(clusterRef, why string) error {
	return Fail(errcode.ErrState,
		"clusters/%s 下的交付配置非本域派生（%s）：本域不改写人工或其他工具产出的配置，须清理该集群目录后带节点清单重新受理（R-15 受理即刻拒绝）",
		clusterRef, why)
}

// ignHostsGroups 读取既有 inventory 的组成员（节点增删的受理侧前置核对）。
func ignHostsGroups(clusterRef string) (map[string][]string, error) {
	text, err := ignReadClusterFile(ignClusterDir(clusterRef), "hosts")
	if err != nil {
		return nil, Fail(errcode.ErrState,
			"集群交付配置不可读（clusters/%s/hosts 缺失）：节点增删必须以已对齐的 inventory 为前提（R-15）",
			clusterRef)
	}
	_, sections := ignSplitINI(text)
	out := map[string][]string{}
	for _, s := range sections {
		if s.name == "all:vars" {
			continue // [all:vars] 的键行不是组成员（形如 "10.0.0.7 k8s_nodename=xx" 才是成员行）
		}
		var addrs []string
		for _, l := range s.body {
			t := strings.TrimSpace(l)
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			addrs = append(addrs, strings.Fields(t)[0])
		}
		out[s.name] = addrs
	}
	return out, nil
}

// ignAlignNodeOps 节点增删的受理侧前置（不写 hosts：新地址由命令面自己 sed 进工作组）。
func ignAlignNodeOps(clusterRef, action string, addrs []string) (string, error) {
	groups, err := ignHostsGroups(clusterRef)
	if err != nil {
		return "", err
	}
	inNodes := groups["kube_node"]
	inAll := append(append([]string{}, inNodes...), groups["kube_master"]...)
	inAll = append(inAll, groups["etcd"]...)
	switch action {
	case "ezctl.add-node":
		for _, a := range addrs {
			if ignHasAddr(inNodes, a) {
				return "", Fail(errcode.ErrForbidden,
					"节点 %q 已在 clusters/%s 的 [kube_node] 组内（命令面会直接以 already existed 失败退出）：R-13 受理侧拒绝",
					a, clusterRef)
			}
		}
	case "ezctl.del-node":
		for _, a := range addrs {
			if !ignHasAddr(inAll, a) {
				return "", Fail(errcode.ErrForbidden,
					"节点 %q 不在 clusters/%s 的 inventory 内（命令面会以 not existed 失败退出）：R-13 受理侧拒绝",
					a, clusterRef)
			}
		}
	}
	note := fmt.Sprintf("节点增删前置已核对：inventory 工作组 %d 台在册", len(inNodes))
	if m, err := ignLoadManifest(ignClusterDir(clusterRef)); err == nil && m == nil {
		note += "（inventory 非本域派生，本次按发行包既有配置执行，不改写）"
	} else if err != nil {
		return "", err
	}
	return note, nil
}

// ---------- 部署参数缺省面（REF-17~25）+ 交付版本派生 + 形态校验 ----------

func ignConfigStr(db *gorm.DB, key, def string) string {
	if db == nil {
		return def
	}
	v, err := ignConfigGet(db, key)
	if err != nil || strings.TrimSpace(v) == "" {
		return def
	}
	return strings.TrimSpace(v)
}

// ignDeployProfileOf 读取本域交付参数缺省面（平台配置项；部署配置只补 SSH 两项）。
func ignDeployProfileOf(db *gorm.DB) ignDeployParams {
	d := config.Config.Delivery
	p := ignDeployParams{
		ServiceCIDR:   ignConfigStr(db, ignCfgDeployServiceCIDR, "10.68.0.0/16"),
		PodCIDR:       ignConfigStr(db, ignCfgDeployPodCIDR, "10.200.0.0/16"),
		DNSDomain:     ignConfigStr(db, ignCfgDeployDNSDomain, "cluster.local"),
		NetworkPlugin: ignConfigStr(db, ignCfgDeployNetworkPlugin, "flannel"),
		Runtime:       ignConfigStr(db, ignCfgDeployRuntime, "containerd"),
		ProxyMode:     ignConfigStr(db, ignCfgDeployProxyMode, "iptables"),
		NodePortRange: ignConfigStr(db, ignCfgDeployNodePortRange, "30000-32767"),
		InstallSource: ignConfigStr(db, ignCfgDeployInstallSource, "online"),
		ImageMirror:   ignConfigStr(db, ignCfgDeployImageMirror, ""),
		SSHUser:       strings.TrimSpace(d.SSHUser),
	}
	p.SSHKey = strings.TrimSpace(d.SSHKey)
	if p.SSHKey == "" {
		if dir := strings.TrimSpace(d.SSHDir); dir != "" {
			p.SSHKey = filepath.Join(dir, "id_rsa")
		}
	}
	return p
}

// ignRunEnvProfile 执行位的环境参数面：网络缺省取平台配置（REF-17~25），
// 网段与节点地址取接管标记里登记的派生事实（单一事实源，不另设第二套清单）。
// 交付配置尚未生成时只按缺省面（RFC1918 + 本地仓库域名）直连，不影响正确性。
func ignRunEnvProfile(clusterRef string) ignDeployParams {
	p := ignDeployProfileOf(store.DB)
	m, err := ignLoadManifest(ignClusterDir(clusterRef))
	if err != nil || m == nil {
		return p
	}
	if v := strings.TrimSpace(m.ServiceCIDR); v != "" {
		p.ServiceCIDR = v
	}
	if v := strings.TrimSpace(m.PodCIDR); v != "" {
		p.PodCIDR = v
	}
	if v := strings.TrimSpace(m.DNSDomain); v != "" {
		p.DNSDomain = v
	}
	if v := strings.TrimSpace(m.ImageMirror); v != "" {
		p.ImageMirror = v
	}
	if len(m.Nodes) > 0 {
		p.NodeAddrs = m.Nodes
	}
	return p
}

var ignCIDRRe = regexp.MustCompile(`^\d{1,3}(\.\d{1,3}){3}/\d{1,2}$`)
var ignPortRangeRe = regexp.MustCompile(`^\d{4,5}-\d{4,5}$`)

// ignValidateDeployParams 缺省面形态校验（配置写错 ⇒ 明确报错，不静默回落缺省值：
// "参数不生效"与"参数生效但值不对"在交付链路上的后果完全不同）。
func ignValidateDeployParams(p ignDeployParams) error {
	checks := []struct{ key, val string }{
		{ignCfgDeployServiceCIDR, p.ServiceCIDR}, {ignCfgDeployPodCIDR, p.PodCIDR},
	}
	for _, c := range checks {
		if !ignCIDRRe.MatchString(c.val) {
			return Fail(errcode.ErrConfig, "交付参数 %s=%q 不是 CIDR 形态（REF-17/18）", c.key, c.val)
		}
	}
	if !ignPortRangeRe.MatchString(p.NodePortRange) {
		return Fail(errcode.ErrConfig, "交付参数 %s=%q 不是 起始-结束 端口区间形态（REF-23）", ignCfgDeployNodePortRange, p.NodePortRange)
	}
	switch p.Runtime {
	case "containerd", "docker":
	default:
		return Fail(errcode.ErrConfig, "交付参数 %s=%q 不在命令面支持的运行时枚举内（REF-21）", ignCfgDeployRuntime, p.Runtime)
	}
	switch p.ProxyMode {
	case "iptables", "ipvs", "nftables":
	default:
		return Fail(errcode.ErrConfig, "交付参数 %s=%q 不在 kube-proxy 支持的模式枚举内（REF-22）", ignCfgDeployProxyMode, p.ProxyMode)
	}
	switch p.InstallSource {
	case "online", "offline":
	default:
		return Fail(errcode.ErrConfig, "交付参数 %s=%q 只能是 online/offline（REF-24）", ignCfgDeployInstallSource, p.InstallSource)
	}
	return nil
}

// ignVersionStatusLabel 版本状态的中文名列（字典 IGN_VERSION_STATUS 的取值，拒绝理由要能读懂）。
func ignVersionStatusLabel(code string) string {
	if l, ok := ignVersionLabels[code]; ok {
		return l
	}
	return code
}

var ignVersionLabels = map[string]string{
	"DRAFT":               "草稿",
	"DOWNLOADING":         "下载中",
	"READY":               "就绪",
	"PREPUSHING":          "预推中",
	"PREPUSHED":           "已预推",
	"PARTIALLY_PREPUSHED": "部分预推",
	"ARCHIVED":            "已归档",
	"FAILED":              "失败",
}

// ignVersionDeliverable 可被交付引用的版本状态（R-19：未归档 + 就绪及之后，与上游 O-CLC 的
// REF-05 同口径）。就绪由发行包下载执行（B-AUTO-01）达成；草稿 / 下载中 / 失败的版本其组件
// 版本面与镜像清单尚未登记完成，据此派生参数等于把空版本号带进装机现场。
var ignVersionDeliverable = map[string]bool{
	"READY":               true,
	"PREPUSHING":          true,
	"PREPUSHED":           true,
	"PARTIALLY_PREPUSHED": true,
}

// ignApplyVersionRow 把已解析、已判态的交付版本行灌进渲染面（resolveParamFace 第 2 层字段映射的
// 唯一实现；版本引用能否被交付引用的判定在 ignResolveVersionRow，两处读同一份事实）。
// 版本行里没有的登记事实不覆盖下层已取到的值（"命中不到就跳过"，与 ignVersionFaceHits 的键域口径一致）。
func ignApplyVersionRow(row map[string]any, p *ignDeployParams) error {
	if row == nil {
		return nil
	}
	p.K8sVersion = strings.TrimPrefix(Str(row["orchestrator_version"]), "v")
	if cni := strings.ToLower(Str(row["cni_plugin"])); cni != "" {
		p.NetworkPlugin = cni
	}
	p.CNIVersion = Str(row["cni_version"])
	p.CoreDNSVersion = strings.TrimPrefix(Str(row["coredns_version"]), "v")
	// pause 镜像坐标只从版本镜像清单取（清单是 FUNC-10 的登记事实）：
	// 猜 tag 等于把 06/07 步的 ImagePullBackOff 留到现场才发现
	for _, it := range parseManifestItems(Str(row["image_manifest"])) {
		repo := strings.ToLower(Str(it["repo"]))
		if !strings.Contains(repo, "pause") {
			continue
		}
		tag := Str(it["tag"])
		if tag == "" {
			continue
		}
		p.PauseImage = repo + ":" + tag
		break
	}
	return nil
}

// ignProfileForAction 受理侧的交付参数面：四层取值（R-20 唯一实现 resolveParamFace）+ 一次形态收口。
//
// 取值上下文 = clusterRef（覆写层的天然坐标）+ 本次请求携带的面标识（缺省回落到接管标记里
// 快照的那张牌）+ 交付版本引用。拓扑面（节点成员与角色）仍只来自请求体节点清单（Q-72），
// 两条轴不同轴：这里产出的只是 hosts/[all:vars] 与 config.yml 的受管键取值。
// 治理拒绝口径由求值器与 ignFaceGateUsable 一处实现（模板非启用态 / 版本不可解析 INV-33 /
// 不可交付 R-19），形态校验由 ignFaceEffectiveUsable 一处跑一次（R-23）—— 受理侧不再另写一套。
func ignProfileForAction(clusterRef, paramTemplateRef, versionRef string) (*paramFaceView, error) {
	tplRef, verRef := ignFaceRequestContext(clusterRef, paramTemplateRef, versionRef)
	view, err := resolveParamFace(store.DB, clusterRef, "", tplRef, verRef)
	if err != nil {
		return nil, err
	}
	if err := ignFaceEffectiveUsable(view, view.Params); err != nil {
		return view, err
	}
	return view, nil
}
