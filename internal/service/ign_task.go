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
	"strings"
	"time"

	"gorm.io/gorm"

	"gitcode.com/opic-ontology/opic-techbase/internal/store"
	"gitcode.com/opic-ontology/opic-techbase/pkg/errcode"
)

// ignActionWhitelist 执行动作白名单(INV-28;模型 IGN_ACTION;白名单外是治理拒绝不是表单校验错误)。
func ignActionWhitelist() map[string]bool {
	return ignDictCodes("IGN_ACTION")
}

// SubmitTask FUNC-23 提交执行任务(FLOW-PROC-03):
// 治理校验 R-12 → 容量原子校验 R-05 → 登记任务 + 计数递增 → (转发执行机为跨域通道,留痕)。
func SubmitTask(data map[string]any, user UserInfo) (map[string]any, error) {
	var result map[string]any
	err := tx(func(db *gorm.DB) error {
		executorID := ignToInt(data["executor_id"])
		action := strings.TrimSpace(Str(data["action"]))
		args := strings.TrimSpace(Str(data["args"]))
		clusterRef := strings.TrimSpace(Str(data["cluster_ref"]))
		env := strings.TrimSpace(Str(data["env"]))
		workingDir := strings.TrimSpace(Str(data["working_dir"]))
		if executorID == 0 || action == "" || clusterRef == "" {
			return Fail(errcode.ErrParam, "执行机、执行动作与关联集群为必填项（INV-33：任务必须归属一个关联集群）")
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
			if _, err := store.Exec(db, `
				INSERT INTO ign_reconcile_log (event_type, object_type, object_no, local_value, remote_value, trigger_reason, operator_id, operator_name, created_by)
				VALUES ('STATUS_FIX', 'TASK', ?, ?, '治理拒绝', 'R-12 白名单外动作提交被拒', ?, ?, ?)`,
				"action="+action, action, user.ID, user.Username, user.ID); err != nil {
				return err
			}
			return Fail(errcode.ErrForbidden, "执行动作 %s 不在 IGN_ACTION 白名单内（R-12 治理拒绝：白名单外动作一律拒绝并审计，拒绝记录可在「日志与对账」台账查询）", action)
		}
		// R-12:无 shell 治理——参数与环境变量不得携带拼接风险字符(shell 元字符)
		for _, kv := range []struct{ label, val string }{{"动作参数", args}, {"环境变量", env}, {"工作目录", workingDir}} {
			if strings.ContainsAny(kv.val, ";|&`$><\n\\") {
				if _, err := store.Exec(db, `
					INSERT INTO ign_reconcile_log (event_type, object_type, object_no, local_value, remote_value, trigger_reason, operator_id, operator_name, created_by)
					VALUES ('STATUS_FIX', 'TASK', ?, ?, '治理拒绝', 'R-12 含拼接风险字符的提交被拒', ?, ?, ?)`,
					"action="+action, kv.val, user.ID, user.Username, user.ID); err != nil {
					return err
				}
				return Fail(errcode.ErrForbidden, "%s 含拼接风险字符（; | & ` $ > < 换行 反斜杠），已治理拒绝（R-12：代理以独立 argv 无 shell 执行,此类输入一律拒绝并审计）", kv.label)
			}
		}
		// 防重放摘要(REF-09:执行机侧窗口 24h,本域以「执行机 + 摘要」唯一性兜底)
		digestSrc := strings.TrimSpace(Str(data["request_token"]))
		if digestSrc == "" {
			digestSrc = fmt.Sprintf("%d|%s|%s|%s|%d", executorID, action, args, clusterRef, time.Now().UnixNano()/int64(time.Second))
		}
		sum := sha256.Sum256([]byte(digestSrc))
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
				(task_no, executor_id, action, args, env, working_dir, cluster_ref, request_token_digest,
				 task_status, timeout_seconds, started_at, submitted_by, created_by)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'RUNNING', ?, ?, ?, ?)`,
			taskNo, executorID, action, args, ignNullableStr(env), ignNullableStr(workingDir),
			clusterRef, digest, timeout, now, user.Username, user.ID); err != nil {
			return err
		}
		row, err := store.One(db, `SELECT * FROM exec_task WHERE task_no = ?`, taskNo)
		if err != nil {
			return err
		}
		row["executor_no"] = exec["executor_no"]
		row["dispatch_note"] = "任务已登记且执行机计数已递增（R-05）；mTLS 转发执行机为跨域通道（阶段三未接入，任务以 RUNNING 状态登记,结果经回执端点回流 R-09）"
		result = row
		return nil
	})
	if err != nil {
		return nil, err
	}
	if b, ok := result["replayed"].(bool); ok && b {
		return result, nil // 防重放重复提交:返回原任务,不重复审计
	}
	WriteAudit(nil, user.ID, user.Username, "TASK_SUBMIT", "提交执行任务 "+Str(result["task_no"])+"（动作 "+Str(result["action"])+"）")
	publishIgnEvent("task", "submitted", map[string]any{
		"task_no": Str(result["task_no"]), "action": Str(result["action"]), "cluster_ref": Str(result["cluster_ref"]),
	})
	return result, nil
}

// CompleteTask 代理回执端点(任务结束:计数递减 + 任务终态 + 退出码留痕;R-09 事实回填以执行机为准且留痕)。
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
		if target != "COMPLETED" && target != "FAILED" && target != "CANCELLED" {
			return Fail(errcode.ErrParam, "回执终态仅允许 COMPLETED / FAILED / CANCELLED（INV-29：三个终态必须有结束时间与退出码或失败原因之一）")
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
			UPDATE exec_task SET task_status=?, exit_code=?, fail_reason=?, ended_at=?, duration_ms=?,
			updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`,
			target, exitCode, ignNullableStr(failReason), endedAt, durationMs, row["id"]); err != nil {
			return err
		}
		// R-05:任务结束计数原子递减(异常结束也保证不下溢)
		if _, err := store.Exec(db, `
			UPDATE executor SET current_tasks_count = GREATEST(current_tasks_count - 1, 0)
			WHERE id = ? AND flag = 1`, toInt(row["executor_id"])); err != nil {
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
	return result, nil
}

// CancelTask FUNC-24 取消执行任务(人为中止 → CANCELLED,与 FAILED 分列 INV-30)。
func CancelTask(taskNo string, user UserInfo) (map[string]any, error) {
	var result map[string]any
	err := tx(func(db *gorm.DB) error {
		row, err := store.One(db, `SELECT * FROM exec_task WHERE task_no = ? AND flag = 1`, strings.TrimSpace(taskNo))
		if err != nil {
			return err
		}
		if row == nil {
			return NotFound("执行任务")
		}
		st := Str(row["task_status"])
		if st == "CANCELLING" {
			return Fail(errcode.ErrState, "任务取消中，请等待回执（FUNC-24）")
		}
		if st != "RUNNING" {
			return Fail(errcode.ErrState, "仅执行中任务可取消，当前状态 %s（INV-30：FAILED 与 CANCELLED 分列,恢复路径不同）", st)
		}
		if _, err := store.Exec(db, `
			UPDATE exec_task SET task_status='CANCELLING', updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`,
			row["id"]); err != nil {
			return err
		}
		// 跨域留痕:取消指令下发执行机为跨域通道;本地先置 CANCELLING,由代理回执 CANCELLED 收口(R-09)
		out, err := store.One(db, `SELECT * FROM exec_task WHERE id = ?`, row["id"])
		if err != nil {
			return err
		}
		out["cancel_note"] = "取消指令已登记（CANCELLING）；向执行机转发取消为跨域通道（阶段三未接入,留痕）;代理确认后终态 CANCELLED"
		result = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	WriteAudit(nil, user.ID, user.Username, "TASK_CANCEL", "取消执行任务 "+taskNo)
	publishIgnEvent("task", "cancelling", map[string]any{"task_no": taskNo})
	return result, nil
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
	logs, err := qList(`SELECT * FROM task_log WHERE task_id = ? ORDER BY id`, toInt(row["id"]))
	if err != nil {
		return nil, err
	}
	if logs == nil {
		logs = []map[string]any{}
	}
	row["logs"] = logs
	return row, nil
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

// IgnStats FUNC-29 统计总览(REP-13/MU frmIgnOps:版本数/可用版本/执行机就绪/不可达/任务 7 日成功/纠偏次数)。
func IgnStats() (map[string]any, error) {
	out := map[string]any{}
	v, err := qOne(`
		SELECT COUNT(*) AS total,
		       SUM(CASE WHEN version_status IN ('PREPUSHED','PARTIALLY_PREPUSHED') THEN 1 ELSE 0 END) AS available
		FROM delivery_version WHERE flag = 1`)
	if err != nil {
		return nil, err
	}
	out["version_total"] = toInt(v["total"])
	out["version_available"] = toInt(v["available"])
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
	t, err := qOne(`
		SELECT COUNT(*) AS n FROM exec_task
		WHERE flag = 1 AND task_status = 'COMPLETED' AND ended_at::timestamp > now() - interval '7 days'`)
	if err != nil {
		return nil, err
	}
	out["task_success_7d"] = toInt(t["n"])
	r, err := qCount(`SELECT COUNT(*) FROM ign_reconcile_log WHERE flag = 1`)
	if err != nil {
		return nil, err
	}
	out["reconcile_count"] = r
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
