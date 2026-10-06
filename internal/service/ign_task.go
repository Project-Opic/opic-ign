// Package service —— 点火中心(ignition):执行任务聚合(AGG-IGN-06)与运维视图(FUNC-29/30)。
//
// FUNC-23 提交执行任务 / FUNC-24 取消执行任务 / FUNC-25 任务台账(REP-09) / FUNC-26 任务详情(REP-10) /
// FUNC-27 任务日志查询(REP-11) / FUNC-28 对账与纠偏台账(REP-12) / FUNC-29 统计总览(REP-13) / FUNC-30 运行参数维护。
// 规则:INV-28~33;R-05 计数原子增减、R-09 对账回填留痕、R-12 白名单与无 shell 治理拒绝;REF-02/09。
package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"gitcode.com/opic-ontology/opic-techbase/internal/config"
	"gitcode.com/opic-ontology/opic-techbase/internal/store"
	"gitcode.com/opic-ontology/opic-techbase/pkg/errcode"
)

// ignActionWhitelist 执行动作白名单(INV-28;模型 IGN_ACTION;白名单外是治理拒绝不是表单校验错误)。
func ignActionWhitelist() map[string]bool {
	return ignDictCodes("IGN_ACTION")
}

// ignRefuse 受理侧治理拒绝的待写留痕。
// 必须攒到**事务之外**再落库：受理事务随后 return error 会整体回滚，事务内的 INSERT 一并消失，
// 拒绝记录反而写不进去（REP-13「拒绝审计计数」漏一块、且 R-12「拒绝记录可查」不成立）。
type ignRefuse struct {
	objectNo string
	detail   string
	reason   string
}

// ignSafeAuditText 留痕文本口径：先按 R-16 掩码（拒绝理由常引用被拒输入，可能含凭据串），
// 再按列宽截断。
func ignSafeAuditText(s string) string {
	m, _ := ignMaskLine(s)
	return ignClampRunes(m, 200)
}

// SubmitTask FUNC-23 提交执行任务(FLOW-PROC-03):
// 治理校验 R-12 → 容量原子校验 R-05 → 登记任务 + 计数递增 → 本机执行位派发（B-AUTO-11）。
//
// 人工路径与门面路径的分工（INV-34 来源与去处不可混淆）：
// 人工必带 request_token（INV-31 摘要防重放）、必不带来源两列；门面反之（INV-35 幂等键）。
func SubmitTask(data map[string]any, user UserInfo) (map[string]any, error) {
	// INV-34：来源两列是门面路径专用标记，人工提交带上即"去处冒充来源"
	if strings.TrimSpace(Str(data["source_task_no"])) != "" || ignToInt(data["source_step_index"]) != 0 {
		return nil, Fail(errcode.ErrParam,
			"来源任务标识与来源步骤序号由交付门面受理专用，人工提交两者必须为空（INV-34：来源与去处不可混淆）")
	}
	// INV-31：人工路径的防重放靠令牌摘要，缺令牌即无兜底
	token := strings.TrimSpace(Str(data["request_token"]))
	if token == "" {
		return nil, Fail(errcode.ErrParam,
			"请求令牌必填（INV-31：人工提交以「执行机 + 令牌摘要」在防重放窗口内唯一；仅交付门面以幂等键替代）")
	}

	var result map[string]any
	var dispatched map[string]any
	var refuse *ignRefuse
	err := tx(func(db *gorm.DB) error {
		executorID := ignToInt(data["executor_id"])
		action := strings.TrimSpace(Str(data["action"]))
		args := strings.TrimSpace(Str(data["args"]))
		clusterRef := strings.TrimSpace(Str(data["cluster_ref"]))
		stepCode := strings.TrimSpace(Str(data["step_code"]))
		env := strings.TrimSpace(Str(data["env"]))
		workingDir := strings.TrimSpace(Str(data["working_dir"]))
		if executorID == 0 || action == "" || clusterRef == "" {
			return Fail(errcode.ErrParam, "执行机、执行动作与关联集群为必填项（INV-33：任务必须归属一个关联集群）")
		}
		if !ignClusterIDRe.MatchString(clusterRef) {
			return Fail(errcode.ErrParam, "关联集群标识 %q 不符合格式白名单（字母数字与 . _ -，1-64 位；INV-33）", clusterRef)
		}
		// INV-36：步骤编码只在安装族动作上有合法域（01~07），其余动作该列必须为空
		if action == "ezctl.setup" {
			if !ignStepCodeRe.MatchString(stepCode) {
				return Fail(errcode.ErrRule, "ezctl.setup 必须携带步骤编码且命中 IGN_INSTALL_STEP（01~07），收到 %q（INV-36）", stepCode)
			}
		} else if stepCode != "" {
			return Fail(errcode.ErrRule, "步骤编码仅 ezctl.setup 使用（INV-36：其余动作该列必须为空），收到 %q", stepCode)
		}
		exec, err := store.One(db, `SELECT * FROM executor WHERE id = ? AND flag = 1`, executorID)
		if err != nil {
			return err
		}
		if exec == nil {
			return NotFound("执行机")
		}
		// 治理状态门:REMOVED 终态/LOCKED/DEGRADED 拒绝新任务分发(R-10 降级拒绝新分发、运行中保护)
		switch Str(exec["status"]) {
		case "REMOVED":
			return Fail(errcode.ErrState, "执行机已注销，不再分发任务（INV-17：REMOVED 终态）")
		case "LOCKED":
			return Fail(errcode.ErrState, "执行机已锁定，不再分发任务（R-04：解锁 = 重新生成引导令牌）")
		case "DEGRADED":
			return Fail(errcode.ErrState, "执行机处于降级态，拒绝新任务分发（R-10：服务端证书被吊销；运行中任务受保护不强制中断）")
		case "ENROLLING":
			return Fail(errcode.ErrState, "执行机尚未完成引导，不可分发任务（R-03：引导完成判定未达成）")
		}
		if boolOfAny(exec["tls_declined"]) {
			return Fail(errcode.ErrState, "执行机已声明 TLS 降级，不可分发任务（R-11：降级必须登记在案；如需恢复请撤销降级声明）")
		}
		// INV-28:执行动作必须命中 IGN_ACTION 白名单(白名单外为治理拒绝)
		wl := ignActionWhitelist()
		if len(wl) > 0 && !wl[action] {
			// R-12:治理拒绝并审计(403 语义;拒绝记录可查)
			refuse = &ignRefuse{"action=" + action, action, "R-12 白名单外动作提交被拒"}
			return Fail(errcode.ErrForbidden, "执行动作 %s 不在 IGN_ACTION 白名单内（R-12 治理拒绝：白名单外动作一律拒绝并审计，拒绝记录可在「日志与对账」台账查询）", action)
		}
		// 需求文档 7.3 三层可达面：白名单枚举 ≠ 可提交集合。
		// 内部前置层（ezctl.new 由 B-AUTO-15 使用）与平台运维层（scripts.* 由 B-AUTO-02/06~08 发起）
		// 都不对人工提交开放 —— 人工可提交集合只有交付动作层。
		if plane := ignActionPlane(action); plane != "delivery" {
			refuse = &ignRefuse{"action=" + action, action, "7.3 可达面外动作提交被拒（执行位 " + plane + "）"}
			return Fail(errcode.ErrForbidden, "执行动作 %s 属 %s 可达面，不对人工提交开放（7.3：人工与门面只可达交付动作层；内部前置归 B-AUTO-15、平台运维归 B-AUTO-02/06~08）", action, plane)
		}
		// R-12:无 shell 治理——参数与环境变量不得携带拼接风险字符(shell 元字符)
		for _, kv := range []struct{ label, val string }{{"动作参数", args}, {"环境变量", env}, {"工作目录", workingDir}} {
			if strings.ContainsAny(kv.val, ";|&`$><\n\\") {
				refuse = &ignRefuse{"action=" + action, kv.val, "R-12 含拼接风险字符的提交被拒"}
				return Fail(errcode.ErrForbidden, "%s 含拼接风险字符（; | & ` $ > < 换行 反斜杠），已治理拒绝（R-12：代理以独立 argv 无 shell 执行,此类输入一律拒绝并审计）", kv.label)
			}
		}
		// 防重放摘要(REF-09:执行机侧窗口 24h,本域以「执行机 + 摘要」唯一性兜底)
		sum := sha256.Sum256([]byte(token))
		digest := hex.EncodeToString(sum[:])
		// INV-31:同一执行机上防重放窗口内摘要至多一个任务(重复提交返回原任务)
		existing, err := store.One(db, `
			SELECT * FROM exec_task WHERE executor_id = ? AND request_token_digest = ? AND flag = 1`, executorID, digest)
		if err != nil {
			return err
		}
		if existing != nil {
			if created, ok := parseIgnTime(Str(existing["created_at"])); ok {
				window := ignConfigInt(db, ignCfgReplayWindowHours, 24)
				if time.Since(created) < time.Duration(window)*time.Hour {
					result = existing
					result["replayed"] = true
					result["replay_note"] = "防重放窗口内重复提交，返回原任务（INV-31）"
					return nil
				}
			}
			return Fail(errcode.ErrRule, "请求令牌摘要与既有历史任务冲突（INV-31：请携带新的 request_token 重试）")
		}
		// R-05:容量原子校验——超上限影响 0 行 = 容量拒绝
		res, err := store.Exec(db, `
			UPDATE executor SET current_tasks_count = current_tasks_count + 1
			WHERE id = ? AND flag = 1 AND current_tasks_count < concurrent_tasks_max`, executorID)
		if err != nil {
			return err
		}
		if res == 0 {
			return Fail(errcode.ErrState, "执行机并发已达上限（R-05 容量拒绝：当前 %d/%d，不进入队列；请提升并发上限或等待任务结束）",
				toInt(exec["current_tasks_count"]), toInt(exec["concurrent_tasks_max"]))
		}
		timeout := ignToInt(data["timeout_seconds"])
		if timeout <= 0 {
			timeout = ignConfigInt(db, ignCfgTaskTimeoutSeconds, 7200) // REF-02
		}
		taskNo, err := genIgnNo(db, "ExecTask", "exec_task", "task_no")
		if err != nil {
			return err
		}
		now := ignNow()
		if _, err := store.Exec(db, `
			INSERT INTO exec_task
				(task_no, executor_id, action, args, env, working_dir, cluster_ref, step_code,
				 request_token_digest, task_status, timeout_seconds, started_at, submitted_by, created_by)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'RUNNING', ?, ?, ?, ?)`,
			taskNo, executorID, action, args, ignNullableStr(env), ignNullableStr(workingDir),
			clusterRef, ignNullableStr(stepCode), digest, timeout, now, user.Username, user.ID); err != nil {
			return err
		}
		row, err := store.One(db, `SELECT * FROM exec_task WHERE task_no = ?`, taskNo)
		if err != nil {
			return err
		}
		row["executor_no"] = exec["executor_no"]
		if Str(exec["executor_type"]) == "LOCAL" {
			dispatched = row
			row["dispatch_note"] = "任务已登记且执行机计数已递增（R-05）；本机执行位异步派发（B-AUTO-11：受理与执行解耦，同集群在途则排队 R-17）"
		} else {
			row["dispatch_note"] = "任务已登记且执行机计数已递增（R-05）；REMOTE 通道由执行机代理回执收口（R-09：终态以执行事实为准，代理对端本期未实现，任务留在执行中）"
		}
		result = row
		return nil
	})
	if err != nil {
		// 拒绝留痕写在受理事务之外：事务已随错误整体回滚（见 ignRefuse 的说明）
		if refuse != nil {
			ignRefuseAudit(ignEvtGovernanceRefuse, refuse.objectNo, refuse.detail, refuse.reason, user)
		}
		return nil, err
	}
	if b, ok := result["replayed"].(bool); ok && b {
		return result, nil // 防重放重复提交:返回原任务,不重复审计
	}
	WriteAudit(nil, user.ID, user.Username, "TASK_SUBMIT", "提交执行任务 "+Str(result["task_no"])+"（动作 "+Str(result["action"])+"）")
	publishIgnEvent("task", "submitted", map[string]any{
		"task_no": Str(result["task_no"]), "action": Str(result["action"]), "cluster_ref": Str(result["cluster_ref"]),
	})
	if dispatched != nil {
		ignAppendTaskLog(toInt(dispatched["id"]), "PLATFORM", fmt.Sprintf(
			"人工提交：动作 %s，集群 %s（命令面由本域私有映射派生，界面不下发 argv）",
			Str(dispatched["action"]), Str(dispatched["cluster_ref"])))
		ignDispatchLocal(dispatched)
	}
	return result, nil
}

// CompleteTask 代理回执端点(任务结束:计数递减 + 任务终态 + 退出码留痕;R-09 事实回填以执行机为准且留痕)。
//
// 结论依据固定为 AGENT_RECEIPT（IGN_VERDICT_SOURCE）：这条路径的终态来自代理上报，
// 不是本机进程事实（本机路径由判定网关写 EXIT_CODE / PLAY_RECAP）。LOST 不可由回执产生
// —— "痕迹不可存活"是本域对无法验真痕迹的判定，代理能回执即说明痕迹存活（INV-37）。
func CompleteTask(taskNo string, data map[string]any, user UserInfo) (map[string]any, error) {
	var result map[string]any
	err := tx(func(db *gorm.DB) error {
		row, err := store.One(db, `SELECT * FROM exec_task WHERE task_no = ? AND flag = 1`, strings.TrimSpace(taskNo))
		if err != nil {
			return err
		}
		if row == nil {
			return NotFound("执行任务")
		}
		if st := Str(row["task_status"]); st != "RUNNING" && st != "CANCELLING" {
			return Fail(errcode.ErrState, "任务已处于终态 %s，不可重复回执（INV-29/INV-32：任务记录创建后不可改写，状态对账回填除外且必须留痕）", st)
		}
		target := strings.ToUpper(strings.TrimSpace(Str(data["task_status"])))
		if target == "" {
			target = "COMPLETED"
		}
		if target == "LOST" {
			return Fail(errcode.ErrParam, "LOST 不由代理回执产生（INV-37：痕迹不可存活是本域的失联判定；代理可回执即痕迹存活）")
		}
		if target != "COMPLETED" && target != "FAILED" && target != "CANCELLED" {
			return Fail(errcode.ErrParam, "回执终态仅允许 COMPLETED / FAILED / CANCELLED（INV-29：四个终态必须有结束时间与退出码或失败原因之一）")
		}
		exitCode := ignToInt(data["exit_code"])
		failReason := strings.TrimSpace(Str(data["fail_reason"]))
		if failReason != "" {
			if err := ignDictRequire("IGN_TASK_FAIL_REASON", failReason, "失败原因"); err != nil {
				return err
			}
		}
		if exitCode == 0 && failReason == "" && target == "FAILED" {
			return Fail(errcode.ErrRule, "FAILED 终态必须携带失败原因（INV-29：退出码或失败原因之一;INV-30 失败与取消分列）")
		}
		startedAt := Str(row["started_at"])
		endedAt := ignNow()
		var durationMs int64
		if st, ok := parseIgnTime(startedAt); ok {
			if et, ok2 := parseIgnTime(endedAt); ok2 {
				durationMs = et.Sub(st).Milliseconds()
			}
		}
		if _, err := store.Exec(db, `
			UPDATE exec_task SET task_status=?, exit_code=?, fail_reason=?, verdict_source='AGENT_RECEIPT',
			ended_at=?, duration_ms=?, updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`,
			target, exitCode, ignNullableStr(failReason), endedAt, durationMs, row["id"]); err != nil {
			return err
		}
		// R-05:任务结束计数原子递减(异常结束也保证不下溢)
		if _, err := store.Exec(db, `
			UPDATE executor SET current_tasks_count = GREATEST(current_tasks_count - 1, 0)
			WHERE id = ? AND flag = 1`, toInt(row["executor_id"])); err != nil {
			return err
		}
		summary := strings.TrimSpace(Str(data["summary"]))
		if summary == "" {
			summary = "退出码 " + strconv.FormatInt(exitCode, 10)
			if failReason != "" {
				summary += "，失败原因 " + failReason
			}
		}
		// 结论摘要与本机路径同源（FUNC-35 的 summary 只读「终态回填：」注记行，不另建存储列）；
		// 代理上报的摘要同样要过写前脱敏（INV-40：不存在第二条未脱敏通道）。
		note := fmt.Sprintf("终态回填：%s（结论依据 AGENT_RECEIPT，R-09 事实回填以执行位为准）", summary)
		masked, hit := ignMaskLine(ignTruncateLogLine(note))
		seq, err := txLogCursor(db, toInt(row["id"]))
		if err != nil {
			return err
		}
		if err := txInsertTaskLog(db, toInt(row["id"]), seq+1, "PLATFORM", masked, hit); err != nil {
			return err
		}
		out, err := store.One(db, `SELECT * FROM exec_task WHERE id = ?`, row["id"])
		if err != nil {
			return err
		}
		result = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	WriteAudit(nil, user.ID, user.Username, "TASK_COMPLETE", "任务回执 "+taskNo+" → "+Str(result["task_status"])+"（R-05 计数递减/R-09 留痕）")
	publishIgnEvent("task", "completed", map[string]any{
		"task_no": taskNo, "task_status": Str(result["task_status"]), "cluster_ref": Str(result["cluster_ref"]),
	})
	ignClaimNextQueued(Str(result["cluster_ref"])) // R-17：前序收口即续派同集群排队任务
	return result, nil
}

// CancelTask FUNC-24 取消执行任务(人为中止 → CANCELLED,与 FAILED 分列 INV-30)。
// 与门面撤销（FUNC-36）共用 ignCancelChain：同一终止链、同一幂等口径，避免两套语义漂移。
func CancelTask(taskNo string, user UserInfo) (map[string]any, error) {
	return ignCancelChain(taskNo, user, "TASK_CANCEL", "取消执行任务")
}

// ListExecTasks FUNC-25 任务台账(REP-09:编号/动作/执行机/关联集群/状态+来源/退出码或失败原因/起止时间)。
func ListExecTasks(page, size int, filters map[string]any) (*Page, error) {
	where := "WHERE t.flag = 1"
	args := []any{}
	if v := ignToInt(filters["executor_id"]); v != 0 {
		where += " AND t.executor_id = ?"
		args = append(args, v)
	}
	if v := Str(filters["action"]); v != "" {
		where += " AND t.action = ?"
		args = append(args, v)
	}
	if v := Str(filters["cluster_ref"]); v != "" {
		where += " AND t.cluster_ref LIKE ?"
		args = append(args, "%"+v+"%")
	}
	if v := Str(filters["task_status"]); v != "" {
		where += " AND t.task_status = ?"
		args = append(args, v)
	}
	if v := Str(filters["keyword"]); v != "" {
		where += " AND (t.task_no LIKE ? OR t.submitted_by LIKE ?)"
		kw := "%" + v + "%"
		args = append(args, kw, kw)
	}
	total, err := qCount(`SELECT COUNT(*) FROM exec_task t `+where, args...)
	if err != nil {
		return nil, err
	}
	rows, err := qList(`
		SELECT t.*, e.executor_no, e.name AS executor_name FROM exec_task t
		JOIN executor e ON e.id = t.executor_id
		`+where+` ORDER BY t.id DESC LIMIT ? OFFSET ?`,
		append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	for _, r := range rows {
		r["env_masked"] = ignMaskEnv(Str(r["env"]))
	}
	return &Page{List: rows, Total: total, PageV: page, Size: size}, nil
}

// ignMaskEnv 环境变量值脱敏(REP-10:动作与参数环境变量值脱敏)。
func ignMaskEnv(env string) string {
	env = strings.TrimSpace(env)
	if env == "" {
		return ""
	}
	out := []string{}
	for _, line := range strings.Split(env, "\n") {
		if i := strings.Index(line, "="); i >= 0 {
			out = append(out, line[:i+1]+"******")
		} else {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// GetTask FUNC-26 任务详情(REP-10:提交信息/动作与参数(脱敏)/执行结果(来源标注)/对账注记)。
func GetTask(taskNo string) (map[string]any, error) {
	row, err := qOne(`
		SELECT t.*, e.executor_no, e.name AS executor_name FROM exec_task t
		JOIN executor e ON e.id = t.executor_id
		WHERE t.task_no = ? AND t.flag = 1`, strings.TrimSpace(taskNo))
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, NotFound("执行任务")
	}
	row["env_masked"] = ignMaskEnv(Str(row["env"]))
	row["status_source"] = "执行机事实回填（R-09：状态对账以执行机为准）"
	if Str(row["reconciled_note"]) != "" {
		row["status_source"] = "已对账修正（R-09 留痕：" + Str(row["reconciled_note"]) + "）"
	}
	const rep11DefaultRows = 500 // REP-11 pagination.defaultPageSize（缺省最近 500 行）
	taskID := toInt(row["id"])
	logs, err := qList(`
		SELECT seq, direction, log_line, masked, logged_at FROM ign_task_log
		WHERE task_id = ? AND flag = 1 ORDER BY seq DESC LIMIT ?`, taskID, rep11DefaultRows)
	if err != nil {
		return nil, err
	}
	if logs == nil {
		logs = []map[string]any{}
	}
	// 取的是尾部行，回吐前按游标升序复位（REP-11 orderBy TaskLogLine.seq ASC）
	sort.Slice(logs, func(i, j int) bool { return ignToInt(logs[i]["seq"]) < ignToInt(logs[j]["seq"]) })
	row["logs"] = logs
	row["log_cursor"] = ignLogCursor(taskID) // 游标右界（REP-11 完整性注记）
	row["log_line_count"] = len(logs)
	if str := strings.TrimSpace(Str(row["source_task_no"])); str != "" {
		row["source_note"] = "由交付门面受理（来源 " + str + "/第 " +
			strconv.FormatInt(ignToInt(row["source_step_index"]), 10) + " 步）；反查见门面受理台账（FUNC-37/INV-34）"
	} else {
		row["source_note"] = "人工提交（FUNC-23：来源两列为空，INV-34 来源与去处不可混淆）"
	}
	return row, nil
}

// TaskLogs FUNC-27 任务日志查询(REP-11 流式明细：按行游标升序、增量续传、分页)。
// 空洞显式标注为「少了几行」（INV-38 的界面落点）—— 游标右界与行数之差即缺失行数。
func TaskLogs(taskNo string, sinceSeq int64, page, size int) (map[string]any, error) {
	task, err := qOne(`SELECT id FROM exec_task WHERE task_no = ? AND flag = 1`, strings.TrimSpace(taskNo))
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, NotFound("执行任务")
	}
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 2000 {
		size = 500 // REP-11 pagination: defaultPageSize 500 / maxPageSize 2000
	}
	taskID := toInt(task["id"])
	where := "WHERE task_id = ? AND flag = 1"
	args := []any{taskID}
	if sinceSeq > 0 {
		where += " AND seq > ?"
		args = append(args, sinceSeq)
	}
	total, err := qCount(`SELECT COUNT(*) FROM ign_task_log `+where, args...)
	if err != nil {
		return nil, err
	}
	rows, err := qList(`
		SELECT seq, direction, log_line, masked, logged_at FROM ign_task_log
		`+where+` ORDER BY seq LIMIT ? OFFSET ?`, append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	all, err := qOne(`SELECT COUNT(*) AS n, COALESCE(MAX(seq), 0) AS c FROM ign_task_log WHERE task_id = ? AND flag = 1`, taskID)
	if err != nil {
		return nil, err
	}
	cursor, count := ignToInt(all["c"]), ignToInt(all["n"])
	var gap int64 = cursor - count
	if gap < 0 {
		gap = 0
	}
	var maskedCount int64
	for _, r := range rows {
		if boolOfAny(r["masked"]) {
			maskedCount++
		}
	}
	out := map[string]any{
		"list":         rows,
		"total":        total,
		"page":         page,
		"size":         size,
		"log_cursor":   cursor,
		"gap_lines":    gap,
		"masked_count": maskedCount, // R-16 的审计可见性：本批有多少行被掩码改写过
	}
	if gap > 0 { // INV-38 的界面落点：缺失显式标注为「少了几行」，不是乱序
		out["gap_note"] = "游标存在空洞：缺失 " + strconv.FormatInt(gap, 10) + " 行（入库失败不占号，故行序仍单调递增）"
	}
	return out, nil
}

// ListReconcileLogs FUNC-28 对账与纠偏台账(REP-12)。
func ListReconcileLogs(page, size int, filters map[string]any) (*Page, error) {
	where := "WHERE flag = 1"
	args := []any{}
	if v := Str(filters["event_type"]); v != "" {
		where += " AND event_type = ?"
		args = append(args, v)
	}
	if v := Str(filters["keyword"]); v != "" {
		where += " AND (object_no LIKE ? OR local_value LIKE ? OR remote_value LIKE ? OR trigger_reason LIKE ?)"
		kw := "%" + v + "%"
		args = append(args, kw, kw, kw, kw)
	}
	total, err := qCount(`SELECT COUNT(*) FROM ign_reconcile_log `+where, args...)
	if err != nil {
		return nil, err
	}
	rows, err := qList(`
		SELECT * FROM ign_reconcile_log `+where+` ORDER BY id DESC LIMIT ? OFFSET ?`,
		append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	return &Page{List: rows, Total: total, PageV: page, Size: size}, nil
}

// ignGroupCounts 把「分组计数」查询折叠成 [{code,count}]（REP-13 各分布块的统一形状）。
func ignGroupCounts(sql string, args ...any) ([]map[string]any, error) {
	rows, err := qList(sql, args...)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, r := range rows {
		out = append(out, map[string]any{"code": Str(r["k"]), "count": toInt(r["n"])})
	}
	return out, nil
}

// ignCountWhere 单值计数（REP-13 的"分列"块：同一维度的每个取值各数一次）。
func ignCountWhere(where string, args ...any) (int64, error) {
	n, err := qCount(`SELECT COUNT(*) FROM exec_task WHERE flag = 1 AND `+where, args...)
	return n, err
}

// IgnStats FUNC-29 统计总览（REP-13 九块：c1 版本状态分布 ｜ c2 预推完成度 ｜ c3 执行机治理分布 ｜
// c4 健康快照（观测类）｜ c5 任务结论计数（四终态分列）｜ c6 门面受理量与结论依据分布 ｜
// c7 执行位可用（观测类）｜ c8 拒绝审计计数 ｜ c9 纠偏计数）。
// 口径：计数类与观测类视觉可分、观测类各带扫描时间；LOST 独立成块、不并入失败（INV-30/B-CONF-13）；
// 门面受理量与人工提交量分列（INV-34 来源列即分列依据）。
func IgnStats() (map[string]any, error) {
	out := map[string]any{}
	scannedAt := ignNow() // 观测类卡片的扫描时间（REP-13 口径 7）

	// c1 版本状态分布 + 既有兼容位（版本总数 / 可用版本）
	v, err := qOne(`
		SELECT COUNT(*) AS total,
		       SUM(CASE WHEN version_status IN ('PREPUSHED','PARTIALLY_PREPUSHED') THEN 1 ELSE 0 END) AS available
		FROM delivery_version WHERE flag = 1`)
	if err != nil {
		return nil, err
	}
	out["version_total"] = toInt(v["total"])
	out["version_available"] = toInt(v["available"])
	if out["version_status_dist"], err = ignGroupCounts(
		`SELECT version_status AS k, COUNT(*) AS n FROM delivery_version WHERE flag = 1 GROUP BY version_status`); err != nil {
		return nil, err
	}

	// c2 预推完成度（明细行的状态分布；完成度 = SUCCESS 占比）
	if out["prepush_status_dist"], err = ignGroupCounts(
		`SELECT prepush_status AS k, COUNT(*) AS n FROM image_prepush_item WHERE flag = 1 GROUP BY prepush_status`); err != nil {
		return nil, err
	}

	// c3 执行机治理分布 + c4 健康快照（观测类，带扫描时间）
	if out["executor_status_dist"], err = ignGroupCounts(
		`SELECT status AS k, COUNT(*) AS n FROM executor WHERE flag = 1 GROUP BY status`); err != nil {
		return nil, err
	}
	threshold := ignConfigInt(store.DB, ignCfgHeartbeatTimeout, 120)
	cutoff := time.Now().Add(-time.Duration(threshold) * time.Second).Format(ignTimeLayout)
	e, err := qOne(`
		SELECT SUM(CASE WHEN status = 'READY' THEN 1 ELSE 0 END) AS ready,
		       SUM(CASE WHEN last_heartbeat_at IS NOT NULL AND status <> 'REMOVED'
			                AND last_heartbeat_at < ?
		               THEN 1 ELSE 0 END) AS unreachable
		FROM executor WHERE flag = 1`, cutoff)
	if err != nil {
		return nil, err
	}
	out["executor_ready"] = toInt(e["ready"])
	out["executor_unreachable"] = toInt(e["unreachable"])
	out["health_snapshot"] = map[string]any{
		"ready": toInt(e["ready"]), "unreachable": toInt(e["unreachable"]),
		"timeout_seconds": threshold, "scanned_at": scannedAt,
		"note": "观测类：心跳超时窗口内的不可达计数，不是治理状态（7.2「不可达不落治理状态」）",
	}

	// c5 任务结论计数：COMPLETED / FAILED / CANCELLED / LOST 四列分列（INV-30，LOST 不并入失败）
	in7d := "ended_at IS NOT NULL AND ended_at::timestamp > now() - interval '7 days'"
	for _, st := range []string{"COMPLETED", "FAILED", "CANCELLED", "LOST"} {
		n, err := ignCountWhere("task_status = ? AND "+in7d, st)
		if err != nil {
			return nil, err
		}
		out["task_"+strings.ToLower(st)+"_7d"] = n
		if all, err := ignCountWhere("task_status = ?", st); err != nil {
			return nil, err
		} else {
			out["task_"+strings.ToLower(st)] = all
		}
	}
	out["task_success_7d"] = out["task_completed_7d"] // 既有兼容位
	open, err := ignCountWhere("task_status IN ('RUNNING','CANCELLING')")
	if err != nil {
		return nil, err
	}
	out["task_open"] = open

	// c6 门面受理量与结论依据分布（v1.1；INV-34：来源列非空即门面受理，与人工量分列）
	facadeN, err := ignCountWhere("source_task_no IS NOT NULL")
	if err != nil {
		return nil, err
	}
	manualN, err := ignCountWhere("source_task_no IS NULL")
	if err != nil {
		return nil, err
	}
	out["facade_accept_count"] = facadeN
	out["manual_submit_count"] = manualN
	facade7d, err := qCount(`
		SELECT COUNT(*) FROM exec_task
		WHERE flag = 1 AND source_task_no IS NOT NULL
		  AND created_at::timestamp > now() - interval '7 days'`)
	if err != nil {
		return nil, err
	}
	out["facade_accept_7d"] = facade7d
	if out["verdict_source_dist"], err = ignGroupCounts(`
		SELECT verdict_source AS k, COUNT(*) AS n FROM exec_task
		WHERE flag = 1 AND source_task_no IS NOT NULL AND verdict_source IS NOT NULL
		GROUP BY verdict_source`); err != nil {
		return nil, err
	}

	// c7 执行位可用（观测类，v1.1；R-15 的观测侧，不是台账）
	avail, why := ignLocalExecAvailable()
	out["local_exec_available"] = avail
	out["local_exec_observation"] = map[string]any{
		"available": avail, "reason": why, "scanned_at": scannedAt,
		"work_root": ignWorkRoot(), "tool_bin": strings.TrimSpace(config.Config.Delivery.ToolBin),
		"note": "观测类：命令面挂载与可执行探测的即时结论（R-15）；路径只以部署配置值呈现，界面不可维护（1.6.1）",
	}

	// c8 拒绝审计计数：治理拒绝（Z1）与执行位不可用（Z2）分列，不与执行结论（I）混计
	refuse, err := qList(`
		SELECT event_type AS k, COUNT(*) AS n FROM ign_reconcile_log
		WHERE flag = 1 AND event_type IN (?, ?) GROUP BY event_type`,
		ignEvtGovernanceRefuse, ignEvtToolUnavailable)
	if err != nil {
		return nil, err
	}
	refuseCount := map[string]int64{ignEvtGovernanceRefuse: 0, ignEvtToolUnavailable: 0}
	for _, r := range refuse {
		refuseCount[Str(r["k"])] = toInt(r["n"])
	}
	out["refuse_governance_count"] = refuseCount[ignEvtGovernanceRefuse]
	out["refuse_tool_unavailable_count"] = refuseCount[ignEvtToolUnavailable]

	// c9 纠偏计数（审计事实；失联判定与对账修正分列）
	corr, err := qList(`
		SELECT event_type AS k, COUNT(*) AS n FROM ign_reconcile_log
		WHERE flag = 1 AND event_type IN ('COUNT_CORRECT','STATUS_FIX','TASK_LOST') GROUP BY event_type`)
	if err != nil {
		return nil, err
	}
	corrCount := map[string]int64{"COUNT_CORRECT": 0, "STATUS_FIX": 0, "TASK_LOST": 0}
	for _, r := range corr {
		corrCount[Str(r["k"])] = toInt(r["n"])
	}
	out["correct_count"] = corrCount
	reconcileTotal, err := qCount(`SELECT COUNT(*) FROM ign_reconcile_log WHERE flag = 1`)
	if err != nil {
		return nil, err
	}
	out["reconcile_count"] = reconcileTotal

	params := map[string]any{}
	for _, c := range defaultIgnConfigs() {
		val, err := ignConfigGet(store.DB, c.Key)
		if err == nil && val != "" {
			params[c.Key] = val
		}
	}
	out["params"] = params
	return out, nil
}

// GetIgnParams FUNC-30 运行参数读取。
func GetIgnParams() ([]map[string]any, error) {
	rows, err := qList(`SELECT config_key, config_value, description, updated_at FROM ign_platform_config WHERE flag = 1 ORDER BY config_key`)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	return rows, nil
}

// SaveIgnParams FUNC-30 运行参数维护(逐项 upsert;REF-* 语义由描述行承载)。
func SaveIgnParams(items []map[string]any, user UserInfo) error {
	if len(items) == 0 {
		return Fail(errcode.ErrParam, "无可保存的运行参数")
	}
	known := map[string]string{}
	for _, c := range defaultIgnConfigs() {
		known[c.Key] = c.Desc
	}
	err := tx(func(db *gorm.DB) error {
		for _, it := range items {
			key := strings.TrimSpace(Str(it["config_key"]))
			val := strings.TrimSpace(Str(it["config_value"]))
			if key == "" {
				continue
			}
			desc, ok := known[key]
			if !ok {
				return Fail(errcode.ErrParam, "运行参数 %s 不在平台配置键清单内（不允许新增未登记键）", key)
			}
			if err := upsertIgnConfig(db, key, val, desc, user.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	WriteAudit(nil, user.ID, user.Username, "PARAMS_SAVE", "运行参数维护（Ignition REF-* 配置项）")
	publishIgnEvent("ops", "params_changed", map[string]any{"count": len(items)})
	return nil
}
