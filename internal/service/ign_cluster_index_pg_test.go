// ign_cluster_index_pg_test.go —— FUNC-52 本域交付集群索引（REP-19，v1.5）的服务层语义用例。
//
// 覆盖派生索引的三条立身性质：并集（跨两张既有表去重）、即时现算（零缓存、零写腿）、
// 不抄上游（行里只有本域标识与计数）。摘要列的取材一律走真实写腿（CompleteTask /
// Save·RollbackIgnClusterConfigOverride / IgnRecoverLocalExec），不靠夹具伪造结论。
package service

import (
	"fmt"
	"strings"
	"testing"
)

// newIndexCluster 索引用例专属标识：定宽后缀保证关键字包含匹配不跨行命中。
func newIndexCluster(t *testing.T, tag string) string {
	t.Helper()
	ref := fmt.Sprintf("ix-%s-%06d", tag, nextSeq())
	trackCluster(t, ref)
	t.Cleanup(func() {
		softSQL(`DELETE FROM ign_cluster_config_override WHERE cluster_ref = ?`, ref)
	})
	return ref
}

func indexList(out map[string]any) []map[string]any { return out["list"].([]map[string]any) }

func indexOfCluster(out map[string]any, clusterRef string) int {
	for i, r := range indexList(out) {
		if Str(r["cluster_ref"]) == clusterRef {
			return i
		}
	}
	return -1
}

func listHasCluster(out map[string]any, clusterRef string) bool {
	return indexOfCluster(out, clusterRef) >= 0
}

func indexRow(t *testing.T, out map[string]any, clusterRef string) map[string]any {
	t.Helper()
	i := indexOfCluster(out, clusterRef)
	if i < 0 {
		t.Fatalf("索引清单缺少集群 %s（并集口径：本域服务过就必须出行）", clusterRef)
	}
	return indexList(out)[i]
}

func mustSaveOverride(t *testing.T, clusterRef, fieldKey, value string) {
	t.Helper()
	_, err := SaveIgnClusterConfigOverride(map[string]any{
		"cluster_ref": clusterRef, "file_face": "HOSTS", "field_key": fieldKey,
		"value": value, "reason": "索引用例登记覆写",
	}, faceTestUser)
	mustNoErr(t, err)
}

func mustRollbackOverride(t *testing.T, clusterRef, fieldKey string) {
	t.Helper()
	_, err := RollbackIgnClusterConfigOverride(map[string]any{
		"cluster_ref": clusterRef, "file_face": "HOSTS", "field_key": fieldKey,
	}, faceTestUser)
	mustNoErr(t, err)
}

func completeIndexTask(taskNo, status string, exitCode int64) error {
	_, err := CompleteTask(taskNo, map[string]any{
		"task_status": status, "exit_code": exitCode, "summary": "索引用例收口",
	}, facadeUser())
	return err
}

// ---------- 并集键集 + 四列摘要 ----------

func TestClusterIndexUnionAndCounts(t *testing.T) {
	requireDB(t)
	useDeliveryEnv(t, facadeToolBody(`  start) echo ok; exit 0 ;;`))
	exec := localExec(t)

	onlyTask := newIndexCluster(t, "task")
	both := newIndexCluster(t, "both")
	onlyOverride := newIndexCluster(t, "ovr")

	noA := insertRunningTask(t, toInt(exec["id"]), "ezctl.start", onlyTask, "", "", 0)
	mustNoErr(t, completeIndexTask(noA, "COMPLETED", 0))
	// 两侧同名：两条任务 + 两条覆写（其中一条已回落），清单上只出一行
	noB1 := insertRunningTask(t, toInt(exec["id"]), "ezctl.setup", both, "01", "", 0)
	mustNoErr(t, completeIndexTask(noB1, "FAILED", 1))
	noB2 := insertRunningTask(t, toInt(exec["id"]), "ezctl.setup", both, "02", "", 0)
	mustNoErr(t, completeIndexTask(noB2, "COMPLETED", 0))
	mustSaveOverride(t, onlyOverride, ignCfgDeployServiceCIDR, "10.99.0.0/16")
	mustSaveOverride(t, both, ignCfgDeployServiceCIDR, "10.98.0.0/16")
	mustSaveOverride(t, both, ignCfgDeployPodCIDR, "10.97.0.0/16")
	mustRollbackOverride(t, both, ignCfgDeployPodCIDR)

	out, err := ListDeliveryClusterIndex(1, 200, map[string]any{})
	mustNoErr(t, err)
	if Str(out["stats_at"]) == "" {
		t.Fatalf("统计时点缺失（9.10.2「统计时点标注」必现）")
	}
	if !strings.Contains(Str(out["caliber_note"]), "摘要截至") {
		t.Errorf("口径标注应明示派生口径: %q", Str(out["caliber_note"]))
	}

	rowA := indexRow(t, out, onlyTask)
	if toInt(rowA["task_count"]) != 1 {
		t.Errorf("任务侧计数 %v，期望 1", rowA["task_count"])
	}
	if toInt(rowA["effective_override_count"]) != 0 {
		t.Errorf("无覆写集群的生效覆写列应为 0 而非留空（口径 11）: %v", rowA["effective_override_count"])
	}
	if toInt(rowA["running_task_count"]) != 0 {
		t.Errorf("在跑数 %v，期望 0", rowA["running_task_count"])
	}
	if Str(rowA["last_task_no"]) != noA {
		t.Errorf("最近一次交付 %v，期望 %s", rowA["last_task_no"], noA)
	}
	if Str(rowA["last_ended_at"]) == "" {
		t.Errorf("终态行结束时间为空（CompleteTask 真实腿未落 ended_at？）")
	}
	if Str(rowA["last_verdict_source"]) != "AGENT_RECEIPT" {
		t.Errorf("结论依据 %v，期望 AGENT_RECEIPT（REP-19 c3 与任务台账同源）", rowA["last_verdict_source"])
	}

	rowB := indexRow(t, out, both)
	if toInt(rowB["task_count"]) != 2 {
		t.Errorf("两侧同名的任务计数 %v，期望 2（口径 12：终态记录默认可见）", rowB["task_count"])
	}
	if toInt(rowB["effective_override_count"]) != 1 || toInt(rowB["rolled_back_override_count"]) != 1 {
		t.Errorf("生效 / 已回落应分列: %v", rowB)
	}
	if Str(rowB["last_task_no"]) != noB2 || Str(rowB["last_task_status"]) != "COMPLETED" {
		t.Errorf("最近一次交付应取最新收口的那行: %v", rowB)
	}

	rowC := indexRow(t, out, onlyOverride)
	if toInt(rowC["task_count"]) != 0 || Str(rowC["last_task_no"]) != "" ||
		toInt(rowC["running_task_count"]) != 0 {
		t.Errorf("覆写侧独有的集群摘要列应为空: %v", rowC)
	}
	if toInt(rowC["effective_override_count"]) != 1 {
		t.Errorf("只登记覆写未派发也须出行: %v", rowC)
	}
	// 9.10.6：无交付记录的行垫在已结束的行之后（单一排序，不做多列排序）
	if indexOfCluster(out, onlyOverride) < indexOfCluster(out, both) {
		t.Errorf("无结束时间的行排在了有结束时间的行之前")
	}
	// 台内徽章条（9.11.2 el01/el02 同样绑 REP-19）：按标识精确取一行，不走包含匹配
	one, err := ListDeliveryClusterIndex(1, 10, map[string]any{"cluster_ref": both})
	mustNoErr(t, err)
	if len(indexList(one)) != 1 || Str(indexList(one)[0]["cluster_ref"]) != both {
		t.Errorf("标识精确取数应只回一行: %v", indexList(one))
	}
}

// ---------- LOST / 状态修正派生的未决偏差 ----------

func TestClusterIndexLostDeviation(t *testing.T) {
	requireDB(t)
	useDeliveryEnv(t, facadeToolBody(`  start) echo ok; exit 0 ;;`))
	exec := localExec(t)

	lost := newIndexCluster(t, "lost")
	never := newIndexCluster(t, "never")
	// 失联留痕按任务号登记，须在 exec_task 之前清（t.Cleanup 逆序 ⇒ 这里后注册的先跑）
	t.Cleanup(func() {
		softSQL(`DELETE FROM ign_reconcile_log WHERE object_no IN
			(SELECT task_no FROM exec_task WHERE cluster_ref = ?)`, lost)
	})

	// 同集群先有一笔 FAILED（交付记录，但不是偏差），再有一笔在途任务被失联判定收口成 LOST
	noF := insertRunningTask(t, toInt(exec["id"]), "ezctl.start", lost, "", "", 0)
	mustNoErr(t, completeIndexTask(noF, "FAILED", 1))
	insertRunningTask(t, toInt(exec["id"]), "ezctl.stop", lost, "", "", 0)
	if err := IgnRecoverLocalExec(); err != nil {
		t.Fatal(err)
	}

	out, err := ListDeliveryClusterIndex(1, 200, map[string]any{})
	mustNoErr(t, err)
	row := indexRow(t, out, lost)
	if toInt(row["pending_deviation_count"]) != 1 {
		t.Errorf("未决偏差 %v，期望 1（FAILED 不算偏差）", row["pending_deviation_count"])
	}
	if Str(row["last_task_status"]) != "LOST" || Str(row["last_verdict_source"]) != "TRACE_LOST" {
		t.Errorf("最近一次交付 %v/%v，期望 LOST/TRACE_LOST（口径 7：LOST 单列，不折成 FAILED）",
			Str(row["last_task_status"]), Str(row["last_verdict_source"]))
	}
	if toInt(row["task_count"]) != 2 {
		t.Errorf("交付记录数 %v，期望 2", row["task_count"])
	}
	if listHasCluster(out, never) {
		t.Errorf("本域从未服务过的集群出行（并集口径：只列「本域服务过的」）")
	}

	filtered, err := ListDeliveryClusterIndex(1, 200, map[string]any{"pending": "DEVIATION"})
	mustNoErr(t, err)
	if !listHasCluster(filtered, lost) {
		t.Errorf("偏差筛项未含 LOST 集群")
	}
	for _, r := range indexList(filtered) {
		if toInt(r["pending_deviation_count"]) <= 0 {
			t.Errorf("偏差筛项把无偏差的集群带出: %v", r)
		}
	}

	// R-09 状态修正留痕同样算偏差。注意：本域 reconciled_note 尚无生产写入腿（DEF-12），
	// 这里用夹具置位只为验证查询谓词覆盖该口径，**不构成该列有写入腿的证据**。
	mustSQL(t, `UPDATE exec_task SET reconciled_note = ? WHERE task_no = ?`, "夹具：状态修正留痕", noF)
	after, err := ListDeliveryClusterIndex(1, 200, map[string]any{})
	mustNoErr(t, err)
	if toInt(indexRow(t, after, lost)["pending_deviation_count"]) != 2 {
		t.Errorf("修正留痕后的偏差数 %v，期望 2", indexRow(t, after, lost)["pending_deviation_count"])
	}
	mustSQL(t, `UPDATE exec_task SET reconciled_note = NULL WHERE task_no = ?`, noF)
}

// ---------- 待办筛项 / 关键字 / 空态 / 分页 ----------

func TestClusterIndexFiltersAndEmpty(t *testing.T) {
	requireDB(t)
	useDeliveryEnv(t, facadeToolBody(`  start) echo ok; exit 0 ;;`))
	exec := localExec(t)

	running := newIndexCluster(t, "run")
	done := newIndexCluster(t, "done")
	overrideOnly := newIndexCluster(t, "ovl")
	insertRunningTask(t, toInt(exec["id"]), "ezctl.start", running, "", "", 0)
	noDone := insertRunningTask(t, toInt(exec["id"]), "ezctl.start", done, "", "", 0)
	mustNoErr(t, completeIndexTask(noDone, "COMPLETED", 0))
	mustSaveOverride(t, overrideOnly, ignCfgDeployServiceCIDR, "10.96.0.0/16")

	// 关键字：包含匹配、不做存在性校验
	kw, err := ListDeliveryClusterIndex(1, 50, map[string]any{"keyword": running})
	mustNoErr(t, err)
	if len(indexList(kw)) != 1 || Str(indexList(kw)[0]["cluster_ref"]) != running {
		t.Errorf("关键字过滤结果 %v，期望仅 %s", indexList(kw), running)
	}
	part, err := ListDeliveryClusterIndex(1, 50, map[string]any{"keyword": running[3:9]})
	mustNoErr(t, err)
	if !listHasCluster(part, running) {
		t.Errorf("前缀/包含匹配未命中: %v", part["list"])
	}
	miss, err := ListDeliveryClusterIndex(1, 50, map[string]any{"keyword": "clu-never-served-here"})
	mustNoErr(t, err)
	if toInt(miss["total"]) != 0 || len(indexList(miss)) != 0 {
		t.Errorf("无命中应空集: total=%v list=%v", miss["total"], miss["list"])
	}
	if note := Str(miss["empty_note"]); strings.Contains(note, "没有集群") {
		t.Errorf("空态不得断言上游是否存在集群（INV-33）: %q", note)
	} else if !strings.Contains(note, "筛选") {
		t.Errorf("带筛选的零命中应说明是筛选未命中，而不是「本域没服务过」: %q", note)
	}

	r, err := ListDeliveryClusterIndex(1, 200, map[string]any{"pending": "RUNNING"})
	mustNoErr(t, err)
	if !listHasCluster(r, running) || listHasCluster(r, done) {
		t.Errorf("在跑筛项: %v", indexList(r))
	}
	o, err := ListDeliveryClusterIndex(1, 200, map[string]any{"pending": "OVERRIDE"})
	mustNoErr(t, err)
	if !listHasCluster(o, overrideOnly) || listHasCluster(o, done) {
		t.Errorf("生效覆写筛项: %v", indexList(o))
	}
	all, err := ListDeliveryClusterIndex(1, 200, map[string]any{"pending": "ALL"})
	mustNoErr(t, err)
	for _, ref := range []string{running, done, overrideOnly} {
		if !listHasCluster(all, ref) {
			t.Errorf("全部档漏行 %s", ref)
		}
	}
	if _, err := ListDeliveryClusterIndex(1, 50, map[string]any{"pending": "SOMETHING"}); err == nil {
		t.Errorf("未知待办筛项应拒绝（值域四值）")
	} else {
		wantErrContains(t, err, "待办筛项")
	}

	p1, err := ListDeliveryClusterIndex(1, 1, map[string]any{"keyword": running})
	mustNoErr(t, err)
	if toInt(p1["total"]) != 1 || len(indexList(p1)) != 1 {
		t.Errorf("分页 %v / %v", p1["total"], p1["list"])
	}
	p2, err := ListDeliveryClusterIndex(2, 1, map[string]any{"keyword": running})
	mustNoErr(t, err)
	if len(indexList(p2)) != 0 {
		t.Errorf("越页应空: %v", p2["list"])
	}
}

// ---------- 空态（9.10.1：文案不得写成「没有集群」） ----------

// TestClusterIndexEmptyState 把两张供数表清空后取全量清单 —— 空态是本屏唯一的"零行"形状，
// 清表沿用同域既有装置的口径（faceWorkRoot 即整表 DELETE），用例各自造数、互不依赖存量。
func TestClusterIndexEmptyState(t *testing.T) {
	requireDB(t)
	mustSQL(t, `DELETE FROM ign_task_log`)
	mustSQL(t, `DELETE FROM ign_reconcile_log`)
	mustSQL(t, `DELETE FROM exec_task`)
	mustSQL(t, `DELETE FROM ign_cluster_config_override`)

	out, err := ListDeliveryClusterIndex(1, 20, map[string]any{})
	mustNoErr(t, err)
	if toInt(out["total"]) != 0 || len(indexList(out)) != 0 {
		t.Fatalf("清空后应零行: total=%v list=%v", out["total"], out["list"])
	}
	note := Str(out["empty_note"])
	if !strings.Contains(note, "本域尚未替任何集群执行过交付") {
		t.Errorf("空态文案应给「本域尚未替任何集群执行过交付」: %q", note)
	}
	if strings.Contains(note, "没有集群") {
		t.Errorf("空态不得断言上游集群是否存在（INV-33）: %q", note)
	}
	if Str(out["stats_at"]) == "" {
		t.Errorf("空态仍须给出统计时点（口径标注与有无行无关）")
	}
}

// ---------- 纯读：反复调用无副作用 ----------

func TestClusterIndexReadOnly(t *testing.T) {
	requireDB(t)
	tables := []string{"exec_task", "ign_task_log", "ign_reconcile_log", "ign_cluster_config_override", "audit_logs"}
	before := map[string]int64{}
	for _, tb := range tables {
		before[tb] = countRows(t, `SELECT COUNT(*) FROM `+tb)
	}
	for range 3 {
		if _, err := ListDeliveryClusterIndex(1, 200, map[string]any{"pending": "DEVIATION"}); err != nil {
			t.Fatal(err)
		}
		if _, err := ListDeliveryClusterIndex(1, 200, map[string]any{"keyword": "ix-"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tb := range tables {
		if after := countRows(t, `SELECT COUNT(*) FROM `+tb); after != before[tb] {
			t.Errorf("%s 行数 %d→%d：FUNC-52 是纯读查询，不得有写腿（9.10.3 本屏无 CUD）", tb, before[tb], after)
		}
	}
}

// ---------- REP-12 的本集群 scope（9.6 载体注记 / 9.11.1：台内挂点带 clusterRef） ----------

// TestReconcileScopeByCluster 集群交付台的「日志与对账」页签与横看挂点共用同一套查询面，
// 差的是过滤上下文：台内按该集群的任务派生 scope，不传则出全量。
// ign_reconcile_log 没有 cluster_ref 列（零 DDL 批不加列），scope 由 exec_task 现算派生 ——
// 这条派生腿与 REP-19 的并集派生同源，不是第二份存储。
func TestReconcileScopeByCluster(t *testing.T) {
	requireDB(t)
	refA := newIndexCluster(t, "scp")
	refB := newIndexCluster(t, "scp")
	if err := EnsureLocalExecutor(); err != nil {
		t.Fatal(err)
	}
	exec := localExec(t)
	taskA := insertRunningTask(t, toInt(exec["id"]), "DEPLOY", refA, "03", "", 0)
	taskB := insertRunningTask(t, toInt(exec["id"]), "DEPLOY", refB, "03", "", 0)
	t.Cleanup(func() {
		softSQL(`DELETE FROM ign_reconcile_log WHERE object_no IN (?, ?)`, taskA, taskB)
	})
	for _, taskNo := range []string{taskA, taskB} {
		mustSQL(t, `INSERT INTO ign_reconcile_log
			(event_type, object_type, object_no, local_value, remote_value, trigger_reason, operator_id, operator_name, created_by)
			VALUES ('TASK_LOST', 'TASK', ?, 'RUNNING', 'LOST', '集群 scope 用例留痕', 1, 'system', 1)`, taskNo)
	}

	scoped, err := ListReconcileLogs(1, 50, map[string]any{"cluster_ref": refA})
	mustNoErr(t, err)
	if len(scoped.List) != 1 || Str(scoped.List[0]["object_no"]) != taskA {
		t.Fatalf("本集群 scope 返回 %d 行（期望只有 %s 一笔）：%v", len(scoped.List), taskA, scoped.List)
	}
	if scoped.Total != 1 {
		t.Errorf("scope 计数 %d，期望 1", scoped.Total)
	}

	// 横看挂点（不传 cluster_ref）仍出全量：两条都在
	all, err := ListReconcileLogs(1, 200, map[string]any{"keyword": "集群 scope 用例留痕"})
	mustNoErr(t, err)
	seen := map[string]bool{}
	for _, r := range all.List {
		seen[Str(r["object_no"])] = true
	}
	if !seen[taskA] || !seen[taskB] {
		t.Fatalf("横看形态丢了行（scope 过滤泄漏到无参路径）：%v", all.List)
	}

	// 执行机维度的留痕不属于任何集群：按集群 scope 时必须被排除，横看时可见
	execNo := Str(exec["executor_no"])
	mustSQL(t, `INSERT INTO ign_reconcile_log
		(event_type, object_type, object_no, local_value, remote_value, trigger_reason, operator_id, operator_name, created_by)
		VALUES ('TOOL_UNAVAILABLE', 'EXECUTOR', ?, 'ONLINE', 'OFFLINE', '执行机留痕不随集群走', 1, 'system', 1)`, execNo)
	t.Cleanup(func() {
		softSQL(`DELETE FROM ign_reconcile_log WHERE object_no = ? AND object_type = 'EXECUTOR'`, execNo)
	})
	again, err := ListReconcileLogs(1, 50, map[string]any{"cluster_ref": refA})
	mustNoErr(t, err)
	for _, r := range again.List {
		if Str(r["object_type"]) == "EXECUTOR" {
			t.Errorf("按集群 scope 混进了执行机留痕行：%v", r)
		}
	}
	if n := countRows(t, `SELECT COUNT(*) FROM ign_reconcile_log WHERE object_type = 'EXECUTOR' AND object_no = ?`, execNo); n == 0 {
		t.Fatal("执行机留痕夹具未落库")
	}
}
