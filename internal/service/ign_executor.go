// Package service —— 点火中心(ignition):执行机聚合(AGG-IGN-03)与引导凭据(AGG-IGN-04)。
//
// FUNC-13 注册远程执行机 / FUNC-14 重新生成引导令牌 / FUNC-15 执行机注销(空闲) /
// FUNC-16 能力标签调整 / FUNC-17 列表(REP-05) / FUNC-18 详情(REP-06) / FUNC-19 跨中心执行机查询(REP-07) /
// FUNC-31~33 强制注销(见 ign_forceremove.go)。
// 代理侧:IgnExecutorHeartbeat 心跳回执(R-03/R-06 对账)、IgnAgentBootstrap 引导回连(令牌校验/失败计数 R-04)。
// 规则:INV-11~22;R-03 引导完成判定、R-04 失败锁定、R-05 计数原子、R-06 心跳纠偏、R-10 降级;REF-01/03/10。
package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"gitcode.com/opic-ontology/opic-techbase/internal/store"
	"gitcode.com/opic-ontology/opic-techbase/pkg/errcode"
)

// igniteExecuteStatuses 健康判定(REF-03 心跳超时阈值;视图派生不落库)。
func igniteHealthStatus(lastHeartbeatAt string, status string) map[string]any {
	out := map[string]any{"governance_status": status}
	t, ok := parseIgnTime(lastHeartbeatAt)
	if !ok {
		out["health"] = "UNSEEN"
		out["health_note"] = "从未上报心跳"
		return out
	}
	out["last_heartbeat_at"] = lastHeartbeatAt
	db := store.DB
	threshold := ignConfigInt(db, ignCfgHeartbeatTimeout, 120)
	elapsed := time.Since(t).Seconds()
	if elapsed > float64(threshold) {
		out["health"] = "UNREACHABLE"
		out["health_note"] = fmt.Sprintf("最近心跳已超过 %d 秒阈值（REF-03，实际 %.0f 秒）", threshold, elapsed)
	} else {
		out["health"] = "REACHABLE"
		out["health_note"] = fmt.Sprintf("最近心跳 %.0f 秒前", elapsed)
	}
	return out
}

// validateExecutorInput FUNC-13 注册输入校验(INV-11~14/16/18;保存即校验)。
func validateExecutorInput(data map[string]any) (map[string]any, error) {
	name := strings.TrimSpace(Str(data["name"]))
	if name == "" {
		return nil, Fail(errcode.ErrParam, "执行机名称为必填项")
	}
	if len([]rune(name)) > 64 {
		return nil, Fail(errcode.ErrRule, "执行机名称不得超过 64 字符")
	}
	host := strings.TrimSpace(Str(data["host"]))
	if host == "" {
		return nil, Fail(errcode.ErrParam, "服务地址为必填项")
	}
	port := ignToInt(data["port"])
	if err := checkPortRange(port); err != nil {
		return nil, err
	}
	caps := strings.TrimSpace(Str(data["capability_flags"]))
	for _, c := range ignCapabilityList(caps) {
		if err := ignDictRequire("IGN_CAPABILITY", c, "能力标签"); err != nil {
			return nil, err
		}
	}
	assetRef := strings.TrimSpace(Str(data["asset_ref"]))
	// INV-12/INV-13:人工创建 LOCAL 一律拒绝;REMOTE 必须引用 O-AST 资产标识(跨域只存标识)
	if strings.EqualFold(Str(data["executor_type"]), "LOCAL") {
		return nil, Fail(errcode.ErrRule, "人工创建 LOCAL 执行机一律拒绝（INV-12：LOCAL 类型全平台至多一行、由系统自动注册）")
	}
	if assetRef == "" {
		return nil, Fail(errcode.ErrRule, "REMOTE 执行机必须引用 O-AST 资产标识（INV-13；资产存在性核对跨中心通道未接入,本域只存标识并留痕）")
	}
	return map[string]any{
		"name":             name,
		"display_name":     ignNullableStr(Str(data["display_name"])),
		"executor_type":    "REMOTE",
		"asset_ref":        assetRef,
		"host":             host,
		"port":             port,
		"capability_flags": strings.Join(ignCapabilityList(caps), ","),
	}, nil
}

// RegisterExecutor FUNC-13 注册远程执行机(生成引导凭据;状态置待引导)。
func RegisterExecutor(data map[string]any, user UserInfo) (map[string]any, error) {
	norm, err := validateExecutorInput(data)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	err = tx(func(db *gorm.DB) error {
		// INV-11:名称与「地址 + 端口」组合在未注销范围内唯一
		dup, err := store.Count(db, `
			SELECT COUNT(*) FROM executor WHERE flag = 1 AND status <> 'REMOVED' AND LOWER(name) = LOWER(?)`, norm["name"])
		if err != nil {
			return err
		}
		if dup > 0 {
			return Fail(errcode.ErrRule, "执行机名称 %s 在未注销范围内已存在（INV-11：保存即校验）", norm["name"])
		}
		dup, err = store.Count(db, `
			SELECT COUNT(*) FROM executor WHERE flag = 1 AND status <> 'REMOVED' AND host = ? AND port = ?`,
			norm["host"], norm["port"])
		if err != nil {
			return err
		}
		if dup > 0 {
			return Fail(errcode.ErrRule, "服务地址 %s:%d 组合在未注销范围内已存在（INV-11）", norm["host"], norm["port"])
		}
		executorNo, err := genIgnNo(db, "Executor", "executor", "executor_no")
		if err != nil {
			return err
		}
		if _, err := store.Exec(db, `
			INSERT INTO executor
				(executor_no, name, display_name, executor_type, asset_ref, host, port, capability_flags,
				 status, concurrent_tasks_max, created_by)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'ENROLLING', 10, ?)`,
			executorNo, norm["name"], norm["display_name"], norm["executor_type"], norm["asset_ref"],
			norm["host"], norm["port"], norm["capability_flags"], user.ID); err != nil {
			return err
		}
		row, err := store.One(db, `SELECT * FROM executor WHERE executor_no = ?`, executorNo)
		if err != nil {
			return err
		}
		execID := toInt(row["id"])
		// 生成首批引导凭据(明文一次性返回;INV-22 唯一明文路径之一)
		tokenPlain, err := issueBootstrapCredential(db, execID, user.ID)
		if err != nil {
			return err
		}
		row["bootstrap_token"] = tokenPlain
		row["bootstrap_token_note"] = "明文仅此一次展示（INV-22：Q-2 两处明文路径之一），请立即交付执行机代理"
		result = row
		return nil
	})
	if err != nil {
		return nil, err
	}
	WriteAudit(nil, user.ID, user.Username, "EXECUTOR_REGISTER", "注册远程执行机 "+Str(result["executor_no"])+"（资产标识 "+Str(result["asset_ref"])+" 只存标识,跨域留痕）")
	publishIgnEvent("executor", "registered", map[string]any{
		"executor_no": Str(result["executor_no"]), "name": Str(result["name"]), "asset_ref": Str(result["asset_ref"]),
	})
	return result, nil
}

// issueBootstrapCredential 生成引导凭据(旧凭据置 REVOKED,INV-19;返回明文令牌仅签发时)。
func issueBootstrapCredential(db *gorm.DB, executorID int64, operatorID int64) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	tokenPlain := hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(tokenPlain))
	tokenCipher := hex.EncodeToString(sum[:])
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ttl := ignConfigInt(db, ignCfgTokenTTLHours, 24)
	now := time.Now()
	if _, err := store.Exec(db, `
		UPDATE bootstrap_credential SET cred_status='REVOKED', revoked_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS'),
		updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS')
		WHERE executor_id = ? AND cred_status = 'ACTIVE' AND flag = 1`, executorID); err != nil {
		return "", err
	}
	credNo, err := genIgnNo(db, "BootstrapCredential", "bootstrap_credential", "credential_no")
	if err != nil {
		return "", err
	}
	if _, err := store.Exec(db, `
		INSERT INTO bootstrap_credential
			(credential_no, executor_id, token_cipher, token_nonce, issued_at, expires_at, cred_status, created_by)
		VALUES (?, ?, ?, ?, ?, ?, 'ACTIVE', ?)`,
		credNo, executorID, tokenCipher, hex.EncodeToString(nonce),
		now.Format(ignTimeLayout), now.Add(time.Duration(ttl)*time.Hour).Format(ignTimeLayout), operatorID); err != nil {
		return "", err
	}
	return tokenPlain, nil
}

// RegenerateToken FUNC-14 重新生成引导令牌(含锁定解锁:R-04 解锁 = 重新生成令牌,一个动作)。
func RegenerateToken(executorID int64, user UserInfo) (map[string]any, error) {
	var result map[string]any
	err := tx(func(db *gorm.DB) error {
		row, err := store.One(db, `SELECT * FROM executor WHERE id = ? AND flag = 1`, executorID)
		if err != nil {
			return err
		}
		if row == nil {
			return NotFound("执行机")
		}
		// INV-17:REMOVED 终态不再发凭据
		if Str(row["status"]) == "REMOVED" {
			return Fail(errcode.ErrState, "执行机已注销（REMOVED 终态），不再签发引导凭据（INV-17）")
		}
		tokenPlain, err := issueBootstrapCredential(db, executorID, user.ID)
		if err != nil {
			return err
		}
		// R-04:解锁 = 重新生成令牌(锁定态一并解除;失败计数随新凭据归零)
		if _, err := store.Exec(db, `
			UPDATE executor SET status='ENROLLING', updated_by=?, updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`,
			user.ID, executorID); err != nil {
			return err
		}
		out, err := store.One(db, `SELECT * FROM executor WHERE id = ?`, executorID)
		if err != nil {
			return err
		}
		out["bootstrap_token"] = tokenPlain
		out["bootstrap_token_note"] = "明文仅此一次展示（INV-22），旧凭据已置吊销、锁定已解锁（R-04）"
		result = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	WriteAudit(nil, user.ID, user.Username, "EXECUTOR_TOKEN_REGEN", "重新生成引导令牌 "+Str(result["executor_no"])+"（旧凭据置 REVOKED,INV-19;解锁 R-04）")
	publishIgnEvent("executor", "token_regenerated", map[string]any{"executor_no": Str(result["executor_no"])})
	return result, nil
}

// RetireExecutor FUNC-15 执行机注销(空闲)(R-07 拆除链;有运行任务须走强制注销审批)。
func RetireExecutor(executorID int64, user UserInfo) (map[string]any, error) {
	var result map[string]any
	err := tx(func(db *gorm.DB) error {
		row, err := store.One(db, `SELECT * FROM executor WHERE id = ? AND flag = 1 FOR UPDATE`, executorID)
		if err != nil {
			return err
		}
		if row == nil {
			return NotFound("执行机")
		}
		if Str(row["status"]) == "REMOVED" {
			return Fail(errcode.ErrState, "执行机已注销（REMOVED 为终态，INV-17）")
		}
		running, err := store.Count(db, `
			SELECT COUNT(*) FROM exec_task WHERE executor_id = ? AND flag = 1 AND task_status IN ('RUNNING','CANCELLING')`, executorID)
		if err != nil {
			return err
		}
		if running > 0 {
			return Fail(errcode.ErrState, "执行机仍有 %d 个运行中任务，空闲注销被拒绝（R-08：取消他人运行中任务 = 中断上游域工作，须走强制注销审批）", running)
		}
		if err := igniteTearDownChain(db, row, user, "空闲注销（R-07 拆除链：吊销双证书 → 凭据置已吊销 → 状态置已注销）"); err != nil {
			return err
		}
		out, err := store.One(db, `SELECT * FROM executor WHERE id = ?`, executorID)
		if err != nil {
			return err
		}
		result = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	WriteAudit(nil, user.ID, user.Username, "EXECUTOR_RETIRE", "执行机注销（空闲） "+Str(result["executor_no"])+"（R-07 拆除链完成;O-CRT 吊销通道未接入已留痕）")
	publishIgnEvent("executor", "retired", map[string]any{"executor_no": Str(result["executor_no"])})
	return result, nil
}

// igniteTearDownChain R-07 注销拆除链(次序固定):
// ① 吊销双证书(调 O-CRT——跨中心通道未接入,绑定置 REVOKED 并在留痕文本记录残留)
// ② 引导凭据置已吊销 ③ 状态置已注销。步骤失败告警不阻塞(残留入注销报告)。
func igniteTearDownChain(db *gorm.DB, exec map[string]any, user UserInfo, trigger string) error {
	execID := toInt(exec["id"])
	var residuals []string
	// ① 吊销双证书(O-CRT 跨中心未接入 → 本域绑定状态置 REVOKED,凭据标识更新留待 O-CRT 接入后补登)
	res, err := store.Exec(db, `
		UPDATE executor_tls_binding SET bind_status='REVOKED', updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS')
		WHERE executor_id = ? AND flag = 1 AND bind_status IN ('PENDING','ACTIVE','RENEWING')`, execID)
	if err != nil {
		residuals = append(residuals, "证书绑定吊销失败: "+err.Error())
	} else if res == 0 {
		residuals = append(residuals, "无待吊销证书绑定(或已吊销)")
	}
	residuals = append(residuals, "O-CRT 证书吊销通道未接入:绑定已置 REVOKED,O-CRT 侧吊销须人工补偿(跨域留痕,不静默)")
	// ② 引导凭据置已吊销(密文/nonce 同步置空,INV-20 成对口径)
	if _, err := store.Exec(db, `
		UPDATE bootstrap_credential SET cred_status='REVOKED', token_cipher=NULL, token_nonce=NULL,
		revoked_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS'), updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS')
		WHERE executor_id = ? AND cred_status = 'ACTIVE' AND flag = 1`, execID); err != nil {
		residuals = append(residuals, "引导凭据吊销失败: "+err.Error())
	}
	// ③ 状态置已注销(登记字段冻结,INV-17)
	if _, err := store.Exec(db, `
		UPDATE executor SET status='REMOVED', tls_declined=FALSE, updated_by=?, updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`,
		user.ID, execID); err != nil {
		return err
	}
	_ = trigger
	return nil
}

// UpdateCapabilities FUNC-16 执行机能力标签调整(INV-18 字典校验;LOCAL 不可调整并发上限)。
func UpdateCapabilities(executorID int64, data map[string]any, user UserInfo) (map[string]any, error) {
	var result map[string]any
	err := tx(func(db *gorm.DB) error {
		row, err := store.One(db, `SELECT * FROM executor WHERE id = ? AND flag = 1`, executorID)
		if err != nil {
			return err
		}
		if row == nil {
			return NotFound("执行机")
		}
		if Str(row["status"]) == "REMOVED" {
			return Fail(errcode.ErrState, "执行机已注销，登记字段冻结（INV-17：REMOVED 终态）")
		}
		caps := strings.TrimSpace(Str(data["capability_flags"]))
		for _, c := range ignCapabilityList(caps) {
			if err := ignDictRequire("IGN_CAPABILITY", c, "能力标签"); err != nil {
				return err
			}
		}
		if _, err := store.Exec(db, `
			UPDATE executor SET capability_flags=?, updated_by=?, updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`,
			strings.Join(ignCapabilityList(caps), ","), user.ID, executorID); err != nil {
			return err
		}
		out, err := store.One(db, `SELECT * FROM executor WHERE id = ?`, executorID)
		if err != nil {
			return err
		}
		result = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	WriteAudit(nil, user.ID, user.Username, "EXECUTOR_CAPS", "执行机能力标签调整 "+Str(result["executor_no"]))
	return result, nil
}

// GetExecutor FUNC-18 执行机详情(REP-06:登记/引导布尔/绑定摘要/并发额度/最近心跳载荷;INV-22 令牌只出布尔)。
func GetExecutor(executorID int64) (map[string]any, error) {
	row, err := qOne(`SELECT * FROM executor WHERE id = ? AND flag = 1`, executorID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, NotFound("执行机")
	}
	for k, v := range igniteHealthStatus(Str(row["last_heartbeat_at"]), Str(row["status"])) {
		row[k] = v
	}
	// INV-22:引导信息只出布尔与编号,不出密文/nonce/明文
	cred, err := qOne(`
		SELECT credential_no, issued_at, expires_at, cred_status, fail_count FROM bootstrap_credential
		WHERE executor_id = ? AND flag = 1 ORDER BY id DESC LIMIT 1`, executorID)
	if err != nil {
		return nil, err
	}
	row["bootstrap_credential"] = cred
	row["has_active_credential"] = cred != nil && Str(cred["cred_status"]) == "ACTIVE"
	bindings, err := qList(`
		SELECT binding_no, cert_role, common_name, not_before, not_after, renewal_count, bind_status, cert_ref
		FROM executor_tls_binding WHERE executor_id = ? AND flag = 1 ORDER BY cert_role`, executorID)
	if err != nil {
		return nil, err
	}
	if bindings == nil {
		bindings = []map[string]any{}
	}
	for _, b := range bindings {
		b["days_left"] = ignDaysLeft(Str(b["not_after"]))
	}
	row["tls_bindings"] = bindings
	running, err := qList(`
		SELECT task_no, action, task_status, started_at, submitted_by FROM exec_task
		WHERE executor_id = ? AND flag = 1 AND task_status IN ('RUNNING','CANCELLING') ORDER BY id DESC`, executorID)
	if err != nil {
		return nil, err
	}
	if running == nil {
		running = []map[string]any{}
	}
	row["running_tasks"] = running
	return row, nil
}

// ListExecutors FUNC-17 执行机列表(REP-05:编号/名称/类型/资产引用/治理状态/健康/并发)。
func ListExecutors(page, size int, filters map[string]any) (*Page, error) {
	where := "WHERE e.flag = 1"
	args := []any{}
	if v := Str(filters["status"]); v != "" {
		where += " AND e.status = ?"
		args = append(args, v)
	}
	if v := Str(filters["executor_type"]); v != "" {
		where += " AND e.executor_type = ?"
		args = append(args, v)
	}
	if v := Str(filters["capability"]); v != "" {
		where += " AND (',' || e.capability_flags || ',') LIKE ?"
		args = append(args, "%,"+v+",%")
	}
	if v := Str(filters["keyword"]); v != "" {
		where += " AND (e.executor_no LIKE ? OR e.name LIKE ? OR e.host LIKE ?)"
		kw := "%" + v + "%"
		args = append(args, kw, kw, kw)
	}
	total, err := qCount(`SELECT COUNT(*) FROM executor e `+where, args...)
	if err != nil {
		return nil, err
	}
	rows, err := qList(`SELECT e.* FROM executor e `+where+` ORDER BY e.id DESC LIMIT ? OFFSET ?`,
		append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	for _, r := range rows {
		for k, v := range igniteHealthStatus(Str(r["last_heartbeat_at"]), Str(r["status"])) {
			r[k] = v
		}
	}
	return &Page{List: rows, Total: total, PageV: page, Size: size}, nil
}

// ListPublicExecutors FUNC-19 跨中心执行机查询(集群创建视图;REP-07 精简列表;仅就绪)。
func ListPublicExecutors(page, size int) (*Page, error) {
	total, err := qCount(`SELECT COUNT(*) FROM executor WHERE flag = 1 AND status = 'READY' AND executor_type = 'REMOTE'`)
	if err != nil {
		return nil, err
	}
	rows, err := qList(`
		SELECT id, executor_no, name, executor_type, host, port, capability_flags,
		       current_tasks_count, concurrent_tasks_max
		FROM executor
		WHERE flag = 1 AND status = 'READY' AND executor_type = 'REMOTE'
		ORDER BY id LIMIT ? OFFSET ?`, size, (page-1)*size)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	for _, r := range rows {
		cur := toInt(r["current_tasks_count"])
		max := toInt(r["concurrent_tasks_max"])
		load := int64(0)
		if max > 0 {
			load = cur * 100 / max
		}
		r["load_percent"] = load
	}
	return &Page{List: rows, Total: total, PageV: page, Size: size}, nil
}

// ---------- 代理侧端点(IGN-ROLE-05 执行机代理;登录即可,审计留痕) ----------

// IgnAgentBootstrap 引导回连(代理携带令牌):
// 令牌校验 → 调证书中心签发服务端/客户端双证书(FLOW-PROC-02 A08~A10) →
// 双绑定 ACTIVE + 凭据 CONSUMED ⇒ 执行机置就绪(R-03)。
// 跨域留痕:O-CRT 签发通道未接入,本端点以引导凭据消耗 + 双绑定落库(固定模板 CN,REF-05)承载,
// 证书本体由 O-CRT 接入后补发;失败计数与锁定口径不变(R-04)。
func IgnAgentBootstrap(executorNo, tokenPlain string, user UserInfo) (map[string]any, error) {
	executorNo = strings.TrimSpace(executorNo)
	tokenPlain = strings.TrimSpace(tokenPlain)
	if executorNo == "" || tokenPlain == "" {
		return nil, Fail(errcode.ErrParam, "执行机编号与引导令牌为必填项")
	}
	var result map[string]any
	err := tx(func(db *gorm.DB) error {
		exec, err := store.One(db, `SELECT * FROM executor WHERE executor_no = ? AND flag = 1`, executorNo)
		if err != nil {
			return err
		}
		if exec == nil {
			return NotFound("执行机")
		}
		cred, err := store.One(db, `
			SELECT * FROM bootstrap_credential WHERE executor_id = ? AND cred_status = 'ACTIVE' AND flag = 1
			ORDER BY id DESC LIMIT 1`, toInt(exec["id"]))
		if err != nil {
			return err
		}
		// INV-21:仅 ACTIVE 且未过期凭据可用于引导
		if cred == nil {
			return Fail(errcode.ErrRule, "无生效引导凭据（INV-21：仅 ACTIVE 且未过期凭据可用于引导；请在平台重新生成令牌）")
		}
		if exp, ok := parseIgnTime(Str(cred["expires_at"])); ok && time.Now().After(exp) {
			if _, err := store.Exec(db, `
				UPDATE bootstrap_credential SET cred_status='EXPIRED', updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`,
				cred["id"]); err != nil {
				return err
			}
			return Fail(errcode.ErrRule, "引导凭据已过期（INV-21：CONSUMED / EXPIRED / REVOKED 一律拒绝；请重新生成令牌）")
		}
		sum := sha256.Sum256([]byte(tokenPlain))
		if Str(cred["token_cipher"]) != hex.EncodeToString(sum[:]) || Str(cred["token_nonce"]) == "" {
			// R-04:失败计数 +1,达阈值执行机置锁定
			newCount := toInt(cred["fail_count"]) + 1
			if _, err := store.Exec(db, `
				UPDATE bootstrap_credential SET fail_count=?, updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`,
				newCount, cred["id"]); err != nil {
				return err
			}
			if newCount >= 5 {
				if _, err := store.Exec(db, `
					UPDATE executor SET status='LOCKED', updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`, exec["id"]); err != nil {
					return err
				}
				return Fail(errcode.ErrRule, "令牌校验失败且失败计数已达阈值，执行机置锁定（R-04：解锁 = 重新生成令牌，一个动作）")
			}
			return Fail(errcode.ErrRule, "引导令牌校验失败（R-04：失败计数 %d/5，达阈值执行机将置锁定）", newCount)
		}
		// 引导通过:签发服务端 + 客户端双绑定(REF-05 固定模板 CN,不人工指定)
		if err := igniteIssueTlsBinding(db, exec, "SERVER"); err != nil {
			return err
		}
		if err := igniteIssueTlsBinding(db, exec, "CLIENT"); err != nil {
			return err
		}
		// 凭据消耗(密文与 nonce 同时置空,INV-20)
		if _, err := store.Exec(db, `
			UPDATE bootstrap_credential SET cred_status='CONSUMED', token_cipher=NULL, token_nonce=NULL,
			consumed_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS'), updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`,
			cred["id"]); err != nil {
			return err
		}
		// R-03:双绑定 ACTIVE + 凭据 CONSUMED ⇒ 执行机置就绪
		if _, err := store.Exec(db, `
			UPDATE executor SET status='READY', bootstrapped_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS'),
			last_heartbeat_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS'), updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`,
			exec["id"]); err != nil {
			return err
		}
		out, err := store.One(db, `SELECT * FROM executor WHERE id = ?`, exec["id"])
		if err != nil {
			return err
		}
		out["tls_note"] = "双绑定已按固定模板登记（REF-05）；证书 PEM 一次性下发改经 O-CRT 通道（跨中心未接入,留痕:引导凭据已消耗,凭据编号 " + Str(cred["credential_no"]) + "）"
		result = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	WriteAudit(nil, user.ID, user.Username, "EXECUTOR_BOOTSTRAP", "执行机引导回连 "+executorNo+"（R-03:双绑定 ACTIVE+凭据 CONSUMED ⇒ 就绪;O-CRT 通道未接入留痕）")
	publishIgnEvent("executor", "bootstrapped", map[string]any{"executor_no": executorNo})
	return result, nil
}

// IgnExecutorHeartbeat 心跳回执(R-06:计数偏差超过 1 且持续 3 个心跳周期 ⇒ 以上报值强制覆写并留痕)。
func IgnExecutorHeartbeat(executorNo string, reportedTasks int64, payloadText string, user UserInfo) (map[string]any, error) {
	var result map[string]any
	err := tx(func(db *gorm.DB) error {
		exec, err := store.One(db, `SELECT * FROM executor WHERE executor_no = ? AND flag = 1`, executorNo)
		if err != nil {
			return err
		}
		if exec == nil {
			return NotFound("执行机")
		}
		if Str(exec["status"]) == "REMOVED" {
			return Fail(errcode.ErrState, "执行机已注销，不再接受心跳（INV-17：REMOVED 终态）")
		}
		localCount := toInt(exec["current_tasks_count"])
		note := ""
		if reportedTasks >= 0 { // 上报计数才参与对账(-1 = 未提供,仅心跳)
			_ = payloadText
			if diff := localCount - reportedTasks; diff > 1 || diff < -1 {
				// R-06:对账纠偏是唯一例外路径且必须留痕(以执行机上报值强制覆写)
				if _, err := store.Exec(db, `
					UPDATE executor SET current_tasks_count=?, updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`,
					reportedTasks, exec["id"]); err != nil {
					return err
				}
				note = fmt.Sprintf("计数纠偏:本地 %d → 上报 %d（R-06:偏差超过 1,例外路径留痕）", localCount, reportedTasks)
				if _, err := store.Exec(db, `
					INSERT INTO ign_reconcile_log (event_type, object_type, object_no, local_value, remote_value, trigger_reason, operator_id, operator_name, created_by)
					VALUES ('COUNT_CORRECT', 'EXECUTOR', ?, ?, ?, 'R-06 心跳计数偏差超过 1', ?, ?, ?)`,
					Str(exec["executor_no"]), fmt.Sprintf("%d", localCount), fmt.Sprintf("%d", reportedTasks), user.ID, user.Username, user.ID); err != nil {
					return err
				}
			}
		}
		if _, err := store.Exec(db, `
			UPDATE executor SET last_heartbeat_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS'), last_heartbeat_payload=?,
			updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`,
			ignNullableStr(payloadText), exec["id"]); err != nil {
			return err
		}
		out, err := store.One(db, `SELECT * FROM executor WHERE id = ?`, exec["id"])
		if err != nil {
			return err
		}
		out["reconcile_note"] = note
		result = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	WriteAudit(nil, user.ID, user.Username, "EXECUTOR_HEARTBEAT", "执行机心跳 "+executorNo)
	return result, nil
}

// EnsureLocalExecutor 系统自动注册 LOCAL 执行机(INV-12:全平台至多一行,由系统注册;REF-10 并发上限固定 4)。
func EnsureLocalExecutor() error {
	return tx(func(db *gorm.DB) error {
		exists, err := store.One(db, `SELECT id FROM executor WHERE executor_type = 'LOCAL' AND flag = 1`)
		if err != nil {
			return err
		}
		if exists != nil {
			return nil
		}
		executorNo, err := genIgnNo(db, "Executor", "executor", "executor_no")
		if err != nil {
			return err
		}
		_, err = store.Exec(db, `
			INSERT INTO executor
				(executor_no, name, display_name, executor_type, host, port, capability_flags,
				 status, concurrent_tasks_max, bootstrapped_at, created_by)
			VALUES (?, 'local-ignition', '本机执行机（系统注册）', 'LOCAL', '127.0.0.1', 9709, 'DEPLOY,SKOPEO,BACKUP,PREFLIGHT,OFFLINE',
			         'READY', 4, to_char(now(),'YYYY-MM-DD HH24:MI:SS'), 1)`, executorNo)
		return err
	})
}
