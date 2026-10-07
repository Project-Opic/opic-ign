// ign_delivery_pg_test.go —— 交付门面（FUNC-34~37）与本机执行位（B-AUTO-11~15）的 DB 语义用例。
//
// 依赖 ign_pg_test.go 的临时 PostgreSQL 装置；命令面用 writeExecTool 的 0755 脚本夹具充当 ezctl，
// 执行位真实 fork 该脚本（独立 argv、不经 shell），因此逐行采集/游标/判定网关/终止链都在
// 真实进程与真实库上验证，不打桩。
package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"gitcode.com/opic-ontology/opic-techbase/internal/config"
	"gitcode.com/opic-ontology/opic-techbase/internal/store"
	"gitcode.com/opic-ontology/opic-techbase/pkg/errcode"
)

// ---------- 门面用例夹具 ----------

func facadeUser() UserInfo { return UserInfo{ID: 1, Username: "facade-tester"} }

// facadeInstallNodes 安装族受理的节点清单：一个控制面（同时进 etcd/kube_master/kube_node，
// etcd 成员保持奇数）。安装族的交付配置由本域按这份事实派生，清单不进 argv（FUNC-34/Q-72）。
func facadeInstallNodes() []any {
	return []any{map[string]any{"addr": "10.99.0.11", "role": "MASTER", "nodename": "clu-m1"}}
}

// softSQL 清理钩子用的 SQL（t.Cleanup 里不能再 Fatal，失败只由下一次断言暴露）。
func softSQL(sql string, args ...any) {
	_ = store.DB.Exec(sql, args...).Error
}

// facadeToolBody 命令面夹具脚本：new 分支按发行包形态产出 hosts/config.yml（B-AUTO-15
// 的交付配置打底），其余子命令由用例给出（argv 契约：<bin> <子命令> <集群> [步骤/地址…]），
// 未知命令 exit 64。
func facadeToolBody(cases ...string) string {
	var b strings.Builder
	b.WriteString(`cmd="$1"; shift`)
	b.WriteString("\ncase \"$cmd\" in\n")
	b.WriteString(facadeNewBranch())
	b.WriteString("\n")
	for _, c := range cases {
		b.WriteString(c + "\n")
	}
	b.WriteString(`  *) echo "ezctl: unknown command $cmd" >&2; exit 64 ;;`)
	b.WriteString("\nesac")
	return b.String()
}

// useDeliveryEnv 挂载执行位部署配置（1.6.1：命令面路径只以配置值存在）并登记本机执行机。
func useDeliveryEnv(t *testing.T, body string) string {
	t.Helper()
	bin, root := writeExecTool(t, body)
	d := &config.Config.Delivery
	binPrev, rootPrev, workPrev := d.ToolBin, d.ToolRoot, d.WorkDir
	d.ToolBin, d.ToolRoot, d.WorkDir = bin, root, root
	t.Cleanup(func() { d.ToolBin, d.ToolRoot, d.WorkDir = binPrev, rootPrev, workPrev })
	if err := EnsureLocalExecutor(); err != nil {
		t.Fatal(err)
	}
	mustSQL(t, `UPDATE executor SET current_tasks_count = 0 WHERE executor_type = 'LOCAL' AND flag = 1`)
	return root
}

func localExec(t *testing.T) map[string]any {
	t.Helper()
	row := mustOne(t, `SELECT * FROM executor WHERE executor_type = 'LOCAL' AND flag = 1`)
	if row == nil {
		t.Fatal("本机执行机未注册（INV-12）")
	}
	return row
}

// newCluster 用例专属集群标识 + 结束清理（装置共用一库，用例之间不得互踩）。
func newCluster(t *testing.T) string {
	t.Helper()
	ref := fmt.Sprintf("t%d", nextSeq())
	trackCluster(t, ref)
	return ref
}

func trackCluster(t *testing.T, clusterRef string) {
	t.Helper()
	t.Cleanup(func() {
		softSQL(`DELETE FROM ign_task_log WHERE task_id IN (SELECT id FROM exec_task WHERE cluster_ref = ?)`, clusterRef)
		softSQL(`DELETE FROM exec_task WHERE cluster_ref = ?`, clusterRef)
	})
}

func trackAudit(t *testing.T, sourceTaskNo string) {
	t.Helper()
	t.Cleanup(func() {
		softSQL(`DELETE FROM ign_reconcile_log WHERE flag = 1 AND object_no LIKE ?`, sourceTaskNo+"%")
	})
}

// insertRunningTask 直接登记在途任务（无在途协程）：幂等复用、撤销就地收口、失联判定的确定性夹具。
func insertRunningTask(t *testing.T, executorID int64, action, clusterRef, stepCode, sourceTaskNo string, sourceStepIndex int64) string {
	t.Helper()
	taskNo := fmt.Sprintf("TSK-TEST-%08d", nextSeq())
	mustSQL(t, `
		INSERT INTO exec_task
			(task_no, executor_id, action, args, cluster_ref, step_code,
			 source_task_no, source_step_index, task_status, timeout_seconds, started_at, submitted_by, created_by)
		VALUES (?, ?, ?, '', ?, ?, ?, ?, 'RUNNING', 60, ?, ?, 1)`,
		taskNo, executorID, action, clusterRef, ignNullableStr(stepCode),
		ignNullableStr(sourceTaskNo), nullableBigInt(sourceStepIndex), ignNow(), "fixture")
	return taskNo
}

func assertBizCode(t *testing.T, err error, want errcode.ErrCode) *BizErr {
	t.Helper()
	if err == nil {
		t.Fatalf("预期拒绝（%s），实际受理成功", want)
	}
	be := AsBizErr(err)
	if be.Code != want {
		t.Fatalf("错误码 %s（%s），期望 %s", be.Code, be.Msg, want)
	}
	return be
}

func auditRows(t *testing.T, eventType, sourceTaskNo string) []map[string]any {
	t.Helper()
	return mustList(t, `
		SELECT * FROM ign_reconcile_log
		WHERE flag = 1 AND event_type = ? AND object_no LIKE ? ORDER BY id`,
		eventType, sourceTaskNo+"%")
}

// ---------- FUNC-34 受理 ----------

// TestFacadeSubmitRunsLocal 全新受理：新建任务行 + R-05 计数递增 + B-AUTO-11 异步收口
// （COMPLETED / EXIT_CODE）+ 逐行入库（INV-38 游标连续）+ 受理注记行。
func TestFacadeSubmitRunsLocal(t *testing.T) {
	requireDB(t)
	useDeliveryEnv(t, facadeToolBody(`  setup) echo "step $2 begin"; echo "step $2 done"; exit 0 ;;`))
	exec := localExec(t)
	src := fmt.Sprintf("CLC-L-%d", nextSeq())
	cluster := newCluster(t)
	before := toInt(exec["current_tasks_count"])

	got, err := DeliverySubmit(map[string]any{
		"source_task_no": src, "source_step_index": int64(1),
		"cluster_ref": cluster, "step_code": "03", "nodes": facadeInstallNodes(),
	}, facadeUser())
	if err != nil {
		t.Fatal(err)
	}
	taskNo := Str(got["task_no"])
	if !strings.HasPrefix(taskNo, "TSK-") {
		t.Errorf("任务编号口径异常: %q", taskNo)
	}
	if reused, _ := got["facade_reused"].(bool); reused {
		t.Error("全新受理不应命中幂等复用")
	}
	if Str(got["action"]) != "ezctl.setup" || Str(got["step_code"]) != "03" {
		t.Errorf("解析结果 %s/%s，期望 ezctl.setup/03（R-13/INV-36）", Str(got["action"]), Str(got["step_code"]))
	}
	if Str(got["source_task_no"]) != src {
		t.Errorf("来源标识 %q，期望 %q（INV-34）", Str(got["source_task_no"]), src)
	}
	if got["request_token_digest"] != nil {
		t.Error("门面路径不携带业务请求令牌：摘要列必须落 NULL（INV-31 条件必填）")
	}

	row := waitTaskStatus(t, taskNo, "COMPLETED")
	if Str(row["verdict_source"]) != "EXIT_CODE" {
		t.Errorf("结论依据 %s，期望 EXIT_CODE（无剧本汇总即以退出码为准，R-14）", Str(row["verdict_source"]))
	}
	if toInt(row["exit_code"]) != 0 {
		t.Errorf("退出码 %v，期望 0", row["exit_code"])
	}
	if Str(row["source_task_no"]) != src || toInt(row["source_step_index"]) != 1 {
		t.Errorf("来源两列未落库: %v/%v", row["source_task_no"], row["source_step_index"])
	}
	if n := toInt(mustOne(t, `SELECT current_tasks_count AS c FROM executor WHERE id = ?`, toInt(exec["id"]))["c"]); n != before {
		t.Errorf("R-05 计数未回卷: %d，期望 %d（收口递减）", n, before)
	}

	logs := mustList(t, `SELECT seq, direction, log_line FROM ign_task_log WHERE task_id = ? AND flag = 1 ORDER BY seq`, toInt(row["id"]))
	if len(logs) < 5 {
		t.Fatalf("日志行数 %d，期望 ≥5（受理注记 + 启动注记 + 2 行工具输出 + 终态回填）", len(logs))
	}
	for i, l := range logs { // INV-38：任务内游标从 1 连续只增
		if toInt(l["seq"]) != int64(i+1) {
			t.Fatalf("游标不连续: 第 %d 行 seq=%v（INV-38）", i+1, l["seq"])
		}
	}
	if Str(logs[0]["direction"]) != "PLATFORM" || !strings.Contains(Str(logs[0]["log_line"]), "门面受理") {
		t.Errorf("首行应为门面受理注记，实际 %s/%s", Str(logs[0]["direction"]), Str(logs[0]["log_line"]))
	}
	var toolLines []string
	var startNotes, alignNotes int
	for _, l := range logs[1 : len(logs)-1] {
		switch Str(l["direction"]) {
		case "TOOL":
			toolLines = append(toolLines, Str(l["log_line"]))
		case "PLATFORM":
			line := Str(l["log_line"])
			if strings.Contains(line, "交付配置") {
				alignNotes++
				if !strings.Contains(line, "hosts") || !strings.Contains(line, "config.yml") {
					t.Errorf("交付配置注记未交代两份产物: %q", line)
				}
				continue
			}
			startNotes++
			if !strings.Contains(line, "执行位已启动") {
				t.Errorf("中段平台注记不符: %q", line)
			}
		default:
			t.Errorf("流向 %s 不在 IGN_LOG_DIRECTION 内", Str(l["direction"]))
		}
	}
	if startNotes != 1 {
		t.Errorf("启动注记行数 %d，期望 1", startNotes)
	}
	if alignNotes != 1 {
		t.Errorf("交付配置注记行数 %d，期望 1（B-AUTO-15 的对齐事实必须可读回）", alignNotes)
	}
	if strings.Join(toolLines, "|") != "step 03 begin|step 03 done" {
		t.Errorf("工具输出逐行入库不符: %v", toolLines)
	}
	last := logs[len(logs)-1]
	if !strings.HasPrefix(Str(last["log_line"]), "终态回填：COMPLETED / 结论依据 EXIT_CODE") {
		t.Errorf("终态回填注记不符: %q", Str(last["log_line"]))
	}
	if n := countRows(t, `SELECT COUNT(*) FROM ign_reconcile_log WHERE flag = 1 AND event_type = 'TASK_LOST' AND object_no = ?`, taskNo); n != 0 {
		t.Error("正常收口不得产生失联留痕")
	}
}

// TestFacadeSubmitIdempotentReuse INV-35：同一「来源任务标识 + 来源步骤序号」的在途任务命中即复用，
// 不新建行、不重复占用容量。
func TestFacadeSubmitIdempotentReuse(t *testing.T) {
	requireDB(t)
	useDeliveryEnv(t, facadeToolBody(`  setup) echo ok; exit 0 ;;`))
	exec := localExec(t)
	src := fmt.Sprintf("CLC-IDEM-%d", nextSeq())
	cluster := newCluster(t)
	existing := insertRunningTask(t, toInt(exec["id"]), "ezctl.setup", cluster, "02", src, 7)
	other := newCluster(t) // 复用路径不得被同集群排队逻辑牵连

	got, err := DeliverySubmit(map[string]any{
		"source_task_no": src, "source_step_index": int64(7),
		"cluster_ref": other, "step_code": "02", "nodes": facadeInstallNodes(),
	}, facadeUser())
	if err != nil {
		t.Fatal(err)
	}
	if Str(got["task_no"]) != existing {
		t.Errorf("幂等键命中却新建任务: %q ≠ %q（INV-35）", Str(got["task_no"]), existing)
	}
	if reused, _ := got["facade_reused"].(bool); !reused {
		t.Error("复用标记 facade_reused 未置真")
	}
	if !strings.Contains(Str(got["facade_note"]), "幂等复用") {
		t.Errorf("复用说明缺失: %q", Str(got["facade_note"]))
	}
	if n := countRows(t, `SELECT COUNT(*) FROM exec_task WHERE flag = 1 AND source_task_no = ?`, src); n != 1 {
		t.Errorf("任务行数 %d，期望 1（INV-35 不新建）", n)
	}
	if n := toInt(mustOne(t, `SELECT current_tasks_count AS c FROM executor WHERE id = ?`, toInt(exec["id"]))["c"]); n != 0 {
		t.Errorf("复用路径不得重复占用容量: %d（R-05）", n)
	}
	if st := Str(mustOne(t, `SELECT task_status FROM exec_task WHERE task_no = ?`, existing)["task_status"]); st != "RUNNING" {
		t.Errorf("被复用任务状态被改写: %s", st)
	}
}

// TestFacadeGovernanceRefuse R-13/R-12 受理侧治理拒绝（Z1）：不建任务行、逐条落 GOVERNANCE_REFUSE 台账。
func TestFacadeGovernanceRefuse(t *testing.T) {
	requireDB(t)
	useDeliveryEnv(t, facadeToolBody(`  setup) echo ok; exit 0 ;;`))
	src := fmt.Sprintf("CLC-GR-%d", nextSeq())
	trackAudit(t, src)
	cluster := newCluster(t)

	cases := []struct {
		name      string
		extra     map[string]any
		msgSubstr string
	}{
		{"步骤编码越界", map[string]any{"step_code": "08"}, "既不属安装族参数域"},
		{"动作码与步骤编码冲突", map[string]any{"action_code": "ezctl.start", "step_code": "03"}, "动作码却为"},
		{"内部前置层不可达", map[string]any{"action_code": "ezctl.new"}, "不属交付动作层"},
		{"平台运维层不可达", map[string]any{"action_code": "scripts.health_check"}, "不属交付动作层"},
		{"白名单外动作", map[string]any{"action_code": "rm -rf"}, "不在 IGN_ACTION 白名单"},
		{"两列皆缺", map[string]any{}, "至少给一个"},
	}
	for i, c := range cases {
		body := map[string]any{
			"source_task_no": src, "source_step_index": int64(i + 1), "cluster_ref": cluster,
		}
		for k, v := range c.extra {
			body[k] = v
		}
		_, err := DeliverySubmit(body, facadeUser())
		be := assertBizCode(t, err, errcode.ErrForbidden)
		if !strings.Contains(be.Msg, c.msgSubstr) {
			t.Errorf("%s: 中文提示缺 %q（实际 %s）", c.name, c.msgSubstr, be.Msg)
		}
	}
	if n := countRows(t, `SELECT COUNT(*) FROM exec_task WHERE flag = 1 AND source_task_no = ?`, src); n != 0 {
		t.Errorf("治理拒绝却建了任务行: %d 行（Z1 不建任务、不占容量）", n)
	}
	rows := auditRows(t, ignEvtGovernanceRefuse, src)
	if len(rows) != len(cases) {
		t.Fatalf("治理拒绝留痕 %d 条，期望 %d 条（REP-13 c8 可数）", len(rows), len(cases))
	}
	for _, r := range rows {
		if Str(r["object_type"]) != "TASK" || Str(r["remote_value"]) != "治理拒绝" {
			t.Errorf("拒绝留痕形态不符: %v/%v", r["object_type"], r["remote_value"])
		}
		if !strings.Contains(Str(r["trigger_reason"]), "R-13") {
			t.Errorf("拒绝留痕缺规则口径: %q", Str(r["trigger_reason"]))
		}
	}
}

// TestFacadeRequestFieldContract FUNC-34 请求体字段口径：节点地址清单只随节点增删类动作，
// 且地址须过格式白名单（Q-72：清单进请求体、两侧各校验一次）。
func TestFacadeRequestFieldContract(t *testing.T) {
	requireDB(t)
	useDeliveryEnv(t, facadeToolBody(`  add-node) echo "add $@"; exit 0 ;;`, `  start) echo ok; exit 0 ;;`))
	src := fmt.Sprintf("CLC-FC-%d", nextSeq())
	trackAudit(t, src)
	cluster := newCluster(t)

	_, err := DeliverySubmit(map[string]any{
		"source_task_no": src, "source_step_index": int64(1),
		"cluster_ref": cluster, "action_code": "ezctl.add-node",
	}, facadeUser())
	be := assertBizCode(t, err, errcode.ErrParam)
	if !strings.Contains(be.Msg, "必须携带节点地址清单") {
		t.Errorf("缺清单提示不符: %s", be.Msg)
	}

	_, err = DeliverySubmit(map[string]any{
		"source_task_no": src, "source_step_index": int64(2),
		"cluster_ref": cluster, "action_code": "ezctl.start", "nodes": []any{"10.0.0.1"},
	}, facadeUser())
	be = assertBizCode(t, err, errcode.ErrParam)
	if !strings.Contains(be.Msg, "不得携带节点地址清单") {
		t.Errorf("多余清单提示不符: %s", be.Msg)
	}

	_, err = DeliverySubmit(map[string]any{
		"source_task_no": src, "source_step_index": int64(3),
		"cluster_ref": cluster, "action_code": "ezctl.add-node", "nodes": []any{"10.0.0.1; rm -rf /"},
	}, facadeUser())
	assertBizCode(t, err, errcode.ErrForbidden)

	// 被拒输入可以是凭据形态：留痕文本同样要过写前掩码（R-16/INV-40 不给"拒绝理由"开后门）
	_, err = DeliverySubmit(map[string]any{
		"source_task_no": src, "source_step_index": int64(4),
		"cluster_ref": cluster, "action_code": "ezctl.add-node", "nodes": []any{"password=Sup3rSecretValue"},
	}, facadeUser())
	assertBizCode(t, err, errcode.ErrForbidden)

	rows := auditRows(t, ignEvtGovernanceRefuse, src)
	if len(rows) != 2 {
		t.Fatalf("字段口径拒绝留痕 %d 条，期望 2 条（非法地址 + 凭据形态地址）", len(rows))
	}
	if !strings.Contains(Str(rows[0]["local_value"]), "节点地址") {
		t.Errorf("拒绝留痕应交代被拒字段: %q", Str(rows[0]["local_value"]))
	}
	if strings.Contains(Str(rows[1]["local_value"]), "Sup3rSecretValue") {
		t.Errorf("拒绝留痕泄漏凭据明文: %q（R-16）", Str(rows[1]["local_value"]))
	}
	if !strings.Contains(Str(rows[1]["local_value"]), ignMaskMark) {
		t.Errorf("拒绝留痕缺掩码标记: %q", Str(rows[1]["local_value"]))
	}
	if n := countRows(t, `SELECT COUNT(*) FROM exec_task WHERE flag = 1 AND source_task_no = ?`, src); n != 0 {
		t.Errorf("字段口径不符却建了任务行: %d 行", n)
	}
}

// TestFacadeNodeOpsPrecheck 节点增删的受理侧前置（FUNC-34/Q-72 + 真实命令面事实）：
// 地址须 IPv4（ezctl 硬校验）、须以已对齐的交付配置为前提、在册/不在册在受理侧判 ——
// 三类都拒绝得掉，才不会出现"任务已登记、执行位在远端才失败"的半途事实。
func TestFacadeNodeOpsPrecheck(t *testing.T) {
	requireDB(t)
	useDeliveryEnv(t, facadeToolBody(
		`  setup) echo ok; exit 0 ;;`,
		`  add-node) echo "add $@"; exit 0 ;;`,
		`  add-nodes) echo "adds $@"; exit 0 ;;`))
	src := fmt.Sprintf("CLC-NP-%d", nextSeq())
	trackAudit(t, src)
	cluster := newCluster(t)

	_, err := DeliverySubmit(map[string]any{
		"source_task_no": src, "source_step_index": int64(1),
		"cluster_ref": cluster, "action_code": "ezctl.add-node", "nodes": []any{"10.99.0.99"},
	}, facadeUser())
	be := assertBizCode(t, err, errcode.ErrState)
	if !strings.Contains(be.Msg, "hosts") {
		t.Errorf("无交付配置时的拒绝理由未指出缺什么: %s", be.Msg)
	}

	// 安装族先落定 inventory（控制面 10.99.0.11 同时进 etcd/kube_master/kube_node）
	got, err := DeliverySubmit(map[string]any{
		"source_task_no": src, "source_step_index": int64(2),
		"cluster_ref": cluster, "step_code": "01", "nodes": facadeInstallNodes(),
	}, facadeUser())
	if err != nil {
		t.Fatal(err)
	}
	waitTaskStatus(t, Str(got["task_no"]), "COMPLETED")

	// 新地址不在册 ⇒ 受理（本域不预写 hosts：sed 进工作组是命令面自己的事）
	// 上游类别名走 step_code 列（R-13 私有映射：CLC_TASK_KIND 词汇由被调用方解析）
	got2, err := DeliverySubmit(map[string]any{
		"source_task_no": src, "source_step_index": int64(3),
		"cluster_ref": cluster, "step_code": "ADD_NODE", "nodes": []any{"10.99.0.99"},
	}, facadeUser())
	if err != nil {
		t.Fatalf("不在册地址的加节点应受理: %v", err)
	}
	waitTaskStatus(t, Str(got2["task_no"]), "COMPLETED")
	if _, err := os.Stat(filepath.Join(config.Config.Delivery.WorkDir, "clusters", cluster, "hosts")); err != nil {
		t.Fatal(err)
	}

	_, err = DeliverySubmit(map[string]any{
		"source_task_no": src, "source_step_index": int64(4),
		"cluster_ref": cluster, "action_code": "ezctl.add-node", "nodes": []any{"10.99.0.11"},
	}, facadeUser())
	be = assertBizCode(t, err, errcode.ErrForbidden)
	if !strings.Contains(be.Msg, "已在") || !strings.Contains(be.Msg, "kube_node") {
		t.Errorf("在册地址的拒绝理由不符: %s", be.Msg)
	}

	_, err = DeliverySubmit(map[string]any{
		"source_task_no": src, "source_step_index": int64(5),
		"cluster_ref": cluster, "action_code": "ezctl.add-node", "nodes": []any{"node-a.example.com"},
	}, facadeUser())
	be = assertBizCode(t, err, errcode.ErrForbidden)
	if !strings.Contains(be.Msg, "IPv4") {
		t.Errorf("主机名地址的拒绝理由不符: %s", be.Msg)
	}

	if n := countRows(t, `SELECT COUNT(*) FROM exec_task WHERE flag = 1 AND source_task_no = ?`, src); n != 2 {
		t.Errorf("任务行 %d 行，期望 2 行（两次拒绝不留半成品事实）", n)
	}
	if n := len(auditRows(t, ignEvtGovernanceRefuse, src)); n != 2 {
		t.Errorf("受理侧拒绝留痕 %d 条，期望 2 条（REP-13 可数）", n)
	}
	if n := len(auditRows(t, ignEvtToolUnavailable, src)); n != 1 {
		t.Errorf("交付配置缺失的拒绝留痕 %d 条，期望 1 条", n)
	}
}

// TestFacadeToolUnavailable R-15（Z2）：命令面挂载缺失即受理即刻拒绝，不建任务行、
// 不转入其他执行位，留痕事件与治理拒绝分列。
func TestFacadeToolUnavailable(t *testing.T) {
	requireDB(t)
	root := useDeliveryEnv(t, facadeToolBody(`  start) echo ok; exit 0 ;;`))
	config.Config.Delivery.ToolBin = filepath.Join(root, "ezctl-not-mounted")

	src := fmt.Sprintf("CLC-RU-%d", nextSeq())
	trackAudit(t, src)
	_, err := DeliverySubmit(map[string]any{
		"source_task_no": src, "source_step_index": int64(1),
		"cluster_ref": newCluster(t), "action_code": "ezctl.start",
	}, facadeUser())
	be := assertBizCode(t, err, errcode.ErrState)
	if !strings.Contains(be.Msg, "本机执行位不可用") {
		t.Errorf("R-15 中文提示不符: %s", be.Msg)
	}
	if n := countRows(t, `SELECT COUNT(*) FROM exec_task WHERE flag = 1 AND source_task_no = ?`, src); n != 0 {
		t.Errorf("执行位不可用却建了任务行: %d 行（受理即刻拒绝）", n)
	}
	rows := auditRows(t, ignEvtToolUnavailable, src)
	if len(rows) != 1 {
		t.Fatalf("执行位不可用留痕 %d 条，期望 1 条", len(rows))
	}
	if Str(rows[0]["remote_value"]) != "受理即刻拒绝" {
		t.Errorf("留痕结论口径: %q（Z2 与 Z1 分列）", Str(rows[0]["remote_value"]))
	}
	if n := countRows(t, `SELECT COUNT(*) FROM ign_reconcile_log WHERE flag = 1 AND event_type = ? AND object_no LIKE ?`,
		ignEvtGovernanceRefuse, src+"%"); n != 0 {
		t.Error("执行位不可用不得记成治理拒绝（三条语义分列）")
	}
}

// TestFacadeDeliveryConfigPrecondition B-AUTO-15：安装族受理时按登记事实产出现实可用的
// 交付配置（hosts/config.yml + 接管标记）；已对齐可沿用、未对齐且无清单即拒绝、
// 非本域产物拒绝接管且不改动（步骤序号权威在 O-CLC，Q-69；配置事实权威在受理侧）。
func TestFacadeDeliveryConfigPrecondition(t *testing.T) {
	requireDB(t)
	root := useDeliveryEnv(t, facadeToolBody(`  setup) echo "setup $1 $2"; exit 0 ;;`))
	src := fmt.Sprintf("CLC-WD-%d", nextSeq())
	trackAudit(t, src)
	cluster := newCluster(t)
	dir := filepath.Join(root, "clusters", cluster)
	if ignDirExists(dir) {
		t.Fatal("夹具集群目录预先存在，用例无法验证交付配置前置")
	}

	got, err := DeliverySubmit(map[string]any{
		"source_task_no": src, "source_step_index": int64(1),
		"cluster_ref": cluster, "step_code": "01", "nodes": facadeInstallNodes(),
	}, facadeUser())
	if err != nil {
		t.Fatal(err)
	}
	// 受理即刻产出的必须是"命令面直接可用"的两份配置，不是空目录
	hosts, err := os.ReadFile(filepath.Join(dir, "hosts"))
	if err != nil {
		t.Fatalf("安装首步未生成交付配置: %v（B-AUTO-15）", err)
	}
	if strings.Contains(string(hosts), "_cluster_name_") || !strings.Contains(string(hosts), "10.99.0.11") {
		t.Errorf("hosts 未按登记事实生成:\n%s", hosts)
	}
	conf, err := os.ReadFile(filepath.Join(dir, "config.yml"))
	if err != nil || !strings.Contains(string(conf), `CLUSTER_NAME: "`+cluster+`"`) {
		t.Errorf("config.yml 未按登记标识改写: err=%v", err)
	}
	waitTaskStatus(t, Str(got["task_no"]), "COMPLETED")

	// 后续步骤不带清单 ⇒ 沿用已对齐配置（重试/续派场景；配置归属由接管标记判定）
	got2, err := DeliverySubmit(map[string]any{
		"source_task_no": src, "source_step_index": int64(2),
		"cluster_ref": cluster, "step_code": "05",
	}, facadeUser())
	if err != nil {
		t.Fatalf("已对齐集群的后续步骤应受理: %v", err)
	}
	waitTaskStatus(t, Str(got2["task_no"]), "COMPLETED")

	// 配置未对齐又不带清单 ⇒ 受理即刻拒绝（不建任务行、不产出空目录）
	cluster3 := newCluster(t)
	_, err = DeliverySubmit(map[string]any{
		"source_task_no": src, "source_step_index": int64(3),
		"cluster_ref": cluster3, "step_code": "03",
	}, facadeUser())
	be := assertBizCode(t, err, errcode.ErrState)
	if !strings.Contains(be.Msg, "未带节点清单") {
		t.Errorf("拒绝理由未说明缺什么: %s", be.Msg)
	}
	if ignDirExists(filepath.Join(root, "clusters", cluster3)) {
		t.Error("拒绝受理却产出了集群目录（Z1：拒绝不留半成品事实）")
	}
	if n := countRows(t, `SELECT COUNT(*) FROM exec_task WHERE flag = 1 AND source_task_no = ? AND source_step_index = 3`, src); n != 0 {
		t.Errorf("拒绝却建了任务行: %d", n)
	}
	if len(auditRows(t, ignEvtToolUnavailable, src)) != 1 {
		t.Error("交付配置未对齐的拒绝须落受理侧留痕（REP-13）")
	}

	// 非本域派生的配置（人工/其他工具产出）⇒ 拒绝接管，且一字不改
	cluster4 := newCluster(t)
	dir4 := filepath.Join(root, "clusters", cluster4)
	if err := os.MkdirAll(dir4, 0o755); err != nil {
		t.Fatal(err)
	}
	manualHosts := "[etcd]\n1.2.3.4\n"
	if err := os.WriteFile(filepath.Join(dir4, "hosts"), []byte(manualHosts), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir4, "config.yml"), []byte("CLUSTER_NAME: \"manual\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = DeliverySubmit(map[string]any{
		"source_task_no": src, "source_step_index": int64(4),
		"cluster_ref": cluster4, "step_code": "01", "nodes": facadeInstallNodes(),
	}, facadeUser())
	be = assertBizCode(t, err, errcode.ErrState)
	if !strings.Contains(be.Msg, "非本域") {
		t.Errorf("接管判定不符: %s", be.Msg)
	}
	if b, _ := os.ReadFile(filepath.Join(dir4, "hosts")); string(b) != manualHosts {
		t.Error("拒绝接管却改写了人工产出的配置")
	}
}

// ---------- R-19 交付版本引用的可用性门槛 ----------

// insertFacadeVersion 建一条用例专属交付版本（装置共用一库；版本名唯一索引只约束未归档行，
// 故按状态取名即可）。cache_path 守 INV-06：仅就绪 / 预推系列状态非空。
func insertFacadeVersion(t *testing.T, status, orchestrator, cni string) string {
	t.Helper()
	seq := nextSeq()
	versionNo := fmt.Sprintf("VER-TEST-%08d", seq)
	name := fmt.Sprintf("t%d-%s", seq, strings.ToLower(status))
	cache := ""
	switch status {
	case "READY", "PREPUSHING", "PREPUSHED", "PARTIALLY_PREPUSHED", "ARCHIVED":
		cache = "/cache/opic/" + name
	}
	mustSQL(t, `
		INSERT INTO delivery_version
			(version_no, name, tool_version, orchestrator_version, runtime_version, etcd_version,
			 cni_plugin, cni_version, coredns_version, tarball_url, image_manifest, cache_path,
			 version_status, created_by)
		VALUES (?, ?, '3.7.0', ?, '2.2.1', '3.7.1', ?, 'v0.28.9', '1.14.6',
		        'https://repo.invalid/kubeasz.tar.gz', '[]', ?, ?, 1)`,
		versionNo, name, orchestrator, cni, cache, status)
	t.Cleanup(func() { softSQL(`DELETE FROM delivery_version WHERE version_no = ?`, versionNo) })
	return name
}

// TestVersionRefProfileDerivation R-19 正面：就绪版本放行，且版本登记的组件面覆盖参数缺省面。
func TestVersionRefProfileDerivation(t *testing.T) {
	requireDB(t)
	name := insertFacadeVersion(t, "READY", "v1.31.9", "FLANNEL")
	p, err := ignProfileForAction(name)
	if err != nil {
		t.Fatalf("就绪版本应可被交付引用: %v", err)
	}
	if p.K8sVersion != "1.31.9" {
		t.Errorf("编排引擎版本未按版本登记派生: %q", p.K8sVersion)
	}
	if p.NetworkPlugin != "flannel" {
		t.Errorf("CNI 插件未按版本登记派生: %q", p.NetworkPlugin)
	}
}

// TestVersionRefAvailabilityGate R-19 反面：草稿 / 下载中 / 失败 / 已归档的版本都在目录里，
// 但不可被交付引用 —— 组件版本面尚未登记完成就派生参数，等于把空版本号带进装机现场。
func TestVersionRefAvailabilityGate(t *testing.T) {
	requireDB(t)
	for _, c := range []struct{ status, label string }{
		{"DRAFT", "草稿"},
		{"DOWNLOADING", "下载中"},
		{"FAILED", "失败"},
		{"ARCHIVED", "已归档"},
	} {
		name := insertFacadeVersion(t, c.status, "v1.31.9", "FLANNEL")
		_, err := ignProfileForAction(name)
		be := assertBizCode(t, err, errcode.ErrForbidden)
		if !strings.Contains(be.Msg, c.label) || !strings.Contains(be.Msg, "R-19") {
			t.Errorf("%s 版本的拒绝理由不符: %s", c.status, be.Msg)
		}
	}

	_, err := ignProfileForAction("VER-TEST-NOT-REGISTERED")
	be := assertBizCode(t, err, errcode.ErrForbidden)
	if !strings.Contains(be.Msg, "无登记") {
		t.Errorf("未登记版本应走可解析口径（INV-33）: %s", be.Msg)
	}
}

// TestFacadeSubmitRefusesUnavailableVersion 门面端到端：不可用版本 ⇒ 治理拒绝 + 受理侧留痕，
// 且不落半成品任务（与 R-12 / R-13 同族）。
func TestFacadeSubmitRefusesUnavailableVersion(t *testing.T) {
	requireDB(t)
	useDeliveryEnv(t, facadeToolBody(`  setup) echo "setup $1 $2"; exit 0 ;;`))
	src := fmt.Sprintf("CLC-VG-%d", nextSeq())
	trackAudit(t, src)
	cluster := newCluster(t)
	name := insertFacadeVersion(t, "DRAFT", "v1.31.9", "FLANNEL")

	_, err := DeliverySubmit(map[string]any{
		"source_task_no": src, "source_step_index": int64(1),
		"cluster_ref": cluster, "step_code": "01",
		"nodes": facadeInstallNodes(), "version_ref": name,
	}, facadeUser())
	be := assertBizCode(t, err, errcode.ErrForbidden)
	if !strings.Contains(be.Msg, "草稿") {
		t.Errorf("拒绝理由未说明版本状态: %s", be.Msg)
	}
	if n := countRows(t, `SELECT COUNT(*) FROM exec_task WHERE flag = 1 AND source_task_no = ?`, src); n != 0 {
		t.Errorf("治理拒绝却建了任务行: %d（Z1 不建任务）", n)
	}
	if len(auditRows(t, ignEvtGovernanceRefuse, src)) != 1 {
		t.Error("版本不可用的受理拒绝须落治理拒绝留痕（REP-13 c8 可数）")
	}
}

// ---------- B-AUTO-12 判定网关 + R-16 写前脱敏 ----------

// TestFacadeVerdictByPlayRecap R-14：剧本汇总优先于退出码 —— failed>0 即 FAILED，
// 结论依据 PLAY_RECAP，失败原因单列，摘要落「终态回填」注记行（FUNC-35 summary 的唯一来源）。
func TestFacadeVerdictByPlayRecap(t *testing.T) {
	requireDB(t)
	useDeliveryEnv(t, facadeToolBody(
		`  setup) echo "PLAY [kubeasz] *****"; echo "10.0.0.11 : ok=8 changed=2 unreachable=0 failed=1"; exit 0 ;;`))
	src := fmt.Sprintf("CLC-PR-%d", nextSeq())
	got, err := DeliverySubmit(map[string]any{
		"source_task_no": src, "source_step_index": int64(1),
		"cluster_ref": newCluster(t), "step_code": "04", "nodes": facadeInstallNodes(),
	}, facadeUser())
	if err != nil {
		t.Fatal(err)
	}
	row := waitTaskStatus(t, Str(got["task_no"]), "FAILED")
	if Str(row["verdict_source"]) != "PLAY_RECAP" {
		t.Errorf("结论依据 %s，期望 PLAY_RECAP", Str(row["verdict_source"]))
	}
	if Str(row["fail_reason"]) != "EXECUTOR_ERROR" {
		t.Errorf("失败原因 %s，期望 EXECUTOR_ERROR", Str(row["fail_reason"]))
	}
	// 退出码 0 而汇总有失败：汇总优先，但退出码仍是执行事实（不伪装成 null）
	if toInt(row["exit_code"]) != 0 {
		t.Errorf("退出码 %v，期望如实登记 0", row["exit_code"])
	}
	rec, err := DeliveryReceipt(Str(got["task_no"]), 0)
	if err != nil {
		t.Fatal(err)
	}
	if Str(rec["status"]) != "FAILED" {
		t.Errorf("回执状态 %s（契约词汇由被调用方定义，Q-70）", Str(rec["status"]))
	}
	if !strings.Contains(Str(rec["summary"]), "剧本汇总：1 个执行面主机，1 个失败/不可达") {
		t.Errorf("回执摘要缺汇总口径: %q", Str(rec["summary"]))
	}
}

// TestFacadeLogMaskedBeforeWrite R-16/INV-40：命中掩码规则即改写后才入库，
// masked 标记「日志被动过」而不是静默替换 —— 门面回吐读的是同一份已脱敏数据。
func TestFacadeLogMaskedBeforeWrite(t *testing.T) {
	requireDB(t)
	useDeliveryEnv(t, facadeToolBody(
		`  setup) echo "sync etcd --password Sup3rSecretValue"; echo "root_token=abc123def456"; echo "plain progress"; exit 0 ;;`))
	src := fmt.Sprintf("CLC-MK-%d", nextSeq())
	got, err := DeliverySubmit(map[string]any{
		"source_task_no": src, "source_step_index": int64(1),
		"cluster_ref": newCluster(t), "step_code": "06", "nodes": facadeInstallNodes(),
	}, facadeUser())
	if err != nil {
		t.Fatal(err)
	}
	taskNo := Str(got["task_no"])
	waitTaskStatus(t, taskNo, "COMPLETED")

	rec, err := DeliveryReceipt(taskNo, 0)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := qList(`SELECT log_line, masked FROM ign_task_log WHERE task_id = (SELECT id FROM exec_task WHERE task_no = ?) AND flag = 1 ORDER BY seq`, taskNo)
	if err != nil {
		t.Fatal(err)
	}
	var maskedRows int
	for _, r := range raw {
		line := Str(r["log_line"])
		for _, secret := range []string{"Sup3rSecretValue", "abc123def456"} {
			if strings.Contains(line, secret) {
				t.Fatalf("凭据明文入库: %q（INV-40 不存在第二条未脱敏通道）", line)
			}
		}
		if boolOfAny(r["masked"]) {
			maskedRows++
			if !strings.Contains(line, ignMaskMark) {
				t.Errorf("masked=true 却无掩码标记: %q", line)
			}
		}
	}
	if maskedRows != 2 {
		t.Errorf("掩码行数 %d，期望 2（命令行选项形态 + 赋值形态）", maskedRows)
	}
	if n := len(rec["logs"].([]map[string]any)); n != len(raw) {
		t.Errorf("门面回吐行数 %d ≠ 库内行数 %d", n, len(raw))
	}
	for _, l := range rec["logs"].([]map[string]any) {
		if strings.Contains(Str(l["log_line"]), "Sup3rSecretValue") {
			t.Error("门面回吐存在未脱敏通道（INV-40）")
		}
	}
}

// ---------- FUNC-35 回执与增量日志 ----------

// TestFacadeReceiptIncrement REF-14：单次回吐行数上限 + 续传游标 + 游标右界；
// 引用未命中给明确"任务不存在"而非空回执（上游据此判登记值与真实位置不符）。
func TestFacadeReceiptIncrement(t *testing.T) {
	requireDB(t)
	useDeliveryEnv(t, facadeToolBody(
		`  setup) i=1; while [ $i -le 10 ]; do echo "progress line $i"; i=$((i+1)); done; exit 0 ;;`))
	prev := Str(mustOne(t, `SELECT config_value FROM ign_platform_config WHERE config_key = ?`, ignCfgReceiptLogBatch)["config_value"])
	mustSQL(t, `UPDATE ign_platform_config SET config_value = '3' WHERE config_key = ?`, ignCfgReceiptLogBatch)
	t.Cleanup(func() {
		mustSQL(t, `UPDATE ign_platform_config SET config_value = ? WHERE config_key = ?`, prev, ignCfgReceiptLogBatch)
	})

	src := fmt.Sprintf("CLC-RC-%d", nextSeq())
	got, err := DeliverySubmit(map[string]any{
		"source_task_no": src, "source_step_index": int64(1),
		"cluster_ref": newCluster(t), "step_code": "07", "nodes": facadeInstallNodes(),
	}, facadeUser())
	if err != nil {
		t.Fatal(err)
	}
	taskNo := Str(got["task_no"])
	waitTaskStatus(t, taskNo, "COMPLETED")

	rec, err := DeliveryReceipt(taskNo, 0)
	if err != nil {
		t.Fatal(err)
	}
	logs := rec["logs"].([]map[string]any)
	if len(logs) != 3 {
		t.Fatalf("首批回吐 %d 行，期望 3 行（REF-14 上限）", len(logs))
	}
	if trunc, _ := rec["truncated"].(bool); !trunc {
		t.Error("超上限必须回吐 truncated=true，让上游知道还有后续")
	}
	if ignToInt(rec["next_seq"]) != 3 {
		t.Errorf("续传游标 next_seq=%v，期望 3", rec["next_seq"])
	}
	cursor := ignToInt(rec["cursor"]) // 受理注记 + 交付配置注记 + 启动注记 + 10 行工具 + 终态回填
	if cursor != 14 {
		t.Errorf("游标右界 %v，期望 14（INV-38 完整性可判读）", cursor)
	}

	seen := map[int64]bool{}
	since := ignToInt(rec["next_seq"])
	total := len(logs)
	for {
		r2, err := DeliveryReceipt(taskNo, since)
		if err != nil {
			t.Fatal(err)
		}
		l2 := r2["logs"].([]map[string]any)
		if len(l2) == 0 {
			break
		}
		for _, l := range l2 {
			seen[ignToInt(l["seq"])] = true
		}
		total += len(l2)
		since = ignToInt(r2["next_seq"])
		if ignToInt(r2["next_seq"]) >= ignToInt(r2["cursor"]) {
			break
		}
	}
	if total != int(cursor) || int64(len(seen)) != cursor-3 {
		t.Errorf("增量续传覆盖不完整: 累计 %d 行、游标 %v（空洞须可判读）", total, cursor)
	}
	if _, err := DeliveryReceipt("TSK-NOT-EXIST", 0); err == nil {
		t.Error("引用未命中不得返回空回执")
	} else if assertBizCode(t, err, errcode.ErrNotFound) == nil {
		t.Error("引用未命中应为 404 语义")
	}
}

// ---------- FUNC-36 / FUNC-24 撤销链与 B-AUTO-13/14 ----------

// TestFacadeCancelChains 撤销的三种落点：无在途协程 ⇒ 就地收口 CANCELLED 且无退出码；
// 有在途协程 ⇒ 只发终止信号、终态由执行事实决定；终态/在途撤销幂等。
func TestFacadeCancelChains(t *testing.T) {
	requireDB(t)
	useDeliveryEnv(t, facadeToolBody(`  start) echo working; sleep 5; echo done; exit 0 ;;`))
	exec := localExec(t)
	src := fmt.Sprintf("CLC-CX-%d", nextSeq())

	// ① 就地收口（痕迹不会产生）
	clusterA := newCluster(t)
	synthetic := insertRunningTask(t, toInt(exec["id"]), "ezctl.start", clusterA, "", src, 1)
	out, err := DeliveryCancel(synthetic, facadeUser())
	if err != nil {
		t.Fatal(err)
	}
	if Str(out["status"]) != "CANCELLED" {
		t.Errorf("就地收口状态 %s，期望 CANCELLED", Str(out["status"]))
	}
	if !strings.Contains(Str(out["note"]), "就地收口") {
		t.Errorf("撤销说明不符: %q", Str(out["note"]))
	}
	row := mustOne(t, `SELECT * FROM exec_task WHERE task_no = ?`, synthetic)
	if row["exit_code"] != nil {
		t.Errorf("未执行的撤销却登记退出码 %v", row["exit_code"])
	}
	if Str(row["fail_reason"]) != "ACTION_DENIED" {
		t.Errorf("撤销失败原因 %s，期望 ACTION_DENIED（INV-29 退出码或失败原因之一）", Str(row["fail_reason"]))
	}
	// 幂等：终态再撤销不报错、不改写（INV-32）
	out2, err := DeliveryCancel(synthetic, facadeUser())
	if err != nil {
		t.Fatalf("终态撤销应幂等: %v", err)
	}
	if Str(out2["status"]) != "CANCELLED" || !strings.Contains(Str(out2["note"]), "终态不可改写") {
		t.Errorf("终态幂等回吐不符: %s / %q", Str(out2["status"]), Str(out2["note"]))
	}

	// ② 真实在途：信号送达后由执行位收口（R-09 终态以执行事实为准）
	clusterB := newCluster(t)
	got, err := DeliverySubmit(map[string]any{
		"source_task_no": src, "source_step_index": int64(2),
		"cluster_ref": clusterB, "action_code": "ezctl.start",
	}, facadeUser())
	if err != nil {
		t.Fatal(err)
	}
	taskNo := Str(got["task_no"])
	bound := false
	for i := 0; i < 200 && !bound; i++ { // 等在途协程绑定（B-AUTO-11 异步派发），否则验不到"信号送达"分支
		bound = ignProcLive(taskNo)
		if !bound {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !bound {
		t.Fatal("在途协程未绑定，取消链无法验证")
	}
	out3, err := DeliveryCancel(taskNo, facadeUser())
	if err != nil {
		t.Fatal(err)
	}
	if Str(out3["status"]) != "CANCELLING" {
		t.Errorf("在途撤销应留在取消中: %s", Str(out3["status"]))
	}
	if !strings.Contains(Str(out3["note"]), "执行事实决定") {
		t.Errorf("在途撤销说明不符: %q", Str(out3["note"]))
	}
	done := waitTaskStatus(t, taskNo, "CANCELLED")
	if Str(done["verdict_source"]) != "EXIT_CODE" {
		t.Errorf("取消的结论依据 %s（进程事实）", Str(done["verdict_source"]))
	}
	if toInt(mustOne(t, `SELECT current_tasks_count AS c FROM executor WHERE id = ?`, toInt(exec["id"]))["c"]) != 0 {
		t.Error("取消收口后 R-05 计数未递减")
	}
}

// TestCancelChainRemoteNoFakeAnswer Q-68/R-09：REMOTE 通道本域没有进程可终止，
// 撤销只登记「取消中」，绝不代答 CANCELLED。
func TestCancelChainRemoteNoFakeAnswer(t *testing.T) {
	requireDB(t)
	useDeliveryEnv(t, facadeToolBody(`  start) echo ok; exit 0 ;;`))
	cluster := newCluster(t)

	execNo := fmt.Sprintf("EXE-TEST-%08d", nextSeq())
	host := fmt.Sprintf("10.77.%d.9", nextSeq()%250)
	mustSQL(t, `
		INSERT INTO executor
			(executor_no, name, display_name, executor_type, host, port, capability_flags,
			 status, concurrent_tasks_max, created_by)
		VALUES (?, ?, 'REMOTE 夹具执行机', 'REMOTE', ?, 9710, 'DEPLOY', 'READY', 4, 1)`,
		execNo, "remote-fixture-"+execNo, host)
	remote := mustOne(t, `SELECT id FROM executor WHERE executor_no = ?`, execNo)
	taskNo := insertRunningTask(t, toInt(remote["id"]), "ezctl.start", cluster, "", "", 0)

	out, err := CancelTask(taskNo, facadeUser()) // FUNC-24 与 FUNC-36 同一条链
	if err != nil {
		t.Fatal(err)
	}
	if Str(out["status"]) != "CANCELLING" {
		t.Fatalf("REMOTE 撤销被代答成 %s（终态须由代理回执收口，R-09）", Str(out["status"]))
	}
	if !strings.Contains(Str(out["note"]), "REMOTE") {
		t.Errorf("撤销说明应交代代理对端: %q", Str(out["note"]))
	}
	if st := Str(mustOne(t, `SELECT task_status FROM exec_task WHERE task_no = ?`, taskNo)["task_status"]); st != "CANCELLING" {
		t.Errorf("库内状态 %s，期望停在 CANCELLING", st)
	}
	// 代理回执端点仍可收口（R-09 事实回填）
	if _, err := CompleteTask(taskNo, map[string]any{"task_status": "CANCELLED", "exit_code": 143, "summary": "代理确认已终止"}, facadeUser()); err != nil {
		t.Fatal(err)
	}
	if st := Str(mustOne(t, `SELECT task_status FROM exec_task WHERE task_no = ?`, taskNo)["task_status"]); st != "CANCELLED" {
		t.Errorf("回执后状态 %s，期望 CANCELLED", st)
	}
}

// TestRecoverLocalExecLost B-AUTO-14/INV-37：执行痕迹不可存活 ⇒ 独立终态 LOST
// （无退出码 + VERDICT_UNRESOLVED + TRACE_LOST），并落 TASK_LOST 失联留痕，不静默重跑。
func TestRecoverLocalExecLost(t *testing.T) {
	requireDB(t)
	useDeliveryEnv(t, facadeToolBody(`  start) echo ok; exit 0 ;;`))
	exec := localExec(t)

	clusterA, clusterB := newCluster(t), newCluster(t)
	noA := insertRunningTask(t, toInt(exec["id"]), "ezctl.start", clusterA, "", "CLC-LOST-A", 1)
	noB := insertRunningTask(t, toInt(exec["id"]), "ezctl.stop", clusterB, "", "CLC-LOST-B", 1)
	mustSQL(t, `UPDATE exec_task SET task_status = 'CANCELLING' WHERE task_no = ?`, noB)

	if err := IgnRecoverLocalExec(); err != nil {
		t.Fatal(err)
	}
	for _, no := range []string{noA, noB} {
		row := mustOne(t, `SELECT * FROM exec_task WHERE task_no = ?`, no)
		if Str(row["task_status"]) != "LOST" {
			t.Errorf("%s 状态 %s，期望 LOST（第四终态独立，Q-72）", no, Str(row["task_status"]))
		}
		if row["exit_code"] != nil {
			t.Errorf("%s LOST 却带退出码 %v（INV-37：不可存活的痕迹不能伪装成退出码非零）", no, row["exit_code"])
		}
		if Str(row["verdict_source"]) != "TRACE_LOST" || Str(row["fail_reason"]) != "VERDICT_UNRESOLVED" {
			t.Errorf("%s 结论口径 %s/%s，期望 TRACE_LOST/VERDICT_UNRESOLVED", no, Str(row["verdict_source"]), Str(row["fail_reason"]))
		}
		if Str(row["ended_at"]) == "" {
			t.Errorf("%s LOST 未落结束时间（INV-29 终态必有）", no)
		}
		if n := countRows(t, `SELECT COUNT(*) FROM ign_reconcile_log WHERE flag = 1 AND event_type = 'TASK_LOST' AND object_no = ?`, no); n != 1 {
			t.Errorf("%s 失联留痕 %d 条（REP-12 不静默）", no, n)
		}
		if n := countRows(t, `SELECT COUNT(*) FROM ign_task_log WHERE flag = 1 AND direction = 'PLATFORM' AND log_line LIKE ? AND task_id = (SELECT id FROM exec_task WHERE task_no = ?)`,
			"终态回填：LOST%", no); n != 1 {
			t.Errorf("%s 缺 LOST 终态回填注记", no)
		}
	}
	// 回执路径不可伪造 LOST（INV-37 是本域失联判定）
	noC := insertRunningTask(t, toInt(exec["id"]), "ezctl.start", newCluster(t), "", "CLC-LOST-C", 1)
	_, err := CompleteTask(noC, map[string]any{"task_status": "LOST"}, facadeUser())
	assertBizCode(t, err, errcode.ErrParam)
}

// ---------- FUNC-23 人工提交路径（与门面路径分列） ----------

// TestManualSubmitValidations INV-31/INV-34/INV-36 与 7.3 三层可达面：
// 人工提交必带令牌、必不带来源两列，可提交集合只有交付动作层。
func TestManualSubmitValidations(t *testing.T) {
	requireDB(t)
	useDeliveryEnv(t, facadeToolBody(`  start) echo ok; exit 0 ;;`))
	exec := localExec(t)
	src := fmt.Sprintf("MANUAL-%d", nextSeq())
	trackAudit(t, "action=")
	cluster := newCluster(t)

	base := func(extra map[string]any) map[string]any {
		m := map[string]any{
			"executor_id": toInt(exec["id"]), "cluster_ref": cluster,
			"request_token": fmt.Sprintf("tk-%d", nextSeq()), "action": "ezctl.start",
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}

	_, err := SubmitTask(base(map[string]any{"request_token": ""}), facadeUser())
	be := assertBizCode(t, err, errcode.ErrParam)
	if !strings.Contains(be.Msg, "请求令牌必填") {
		t.Errorf("INV-31 提示不符: %s", be.Msg)
	}
	_, err = SubmitTask(base(map[string]any{"source_task_no": src}), facadeUser())
	be = assertBizCode(t, err, errcode.ErrParam)
	if !strings.Contains(be.Msg, "来源与去处不可混淆") {
		t.Errorf("INV-34 提示不符: %s", be.Msg)
	}
	_, err = SubmitTask(base(map[string]any{"source_step_index": int64(2)}), facadeUser())
	assertBizCode(t, err, errcode.ErrParam)

	_, err = SubmitTask(base(map[string]any{"action": "ezctl.new"}), facadeUser())
	be = assertBizCode(t, err, errcode.ErrForbidden)
	if !strings.Contains(be.Msg, "内部前置") {
		t.Errorf("7.3 可达面提示不符: %s", be.Msg)
	}
	_, err = SubmitTask(base(map[string]any{"action": "scripts.tls_renew"}), facadeUser())
	assertBizCode(t, err, errcode.ErrForbidden)
	_, err = SubmitTask(base(map[string]any{"action": "drop table"}), facadeUser())
	assertBizCode(t, err, errcode.ErrForbidden)
	if n := countRows(t, `SELECT COUNT(*) FROM ign_reconcile_log WHERE flag = 1 AND event_type = ? AND object_no LIKE ?`,
		ignEvtGovernanceRefuse, "action=%"); n < 3 {
		t.Errorf("人工路径治理拒绝留痕 %d 条，期望 ≥3（R-12 拒绝记录可查）", n)
	}

	_, err = SubmitTask(base(map[string]any{"action": "ezctl.setup"}), facadeUser())
	assertBizCode(t, err, errcode.ErrRule)
	_, err = SubmitTask(base(map[string]any{"action": "ezctl.setup", "step_code": "09"}), facadeUser())
	assertBizCode(t, err, errcode.ErrRule)
	_, err = SubmitTask(base(map[string]any{"step_code": "01"}), facadeUser())
	assertBizCode(t, err, errcode.ErrRule)
	_, err = SubmitTask(base(map[string]any{"args": "--yes; rm -rf /"}), facadeUser())
	assertBizCode(t, err, errcode.ErrForbidden)
	_, err = SubmitTask(base(map[string]any{"cluster_ref": "bad cluster;"}), facadeUser())
	assertBizCode(t, err, errcode.ErrParam)

	// 合法人工提交：来源两列为空、走同一本机执行位收口
	got, err := SubmitTask(base(map[string]any{"action": "ezctl.start"}), facadeUser())
	if err != nil {
		t.Fatal(err)
	}
	if Str(got["source_task_no"]) != "" {
		t.Error("人工提交不得携带来源两列（INV-34）")
	}
	if got["request_token_digest"] == nil || Str(got["request_token_digest"]) == "" {
		t.Error("人工提交必须落令牌摘要（INV-31）")
	}
	row := waitTaskStatus(t, Str(got["task_no"]), "COMPLETED")
	if n := countRows(t, `SELECT COUNT(*) FROM ign_task_log WHERE flag = 1 AND task_id = ? AND direction = 'PLATFORM' AND log_line LIKE ?`,
		toInt(row["id"]), "人工提交：%"); n != 1 {
		t.Error("人工提交缺受理注记行（与门面受理注记同源口径）")
	}
}

// ---------- FUNC-27 / FUNC-37 / FUNC-29 视图 ----------

// TestTaskLogsGapAndPagination REP-11/FUNC-27：按游标升序增量续传，空洞显式标注「少了几行」，
// 掩码行数可审计（R-16 的可见性）。
func TestTaskLogsGapAndPagination(t *testing.T) {
	requireDB(t)
	useDeliveryEnv(t, facadeToolBody(`  start) echo ok; exit 0 ;;`))
	exec := localExec(t)
	cluster := newCluster(t)
	taskNo := insertRunningTask(t, toInt(exec["id"]), "ezctl.start", cluster, "", "", 0)
	id := toInt(mustOne(t, `SELECT id FROM exec_task WHERE task_no = ?`, taskNo)["id"])
	// 1/2/4 三行（3 号位入库失败不占号 ⇒ 游标右界 4、行数 3、空洞 1）
	for _, spec := range []struct {
		seq  int64
		line string
		hit  bool
	}{{1, "start ok", false}, {2, "token=[已掩码]", true}, {4, "recap done", false}} {
		if err := tx(func(db *gorm.DB) error {
			return txInsertTaskLog(db, id, spec.seq, "TOOL", spec.line, spec.hit)
		}); err != nil {
			t.Fatal(err)
		}
	}

	out, err := TaskLogs(taskNo, 0, 1, 500)
	if err != nil {
		t.Fatal(err)
	}
	if toInt(out["total"]) != 3 || ignToInt(out["log_cursor"]) != 4 {
		t.Errorf("行数/游标 %v/%v，期望 3/4", out["total"], out["log_cursor"])
	}
	if toInt(out["gap_lines"]) != 1 {
		t.Errorf("空洞 %v，期望 1（INV-38 可判读）", out["gap_lines"])
	}
	if !strings.Contains(Str(out["gap_note"]), "缺失 1 行") {
		t.Errorf("空洞注记不符: %q", Str(out["gap_note"]))
	}
	if toInt(out["masked_count"]) != 1 {
		t.Errorf("本批掩码行数 %v，期望 1（R-16 审计可见）", out["masked_count"])
	}
	list := out["list"].([]map[string]any)
	if ignToInt(list[0]["seq"]) > ignToInt(list[len(list)-1]["seq"]) {
		t.Error("REP-11 未按游标升序回吐")
	}
	// size 越界回落到模型口径（500），不被通用分页上限改写
	out2, err := TaskLogs(taskNo, 2, 1, 99999)
	if err != nil {
		t.Fatal(err)
	}
	if toInt(out2["size"]) != 500 {
		t.Errorf("size=%v，期望回落 500（REP-11 defaultPageSize）", out2["size"])
	}
	if toInt(out2["total"]) != 1 { // since_seq=2 ⇒ seq 4 一行
		t.Errorf("增量续传行数 %v，期望 1", out2["total"])
	}
	if _, err := TaskLogs("TSK-NOPE", 0, 1, 10); err == nil {
		t.Error("任务不存在应报错")
	} else {
		assertBizCode(t, err, errcode.ErrNotFound)
	}
	// 详情视图（FUNC-26/REP-10）带来源说明与游标
	detail, err := GetTask(taskNo)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(Str(detail["source_note"]), "人工提交") {
		t.Errorf("来源说明不符: %q（INV-34 分列）", Str(detail["source_note"]))
	}
	if ignToInt(detail["log_cursor"]) != 4 {
		t.Errorf("详情游标 %v，期望 4", detail["log_cursor"])
	}
}

// TestFacadeLedgerAndStats REP-14 门面受理台账（固定过滤来源列非空 + 日志行数/游标）
// 与 REP-13 九块统计（计数类/观测类分列、LOST 独立、拒绝两列）。
func TestFacadeLedgerAndStats(t *testing.T) {
	requireDB(t)
	useDeliveryEnv(t, facadeToolBody(`  setup) echo "PLAY RECAP *****"; echo "10.0.0.1 : ok=1 changed=0 unreachable=0 failed=0"; exit 0 ;;`))
	src := fmt.Sprintf("CLC-LG-%d", nextSeq())
	trackAudit(t, src)
	exec := localExec(t)
	cluster := newCluster(t)
	manual := insertRunningTask(t, toInt(exec["id"]), "ezctl.start", newCluster(t), "", "", 0)

	got, err := DeliverySubmit(map[string]any{
		"source_task_no": src, "source_step_index": int64(1),
		"cluster_ref": cluster, "step_code": "02", "nodes": facadeInstallNodes(),
	}, facadeUser())
	if err != nil {
		t.Fatal(err)
	}
	taskNo := Str(got["task_no"])
	waitTaskStatus(t, taskNo, "COMPLETED")

	page, err := ListFacadeTasks(1, 20, map[string]any{"source_task_no": src})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.List) != 1 {
		t.Fatalf("门面台账 %d 行，期望 1 行（REP-14 固定过滤来源列非空）", page.Total)
	}
	r := page.List[0]
	if Str(r["task_no"]) != taskNo || Str(r["verdict_source"]) != "PLAY_RECAP" {
		t.Errorf("台账行不符: %s / %s", Str(r["task_no"]), Str(r["verdict_source"]))
	}
	if toInt(r["log_line_count"]) == 0 || toInt(r["log_cursor"]) == 0 {
		t.Errorf("台账缺日志行数/游标列: %v / %v", r["log_line_count"], r["log_cursor"])
	}
	all, err := ListFacadeTasks(1, 500, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range all.List {
		if Str(row["source_task_no"]) == "" {
			t.Fatal("人工提交混进门面台账（INV-34 分列口径失效）")
		}
	}
	if n := countRows(t, `SELECT COUNT(*) FROM exec_task WHERE task_no = ?`, manual); n != 1 {
		t.Fatal("夹具任务丢失")
	}

	stats, err := IgnStats()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"version_status_dist", "prepush_status_dist", "executor_status_dist", // c1~c3
		"health_snapshot", "task_completed_7d", "task_failed_7d", "task_cancelled_7d", "task_lost", // c4~c5
		"facade_accept_count", "manual_submit_count", "verdict_source_dist", // c6
		"local_exec_available", "local_exec_observation", // c7
		"refuse_governance_count", "refuse_tool_unavailable_count", "correct_count", "reconcile_count", // c8~c9
	} {
		if _, ok := stats[key]; !ok {
			t.Errorf("REP-13 缺块 %s（九块 c1~c9）", key)
		}
	}
	if toInt(stats["facade_accept_count"]) < 1 || toInt(stats["manual_submit_count"]) < 1 {
		t.Errorf("受理量与人工量未分列: %v / %v（INV-34）", stats["facade_accept_count"], stats["manual_submit_count"])
	}
	lostKey := stats["task_lost_7d"]
	if lostKey == nil {
		t.Error("LOST 未独立成位（不并入失败，B-CONF-13）")
	}
	obs, _ := stats["local_exec_observation"].(map[string]any)
	if obs == nil || !boolOfAny(obs["available"]) {
		t.Errorf("执行位观测异常: %v", obs)
	}
	if Str(obs["tool_bin"]) != strings.TrimSpace(config.Config.Delivery.ToolBin) {
		t.Errorf("观测位未呈现部署配置值: %q（1.6.1 只读呈现）", Str(obs["tool_bin"]))
	}
	if Str(obs["scanned_at"]) == "" {
		t.Error("观测类缺扫描时间（REP-13 口径 7）")
	}
	hs, _ := stats["health_snapshot"].(map[string]any)
	if hs == nil || Str(hs["note"]) == "" {
		t.Error("健康快照缺观测类口径说明")
	}
	params, _ := stats["params"].(map[string]any)
	if params != nil {
		t.Error("统计总览（FUNC-29 / REP-13）回吐运行参数集合 —— 参数面只由 FUNC-30 的 GET /api/v1/ign/params 提供，" +
			"随统计一并吐出等于绕过 FUNC-30 的读门禁（REP-13 九块 c1~c9 里没有参数块）")
	}
	rows, err := GetIgnParams()
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]string{}
	for _, r := range rows {
		byKey[Str(r["config_key"])] = Str(r["config_value"])
	}
	for _, k := range []string{ignCfgReceiptLogBatch, ignCfgKillGraceSeconds, ignCfgLogRetentionDays} {
		if byKey[k] == "" {
			t.Errorf("REF-14~16 运行参数未入库（%s）", k)
		}
	}
}
