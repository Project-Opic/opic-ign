// Package service —— 点火中心(ignition):执行机强制注销(FUNC-31~33;FLOW-PROC-APP-01)。
//
// FUNC-31 提交申请(影响面强制展示,R-08) / FUNC-32 审批通过(进入拆除链) / FUNC-33 驳回(状态不变,意见留痕)。
// 载体表 ign_force_remove;审批回调 ApplyIgnForceRemoveOutcome 由 workbench 同事务调用。
// 规则:R-07 拆除链次序固定、R-08 强制注销前置 = 审批通过;INV-15/17。
package service

import (
	"fmt"
	"strings"

	"gorm.io/gorm"

	"gitcode.com/opic-ontology/opic-techbase/internal/infra"
	"gitcode.com/opic-ontology/opic-techbase/internal/service/engine"
	"gitcode.com/opic-ontology/opic-techbase/internal/store"
	"gitcode.com/opic-ontology/opic-techbase/pkg/errcode"
)

// igniteRunningTasks 影响面:运行中任务清单(R-08 强制展示:任务清单 + 所属集群)。
func igniteRunningTasks(db *gorm.DB, executorID int64) ([]map[string]any, error) {
	rows, err := store.List(db, `
		SELECT task_no, action, cluster_ref, task_status, started_at, submitted_by
		FROM exec_task WHERE executor_id = ? AND flag = 1 AND task_status IN ('RUNNING','CANCELLING')
		ORDER BY id DESC`, executorID)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	return rows, nil
}

// SaveForceRemove FUNC-31 强制注销 · 提交申请。
// mode=draft 仅保存草稿(MU act-FUNC-31 DRAFT 形态);mode=submit 保存并提交审批(启动 FLOW-PROC-APP-01)。
// 已有待审申请时更新原因复用同一申请(避免重复挂起)。
func SaveForceRemove(mode string, executorID int64, reason string, user UserInfo) (map[string]any, error) {
	if mode != "draft" && mode != "submit" {
		return nil, Fail(errcode.ErrParam, "模式不合法（draft=保存草稿 / submit=提交审批）")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, Fail(errcode.ErrParam, "强制原因为必填项（MU el12;R-08:中断上游域工作必须留痕说明）")
	}
	var result map[string]any
	var submitted bool
	err := tx(func(db *gorm.DB) error {
		exec, err := store.One(db, `SELECT * FROM executor WHERE id = ? AND flag = 1`, executorID)
		if err != nil {
			return err
		}
		if exec == nil {
			return NotFound("执行机")
		}
		if Str(exec["status"]) == "REMOVED" {
			return Fail(errcode.ErrState, "执行机已注销（REMOVED 终态，无须强制注销，INV-17）")
		}
		// 空闲执行机直达 FUNC-15 空闲注销(FLOW-PROC-APP-01 A03:不应到达——空闲注销直达)
		running, err := store.Count(db, `
			SELECT COUNT(*) FROM exec_task WHERE executor_id = ? AND flag = 1 AND task_status IN ('RUNNING','CANCELLING')`, executorID)
		if err != nil {
			return err
		}
		if running == 0 {
			return Fail(errcode.ErrState, "执行机当前无运行中任务，空闲注销直达即可（FUNC-15），无须走强制注销审批（FLOW-PROC-APP-01 A03）")
		}
		// 影响面快照冻结(R-08 强制展示:运行中任务清单 + 所属集群)
		tasks, err := igniteRunningTasks(db, executorID)
		if err != nil {
			return err
		}
		impact := map[string]any{"running_tasks": tasks, "running_count": len(tasks), "executor_status": Str(exec["status"])}
		// 复用同执行机未完结申请
		existing, err := store.One(db, `
			SELECT * FROM ign_force_remove
			WHERE executor_id = ? AND status IN ('草稿','待平台运维审批') AND flag = 1
			ORDER BY id DESC LIMIT 1`, executorID)
		if err != nil {
			return err
		}
		var applyID int64
		var applyNo string
		if existing != nil {
			applyID = toInt(existing["id"])
			applyNo = Str(existing["apply_no"])
			if _, err := store.Exec(db, `
				UPDATE ign_force_remove SET reason=?, impact_snapshot=?, updated_by=?,
				updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`,
				reason, MarshalJSONVal(impact), user.ID, applyID); err != nil {
				return err
			}
		} else {
			applyNo, err = genIgnNo(db, "ForceRemove", "ign_force_remove", "apply_no")
			if err != nil {
				return err
			}
			if _, err := store.Exec(db, `
				INSERT INTO ign_force_remove
					(apply_no, executor_id, reason, impact_snapshot, status, applicant_id, applicant_name, created_by)
				VALUES (?, ?, ?, ?, '草稿', ?, ?, ?)`,
				applyNo, executorID, reason, MarshalJSONVal(impact), user.ID, user.Username, user.ID); err != nil {
				return err
			}
			row, err := store.One(db, `SELECT id FROM ign_force_remove WHERE apply_no = ?`, applyNo)
			if err != nil {
				return err
			}
			applyID = toInt(row["id"])
		}
		out, err := store.One(db, `SELECT * FROM ign_force_remove WHERE id = ?`, applyID)
		if err != nil {
			return err
		}
		out["executor_no"] = exec["executor_no"]
		out["executor_name"] = exec["name"]
		out["impact_snapshot"] = impact
		result = out
		if mode == "submit" {
			submitted = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if submitted {
		result, err = SubmitForceRemoveApproval(toInt(result["id"]), user)
		if err != nil {
			return nil, err
		}
		return result, nil
	}
	WriteAudit(nil, user.ID, user.Username, "FORCE_REMOVE_DRAFT", "保存强制注销申请草稿 "+Str(result["apply_no"]))
	return result, nil
}

// SubmitForceRemoveApproval 提交审批(草稿/已驳回 → 待平台运维审批;启动 FLOW-PROC-APP-01)。
func SubmitForceRemoveApproval(applyID int64, user UserInfo) (map[string]any, error) {
	var result map[string]any
	err := tx(func(db *gorm.DB) error {
		apply, err := store.One(db, `SELECT * FROM ign_force_remove WHERE id = ? AND flag = 1`, applyID)
		if err != nil {
			return err
		}
		if apply == nil {
			return NotFound("强制注销申请")
		}
		if Str(apply["status"]) != "草稿" && Str(apply["status"]) != "已驳回" {
			return Fail(errcode.ErrState, "仅草稿或已驳回状态的申请可提交审批，当前状态 %s", Str(apply["status"]))
		}
		// 提交前重取影响面(运行任务可能已变化)
		tasks, err := igniteRunningTasks(db, toInt(apply["executor_id"]))
		if err != nil {
			return err
		}
		if len(tasks) == 0 {
			return Fail(errcode.ErrState, "执行机运行中任务已清空，空闲注销直达即可（FUNC-15），审批已阻止")
		}
		def, err := store.One(db, `SELECT * FROM flow_definition WHERE code = ? AND status = 1`, "FLOW-PROC-APP-01")
		if err != nil {
			return err
		}
		if def == nil {
			return Fail(errcode.ErrFlowNotPublished, "执行机强制注销审批流程（FLOW-PROC-APP-01）未发布")
		}
		eng := engine.NewWithInfra(db, infra.EB(), infra.WF(), infra.SLAHours(), infra.TemporalDriver())
		instanceID, err := eng.Start(toInt(def["id"]), Str(apply["apply_no"]),
			[]any{"AGG-IGN-03"}, map[string]any{
				"business_type": "IGN_FORCE_REMOVE",
				"apply_id":      applyID,
				"executor_id":   toInt(apply["executor_id"]),
			}, user.ID)
		if err != nil {
			return err
		}
		if _, err := store.Exec(db, `
			UPDATE ign_force_remove SET status='待平台运维审批', instance_id=?, impact_snapshot=?, applicant_id=?, applicant_name=?,
			updated_by=?, updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`,
			instanceID, MarshalJSONVal(map[string]any{"running_tasks": tasks, "running_count": len(tasks)}),
			user.ID, user.Username, user.ID, applyID); err != nil {
			return err
		}
		out, err := store.One(db, `SELECT * FROM ign_force_remove WHERE id = ?`, applyID)
		if err != nil {
			return err
		}
		out["impact_snapshot"] = ParseJSONText(Str(out["impact_snapshot"]), map[string]any{})
		exec, _ := store.One(db, `SELECT executor_no, name FROM executor WHERE id = ?`, toInt(out["executor_id"]))
		if exec != nil {
			out["executor_no"] = exec["executor_no"]
			out["executor_name"] = exec["name"]
		}
		result = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	WriteAudit(nil, user.ID, user.Username, "FORCE_REMOVE_SUBMIT", "提交强制注销审批 "+Str(result["apply_no"])+"（FLOW-PROC-APP-01）")
	publishIgnEvent("executor", "force_remove_submitted", map[string]any{"apply_no": Str(result["apply_no"])})
	return result, nil
}

// WithdrawForceRemove 撤回(仅待审批且审批人未处理)。
func WithdrawForceRemove(applyID int64, user UserInfo) (map[string]any, error) {
	var result map[string]any
	err := tx(func(db *gorm.DB) error {
		apply, err := store.One(db, `SELECT * FROM ign_force_remove WHERE id = ? AND flag = 1 FOR UPDATE`, applyID)
		if err != nil {
			return err
		}
		if apply == nil {
			return NotFound("强制注销申请")
		}
		if Str(apply["status"]) != "待平台运维审批" {
			return Fail(errcode.ErrState, "仅待审批状态的申请可撤回，当前状态 %s", Str(apply["status"]))
		}
		instanceID := toInt(apply["instance_id"])
		handled, err := store.Count(db, `
			SELECT COUNT(*) FROM flow_task WHERE instance_id=? AND status IN ('DONE','CANCEL') AND action IS NOT NULL`, instanceID)
		if err != nil {
			return err
		}
		if handled > 0 {
			return Fail(errcode.ErrState, "审批人已处理，无法撤回")
		}
		if _, err := store.Exec(db, `
			UPDATE flow_instance SET status='TERMINATED', ended_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`, instanceID); err != nil {
			return err
		}
		if _, err := store.Exec(db, `UPDATE flow_task SET status='CANCEL' WHERE instance_id=? AND status='TODO'`, instanceID); err != nil {
			return err
		}
		if _, err := store.Exec(db, `
			UPDATE ign_force_remove SET status='已撤回', updated_by=?, updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`,
			user.ID, applyID); err != nil {
			return err
		}
		out, err := store.One(db, `SELECT * FROM ign_force_remove WHERE id = ?`, applyID)
		if err != nil {
			return err
		}
		result = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	WriteAudit(nil, user.ID, user.Username, "FORCE_REMOVE_WITHDRAW", "撤回强制注销申请 "+Str(result["apply_no"]))
	return result, nil
}

// ListForceRemoves 强制注销申请列表(审批弹窗与台账)。
func ListForceRemoves(page, size int, filters map[string]any) (*Page, error) {
	where := "WHERE f.flag = 1"
	args := []any{}
	if v := Str(filters["status"]); v != "" {
		where += " AND f.status = ?"
		args = append(args, v)
	}
	if v := ignToInt(filters["executor_id"]); v != 0 {
		where += " AND f.executor_id = ?"
		args = append(args, v)
	}
	total, err := qCount(`SELECT COUNT(*) FROM ign_force_remove f `+where, args...)
	if err != nil {
		return nil, err
	}
	rows, err := qList(`
		SELECT f.*, e.executor_no, e.name AS executor_name, e.status AS executor_status
		FROM ign_force_remove f JOIN executor e ON e.id = f.executor_id
		`+where+` ORDER BY f.id DESC LIMIT ? OFFSET ?`,
		append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	for _, r := range rows {
		r["impact_snapshot"] = ParseJSONText(Str(r["impact_snapshot"]), map[string]any{})
	}
	return &Page{List: rows, Total: total, PageV: page, Size: size}, nil
}

// ApplyIgnForceRemoveOutcome FUNC-32/FUNC-33 审批结果落库(workbench 同事务调用)。
// 通过 → 进入拆除链(R-07 次序固定:吊销双证书 → 凭据置已吊销 → 状态置已注销;
// 先取消全部运行中任务并等待至多 30 秒,R-08) + 注销报告留痕;驳回 → 状态不变,意见留痕。
func ApplyIgnForceRemoveOutcome(db *gorm.DB, instanceID int64, approved bool, user UserInfo, comment string) error {
	apply, err := store.One(db, `SELECT * FROM ign_force_remove WHERE instance_id = ? AND flag = 1`, instanceID)
	if err != nil {
		return err
	}
	if apply == nil {
		return nil // 非本域审批实例
	}
	applyID := toInt(apply["id"])
	executorID := toInt(apply["executor_id"])
	now := ignNow()
	if !approved {
		// FUNC-33 驳回:执行机状态不变,意见留痕
		_, err = store.Exec(db, `
			UPDATE ign_force_remove SET status='已驳回', approve_comment=?, approved_by=?, approved_name=?, approved_at=?,
			updated_by=?, updated_at=? WHERE id=?`,
			comment, user.ID, user.Username, now, user.ID, now, applyID)
		return err
	}
	// FUNC-32 通过:R-08 取消全部运行中任务(等待至多 30 秒的等待窗以回执驱动,此处先置 CANCELLING 并留痕)
	tasks, err := store.List(db, `
		SELECT id, task_no FROM exec_task WHERE executor_id = ? AND flag = 1 AND task_status = 'RUNNING'`, executorID)
	if err != nil {
		return err
	}
	cancelled := 0
	var cancelledNos []string
	for _, t := range tasks {
		if _, err := store.Exec(db, `
			UPDATE exec_task SET task_status='CANCELLING', updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`, t["id"]); err != nil {
			return err
		}
		cancelled++
		cancelledNos = append(cancelledNos, Str(t["task_no"]))
	}
	// R-07 拆除链(吊销双证书 → 凭据置已吊销 → 状态置已注销;步骤失败告警不阻塞,残留入注销报告)
	exec, err := store.One(db, `SELECT * FROM executor WHERE id = ? AND flag = 1`, executorID)
	if err != nil {
		return err
	}
	if exec == nil {
		return Fail(errcode.ErrState, "目标执行机已不存在，无法执行拆除链")
	}
	removalReport := map[string]any{
		"trigger":       "强制注销审批通过（FLOW-PROC-APP-01）",
		"apply_no":      Str(apply["apply_no"]),
		"cancelled":     cancelled,
		"cancelled_ids": cancelledNos,
		"wait_note":     "取消等待窗至多 30 秒:代理回执 CANCELLED 后计数递减(R-05);超时未回执的任务在对账任务中显式置败（R-09,原因分列）",
		"residuals":     []string{"O-CRT 证书吊销通道未接入:绑定已置 REVOKED,O-CRT 侧吊销须人工补偿(跨域留痕,不静默)"},
	}
	if err := igniteTearDownChain(db, exec, user, "强制注销（审批通过 "+Str(apply["apply_no"])+"）"); err != nil {
		return err
	}
	report := MarshalJSONVal(removalReport)
	if _, err := store.Exec(db, `
		UPDATE ign_force_remove SET status='已注销', approve_comment=?, approved_by=?, approved_name=?, approved_at=?,
		removal_report=?, updated_by=?, updated_at=? WHERE id=?`,
		comment, user.ID, user.Username, now, report, user.ID, now, applyID); err != nil {
		return err
	}
	if _, err := store.Exec(db, `
		INSERT INTO ign_reconcile_log (event_type, object_type, object_no, local_value, remote_value, trigger_reason, operator_id, operator_name, created_by)
		VALUES ('STATUS_FIX', 'EXECUTOR', ?, ?, 'REMOVED', ?, ?, ?, ?)`,
		Str(exec["executor_no"]), Str(exec["status"]),
		fmt.Sprintf("R-08 强制注销拆除链完成,取消运行任务 %d 个", cancelled), user.ID, user.Username, user.ID); err != nil {
		return err
	}
	publishIgnEvent("executor", "force_removed", map[string]any{
		"apply_no": Str(apply["apply_no"]), "executor_no": Str(exec["executor_no"]), "cancelled": cancelled,
	})
	return nil
}
