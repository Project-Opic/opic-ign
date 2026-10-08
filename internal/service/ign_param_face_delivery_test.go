// ign_param_face_delivery_test.go —— 受理链路（FUNC-34 / B-AUTO-15）真消费四层参数面的 DB 用例。
//
// 这一组用例盯的是"台账里显示的取值"与"装机现场用的取值"必须是同一份事实：
//  1. 覆写层 / 模板层的值要落进 clusters/<ref>/hosts|config.yml，且被覆盖的下层值不残留
//     （R-20 逐字段取最高优先命中、不做层间合并；R-22 列表整项替换）；
//  2. 拓扑面与参数面不同轴（Q-72）：节点成员只由请求体清单决定，参数面取值改不了成员；
//  3. 命中生效的覆写行按当次任务号成对回填追溯对，回落即成对清空（INV-42 / R-21）；
//  4. 同一输入下 dry-run 与受理结论一致（R-23）—— 预检绿而受理走旧路径就是误导源。
package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitcode.com/opic-ontology/opic-techbase/pkg/errcode"
)

// faceHostsINIKey HOSTS 面键在 hosts 里的实名（参数面键名与发行包 inventory 键名对照）。
var faceHostsINIKey = map[string]string{
	ignCfgDeployServiceCIDR:   "SERVICE_CIDR",
	ignCfgDeployPodCIDR:       "CLUSTER_CIDR",
	ignCfgDeployDNSDomain:     "CLUSTER_DNS_DOMAIN",
	ignCfgDeployNetworkPlugin: "CLUSTER_NETWORK",
	ignCfgDeployRuntime:       "CONTAINER_RUNTIME",
	ignCfgDeployProxyMode:     "PROXY_MODE",
	ignCfgDeployNodePortRange: "NODE_PORT_RANGE",
}

// faceDeliveryFixture 命令面夹具 + 干净的覆写/模板表（用例现场互不残留）。
func faceDeliveryFixture(t *testing.T, cases ...string) string {
	t.Helper()
	root := useDeliveryEnv(t, facadeToolBody(cases...))
	mustSQL(t, `DELETE FROM ign_cluster_config_override`)
	mustSQL(t, `DELETE FROM ign_param_template_item`)
	mustSQL(t, `DELETE FROM ign_param_template`)
	return root
}

// faceOverride 集群覆写（事由必填的治理动作）。
func faceOverride(t *testing.T, clusterRef, fileFace, fieldKey, value, reason string) {
	t.Helper()
	if _, err := SaveIgnClusterConfigOverride(map[string]any{
		"cluster_ref": clusterRef, "file_face": fileFace, "field_key": fieldKey,
		"value": value, "reason": reason,
	}, faceTestUser); err != nil {
		t.Fatalf("覆写 %s/%s=%s 失败: %v", fileFace, fieldKey, value, err)
	}
}

// faceAccept 安装族受理（默认第 01 步、单控制面清单）并等任务落终态，返回任务号。
func faceAccept(t *testing.T, root, clusterRef string, data map[string]any) string {
	t.Helper()
	src := fmt.Sprintf("CLC-FACE-%d", nextSeq())
	trackAudit(t, src)
	body := map[string]any{
		"source_task_no": src, "source_step_index": int64(1),
		"cluster_ref": clusterRef, "step_code": "01", "nodes": facadeInstallNodes(),
	}
	for k, v := range data {
		body[k] = v
	}
	got, err := DeliverySubmit(body, facadeUser())
	if err != nil {
		t.Fatalf("安装族受理失败: %v", err)
	}
	taskNo := Str(got["task_no"])
	waitTaskStatus(t, taskNo, "COMPLETED")
	return taskNo
}

// faceOverrideRow 覆写行的追溯对观测（applied_task_no / applied_at 必须成对出现或成对为空）。
func faceOverrideRow(t *testing.T, clusterRef, fileFace, fieldKey string) map[string]any {
	t.Helper()
	row := mustOne(t, `SELECT * FROM ign_cluster_config_override
		WHERE flag = 1 AND cluster_ref = ? AND file_face = ? AND field_key = ?`,
		clusterRef, fileFace, fieldKey)
	if row == nil {
		t.Fatalf("覆写行不存在: %s/%s/%s", clusterRef, fileFace, fieldKey)
	}
	return row
}

func faceManifestSnapshot(t *testing.T, clusterRef string) ignManifest {
	t.Helper()
	m, err := ignLoadManifest(ignClusterDir(clusterRef))
	if err != nil {
		t.Fatalf("接管标记不可读: %v", err)
	}
	if m == nil {
		t.Fatalf("接管标记未落（B-AUTO-15 未产出派生事实快照）")
	}
	return *m
}

// yamlListItems 取 config.yml 里某个列表键的条目（整项替换的断言要看条目数，不能只看子串）。
func yamlListItems(text, key string) []string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		m := ignYAMLKeyRe.FindStringSubmatch(l)
		if m == nil || m[2] != key {
			continue
		}
		out := []string{}
		for _, it := range lines[i+1:] {
			t2 := strings.TrimSpace(it)
			if !strings.HasPrefix(t2, "- ") {
				break
			}
			out = append(out, strings.Trim(t2[2:], `"`))
		}
		return out
	}
	return nil
}

// ---------- ① 覆写层 / 模板层的值必须真落到交付配置 ----------

func TestAcceptanceLandsOverrideLayerOnDisk(t *testing.T) {
	requireDB(t)
	root := faceDeliveryFixture(t, `  setup) echo "setup ok"; exit 0 ;;`)
	cluster := newCluster(t)

	faceOverride(t, cluster, "HOSTS", ignCfgDeployServiceCIDR, "10.111.0.0/16", "Service 网段避让")
	faceOverride(t, cluster, "HOSTS", ignCfgDeployPodCIDR, "10.222.0.0/16", "Pod 网段避让")
	faceOverride(t, cluster, "HOSTS", ignCfgDeployDNSDomain, "svc.internal", "后缀避让")
	faceAccept(t, root, cluster, nil)

	hosts := readClusterFile(t, root, cluster, "hosts")
	for _, want := range []string{
		`SERVICE_CIDR="10.111.0.0/16"`, `CLUSTER_CIDR="10.222.0.0/16"`, `CLUSTER_DNS_DOMAIN="svc.internal"`,
	} {
		if !strings.Contains(hosts, want) {
			t.Fatalf("覆写层的值没落到 hosts（受理仍在走旧的两层路径？）: 缺 %q\n%s", want, hosts)
		}
	}
	// 逐字段取最高优先命中 ⇒ 被覆盖的下层值不得残留（R-20 不做层间合并）
	for _, gone := range []string{
		`SERVICE_CIDR="10.68.0.0/16"`, `CLUSTER_CIDR="10.200.0.0/16"`, `CLUSTER_DNS_DOMAIN="cluster.local"`,
	} {
		if strings.Contains(hosts, gone) {
			t.Fatalf("hosts 仍含平台缺省面旧值 %q（层间残留）", gone)
		}
	}
	// 覆写后的 DNS 后缀要跟着进证书 SAN（否则装好的集群访问端点必然校验失败）
	conf := readClusterFile(t, root, cluster, "config.yml")
	if !strings.Contains(conf, `- "api.svc.internal"`) {
		t.Fatalf("config.yml 证书 SAN 未跟随覆写后的 DNS 后缀: %s", conf)
	}
	if strings.Contains(conf, `- "10.1.1.1"`) {
		t.Fatalf("MASTER_CERT_HOSTS 的打底条目未被整项替换: %s", conf)
	}
	snap := faceManifestSnapshot(t, cluster)
	if snap.ServiceCIDR != "10.111.0.0/16" || snap.PodCIDR != "10.222.0.0/16" || snap.DNSDomain != "svc.internal" {
		t.Fatalf("接管标记快照未记下覆写后的派生事实: %+v", snap)
	}
}

func TestAcceptanceLandsTemplateLayer(t *testing.T) {
	requireDB(t)
	root := faceDeliveryFixture(t, `  setup) echo "setup ok"; exit 0 ;;`)
	cluster := newCluster(t)

	tpl := faceTestTemplate(t, "dl-tpl", "HOSTS", map[string]string{
		ignCfgDeployRuntime:       "docker",
		ignCfgDeployNodePortRange: "20000-22000",
	})
	taskNo := faceAccept(t, root, cluster, map[string]any{"param_template_ref": Str(tpl["template_no"])})

	hosts := readClusterFile(t, root, cluster, "hosts")
	if !strings.Contains(hosts, `CONTAINER_RUNTIME="docker"`) ||
		!strings.Contains(hosts, `NODE_PORT_RANGE="20000-22000"`) {
		t.Fatalf("模板层的值没落到 hosts: \n%s", hosts)
	}
	if strings.Contains(hosts, `CONTAINER_RUNTIME="containerd"`) {
		t.Fatalf("命中模板层后仍留平台缺省面旧值")
	}
	// 一张模板只属一轴：HOSTS 面模板改不动 CONFIG 面键（取源方式仍是发行包打底的 online）
	conf := readClusterFile(t, root, cluster, "config.yml")
	if !strings.Contains(conf, `INSTALL_SOURCE: "online"`) {
		t.Fatalf("CONFIG 面键被 HOSTS 面模板改写（轴间越界）: %s", conf)
	}
	// 当次递交的登记标识要进快照：下次不带标识时回落读的是这张牌，不是默认模板
	snap := faceManifestSnapshot(t, cluster)
	if snap.ParamTemplateRef != Str(tpl["template_no"]) {
		t.Fatalf("接管标记未快照面标识（R-20 快照回落、R-25 ① 反查都无据可依）: %q", snap.ParamTemplateRef)
	}
	lines := mustList(t, `SELECT log_line FROM ign_task_log WHERE task_id =
		(SELECT id FROM exec_task WHERE task_no = ?) AND flag = 1 ORDER BY seq`, taskNo)
	var noted bool
	for _, l := range lines {
		if strings.Contains(Str(l["log_line"]), "参数面模板") {
			noted = true
		}
	}
	if !noted {
		t.Fatalf("任务台账须说明本次命中了哪张参数面模板（界面可回读，R-21）")
	}
}

// ---------- ② 列表整项替换（R-22）：不是元素级增删 ----------

func TestAcceptanceListOverrideReplacesWholeItems(t *testing.T) {
	requireDB(t)
	root := faceDeliveryFixture(t, `  setup) echo "setup ok"; exit 0 ;;`)
	cluster := newCluster(t)

	tpl := faceTestTemplate(t, "dl-mirror", "CONFIG", map[string]string{
		ignCfgDeployImageMirror: "harbor.tpl-a:80,harbor.tpl-b:80",
	})
	faceOverride(t, cluster, "CONFIG", ignCfgDeployImageMirror, "only.harbor:80", "仓库收敛为一个")
	faceAccept(t, root, cluster, map[string]any{"param_template_ref": Str(tpl["template_no"])})

	conf := readClusterFile(t, root, cluster, "config.yml")
	items := yamlListItems(conf, "INSECURE_REG")
	if len(items) != 1 || items[0] != "http://only.harbor:80" {
		t.Fatalf("列表覆写必须是整项替换后的全量条目，实际 %v\n%s", items, conf)
	}
	for _, gone := range []string{"harbor.tpl-a", "harbor.tpl-b", "reg.yourcompany.com", `"http://easzlab.io.local:5000"`} {
		if strings.Contains(conf, gone) {
			t.Fatalf("被整项替换掉的仓库条目仍残留在 config.yml 里: %s", gone)
		}
	}
	// SANDBOX_IMAGE 不在参数面键域内（REF-17~25 无此键）⇒ 仍是发行包打底值，本用例不动它
	if !strings.Contains(conf, `SANDBOX_IMAGE: "easzlab.io.local:5000/easzlab/pause:3.10.2"`) {
		t.Fatalf("打底的非参数面键被改写（面外键不进本域的改写集）: %s", conf)
	}
	if !strings.Contains(conf, `ENABLE_MIRROR_REGISTRY: "true"`) {
		t.Fatalf("有仓库条目时镜像开关必须随之为真: %s", conf)
	}
	snap := faceManifestSnapshot(t, cluster)
	if len(snap.ImageMirrors) != 1 || snap.ImageMirrors[0] != "only.harbor:80" {
		t.Fatalf("接管标记须按整项列表快照仓库面（只存首项会让两列对不上）: %+v", snap.ImageMirrors)
	}
}

// ---------- ③ 面标识为空：第 3 层整体跳过，默认模板不参与渲染兜底（R-20 分支 H） ----------

func TestAcceptanceSkipsTemplateLayerWithoutRef(t *testing.T) {
	requireDB(t)
	root := faceDeliveryFixture(t, `  setup) echo "setup ok"; exit 0 ;;`)
	cluster := newCluster(t)

	// 同面存在启用态默认模板：它只服务"新集群选牌"，不参与未指定牌集群的渲染兜底
	tpl := faceTestTemplate(t, "dl-default", "HOSTS", map[string]string{ignCfgDeployRuntime: "docker"})
	if _, err := SetIgnParamTemplateDefault(tplIntID(tpl), "设为默认", faceTestUser); err != nil {
		t.Fatalf("设默认失败: %v", err)
	}
	faceAccept(t, root, cluster, nil)

	hosts := readClusterFile(t, root, cluster, "hosts")
	if strings.Contains(hosts, `CONTAINER_RUNTIME="docker"`) {
		t.Fatalf("未携带面标识却静默用了默认模板（存量集群的装机参数会因此漂移）")
	}
	// 磁盘上的就是平台缺省面值
	for _, want := range []string{`CONTAINER_RUNTIME="containerd"`, `SERVICE_CIDR="10.68.0.0/16"`,
		`NODE_PORT_RANGE="30000-32767"`} {
		if !strings.Contains(hosts, want) {
			t.Fatalf("第 3 层整体跳过时应落平台缺省面，缺 %q\n%s", want, hosts)
		}
	}
	if ref, _ := ignClusterFaceContext(cluster); ref != "" {
		t.Fatalf("本次未指定模板，快照里不该有面标识: %q", ref)
	}
}

// tplIntID 模板主键（faceTestTemplate 回的是 map）。
func tplIntID(tpl map[string]any) int64 { return toInt(tpl["id"]) }

// ---------- ④ INV-42 追溯对：命中生效成对回填，回落成对清空，下一次受理落三层值 ----------

func TestAcceptanceBackfillsAppliedTracePair(t *testing.T) {
	requireDB(t)
	root := faceDeliveryFixture(t, `  setup) echo "setup ok"; exit 0 ;;`)
	cluster := newCluster(t)

	tpl := faceTestTemplate(t, "dl-rollback", "HOSTS", map[string]string{ignCfgDeployServiceCIDR: "10.70.0.0/16"})
	faceOverride(t, cluster, "HOSTS", ignCfgDeployServiceCIDR, "10.111.0.0/16", "覆写命中生效")
	faceOverride(t, cluster, "HOSTS", ignCfgDeployPodCIDR, "10.222.0.0/16", "先覆写")
	if _, err := RollbackIgnClusterConfigOverride(map[string]any{
		"cluster_ref": cluster, "file_face": "HOSTS", "field_key": ignCfgDeployPodCIDR,
	}, faceTestUser); err != nil {
		t.Fatalf("回落失败: %v", err)
	}

	taskNo := faceAccept(t, root, cluster, map[string]any{"param_template_ref": Str(tpl["template_no"])})

	hit := faceOverrideRow(t, cluster, "HOSTS", ignCfgDeployServiceCIDR)
	if Str(hit["applied_task_no"]) != taskNo {
		t.Fatalf("命中生效的覆写行应回填当次任务号 %s，实际 %q", taskNo, Str(hit["applied_task_no"]))
	}
	if strings.TrimSpace(Str(hit["applied_at"])) == "" {
		t.Fatalf("追溯对必须成对回填（INV-42）")
	}
	// 已回落的行不参与取值 ⇒ 不得回填（留着旧追溯对就分不清"曾经生效"与"现在正生效"）
	missed := faceOverrideRow(t, cluster, "HOSTS", ignCfgDeployPodCIDR)
	if strings.TrimSpace(Str(missed["applied_task_no"])) != "" ||
		strings.TrimSpace(Str(missed["applied_at"])) != "" {
		t.Fatalf("已回落的覆写行被回填了追溯对: %v / %v", missed["applied_task_no"], missed["applied_at"])
	}

	if _, err := RollbackIgnClusterConfigOverride(map[string]any{
		"cluster_ref": cluster, "file_face": "HOSTS", "field_key": ignCfgDeployServiceCIDR,
	}, faceTestUser); err != nil {
		t.Fatalf("FUNC-49 回落失败: %v", err)
	}
	cleared := faceOverrideRow(t, cluster, "HOSTS", ignCfgDeployServiceCIDR)
	if strings.TrimSpace(Str(cleared["applied_task_no"])) != "" ||
		strings.TrimSpace(Str(cleared["applied_at"])) != "" {
		t.Fatalf("回落须成对清空追溯对（INV-42）")
	}

	// 下一次受理不带面标识 ⇒ 读快照里的那张牌，取值回落到模板层（三层值）
	faceAccept(t, root, cluster, nil)
	hosts := readClusterFile(t, root, cluster, "hosts")
	if !strings.Contains(hosts, `SERVICE_CIDR="10.70.0.0/16"`) {
		t.Fatalf("回落后的下一次受理应落模板层的值，实得\n%s", hosts)
	}
	if strings.Contains(hosts, `SERVICE_CIDR="10.111.0.0/16"`) {
		t.Fatalf("已回落的覆写值仍在落盘（R-20：取值回落下层）")
	}
	after := faceOverrideRow(t, cluster, "HOSTS", ignCfgDeployServiceCIDR)
	if strings.TrimSpace(Str(after["applied_task_no"])) != "" {
		t.Fatalf("回落后不得被后续受理重新记上追溯对: %q", Str(after["applied_task_no"]))
	}
}

// ---------- ⑤ dry-run 与受理对同一输入同一结论（R-23） ----------

func TestDryRunAndAcceptanceAgree(t *testing.T) {
	requireDB(t)
	root := faceDeliveryFixture(t, `  setup) echo "setup ok"; exit 0 ;;`)
	cluster := newCluster(t)

	tpl := faceTestTemplate(t, "dl-agree", "HOSTS", map[string]string{ignCfgDeployNetworkPlugin: "calico"})
	faceOverride(t, cluster, "HOSTS", ignCfgDeployServiceCIDR, "10.111.0.0/16", "Service 网段避让")

	pre, err := ValidateIgnClusterConfig(map[string]any{
		"cluster_ref": cluster, "param_template_ref": Str(tpl["template_no"]),
	}, faceTestUser)
	mustNoErr(t, err)
	if pre["passed"] != true {
		t.Fatalf("干净面应预检通过: %v", pre["reasons"])
	}
	fields := pre["fields"].([]map[string]any)
	if len(fields) != 9 {
		t.Fatalf("预检回吐整张渲染面（两轴全部键），实际 %d", len(fields))
	}
	faceAccept(t, root, cluster, map[string]any{"param_template_ref": Str(tpl["template_no"])})

	// 「预检说会生效」与「受理真落盘」逐字段对齐：任何一条不一致都是两套逻辑漂移的证据
	hosts := readClusterFile(t, root, cluster, "hosts")
	conf := readClusterFile(t, root, cluster, "config.yml")
	for _, f := range fields {
		key := Str(f["field_key"])
		value := Str(f["effective_value"])
		if Str(f["file_face"]) == "CONFIG" {
			// CONFIG 面两条键：取源方式看标量行；仓库列表是整项替换，比对交给列表用例
			if key == ignCfgDeployInstallSource {
				if want := fmt.Sprintf(`INSTALL_SOURCE: %q`, value); !strings.Contains(conf, want) {
					t.Fatalf("预检生效值 %q 与落盘不一致：config.yml 缺 %q", value, want)
				}
			}
			continue
		}
		ini, ok := faceHostsINIKey[key]
		if !ok || strings.TrimSpace(value) == "" {
			continue // 空值即让位（R-22）：落盘上就是发行包打底值，本用例不逐字比对
		}
		if want := fmt.Sprintf(`%s=%q`, ini, value); !strings.Contains(hosts, want) {
			t.Fatalf("预检生效值 %q 与落盘不一致：hosts 缺 %q\n%s", value, want, hosts)
		}
	}

	// 快照里那张牌后来被停用 ⇒ 预检与受理给出同一条拒绝结论（两侧读的是同一处门槛）
	if _, err := SetIgnParamTemplateState(tplIntID(tpl), "disable", "停用", faceTestUser); err != nil {
		t.Fatalf("停用失败: %v", err)
	}
	pre2, err := ValidateIgnClusterConfig(map[string]any{"cluster_ref": cluster}, faceTestUser)
	mustNoErr(t, err)
	if pre2["passed"] == true {
		t.Fatalf("快照指向停用模板应预检不通过")
	}
	reason := fmt.Sprint(pre2["reasons"])
	if !strings.Contains(reason, "非启用态不参与取值") {
		t.Fatalf("预检结论口径不符: %s", reason)
	}
	src := fmt.Sprintf("CLC-FACE-REFUSE-%d", nextSeq())
	trackAudit(t, src)
	tasksBefore := countRows(t, `SELECT COUNT(*) FROM exec_task WHERE flag = 1 AND cluster_ref = ?`, cluster)
	_, err = DeliverySubmit(map[string]any{
		"source_task_no": src, "source_step_index": int64(9),
		"cluster_ref": cluster, "step_code": "03", "nodes": facadeInstallNodes(),
	}, facadeUser())
	be := assertBizCode(t, err, errcode.ErrForbidden)
	if !strings.Contains(be.Msg, "非启用态不参与取值") {
		t.Fatalf("受理与预检结论不一致（同一输入必须同一句话）: %s", be.Msg)
	}
	if n := countRows(t, `SELECT COUNT(*) FROM exec_task WHERE flag = 1 AND cluster_ref = ?`, cluster); n != tasksBefore {
		t.Fatalf("治理拒绝却建了任务行（Z1 不建任务）")
	}
	if len(auditRows(t, ignEvtGovernanceRefuse, src)) != 1 {
		t.Fatalf("面标识不可用的受理拒绝须落治理拒绝留痕")
	}
}

// ---------- ⑥ 拓扑面与参数面不同轴（Q-72）：参数面改不动节点成员 ----------

func TestParamFaceNeverChangesNodeMembership(t *testing.T) {
	requireDB(t)
	root := faceDeliveryFixture(t, `  setup) echo "setup ok"; exit 0 ;;`)
	cluster := newCluster(t)

	tpl := faceTestTemplate(t, "dl-topo", "HOSTS", map[string]string{ignCfgDeployDNSDomain: "svc.internal"})
	faceOverride(t, cluster, "HOSTS", ignCfgDeployServiceCIDR, "10.111.0.0/16", "网段避让")
	nodes := []any{
		map[string]any{"addr": "10.99.0.11", "role": "MASTER", "nodename": "clu-m1"},
		map[string]any{"addr": "10.99.0.12", "role": "WORKER"},
		map[string]any{"addr": "10.99.0.13", "role": "WORKER"},
	}
	faceAccept(t, root, cluster, map[string]any{
		"nodes": nodes, "param_template_ref": Str(tpl["template_no"]),
	})

	hosts := readClusterFile(t, root, cluster, "hosts")
	// 两条轴同时生效：参数面取值落了盘，节点成员仍是请求体里那三份
	if !strings.Contains(hosts, `CLUSTER_DNS_DOMAIN="svc.internal"`) ||
		!strings.Contains(hosts, `SERVICE_CIDR="10.111.0.0/16"`) {
		t.Fatalf("参数面取值未落盘:\n%s", hosts)
	}
	for _, addr := range []string{"10.99.0.11", "10.99.0.12", "10.99.0.13"} {
		if !strings.Contains(hosts, addr) {
			t.Fatalf("节点成员未按请求体清单落盘: 缺 %s", addr)
		}
	}
	if strings.Contains(hosts, "172.28.0.11") {
		t.Fatalf("发行包打底的示例节点未替换掉")
	}
	snap := faceManifestSnapshot(t, cluster)
	if len(snap.Nodes) != 3 || snap.ParamTemplateRef != Str(tpl["template_no"]) {
		t.Fatalf("快照应同时记下拓扑面成员与参数面标识: %+v", snap)
	}
}

// ---------- ⑦ 受理侧治理拒绝：不可解析 / 非启用态面标识（文案口径不变） ----------

func TestAcceptanceRefusesUnusableTemplateRef(t *testing.T) {
	requireDB(t)
	root := faceDeliveryFixture(t, `  setup) echo "setup ok"; exit 0 ;;`)

	stopped := faceTestTemplate(t, "dl-stop", "HOSTS", map[string]string{ignCfgDeployRuntime: "docker"})
	if _, err := SetIgnParamTemplateState(tplIntID(stopped), "disable", "停用", faceTestUser); err != nil {
		t.Fatalf("停用失败: %v", err)
	}
	for _, c := range []struct {
		ref, label string
		frags      []string
	}{
		{Str(stopped["template_no"]), "停用态模板", []string{"非启用态不参与取值", "停用是「不再被新集群选中」"}},
		{"TMP-19700101-9999", "未登记标识", []string{"在本域无登记", "R-13"}},
	} {
		cluster := newCluster(t)
		src := fmt.Sprintf("CLC-FACE-BAD-%d", nextSeq())
		trackAudit(t, src)
		_, err := DeliverySubmit(map[string]any{
			"source_task_no": src, "source_step_index": int64(1),
			"cluster_ref": cluster, "step_code": "01",
			"nodes": facadeInstallNodes(), "param_template_ref": c.ref,
		}, facadeUser())
		be := assertBizCode(t, err, errcode.ErrForbidden)
		for _, f := range c.frags {
			if !strings.Contains(be.Msg, f) {
				t.Fatalf("%s 的拒绝理由应含 %q，实际: %s", c.label, f, be.Msg)
			}
		}
		if n := countRows(t, `SELECT COUNT(*) FROM exec_task WHERE flag = 1 AND source_task_no = ?`, src); n != 0 {
			t.Fatalf("%s 的受理拒绝却建了任务行", c.label)
		}
		if _, err := os.Stat(filepath.Join(root, "clusters", cluster)); !os.IsNotExist(err) {
			t.Fatalf("%s 的受理拒绝却写了交付配置目录（拒绝即零副作用）", c.label)
		}
		if len(auditRows(t, ignEvtGovernanceRefuse, src)) != 1 {
			t.Fatalf("%s 须落治理拒绝留痕（REP-13 c8 可数）", c.label)
		}
	}
}

// ---------- ⑧ 版本引用仍按 R-19/INV-33 门槛：四层求值不得把不可交付版本当成可用事实 ----------

func TestAcceptanceVersionGateUnchanged(t *testing.T) {
	requireDB(t)
	root := faceDeliveryFixture(t, `  setup) echo "setup ok"; exit 0 ;;`)
	cluster := newCluster(t)

	faceOverride(t, cluster, "HOSTS", ignCfgDeployServiceCIDR, "10.111.0.0/16", "覆写在生效")
	name := insertFacadeVersion(t, "ARCHIVED", "v1.31.9", "FLANNEL")
	src := fmt.Sprintf("CLC-FACE-VER-%d", nextSeq())
	trackAudit(t, src)
	_, err := DeliverySubmit(map[string]any{
		"source_task_no": src, "source_step_index": int64(1),
		"cluster_ref": cluster, "step_code": "01",
		"nodes": facadeInstallNodes(), "version_ref": name,
	}, facadeUser())
	be := assertBizCode(t, err, errcode.ErrForbidden)
	if !strings.Contains(be.Msg, "R-19") || !strings.Contains(be.Msg, "已归档") {
		t.Fatalf("版本可交付门槛的口径变了: %s", be.Msg)
	}
	if _, err := os.Stat(filepath.Join(root, "clusters", cluster)); !os.IsNotExist(err) {
		t.Fatalf("版本不可用却写了交付配置目录")
	}
	// 门槛拒绝 ⇒ 追溯对不得回填（值没被消费过）
	if strings.TrimSpace(Str(faceOverrideRow(t, cluster, "HOSTS", ignCfgDeployServiceCIDR)["applied_task_no"])) != "" {
		t.Fatalf("受理未成功却回填了覆写追溯对（R-21 说的是真生效的那一次）")
	}
}
