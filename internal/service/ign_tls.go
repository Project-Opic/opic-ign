// Package service —— 点火中心(ignition):通道证书绑定聚合(AGG-IGN-05)。
//
// FUNC-20 手动触发证书续期 / FUNC-21 TLS 显式降级声明 / FUNC-22 证书绑定清单查询(REP-08)。
// 规则:INV-23~27;R-03 引导完成判定(双绑定签发)、R-05/R-07、R-10 服务端证书吊销 → 降级、R-11 降级互斥;REF-05/06/07。
package service

import (
	"fmt"
	"time"

	"gorm.io/gorm"

	"gitcode.com/opic-ontology/opic-techbase/internal/store"
	"gitcode.com/opic-ontology/opic-techbase/pkg/errcode"
)

// ignDaysLeft REF-06:证书剩余天数由生效止即时计算、不落库。
func ignDaysLeft(notAfter string) int64 {
	t, ok := parseIgnTime(notAfter)
	if !ok {
		return 0
	}
	return int64(time.Until(t).Hours() / 24)
}

// igniteIssueTlsBinding 签发证书绑定(REF-05 固定模板 CN:服务端 <名>.exec.local / 客户端 executor-<id>-client)。
func igniteIssueTlsBinding(db *gorm.DB, exec map[string]any, certRole string) error {
	execID := toInt(exec["id"])
	// INV-23:同一执行机同一用途至多一条生效绑定(PENDING/ACTIVE/RENEWING)
	exists, err := store.One(db, `
		SELECT id FROM executor_tls_binding
		WHERE executor_id = ? AND cert_role = ? AND bind_status IN ('PENDING','ACTIVE','RENEWING') AND flag = 1`,
		execID, certRole)
	if err != nil {
		return err
	}
	now := time.Now()
	notBefore := now.AddDate(0, 0, -1)
	notAfter := now.AddDate(1, 0, 0)
	cn := ""
	if certRole == "SERVER" {
		cn = Str(exec["name"]) + ".exec.local"
	} else {
		cn = fmt.Sprintf("executor-%d-client", execID)
	}
	if exists != nil {
		// 续期路径:凭据标识更新伴随续期计数递增(INV-26)
		_, err = store.Exec(db, `
			UPDATE executor_tls_binding SET not_before=?, not_after=?, renewal_count=renewal_count+1,
			last_renewed_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS'), bind_status='ACTIVE',
			updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`,
			notBefore.Format(ignTimeLayout), notAfter.Format(ignTimeLayout), exists["id"])
		return err
	}
	bindingNo, err := genIgnNo(db, "ExecutorTlsBinding", "executor_tls_binding", "binding_no")
	if err != nil {
		return err
	}
	// 跨域留痕:cert_ref 指向 O-CRT 凭据标识;签发通道未接入,先置 PENDING(引导消耗后置 ACTIVE 由 R-03 判定)
	certRef := "OCRT-PENDING-" + bindingNo
	_, err = store.Exec(db, `
		INSERT INTO executor_tls_binding
			(binding_no, executor_id, cert_role, cert_ref, common_name, not_before, not_after, bind_status, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'ACTIVE', ?)`,
		bindingNo, execID, certRole, certRef, cn,
		notBefore.Format(ignTimeLayout), notAfter.Format(ignTimeLayout), toInt(exec["created_by"]))
	return err
}

// RenewTlsBinding FUNC-20 手动触发证书续期(请求证书中心重签 → 更新事实快照,续期计数 +1,INV-26)。
func RenewTlsBinding(bindingID int64, user UserInfo) (map[string]any, error) {
	var result map[string]any
	err := tx(func(db *gorm.DB) error {
		row, err := store.One(db, `SELECT * FROM executor_tls_binding WHERE id = ? AND flag = 1`, bindingID)
		if err != nil {
			return err
		}
		if row == nil {
			return NotFound("证书绑定")
		}
		// INV-25:REVOKED 为终态,重新启用 = 新建绑定(引导/重签发路径)
		if Str(row["bind_status"]) == "REVOKED" {
			return Fail(errcode.ErrState, "该绑定已吊销（INV-25：REVOKED 为终态；重新启用须通过重新引导签发新绑定）")
		}
		exec, err := store.One(db, `SELECT * FROM executor WHERE id = ? AND flag = 1`, toInt(row["executor_id"]))
		if err != nil {
			return err
		}
		if exec == nil {
			return NotFound("执行机（绑定所属执行机已删除）")
		}
		if Str(exec["status"]) == "REMOVED" {
			return Fail(errcode.ErrState, "执行机已注销，其证书绑定不可续期（INV-17：REMOVED 终态）")
		}
		if err := igniteIssueTlsBinding(db, exec, Str(row["cert_role"])); err != nil {
			return err
		}
		out, err := store.One(db, `SELECT * FROM executor_tls_binding WHERE id = ?`, bindingID)
		if err != nil {
			return err
		}
		out["renew_note"] = "重签请求已登记（O-CRT 跨中心通道未接入:绑定事实快照已更新、续期计数 +1;证书本体热加载推送须待通道接入,如实留痕不静默）"
		result = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	WriteAudit(nil, user.ID, user.Username, "TLS_RENEW", "手动触发证书续期 "+Str(result["binding_no"])+"（INV-26:续期计数只增不减）")
	publishIgnEvent("tls", "renewed", map[string]any{"binding_no": Str(result["binding_no"])})
	return result, nil
}

// DeclineTls FUNC-21 TLS 显式降级声明(实验环境;R-11/INV-27:降级声明 ⇔ 无任何证书绑定,互斥双写禁止)。
func DeclineTls(executorID int64, user UserInfo) (map[string]any, error) {
	var result map[string]any
	err := tx(func(db *gorm.DB) error {
		exec, err := store.One(db, `SELECT * FROM executor WHERE id = ? AND flag = 1`, executorID)
		if err != nil {
			return err
		}
		if exec == nil {
			return NotFound("执行机")
		}
		if Str(exec["status"]) == "REMOVED" {
			return Fail(errcode.ErrState, "执行机已注销，不可变更降级声明（INV-17）")
		}
		// INV-27:存在绑定的执行机不得声明降级(互斥口径)
		bindings, err := store.Count(db, `
			SELECT COUNT(*) FROM executor_tls_binding
			WHERE executor_id = ? AND flag = 1 AND bind_status IN ('PENDING','ACTIVE','RENEWING')`, executorID)
		if err != nil {
			return err
		}
		if bindings > 0 {
			return Fail(errcode.ErrRule, "执行机存在 %d 条生效证书绑定，不得声明 TLS 降级（INV-27/R-11：降级声明与证书绑定互斥，双写禁止；请先吊销全部绑定）", bindings)
		}
		if boolOfAny(exec["tls_declined"]) {
			// 再次声明 = 撤销降级(恢复)
			if _, err := store.Exec(db, `
				UPDATE executor SET tls_declined=FALSE, updated_by=?, updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`,
				user.ID, executorID); err != nil {
				return err
			}
			out, err := store.One(db, `SELECT * FROM executor WHERE id = ?`, executorID)
			if err != nil {
				return err
			}
			result = out
			return nil
		}
		if _, err := store.Exec(db, `
			UPDATE executor SET tls_declined=TRUE, updated_by=?, updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`,
			user.ID, executorID); err != nil {
			return err
		}
		out, err := store.One(db, `SELECT * FROM executor WHERE id = ?`, executorID)
		if err != nil {
			return err
		}
		out["decline_note"] = "TLS 已显式禁用（实验环境专用；声明 + 审计 + 界面红标,R-11）"
		result = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	action := "声明降级"
	if !boolOfAny(result["tls_declined"]) {
		action = "撤销降级"
	}
	WriteAudit(nil, user.ID, user.Username, "TLS_DECLINE", "TLS 显式降级声明变更："+action+" "+Str(result["executor_no"]))
	publishIgnEvent("tls", "decline_changed", map[string]any{
		"executor_no": Str(result["executor_no"]), "tls_declined": boolOfAny(result["tls_declined"]),
	})
	return result, nil
}

// ListTlsBindings FUNC-22 证书绑定清单查询(REP-08;含临期视图)。
func ListTlsBindings(page, size int, filters map[string]any) (*Page, error) {
	where := "WHERE b.flag = 1"
	args := []any{}
	if v := ignToInt(filters["executor_id"]); v != 0 {
		where += " AND b.executor_id = ?"
		args = append(args, v)
	}
	if v := Str(filters["cert_role"]); v != "" {
		where += " AND b.cert_role = ?"
		args = append(args, v)
	}
	if v := Str(filters["bind_status"]); v != "" {
		where += " AND b.bind_status = ?"
		args = append(args, v)
	}
	if v := Str(filters["expiring"]); v == "true" || v == "1" {
		// 临期视图:REF-07 提前天数内生效止将到
		days := ignConfigInt(store.DB, ignCfgCertRenewDays, 14)
		where += " AND b.not_after::timestamp < (now() + (? || ' days')::interval)"
		args = append(args, fmt.Sprintf("%d", days))
	}
	if v := Str(filters["keyword"]); v != "" {
		where += " AND (b.binding_no LIKE ? OR b.common_name LIKE ? OR e.name LIKE ? OR e.executor_no LIKE ?)"
		kw := "%" + v + "%"
		args = append(args, kw, kw, kw, kw)
	}
	total, err := qCount(`
		SELECT COUNT(*) FROM executor_tls_binding b JOIN executor e ON e.id = b.executor_id `+where, args...)
	if err != nil {
		return nil, err
	}
	rows, err := qList(`
		SELECT b.*, e.executor_no, e.name AS executor_name, e.tls_declined
		FROM executor_tls_binding b JOIN executor e ON e.id = b.executor_id
		`+where+` ORDER BY b.not_after ASC LIMIT ? OFFSET ?`,
		append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	for _, r := range rows {
		r["days_left"] = ignDaysLeft(Str(r["not_after"]))
	}
	return &Page{List: rows, Total: total, PageV: page, Size: size}, nil
}

// igniteRevokeServerBinding R-10:服务端证书被吊销 ⇒ 执行机置降级态(拒绝新任务分发、运行中任务保护)。
func igniteRevokeServerBinding(db *gorm.DB, executorID int64, reason string) error {
	res, err := store.Exec(db, `
		UPDATE executor_tls_binding SET bind_status='REVOKED', updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS')
		WHERE executor_id = ? AND cert_role = 'SERVER' AND flag = 1 AND bind_status IN ('PENDING','ACTIVE','RENEWING')`, executorID)
	if err != nil {
		return err
	}
	if res > 0 {
		if _, err := store.Exec(db, `
			UPDATE executor SET status='DEGRADED', updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=? AND status <> 'REMOVED'`, executorID); err != nil {
			return err
		}
		if _, err := store.Exec(db, `
			INSERT INTO ign_reconcile_log (event_type, object_type, object_no, local_value, remote_value, trigger_reason, operator_id, created_by)
			VALUES ('STATUS_FIX', 'EXECUTOR', (SELECT executor_no FROM executor WHERE id = ?), 'READY', 'DEGRADED', ?, 1, 1)`,
			executorID, "R-10 服务端证书吊销 → 降级:"+reason); err != nil {
			return err
		}
	}
	return nil
}
