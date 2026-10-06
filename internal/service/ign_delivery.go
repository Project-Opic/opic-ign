// ign_delivery.go —— 交付门面（FUNC-34~37；PROC-06；R-13/R-15/R-17；INV-34~INV-37）。
//
// 门面对上游（O-CLC）只暴露**登记标识与结论**：来源任务标识、来源步骤序号、关联集群标识、
// 步骤编码/动作类别名、节点地址清单 —— 命令面（动作码→argv）留在本域私有映射（ign_exec.go），
// 上游不得下发命令、参数数组或工作目录（否则白名单从"本域的治理边界"退化为"上游的透传面"）。
//
// 契约词汇由**被调用方**定义：回执 status 取 IGN_TASK_STATUS 字典码（含 LOST），
// 由上游适配器解析；门面不做同义翻译（Q-70 裁定）。
package service

import (
	"fmt"
	"strings"

	"gorm.io/gorm"

	"gitcode.com/opic-ontology/opic-techbase/internal/store"
	"gitcode.com/opic-ontology/opic-techbase/pkg/errcode"
	"gitcode.com/opic-ontology/opic-techbase/pkg/logger"
)

// ignFacadeNodes 节点地址清单入参归一（JSON 数组或空白分隔串；Q-72：进请求体、两侧各校验一次）。
func ignFacadeNodes(v any) []string {
	switch x := v.(type) {
	case []any:
		out := make([]string, 0, len(x))
		for _, it := range x {
			if s := strings.TrimSpace(fmt.Sprint(it)); s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(x))
		for _, s := range x {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		return ignArgvTokens(x)
	}
	return nil
}

// ignLocalExecutor 本机执行机行（INV-12：LOCAL 全平台至多一行、由系统自动注册）。
// 门面与人工提交同归这一行 —— 执行位收口本机（Q-68），LOCAL 行即执行位的登记事实。
func ignLocalExecutor() (map[string]any, error) {
	row, err := qOne(`SELECT * FROM executor WHERE executor_type = 'LOCAL' AND flag = 1`)
	if err != nil {
		return nil, err
	}
	if row != nil {
		return row, nil
	}
	if err := EnsureLocalExecutor(); err != nil {
		return nil, err
	}
	return qOne(`SELECT * FROM executor WHERE executor_type = 'LOCAL' AND flag = 1`)
}

// 受理侧拒绝留痕的事件类型（REP-13「拒绝审计计数」的台账落点；与对账纠偏同表不同事件）。
const (
	ignEvtGovernanceRefuse = "GOVERNANCE_REFUSE" // R-12/R-13 治理拒绝（403 语义）
	ignEvtToolUnavailable  = "TOOL_UNAVAILABLE"  // R-15 执行位不可用（受理即刻拒绝）
)

// ignClampRunes 列宽截断：留痕文本按目标列的字符宽度收口，超长截尾而不是整条写失败
// （拒绝留痕丢失 = REP-13「拒绝审计计数」少一块，比少几个字严重得多）。
func ignClampRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return ""
	}
	return string(r[:n-1]) + "…"
}

// ignRefuseAudit 治理拒绝 / 执行位不可用的受理侧留痕（REP-12 可检索、REP-13 可计数）。
// 三条语义分列（需求文档 §4.2.6 第 14 项）：治理拒绝、执行位不可用、执行结论，不可并成一个"失败"。
func ignRefuseAudit(eventType, objectNo, detail, reason string, user UserInfo) {
	verdict := "治理拒绝"
	if eventType == ignEvtToolUnavailable {
		verdict = "受理即刻拒绝"
	}
	_, err := qExec(`
		INSERT INTO ign_reconcile_log
			(event_type, object_type, object_no, local_value, remote_value, trigger_reason, operator_id, operator_name, created_by)
		VALUES (?, 'TASK', ?, ?, ?, ?, ?, ?, ?)`,
		eventType, ignClampRunes(objectNo, 64), ignSafeAuditText(detail), ignSafeAuditText(verdict),
		ignSafeAuditText(reason), user.ID, user.Username, user.ID)
	if err != nil {
		logger.Warnf("[ign-facade] 受理侧拒绝留痕写入失败（事件 %s，对象 %s）: %v", eventType, objectNo, err)
	}
}

// DeliverySubmit FUNC-34 交付门面 · 任务受理（同步返回远端引用，执行异步 B-AUTO-11）。
func DeliverySubmit(data map[string]any, user UserInfo) (map[string]any, error) {
	sourceTaskNo := strings.TrimSpace(Str(data["source_task_no"]))
	sourceStepIndex := ignToInt(data["source_step_index"])
	clusterRef := strings.TrimSpace(Str(data["cluster_ref"]))
	actionCode := strings.TrimSpace(Str(data["action_code"]))
	stepCode := strings.TrimSpace(Str(data["step_code"]))
	nodes := ignFacadeNodes(data["nodes"])

	// ① 请求形态校验（INV-34：门面路径必带来源两列；序号 ≥1）
	if sourceTaskNo == "" || len(sourceTaskNo) > 64 {
		return nil, Fail(errcode.ErrParam, "来源任务标识必填且不超 64 位（INV-34：跨域只存标识）")
	}
	if sourceStepIndex < 1 {
		return nil, Fail(errcode.ErrParam, "来源步骤序号须 ≥1（INV-35：与来源任务标识共同构成幂等键），收到 %d", sourceStepIndex)
	}
	if clusterRef == "" {
		return nil, Fail(errcode.ErrParam, "关联集群标识必填（INV-33：任务必须归属一个关联集群）")
	}
	if !ignClusterIDRe.MatchString(clusterRef) {
		return nil, Fail(errcode.ErrParam, "关联集群标识 %q 不符合格式白名单（字母数字与 . _ -，1-64 位）", clusterRef)
	}

	// ② R-13 受理解析（白名单外 / 非交付动作层 / 动作码与步骤编码冲突 ⇒ 治理拒绝并审计）
	refuseTag := fmt.Sprintf("%s/第%d步", sourceTaskNo, sourceStepIndex)
	action, resolvedStep, err := ignResolveFacadeAction(actionCode, stepCode)
	if err != nil {
		ignRefuseAudit(ignEvtGovernanceRefuse, refuseTag, Str(AsBizErr(err).Msg),
			"R-13 受理解析失败（白名单外 / 非交付动作层 / 动作码与步骤编码冲突）", user)
		return nil, err
	}

	// ③ FUNC-34 请求体字段口径：节点清单只随节点增删类动作（地址非法 ⇒ 治理拒绝）
	args, err := ignFacadeArgs(action, nodes)
	if err != nil {
		if biz := AsBizErr(err); biz.Code == errcode.ErrForbidden {
			ignRefuseAudit(ignEvtGovernanceRefuse, refuseTag, biz.Msg,
				"R-13 请求体字段口径不符（节点地址清单）", user)
		}
		return nil, err
	}

	// ④ R-15 执行位可用性：受理即刻拒绝并记 TOOL_UNAVAILABLE，绝不静默改走别的执行位
	ok, why := ignLocalExecAvailable()
	if !ok {
		ignRefuseAudit(ignEvtToolUnavailable, refuseTag, why,
			"R-15 本机执行位不可用（受理即刻拒绝，不转入其他执行位）", user)
		return nil, Fail(errcode.ErrState, "%s（R-15：本机执行位不可用即受理即刻拒绝，不转入其他执行位）", why)
	}

	// ⑤ B-AUTO-15 工作目录幂等前置（仅安装首步；已对齐即可重入）
	if action == "ezctl.setup" && resolvedStep == "01" {
		if err := ignEnsureClusterWorkDir(clusterRef); err != nil {
			ignRefuseAudit(ignEvtToolUnavailable, refuseTag, Str(AsBizErr(err).Msg),
				"B-AUTO-15 集群工作目录前置未对齐", user)
			return nil, err
		}
	}

	exec, err := ignLocalExecutor()
	if err != nil {
		return nil, err
	}
	if exec == nil {
		return nil, Fail(errcode.ErrConfig, "本机执行机未注册（INV-12：LOCAL 行由系统自动注册）")
	}
	// 执行位行的治理状态与人工路径同口径（INV-17 终态 / R-10 降级 / R-11 降级声明）
	switch Str(exec["status"]) {
	case "REMOVED", "LOCKED", "DEGRADED", "ENROLLING":
		return nil, Fail(errcode.ErrState, "本机执行位当前不可承接任务（执行机状态 %s；INV-17/R-10 同源口径）", Str(exec["status"]))
	}
	if boolOfAny(exec["tls_declined"]) {
		return nil, Fail(errcode.ErrState, "本机执行位已声明 TLS 降级，不可承接任务（R-11：降级必须登记在案）")
	}

	var result map[string]any
	var reused bool
	err = tx(func(db *gorm.DB) error {
		// INV-35：命中「来源任务标识 + 来源步骤序号」的非终态任务 ⇒ 复用原任务、不新建
		open, err := store.One(db, `
			SELECT * FROM exec_task
			WHERE flag = 1 AND source_task_no = ? AND source_step_index = ?
			  AND task_status IN ('RUNNING','CANCELLING')
			ORDER BY id`, sourceTaskNo, sourceStepIndex)
		if err != nil {
			return err
		}
		if open != nil {
			result = open
			reused = true
			return nil
		}
		// R-05：容量原子校验（超上限影响 0 行 = 容量拒绝；R-17 排队不占额外容量）
		res, err := store.Exec(db, `
			UPDATE executor SET current_tasks_count = current_tasks_count + 1
			WHERE id = ? AND flag = 1 AND current_tasks_count < concurrent_tasks_max`,
			toInt(exec["id"]))
		if err != nil {
			return err
		}
		if res == 0 {
			return Fail(errcode.ErrState, "本机执行位并发已达上限（R-05 容量拒绝：当前 %d/%d）",
				toInt(exec["current_tasks_count"]), toInt(exec["concurrent_tasks_max"]))
		}
		taskNo, err := genIgnNo(db, "ExecTask", "exec_task", "task_no")
		if err != nil {
			return err
		}
		timeout := ignConfigInt(db, ignCfgTaskTimeoutSeconds, 7200) // REF-02
		now := ignNow()
		if _, err := store.Exec(db, `
			INSERT INTO exec_task
				(task_no, executor_id, action, args, cluster_ref, step_code,
				 source_task_no, source_step_index, task_status, timeout_seconds,
				 started_at, submitted_by, created_by)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'RUNNING', ?, ?, ?, ?)`,
			taskNo, toInt(exec["id"]), action, args, clusterRef, ignNullableStr(resolvedStep),
			sourceTaskNo, sourceStepIndex, timeout, now, user.Username, user.ID); err != nil {
			return err
		}
		row, err := store.One(db, `SELECT * FROM exec_task WHERE task_no = ?`, taskNo)
		if err != nil {
			return err
		}
		result = row
		return nil
	})
	if err != nil {
		return nil, err
	}
	if reused {
		result["facade_reused"] = true
		result["facade_note"] = "幂等复用：命中同一「来源任务标识 + 来源步骤序号」的在途任务，未新建（INV-35）"
		return result, nil
	}

	ignAppendTaskLog(toInt(result["id"]), "PLATFORM", fmt.Sprintf(
		"门面受理：来源 %s/第 %d 步，解析动作 %s，集群 %s（R-13；命令面私有、上游不下发 argv）",
		sourceTaskNo, sourceStepIndex, action, clusterRef))
	WriteAudit(nil, user.ID, user.Username, "DELIVERY_ACCEPT",
		"交付门面受理 "+sourceTaskNo+"/第 "+fmt.Sprint(sourceStepIndex)+" 步 → "+Str(result["task_no"]))
	publishIgnEvent("delivery", "accepted", map[string]any{
		"task_no": Str(result["task_no"]), "source_task_no": sourceTaskNo,
		"source_step_index": sourceStepIndex, "action": action, "cluster_ref": clusterRef,
	})
	ignDispatchLocal(result) // B-AUTO-11：受理事务提交后异步派发，不阻塞同步返回
	return result, nil
}

// ignFacadeArgs 派生 args 列（登记事实的传递，不是命令面下发）：
// 节点增删类 ⇒ 地址清单（空白分隔，入库即派生事实）；其余动作该字段必须为空。
func ignFacadeArgs(action string, nodes []string) (string, error) {
	if ignNodesAction(action) {
		if len(nodes) == 0 {
			return "", Fail(errcode.ErrParam, "节点增删类动作必须携带节点地址清单（FUNC-34）")
		}
		for _, ip := range nodes {
			if err := ignValidateNodeAddr(ip); err != nil {
				return "", err
			}
		}
		return strings.Join(nodes, " "), nil
	}
	if len(nodes) > 0 {
		return "", Fail(errcode.ErrParam, "非节点增删类动作不得携带节点地址清单（FUNC-34：该字段其余动作必须为空）")
	}
	return "", nil
}

// DeliveryReceipt FUNC-35 交付门面 · 回执与增量日志（只读；反复轮询无副作用）。
// 引用未命中返回明确的"任务不存在"而非空回执（上游据此判登记值与真实位置不符）。
func DeliveryReceipt(taskNo string, sinceSeq int64) (map[string]any, error) {
	taskNo = strings.TrimSpace(taskNo)
	if taskNo == "" {
		return nil, Fail(errcode.ErrParam, "远端任务引用必填")
	}
	row, err := qOne(`SELECT * FROM exec_task WHERE task_no = ? AND flag = 1`, taskNo)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, Fail(errcode.ErrNotFound, "任务不存在：远端任务引用 %q 在本域无登记（登记值与真实位置不符）", taskNo)
	}
	if sinceSeq < 0 {
		sinceSeq = 0
	}
	taskID := toInt(row["id"])
	limit := ignConfigInt(store.DB, ignCfgReceiptLogBatch, 500) // REF-14
	logs, err := qList(`
		SELECT seq, direction, log_line, masked, logged_at FROM ign_task_log
		WHERE task_id = ? AND flag = 1 AND seq > ? ORDER BY seq LIMIT ?`,
		taskID, sinceSeq, limit+1)
	if err != nil {
		return nil, err
	}
	truncated := int64(len(logs)) > limit
	if truncated {
		logs = logs[:limit]
	}
	if logs == nil {
		logs = []map[string]any{}
	}
	nextSeq := sinceSeq
	if len(logs) > 0 {
		nextSeq = ignToInt(logs[len(logs)-1]["seq"])
	}
	cursor, err := qCount(`SELECT COALESCE(MAX(seq), 0) FROM ign_task_log WHERE task_id = ? AND flag = 1`, taskID)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"remote_task_ref": taskNo,
		"task_no":         taskNo,
		"status":          Str(row["task_status"]), // IGN_TASK_STATUS 字典码（契约由被调用方定义）
		"exit_code":       row["exit_code"],        // LOST 与未终态均为 null（INV-37）
		"verdict_source":  row["verdict_source"],
		"fail_reason":     row["fail_reason"],
		"summary":         ignTaskVerdictNote(taskID),
		"logs":            logs,
		"since_seq":       sinceSeq,
		"next_seq":        nextSeq,
		"cursor":          cursor, // 游标右界：日志不完整可判读（INV-38）
		"truncated":       truncated,
		"started_at":      row["started_at"],
		"ended_at":        row["ended_at"],
	}
	return out, nil
}

// ignTaskVerdictNote 结论摘要 = 最近一条「终态回填」平台注记行（不另设存储：摘要即留痕本身）。
func ignTaskVerdictNote(taskID int64) string {
	row, err := qOne(`
		SELECT log_line FROM ign_task_log
		WHERE task_id = ? AND flag = 1 AND direction = 'PLATFORM' AND log_line LIKE '终态回填：%'
		ORDER BY seq DESC LIMIT 1`, taskID)
	if err != nil || row == nil {
		return ""
	}
	return Str(row["log_line"])
}

// DeliveryCancel FUNC-36 交付门面 · 任务撤销（与 FUNC-24 同一条进程组终止链 R-14）。
// 终态直接返回当前终态（幂等，不报错）；取消不等于回滚。
func DeliveryCancel(taskNo string, user UserInfo) (map[string]any, error) {
	return ignCancelChain(taskNo, user, "DELIVERY_CANCEL", "交付门面撤销")
}

// ignCancelChain FUNC-24 与 FUNC-36 共用的撤销链（同一入口，避免两套语义漂移）。
func ignCancelChain(taskNo string, user UserInfo, auditAction, label string) (map[string]any, error) {
	taskNo = strings.TrimSpace(taskNo)
	row, err := qOne(`SELECT * FROM exec_task WHERE task_no = ? AND flag = 1`, taskNo)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, NotFound("执行任务")
	}
	st := Str(row["task_status"])
	if ignTerminalStatus(st) { // 幂等：终态直接返回当前终态，不报错
		return map[string]any{
			"task_no": taskNo, "remote_task_ref": taskNo, "status": st,
			"note": "任务已处终态，撤销请求按幂等返回当前状态（INV-32：终态不可改写）",
		}, nil
	}
	if st == "CANCELLING" {
		return map[string]any{
			"task_no": taskNo, "remote_task_ref": taskNo, "status": st,
			"note": "撤销已在途，最终状态由执行事实决定（R-09）",
		}, nil
	}
	if st != "RUNNING" {
		return nil, Fail(errcode.ErrState, "仅执行中任务可撤销，当前状态 %s（INV-30：失败与取消分列）", st)
	}
	// 执行位形态决定"撤销"的落点（Q-68：本期只有本机执行位是真的，REMOTE 留位不删）
	execType := ""
	if e, err := qOne(`SELECT executor_type FROM executor WHERE id = ? AND flag = 1`, toInt(row["executor_id"])); err == nil && e != nil {
		execType = Str(e["executor_type"])
	}

	n, err := qExec(`
		UPDATE exec_task SET task_status = 'CANCELLING', updated_at = to_char(now(),'YYYY-MM-DD HH24:MI:SS')
		WHERE id = ? AND task_status = 'RUNNING'`, toInt(row["id"]))
	if err != nil {
		return nil, err
	}
	if n == 0 { // 与执行位收口竞态：对方已写终态 ⇒ 幂等返回
		cur, _ := qOne(`SELECT task_status FROM exec_task WHERE id = ?`, toInt(row["id"]))
		return map[string]any{
			"task_no": taskNo, "remote_task_ref": taskNo, "status": Str(cur["task_status"]),
			"note": "任务已处终态，撤销请求按幂等返回当前状态（INV-32）",
		}, nil
	}

	// REMOTE 通道：本域没有执行位进程可终止，终态必须由代理回执收口（R-09）—— 绝不代答 CANCELLED
	if execType != "LOCAL" {
		ignAppendTaskLog(toInt(row["id"]), "PLATFORM",
			"撤销已登记为「取消中」：REMOTE 通道的终态由代理回执收口，本域不代答（R-09/Q-68 代理对端本期未实现）")
		note := "取消指令已登记为「取消中」，等待代理回执收口（R-09：REMOTE 通道终态由执行事实决定）"
		WriteAudit(nil, user.ID, user.Username, auditAction, label+" "+taskNo)
		publishIgnEvent("task", "cancelling", map[string]any{"task_no": taskNo})
		return map[string]any{
			"task_no": taskNo, "remote_task_ref": taskNo, "status": "CANCELLING",
			"cancel_note": note, "note": note,
		}, nil
	}

	handledByRunner := IgnCancelLocal(toInt(row["id"]), taskNo)
	note := "撤销已就地收口为 CANCELLED（无在途执行位协程，痕迹不会产生）"
	if handledByRunner {
		note = "撤销信号已送达执行位（SIGTERM → 宽限 → SIGKILL，R-14）；最终状态由执行事实决定（R-09）"
	}
	status := "CANCELLING"
	if !handledByRunner { // 就地收口已完成，回读实际终态（不返回已被改写前的状态）
		if cur, _ := qOne(`SELECT task_status FROM exec_task WHERE id = ?`, toInt(row["id"])); cur != nil {
			status = Str(cur["task_status"])
		}
	}
	WriteAudit(nil, user.ID, user.Username, auditAction, label+" "+taskNo)
	publishIgnEvent("task", "cancelling", map[string]any{"task_no": taskNo})
	return map[string]any{
		"task_no": taskNo, "remote_task_ref": taskNo, "status": status,
		"cancel_note": note, "note": note,
	}, nil
}

// ListFacadeTasks FUNC-37 / REP-14 门面受理台账（固定过滤「来源任务标识非空」）。
// 与 REP-09 共用同一 ExecTask 查询面，只差固定过滤与列集（不复制事实、不建新存储）。
func ListFacadeTasks(page, size int, filters map[string]any) (*Page, error) {
	where := "WHERE t.flag = 1 AND t.source_task_no IS NOT NULL"
	args := []any{}
	if v := Str(filters["source_task_no"]); v != "" {
		where += " AND t.source_task_no = ?"
		args = append(args, v)
	}
	if v := ignToInt(filters["source_step_index"]); v != 0 {
		where += " AND t.source_step_index = ?"
		args = append(args, v)
	}
	if v := Str(filters["cluster_ref"]); v != "" {
		where += " AND t.cluster_ref LIKE ?"
		args = append(args, "%"+v+"%")
	}
	if v := Str(filters["verdict_source"]); v != "" {
		where += " AND t.verdict_source = ?"
		args = append(args, v)
	}
	if v := Str(filters["task_status"]); v != "" {
		where += " AND t.task_status = ?"
		args = append(args, v)
	}
	if v := Str(filters["step_code"]); v != "" {
		where += " AND t.step_code = ?"
		args = append(args, v)
	}
	total, err := qCount(`SELECT COUNT(*) FROM exec_task t `+where, args...)
	if err != nil {
		return nil, err
	}
	rows, err := qList(`
		SELECT t.task_no, t.source_task_no, t.source_step_index, t.action, t.step_code,
		       t.cluster_ref, t.task_status, t.verdict_source, t.exit_code, t.fail_reason,
		       t.started_at, t.ended_at, t.duration_ms, t.submitted_by, t.created_at,
		       (SELECT COUNT(*) FROM ign_task_log l WHERE l.task_id = t.id AND l.flag = 1)  AS log_line_count,
		       (SELECT COALESCE(MAX(l.seq), 0) FROM ign_task_log l WHERE l.task_id = t.id AND l.flag = 1) AS log_cursor
		FROM exec_task t `+where+` ORDER BY t.id DESC LIMIT ? OFFSET ?`,
		append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	return &Page{List: rows, Total: total, PageV: page, Size: size}, nil
}
