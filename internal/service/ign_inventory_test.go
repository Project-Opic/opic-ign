// ign_inventory_test.go —— 集群交付配置生成（P2：打通执行位与集群配置生成）。
//
// 断言的三条硬事实（都来自 3.200 真跑通 01~07 的实测教训，不是臆测）：
//  1. ezctl 只认 clusters/<集群>/hosts 与 config.yml，且 **只有 ezctl new 会替换
//     `_cluster_name_`**；本域接管后必须产出不含占位符的真实 inventory，否则
//     ansible 会把产物写进 clusters/_cluster_name_（P1.5 实测踩过）；
//  2. 执行位子进程的环境必须让集群内网（节点/Pod/Service 网段、本地仓库域名）
//     **不经 HTTP 代理**，否则表现为 32s Client.Timeout 而非明确报错；
//  3. 私钥绝不进 argv —— 只能以 inventory 里的路径出现（R-12 同源纪律）。
package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitcode.com/opic-ontology/opic-techbase/internal/config"
)

// kubeaszHostsTemplate 取自 3.200 真实发行包 clusters/kasz-lab/hosts（ezctl new 的产物形态）。
// 保留注释、可选组与 [all:vars] 的原始顺序：渲染必须只做"定点改写"，不得丢内容。
const kubeaszHostsTemplate = `# 'etcd' cluster should have odd member(s) (1,3,5,...)
[etcd]
172.28.0.11

# master node(s), set unique 'k8s_nodename' for each node
# CAUTION: 'k8s_nodename' must consist of lower case alphanumeric characters, '-' or '.',
# and must start and end with an alphanumeric character
[kube_master]
172.28.0.11

# work node(s), set unique 'k8s_nodename' for each node
[kube_node]
172.28.0.11 k8s_nodename=''

# [optional] harbor server, a private docker registry
[harbor]
#192.168.1.8 NEW_INSTALL=false

# [optional] loadbalance for accessing k8s from outside
[ex_lb]
#192.168.1.6 LB_ROLE=backup EX_APISERVER_VIP=192.168.1.250 EX_APISERVER_PORT=8443

# [optional] ntp server for the cluster
[chrony]
#192.168.1.1

[all:vars]
# --------- Main Variables ---------------
# Secure port for apiservers
SECURE_PORT="6443"

# Cluster container-runtime supported: docker, containerd
CONTAINER_RUNTIME="containerd"

# Network plugins supported: calico, flannel, kube-router, cilium, kube-ovn
CLUSTER_NETWORK="flannel"

# Service proxy mode of kube-proxy: 'iptables' or 'nftables'
PROXY_MODE="iptables"

# K8S Service CIDR, not overlap with node(host) networking
SERVICE_CIDR="10.68.0.0/16"

# Cluster CIDR (Pod CIDR), not overlap with node(host) networking
CLUSTER_CIDR="10.200.0.0/16"

# NodePort Range
NODE_PORT_RANGE="30000-32767"

# Cluster DNS Domain
CLUSTER_DNS_DOMAIN="cluster.local"

# -------- Additional Variables (don't change the default value right now)---
# Binaries Directory
bin_dir="/opt/kube/bin"

# Deploy Directory (kubeasz workspace)
base_dir="/etc/kubeasz"

# Directory for a specific cluster
cluster_dir="{{ base_dir }}/clusters/_cluster_name_"

# CA and other components cert/key Directory
ca_dir="/etc/kubernetes/ssl"

# Default 'k8s_nodename' is empty
k8s_nodename=''

# Default python interpreter
ansible_python_interpreter=/usr/bin/python3
`

// kubeaszConfigTemplate config.yml 的关键片段（真实发行包形态：注释 + 列表键）。
const kubeaszConfigTemplate = `# 集群名称
INSTALL_SOURCE: "online"
OS_HARDEN: false
CLUSTER_NAME: "cluster1"
CONTEXT_NAME: "context-{{ CLUSTER_NAME }}"
K8S_VER: "1.36.3"
ENABLE_MIRROR_REGISTRY: true
INSECURE_REG:
  - "http://easzlab.io.local:5000"
  - "https://reg.yourcompany.com"
SANDBOX_IMAGE: "easzlab.io.local:5000/easzlab/pause:3.10.2"
MASTER_CERT_HOSTS:
  - "10.1.1.1"
NODE_CIDR_LEN: 24
FLANNEL_BACKEND: "vxlan"
flannel_ver: "v0.28.9"
calico_ver: "v3.32.1"
corednsVer: "1.14.6"
`

func baseParams() ignDeployParams {
	return ignDeployParams{
		ServiceCIDR:    "10.68.0.0/16",
		PodCIDR:        "10.200.0.0/16",
		DNSDomain:      "cluster.local",
		NetworkPlugin:  "flannel",
		Runtime:        "containerd",
		ProxyMode:      "iptables",
		NodePortRange:  "30000-32767",
		InstallSource:  "online",
		K8sVersion:     "1.36.3",
		CoreDNSVersion: "1.14.6",
	}
}

func nodesOf(specs ...string) []ignNodeSpec {
	out := make([]ignNodeSpec, 0, len(specs))
	for _, s := range specs {
		f := strings.Fields(s)
		n := ignNodeSpec{Addr: f[0]}
		if len(f) > 1 {
			n.Role = f[1]
		}
		if len(f) > 2 {
			n.Nodename = f[2]
		}
		out = append(out, n)
	}
	return out
}

// ---------- ① 门面请求体节点清单解析（Q-72：地址清单进请求体、两侧各校验一次） ----------

func TestParseFacadeNodes(t *testing.T) {
	t.Run("纯地址串清单默认工作节点", func(t *testing.T) {
		got, err := ignParseFacadeNodes([]any{"10.0.0.7", "10.0.0.8"})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].Addr != "10.0.0.7" || got[0].Role != ignRoleWorker {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("对象清单带角色与节点名", func(t *testing.T) {
		got, err := ignParseFacadeNodes([]any{
			map[string]any{"addr": "10.0.0.5", "role": "MASTER", "nodename": "clu1-10-0-0-5"},
			map[string]any{"ip_address": "10.0.0.6", "node_role": "WORKER"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if got[0].Role != ignRoleMaster || got[0].Nodename != "clu1-10-0-0-5" {
			t.Fatalf("控制面解析错: %+v", got[0])
		}
		if got[1].Addr != "10.0.0.6" || got[1].Role != ignRoleWorker {
			t.Fatalf("工作节点解析错: %+v", got[1])
		}
	})
	t.Run("上游角色词汇归一", func(t *testing.T) {
		// O-CLC 的 CLC_NODE_ROLE 词汇（MASTER/WORKER/INFRA）由被调用方解析（Q-70）
		got, err := ignParseFacadeNodes([]any{
			map[string]any{"addr": "10.0.0.5", "role": "INFRA"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if got[0].Role != ignRoleEtcd {
			t.Fatalf("INFRA 应归一为 etcd 专责角色，实得 %q", got[0].Role)
		}
	})
	t.Run("角色越界即治理拒绝", func(t *testing.T) {
		if _, err := ignParseFacadeNodes([]any{map[string]any{"addr": "10.0.0.5", "role": "BOSS"}}); err == nil {
			t.Fatal("未登记的角色必须拒绝")
		}
	})
	t.Run("地址非法即治理拒绝", func(t *testing.T) {
		if _, err := ignParseFacadeNodes([]any{"10.0.0.5; rm -rf /"}); err == nil {
			t.Fatal("含拼接风险字符的地址必须拒绝")
		}
	})
	t.Run("重复地址即拒绝", func(t *testing.T) {
		if _, err := ignParseFacadeNodes([]any{"10.0.0.5", "10.0.0.5"}); err == nil {
			t.Fatal("同一地址出现两次必须拒绝")
		}
	})
	t.Run("空白分隔串仍兼容", func(t *testing.T) {
		got, err := ignParseFacadeNodes("10.0.0.7 10.0.0.8")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 {
			t.Fatalf("got %+v", got)
		}
	})
}

// ---------- ② 角色 → 主机组拓扑（本域私有的 kubeasz 知识） ----------

func TestHostGroupsFromNodes(t *testing.T) {
	g, err := ignBuildTopology(nodesOf("10.0.0.5 MASTER", "10.0.0.6 WORKER", "10.0.0.7 WORKER"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(ignAddrList(g.Etcd), ",") != "10.0.0.5" {
		t.Fatalf("控制面须同时进 etcd 组，实得 %v", g.Etcd)
	}
	if strings.Join(ignAddrList(g.Master), ",") != "10.0.0.5" || len(g.Node) != 3 {
		t.Fatalf("master=%v node=%v", g.Master, g.Node)
	}

	t.Run("无控制面即拒绝", func(t *testing.T) {
		if _, err := ignBuildTopology(nodesOf("10.0.0.6 WORKER")); err == nil {
			t.Fatal("安装类交付配置的节点清单必须含控制面")
		}
	})
	t.Run("etcd 偶数成员即拒绝", func(t *testing.T) {
		// kubeasz 明示 'etcd' cluster should have odd member(s)；法定数为偶数不能静默放行
		if _, err := ignBuildTopology(nodesOf(
			"10.0.0.5 MASTER", "10.0.0.6 MASTER", "10.0.0.7 INFRA", "10.0.0.8 INFRA")); err == nil {
			t.Fatal("etcd 成员为偶数必须治理拒绝")
		}
	})
	t.Run("etcd 专责节点不进工作组", func(t *testing.T) {
		g, err := ignBuildTopology(nodesOf("10.0.0.5 MASTER", "10.0.0.6 MASTER", "10.0.0.9 INFRA"))
		if err != nil {
			t.Fatal(err)
		}
		if len(g.Etcd) != 3 {
			t.Fatalf("etcd 组应含控制面与专责节点，实得 %v", ignAddrList(g.Etcd))
		}
		for _, n := range ignAddrList(g.Node) {
			if n == "10.0.0.9" {
				t.Fatalf("INFRA 节点不应进 kube_node，实得 %v", g.Node)
			}
		}
	})
}

// ---------- ③ hosts 渲染：定点改写、占位符清零、未知内容保留 ----------

func TestRenderHosts(t *testing.T) {
	topo, err := ignBuildTopology(nodesOf("172.28.0.11 MASTER n1", "172.28.0.12 WORKER n2"))
	if err != nil {
		t.Fatal(err)
	}
	p := baseParams()
	p.PodCIDR = "10.244.0.0/16"
	p.SSHUser = "root"
	p.SSHKey = "/opt/opic/ssh/id_ed25519"

	out, err := ignRenderHosts(kubeaszHostsTemplate, "CLU-20261006-0001", topo, p)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("占位符必须清零", func(t *testing.T) {
		if strings.Contains(out, "_cluster_name_") {
			t.Fatal("hosts 残留 _cluster_name_ 占位符：ezctl 只有 new 才替换，接管后必须自净")
		}
		if !strings.Contains(out, "cluster_dir=\"{{ base_dir }}/clusters/CLU-20261006-0001\"") {
			t.Fatalf("cluster_dir 未指向本集群目录:\n%s", out)
		}
	})
	t.Run("组成员按拓扑重写", func(t *testing.T) {
		sec := ignHostsSection(out, "kube_node")
		if !strings.Contains(sec, "172.28.0.11 k8s_nodename=n1") || !strings.Contains(sec, "172.28.0.12 k8s_nodename=n2") {
			t.Fatalf("[kube_node] 渲染错:\n%s", sec)
		}
		if strings.Contains(ignHostsSection(out, "etcd"), "172.28.0.12") {
			t.Fatal("工作节点不得进 etcd 组")
		}
		// 模板里的旧成员必须被替换干净（172.28.0.11 本次也是 master，换个用例验证）
		other, err := ignBuildTopology(nodesOf("10.0.0.5 MASTER"))
		if err != nil {
			t.Fatal(err)
		}
		out2, err := ignRenderHosts(kubeaszHostsTemplate, "c2", other, baseParams())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out2, "172.28.0.11") {
			t.Fatalf("模板旧成员残留:\n%s", out2)
		}
	})
	t.Run("[all:vars] 受管键改写、非受管键保留", func(t *testing.T) {
		for _, want := range []string{
			`SERVICE_CIDR="10.68.0.0/16"`,
			`CLUSTER_CIDR="10.244.0.0/16"`,
			`CLUSTER_NETWORK="flannel"`,
			`CONTAINER_RUNTIME="containerd"`,
			`PROXY_MODE="iptables"`,
			`NODE_PORT_RANGE="30000-32767"`,
			`CLUSTER_DNS_DOMAIN="cluster.local"`,
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("受管键缺失 %s", want)
			}
		}
		for _, keep := range []string{`SECURE_PORT="6443"`, `bin_dir="/opt/kube/bin"`,
			`ca_dir="/etc/kubernetes/ssl"`, "ansible_python_interpreter=/usr/bin/python3",
			"# [optional] ntp server for the cluster", "[chrony]", "[harbor]"} {
			if !strings.Contains(out, keep) {
				t.Fatalf("发行包原内容丢失 %s", keep)
			}
		}
	})
	t.Run("私钥只以路径进入 inventory", func(t *testing.T) {
		if !strings.Contains(out, "ansible_ssh_private_key_file=/opt/opic/ssh/id_ed25519") ||
			!strings.Contains(out, "ansible_ssh_user=root") {
			t.Fatalf("SSH 免密未写入 [all:vars]:\n%s", out)
		}
	})
	t.Run("同输入必同输出", func(t *testing.T) {
		again, err := ignRenderHosts(kubeaszHostsTemplate, "CLU-20261006-0001", topo, p)
		if err != nil || again != out {
			t.Fatalf("渲染不幂等: err=%v", err)
		}
	})
}

// ---------- ④ config.yml：受管键改写 + 列表键整体替换 + 注释保留 ----------

func TestMergeConfigYAML(t *testing.T) {
	p := baseParams()
	p.ImageMirror = "easzlab.io.local:5000"
	p.CNIVersion = "v0.28.9"
	p.DNSDomain = "opic-lab.local"
	p.MasterAddrs = []string{"172.28.0.11"}
	p.NodeAddrs = []string{"172.28.0.11", "172.28.0.12"}

	out, err := ignMergeConfigYAML(kubeaszConfigTemplate, "CLU-20261006-0001", p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`CLUSTER_NAME: "CLU-20261006-0001"`,
		`INSTALL_SOURCE: "online"`,
		`K8S_VER: "1.36.3"`,
		`flannel_ver: "v0.28.9"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("受管标量键缺失 %s\n%s", want, out)
		}
	}
	if strings.Contains(out, "https://reg.yourcompany.com") {
		t.Fatalf("INSECURE_REG 列表须按镜像地址整体替换:\n%s", out)
	}
	if !strings.Contains(out, "easzlab.io.local:5000") {
		t.Fatal("本地镜像仓库地址未注入")
	}
	if !strings.Contains(out, "api.opic-lab.local") {
		t.Fatalf("MASTER_CERT_HOSTS 须含 api.<DNS 域>（上游据此登记访问端点）:\n%s", out)
	}
	if !strings.Contains(out, "# 集群名称") {
		t.Fatal("发行包注释不得丢失")
	}
	if !strings.Contains(out, "FLANNEL_BACKEND") || !strings.Contains(out, "NODE_CIDR_LEN: 24") {
		t.Fatal("未受管键必须原样保留")
	}
	again, err := ignMergeConfigYAML(kubeaszConfigTemplate, "CLU-20261006-0001", p)
	if err != nil || again != out {
		t.Fatalf("config.yml 渲染不幂等: err=%v", err)
	}
}

// ---------- ⑤ 执行环境：集群内网不经代理（P1.5 实测 32s 超时的根因） ----------

func TestToolEnvProxyNeutralized(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://10.26.8.196:3228")
	t.Setenv("NO_PROXY", "localhost,127.0.0.1")
	p := baseParams()
	p.NodeAddrs = []string{"172.28.0.11"}

	env := ignToolEnv("clu-a", p)
	for _, forbidden := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "IGN_SSH_DIR"} {
		for _, kv := range env {
			if strings.HasPrefix(kv, forbidden+"=") {
				t.Fatalf("%s 不得进入执行环境（真 ezctl 不认它，且代理会吞掉集群内网流量）: %s", forbidden, kv)
			}
		}
	}
	var noProxy string
	for _, kv := range env {
		if strings.HasPrefix(kv, "NO_PROXY=") {
			noProxy = kv
		}
	}
	if noProxy == "" {
		t.Fatal("必须显式声明 NO_PROXY")
	}
	for _, want := range []string{"10.68.0.0/16", "10.200.0.0/16", "172.28.0.11", "easzlab.io.local", "127.0.0.1"} {
		if !strings.Contains(noProxy, want) {
			t.Fatalf("NO_PROXY 须覆盖 %s，实得 %s", want, noProxy)
		}
	}
	if !strings.Contains(strings.Join(env, "\n"), "HOME=") {
		t.Fatal("ezctl/ansible 依赖 HOME（ssh 已知主机、~/.kube）")
	}
}

// ---------- ⑥ argv：节点增删的真实命令面（ezctl add-node 只吃一个地址） ----------

func TestNodeOpsArgv(t *testing.T) {
	bin := "/etc/kubeasz/ezctl"
	t.Run("单地址走 add-node", func(t *testing.T) {
		got, err := ignBuildArgv(bin, "ezctl.add-node", "clu-1", "", []string{"10.0.0.9"}, "")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(got, " ") != bin+" add-node clu-1 10.0.0.9" {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("多地址改走 add-nodes", func(t *testing.T) {
		// add-node 把 ${@:2} 整体 sed 进 hosts（多个地址会写成一行）⇒ 必须换命令字
		got, err := ignBuildArgv(bin, "ezctl.add-node", "clu-1", "", []string{"10.0.0.9", "10.0.0.10"}, "")
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(got, " ") != bin+" add-nodes clu-1 10.0.0.9 10.0.0.10" {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("del-node 只接受单地址", func(t *testing.T) {
		if _, err := ignBuildArgv(bin, "ezctl.del-node", "clu-1", "", []string{"10.0.0.9", "10.0.0.10"}, ""); err == nil {
			t.Fatal("del-node 在 ezctl 里只取 $2，多地址必须拒绝而不是静默删一个")
		}
	})
	t.Run("节点增删只认 IPv4", func(t *testing.T) {
		// kubeasz 的 add-node/del-node 用 IPv4 正则硬校验主机名 ⇒ 提前拒绝，不留远程困惑
		if _, err := ignBuildArgv(bin, "ezctl.add-node", "clu-1", "", []string{"node-a.example.com"}, ""); err == nil {
			t.Fatal("主机名地址在节点增删类动作必须拒绝")
		}
	})
}

// ---------- ⑦ 交付配置对齐编排：接管/幂等/冲突三态 ----------

// facadeNewBranch 命令面夹具的建目录分支：与真实 `ezctl new` 同形态产出 hosts/config.yml
// （含 _cluster_name_ 占位符并就地替换为集群标识），门面用例与对齐用例共用。
func facadeNewBranch() string {
	return `  new)
    mkdir -p "clusters/$1"
    cat > "clusters/$1/hosts" <<'HOSTS'
` + kubeaszHostsTemplate + `
HOSTS
    sed -i.bak "s/_cluster_name_/$1/g" "clusters/$1/hosts"
    cat > "clusters/$1/config.yml" <<'CONF'
` + kubeaszConfigTemplate + `
CONF
    echo "ezctl new $1" ;;`
}

func withFakeTool(t *testing.T) string {
	t.Helper()
	bin, root := writeExecTool(t, facadeToolBody())
	old := config.Config.Delivery
	config.Config.Delivery.ToolBin = bin
	config.Config.Delivery.ToolRoot = root
	config.Config.Delivery.WorkDir = root
	config.Config.Delivery.SSHDir = "/opt/opic/ssh"
	t.Cleanup(func() { config.Config.Delivery = old })
	return root
}

func readClusterFile(t *testing.T, root, ref, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "clusters", ref, name))
	if err != nil {
		t.Fatalf("读取 %s/%s 失败: %v", ref, name, err)
	}
	return string(b)
}

func TestAlignClusterInventory(t *testing.T) {
	root := withFakeTool(t)
	p := baseParams()
	p.NodeAddrs = []string{"172.28.0.11"}
	topo, err := ignBuildTopology(nodesOf("172.28.0.11 MASTER", "172.28.0.12 WORKER"))
	if err != nil {
		t.Fatal(err)
	}
	p.NodeAddrs = []string{"172.28.0.11", "172.28.0.12"}

	t.Run("首步受理生成真实交付配置", func(t *testing.T) {
		note, err := ignAlignClusterInventory(ignAlignReq{
			ClusterRef: "clu-a", Action: "ezctl.setup", StepCode: "01", Topo: topo, Params: p,
		})
		if err != nil {
			t.Fatal(err)
		}
		hosts := readClusterFile(t, root, "clu-a", "hosts")
		if strings.Contains(hosts, "_cluster_name_") || !strings.Contains(hosts, "172.28.0.12") {
			t.Fatalf("hosts 未按登记事实生成:\n%s", hosts)
		}
		if !strings.Contains(readClusterFile(t, root, "clu-a", "config.yml"), `CLUSTER_NAME: "clu-a"`) {
			t.Fatal("config.yml 集群名未按登记标识改写")
		}
		if _, err := os.Stat(filepath.Join(root, "clusters", "clu-a", ignInventoryManifest)); err != nil {
			t.Fatalf("接管标记未落（后续受理无从判定配置归属）: %v", err)
		}
		if !strings.Contains(note, "hosts") || !strings.Contains(note, "config.yml") {
			t.Fatalf("对齐留痕须说明两份产物，实得 %q", note)
		}
	})

	t.Run("重入幂等且不重复触发骨架动作", func(t *testing.T) {
		before := readClusterFile(t, root, "clu-a", "hosts")
		if _, err := ignAlignClusterInventory(ignAlignReq{
			ClusterRef: "clu-a", Action: "ezctl.setup", StepCode: "03", Topo: topo, Params: p,
		}); err != nil {
			t.Fatal(err)
		}
		if after := readClusterFile(t, root, "clu-a", "hosts"); after != before {
			t.Fatal("同事实重入却改写了 hosts（B-AUTO-15 可重入纪律）")
		}
		// 事实变化（多一台工作节点）⇒ 必须按新事实改写，不能因为"目录已存在"就沿用旧的
		topo2, err := ignBuildTopology(nodesOf("172.28.0.11 MASTER", "172.28.0.12 WORKER", "172.28.0.13 WORKER"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ignAlignClusterInventory(ignAlignReq{
			ClusterRef: "clu-a", Action: "ezctl.setup", StepCode: "03", Topo: topo2, Params: p,
		}); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(readClusterFile(t, root, "clu-a", "hosts"), "172.28.0.13") {
			t.Fatal("节点事实变化后 hosts 未跟进")
		}
	})

	t.Run("非本域生成的交付配置即拒绝接管", func(t *testing.T) {
		ref := "clu-manual"
		dir := filepath.Join(root, "clusters", ref)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "hosts"), []byte("[etcd]\n1.2.3.4\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte("CLUSTER_NAME: \"x\"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := ignAlignClusterInventory(ignAlignReq{
			ClusterRef: ref, Action: "ezctl.setup", StepCode: "01", Topo: topo, Params: p,
		})
		if err == nil || !strings.Contains(err.Error(), "非本域") {
			t.Fatalf("人工/ezctl new 产物须拒绝接管并留痕，实得 %v", err)
		}
		if b, _ := os.ReadFile(filepath.Join(dir, "hosts")); strings.Contains(string(b), "172.28.0.11") {
			t.Fatal("拒绝接管却改写了人工配置")
		}
	})

	t.Run("无节点清单时沿用已对齐配置", func(t *testing.T) {
		if _, err := ignAlignClusterInventory(ignAlignReq{
			ClusterRef: "clu-a", Action: "ezctl.setup", StepCode: "05", Params: p,
		}); err != nil {
			t.Fatalf("重试/续派场景（配置已对齐、不带清单）应通过: %v", err)
		}
		if _, err := ignAlignClusterInventory(ignAlignReq{
			ClusterRef: "clu-new", Action: "ezctl.setup", StepCode: "01", Params: p,
		}); err == nil {
			t.Fatal("配置未对齐又不带节点清单必须拒绝")
		}
	})

	t.Run("节点增删不改写安装期配置", func(t *testing.T) {
		// add-node 由 ezctl 自己把新地址 sed 进 [kube_node]；本域若预先写入会得到
		// "node already existed" 的远程困惑（真实 ezctl 行为，不可双重写入）
		if _, err := ignAlignNodeOps("clu-a", "ezctl.add-node", []string{"172.28.0.99"}); err != nil {
			t.Fatalf("新地址不在 inventory 内应通过: %v", err)
		}
		if _, err := ignAlignNodeOps("clu-a", "ezctl.add-node", []string{"172.28.0.12"}); err == nil {
			t.Fatal("已在工作组的地址须受理侧拒绝")
		}
		if _, err := ignAlignNodeOps("clu-a", "ezctl.del-node", []string{"172.28.0.12"}); err != nil {
			t.Fatalf("在册地址删除应通过: %v", err)
		}
		if _, err := ignAlignNodeOps("clu-a", "ezctl.del-node", []string{"172.28.0.77"}); err == nil {
			t.Fatal("不在册地址删除须受理侧拒绝")
		}
		if _, err := ignAlignNodeOps("clu-none", "ezctl.add-node", []string{"172.28.0.99"}); err == nil {
			t.Fatal("集群交付配置尚未生成时节点增删须拒绝")
		}
		if b, _ := os.ReadFile(filepath.Join(root, "clusters", "clu-a", "hosts")); strings.Contains(string(b), "172.28.0.99") {
			t.Fatal("节点增删动作不得由本域改写 hosts")
		}
	})
}

// ignHostsSection 取某主机组的正文（断言辅助，不参与生产逻辑）。
func ignHostsSection(hosts, section string) string {
	in := false
	var b strings.Builder
	for _, line := range strings.Split(hosts, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			in = t == "["+section+"]"
			continue
		}
		if in {
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}
