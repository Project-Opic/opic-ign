// Package service —— AI 智能助理（点火中心扩展:本体注册表注入 + 只读 SQL 工具）。
//
// 口径：LLM 优先（管理控制台配置启用时，OpenAI 兼容协议），失败/未配置降级为
// 规则式应答（待办/版本/执行机/任务/TLS/指引，全部走既有只读查询）。
// 扩展点（ontology-driven-dev 阶段三）：
//  1. 本体注入:system 提示词注入 models/ 七模型的聚合/行为/规则/表白名单摘要;
//  2. 只读 SQL 工具:AssistantRunReadOnlySQL —— 仅 SELECT、仅白名单表、强制 LIMIT、写审计。
//     白名单刻意不含 bootstrap_credential（INV-22:令牌密文/nonce/本体不得出现在任何查询响应）。
package service

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"

	"gitcode.com/opic-ontology/opic-techbase/internal/pkg/ontology"
	"gitcode.com/opic-ontology/opic-techbase/internal/store"
	"gitcode.com/opic-ontology/opic-techbase/pkg/errcode"
	"gitcode.com/opic-ontology/opic-techbase/pkg/llmcfg"
)

// AssistantReply 助理应答。
type AssistantReply struct {
	Answer string   `json:"answer"`
	Hints  []string `json:"hints"`  // 建议追问
	Action string   `json:"action"` // 建议跳转路径(可空)
}

// ontologyContext 本体注册表摘要(注入 system 提示词;对齐 AI 原生应用技术架构第 7 章)。
func ontologyContext() string {
	reg := ontology.Load()
	var b strings.Builder
	b.WriteString("【交付执行域（点火中心）本体摘要】\n聚合根与数据表:\n")
	aggs := []string{"AGG-IGN-01", "AGG-IGN-02", "AGG-IGN-03", "AGG-IGN-04", "AGG-IGN-05", "AGG-IGN-06"}
	for _, id := range aggs {
		if a, ok := reg.Aggregates[id]; ok {
			fmt.Fprintf(&b, "- %s %s(%s):", id, a["name"], a["alias"])
			if attrs, ok := a["attributes"].([]any); ok {
				names := []string{}
				for i, at := range attrs {
					if i >= 6 {
						names = append(names, "…")
						break
					}
					if m, ok := at.(map[string]any); ok {
						names = append(names, Str(m["name"]))
					}
				}
				b.WriteString(strings.Join(names, ","))
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("数据表白名单(只读 SQL 可查):delivery_version, image_prepush_item, executor, exec_task, " +
		"executor_tls_binding, ign_force_remove, ign_reconcile_log, ign_platform_config, task_log\n")
	b.WriteString("关键业务规则:INV-01(版本名未归档唯一);INV-04(组件版本组创建后不可改,集群契约);" +
		"INV-05(已归档登记字段冻结);INV-07(版本状态单向迁移);INV-08(同版本内仓库+标签唯一,预推明细由清单派生);" +
		"INV-12(LOCAL 执行机系统注册至多一行,人工创建拒绝);INV-17(REMOVED 终态冻结);" +
		"INV-19(同执行机至多一条 ACTIVE 凭据);INV-22(令牌密文/nonce/本体不出现在任何查询响应——AI 工具不含凭据表);" +
		"INV-23(同执行机同用途至多一条生效绑定);INV-27(R-11 TLS 降级与绑定互斥);" +
		"INV-28(动作白名单)+R-12(白名单外与拼接风险提交=治理拒绝并审计);" +
		"INV-31(防重放:同执行机摘要唯一,重复提交返回原任务);R-04(引导失败达阈值锁定,解锁=重新生成令牌);" +
		"R-05(任务分发与结束驱动执行机计数原子增减,超上限=容量拒绝);R-07(注销拆除链次序固定);" +
		"R-08(强制注销前置=审批通过);R-10(服务端证书吊销 → 降级,拒绝新分发保护运行中)。\n")
	b.WriteString("业务编号:VER/EXE/BCR/TLS/TSK/RMV-YYYYMMDD-NNNN(三位前缀+日期+四位流水)。\n")
	return b.String()
}

// assistantSystemPrompt 系统提示词(本体注入 + 只读边界)。
func assistantSystemPrompt() string {
	return "你是 OPIC 点火中心（ignition-center,中心码 ign,交付执行域）的 AI 智能助理。" +
		"只回答与本系统相关的问题（交付版本/镜像预推/执行机/引导凭据/通道证书/执行任务/对账台账/审批与操作指引）。" +
		"你只能给出文字指引与只读查询,不能执行任何写操作。" + ontologyContext() +
		"当用户询问数据时,可建议其使用只读 SQL 工具（仅 SELECT 白名单表）。" +
		"与系统无关的问题请礼貌拒绝。用中文简洁回答。"
}

// AssistantChat 助理问答入口。accountID=0 → 首个启用账号;model 空 → 账号默认模型;
// temperature<=0 → 账号默认温度。LLM 失败/未配置一律降级规则应答。
func AssistantChat(uid int64, question string, accountID int64, model string, temperature float64) (*AssistantReply, error) {
	q := strings.TrimSpace(question)
	sys := assistantSystemPrompt()

	// LLM 优先（多供应商账号;系统提示词注入本体摘要）
	if acct := pickAccount(accountID); acct != nil {
		if model == "" {
			model = acct.DefaultModelResolve()
		}
		answer, err := llmcfg.ChatAccount(context.Background(), acct, model, temperature, []llmcfg.Message{
			{Role: "system", Content: sys},
			{Role: "user", Content: q},
		})
		if err == nil {
			return &AssistantReply{Answer: answer, Hints: ignHints()}, nil
		}
		log.Printf("[assistant] LLM(%s/%s) 调用失败,降级规则匹配: %v", acct.Name, model, err)
	} else if cfg := llmcfg.Current(); cfg != nil {
		// 兼容旧单行配置(未迁移成账号时)
		answer, err := llmcfg.Chat(context.Background(), cfg, []llmcfg.Message{
			{Role: "system", Content: sys},
			{Role: "user", Content: q},
		})
		if err == nil {
			return &AssistantReply{Answer: answer, Hints: ignHints()}, nil
		}
		log.Printf("[assistant] LLM 调用失败,降级规则匹配: %v", err)
	}

	lq := strings.ToLower(q)
	switch {
	case strings.Contains(q, "待办") || strings.Contains(lq, "todo"):
		tasks, err := WorkbenchTodo(uid, 1, 10)
		if err != nil {
			return nil, err
		}
		var b strings.Builder
		fmt.Fprintf(&b, "您当前有 %d 条待办任务。", tasks.Total)
		for i, t := range tasks.List {
			if i >= 5 {
				break
			}
			fmt.Fprintf(&b, "\n%d. %s（%s / 执行机 %s,提交人 %s）", i+1, t["apply_no"], t["reason"], t["executor_name"], t["creator_name"])
		}
		if tasks.Total > 5 {
			fmt.Fprintf(&b, "\n……其余 %d 条请在「审批中心-我的待办」查看。", tasks.Total-5)
		}
		return &AssistantReply{Answer: b.String(), Action: "/workbench/todo",
			Hints: []string{"强制注销怎么审批？", "现在有多少台就绪执行机？"}}, nil
	case strings.Contains(q, "版本") || strings.Contains(lq, "version"):
		row, err := qOne(`
			SELECT COUNT(*) AS n,
			SUM(CASE WHEN version_status IN ('PREPUSHED','PARTIALLY_PREPUSHED') THEN 1 ELSE 0 END) AS available,
			SUM(CASE WHEN version_status = 'DRAFT' THEN 1 ELSE 0 END) AS draft,
			SUM(CASE WHEN version_status = 'ARCHIVED' THEN 1 ELSE 0 END) AS archived
			FROM delivery_version WHERE flag = 1`)
		if err != nil {
			return nil, err
		}
		return &AssistantReply{
			Answer: fmt.Sprintf("交付版本台账:共 %d 个,其中可被集群选择（已预推/部分预推） %d、草稿 %d、已归档 %d。组件版本组（六项）创建后不可修改(INV-04,集群契约);已归档版本登记字段冻结(INV-05);软删除须已归档且超过平台配置天数(REF-08,缺省 30 天)。",
				Int(row["n"]), Int(row["available"]), Int(row["draft"]), Int(row["archived"])),
			Action: "/ign/version", Hints: ignHints(),
		}, nil
	case strings.Contains(q, "执行机") || strings.Contains(lq, "executor"):
		row, err := qOne(`
			SELECT COUNT(*) AS n,
			SUM(CASE WHEN status='READY' THEN 1 ELSE 0 END) AS ready,
			SUM(CASE WHEN status='ENROLLING' THEN 1 ELSE 0 END) AS enrolling,
			SUM(CASE WHEN status='LOCKED' THEN 1 ELSE 0 END) AS locked,
			SUM(CASE WHEN status='DEGRADED' THEN 1 ELSE 0 END) AS degraded,
			SUM(CASE WHEN status='REMOVED' THEN 1 ELSE 0 END) AS removed
			FROM executor WHERE flag = 1`)
		if err != nil {
			return nil, err
		}
		return &AssistantReply{
			Answer: fmt.Sprintf("执行机台账:共 %d 台,其中就绪 %d、待引导 %d、已锁定 %d、已降级 %d、已注销 %d。LOCAL 执行机由系统注册且全平台至多一行(INV-12);锁定解锁=重新生成令牌(R-04);降级态拒绝新任务分发、运行中任务保护(R-10);REMOVED 终态冻结(INV-17)。",
				Int(row["n"]), Int(row["ready"]), Int(row["enrolling"]), Int(row["locked"]), Int(row["degraded"]), Int(row["removed"])),
			Action: "/ign/executor", Hints: ignHints(),
		}, nil
	case strings.Contains(q, "任务") || strings.Contains(lq, "task"):
		row, err := qOne(`
			SELECT COUNT(*) AS n,
			SUM(CASE WHEN task_status='RUNNING' THEN 1 ELSE 0 END) AS running,
			SUM(CASE WHEN task_status='COMPLETED' THEN 1 ELSE 0 END) AS completed,
			SUM(CASE WHEN task_status='FAILED' THEN 1 ELSE 0 END) AS failed,
			SUM(CASE WHEN task_status='CANCELLED' THEN 1 ELSE 0 END) AS cancelled
			FROM exec_task WHERE flag = 1`)
		if err != nil {
			return nil, err
		}
		return &AssistantReply{
			Answer: fmt.Sprintf("任务台账:共 %d 个,其中执行中 %d、完成 %d、失败 %d、已取消 %d。动作必须命中 IGN_ACTION 白名单(INV-28),白名单外与含拼接风险的提交按治理拒绝处理(R-12,403 语义+审计);并发超上限即容量拒绝(R-05);FAILED 与 CANCELLED 分列(INV-30)。",
				Int(row["n"]), Int(row["running"]), Int(row["completed"]), Int(row["failed"]), Int(row["cancelled"])),
			Action: "/ign/task", Hints: ignHints(),
		}, nil
	case strings.Contains(q, "证书") || strings.Contains(q, "TLS") || strings.Contains(q, "tls"):
		row, err := qOne(`
			SELECT COUNT(*) AS n,
			SUM(CASE WHEN bind_status='ACTIVE' THEN 1 ELSE 0 END) AS active
			FROM executor_tls_binding WHERE flag = 1`)
		if err != nil {
			return nil, err
		}
		return &AssistantReply{
			Answer: fmt.Sprintf("通道证书绑定:共 %d 条,其中已生效 %d 条。同一执行机同一用途至多一条生效绑定(INV-23);通用名称由固定模板生成不人工指定(REF-05);剩余天数由生效止即时计算不落库(REF-06);TLS 显式降级与绑定互斥(R-11/INV-27);临期视图按 REF-07 提前天数(缺省 14 天)展示。",
				Int(row["n"]), Int(row["active"])),
			Action: "/ign/tls", Hints: ignHints(),
		}, nil
	case strings.Contains(q, "审批") || strings.Contains(q, "怎么操作") || strings.Contains(q, "如何"):
		return &AssistantReply{
			Answer: "执行机强制注销审批(FLOW-PROC-APP-01,唯一审批流):交付管理员在「执行机」页对有运行中任务的执行机提交强制注销申请(影响面强制展示运行中任务清单,R-08)→ " +
				"平台运维管理员在「审批中心-我的待办」通过/驳回 → 通过则先取消运行任务再执行拆除链(吊销双证书 → 凭据置已吊销 → 状态置已注销,R-07 次序固定);驳回则状态不变、意见留痕。空闲执行机走注销（空闲）直达,不进审批。",
			Action: "/workbench/todo", Hints: ignHints(),
		}, nil
	case strings.Contains(q, "对账") || strings.Contains(q, "纠偏"):
		return &AssistantReply{
			Answer: "对账与纠偏台账(REP-12)记录三类事件:计数纠偏(R-06:心跳计数偏差超过 1,以执行机上报值强制覆写并留痕)、状态修正(R-09/R-10)、治理拒绝(R-12)。全部例外路径必须留痕,可在「日志与对账」页查看。",
			Action: "/ign/log", Hints: ignHints(),
		}, nil
	case strings.Contains(q, "管理台") || strings.Contains(q, "管理控制台") || strings.Contains(lq, "admin"):
		return &AssistantReply{
			Answer: "管理控制台(/admin)面向系统管理员:仪表盘、用户/角色/权限/资源管理、流程管理。" +
				"工作台右上角头像菜单可直达。gopherforge 视觉体系,深空玻璃双主题。",
			Action: "/admin/dashboard", Hints: ignHints(),
		}, nil
	default:
		return &AssistantReply{
			Answer: "我是点火中心工作台助理,可以帮您查询交付版本/镜像预推/执行机/任务/证书绑定台账、待办审批、统计总览与运行参数,并解读业务规则" +
				"(组件契约冻结/终态不可逆/审批前置/白名单治理/防重放/容量拒绝等)。也可用只读 SQL 工具查白名单表。试试下方建议问题。",
			Hints: ignHints(),
		}, nil
	}
}

func ignHints() []string {
	return []string{
		"现在有多少个可用交付版本？",
		"强制注销怎么审批？",
		"提交任务为什么被治理拒绝？",
		"SELECT executor_no, name, status FROM executor",
	}
}

// ---------- 只读 SQL 工具(白名单 + SELECT-only + 强制 LIMIT + 审计) ----------

var forbiddenSQLRe = regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE|DROP|ALTER|CREATE|TRUNCATE|GRANT|REVOKE|COPY|VACUUM|CALL|DO|MERGE|EXECUTE|SET|BEGIN|COMMIT|ROLLBACK)\b`)
var selectOnlyRe = regexp.MustCompile(`(?is)^\s*(SELECT|WITH)\s.+$`)

// AllowedSQLTables 只读工具可用表(与本体注册表白名单对齐 + 本域扩展/配置表)。
// 刻意不含 bootstrap_credential:INV-22 令牌密文/nonce/本体不得出现在任何查询响应。
var AllowedSQLTables = []string{
	"delivery_version", "image_prepush_item", "executor", "exec_task",
	"executor_tls_binding", "ign_force_remove", "ign_reconcile_log",
	"ign_platform_config", "task_log",
}

// SQLQueryResult 只读 SQL 工具结果。
type SQLQueryResult struct {
	Columns []string         `json:"columns"`
	Rows    []map[string]any `json:"rows"`
	Count   int              `json:"count"`
	SQL     string           `json:"sql"`
}

// AssistantRunReadOnlySQL 执行只读查询:
//   - 仅单条 SELECT/WITH 语句(禁止分号尾随多语句、禁止 DML/DDL 关键词);
//   - 仅白名单表(解析 FROM/JOIN 后的表名);
//   - 无 LIMIT 时自动追加 LIMIT 100;
//   - 每次调用写审计(audit_logs,含 SQL 与返回行数)。
func AssistantRunReadOnlySQL(uid int64, username, raw string) (*SQLQueryResult, error) {
	sqlText := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(raw), ";"))
	if sqlText == "" {
		return nil, Fail(errcode.ErrParam, "SQL 不得为空")
	}
	if strings.Contains(sqlText, ";") {
		return nil, Fail(errcode.ErrParam, "仅允许单条语句（不得包含分号）")
	}
	if !selectOnlyRe.MatchString(sqlText) {
		return nil, Fail(errcode.ErrRule, "仅允许 SELECT / WITH 只读查询")
	}
	if forbiddenSQLRe.MatchString(sqlText) {
		return nil, Fail(errcode.ErrRule, "检测到写操作关键词,已拒绝（只读工具）")
	}
	// 表白名单:FROM/JOIN 后的标识符(含 schema 前缀时取尾段;表名统一 ign_* / 业务表名)
	tableRe := regexp.MustCompile(`(?i)\b(?:FROM|JOIN)\s+([a-zA-Z_][a-zA-Z0-9_.]*)`)
	allowed := map[string]bool{}
	for _, t := range AllowedSQLTables {
		allowed[t] = true
	}
	for _, m := range tableRe.FindAllStringSubmatch(sqlText, -1) {
		tbl := m[1]
		if i := strings.LastIndex(tbl, "."); i >= 0 {
			tbl = tbl[i+1:]
		}
		if !allowed[strings.ToLower(tbl)] {
			return nil, Fail(errcode.ErrForbidden, "表 %s 不在只读白名单内（允许:%s）", tbl, strings.Join(AllowedSQLTables, ", "))
		}
	}
	// 强制 LIMIT
	if !regexp.MustCompile(`(?i)\bLIMIT\s+\d+`).MatchString(sqlText) {
		sqlText += " LIMIT 100"
	} else {
		sqlText = regexp.MustCompile(`(?i)\bLIMIT\s+(\d+)`).ReplaceAllStringFunc(sqlText, func(m string) string {
			n := regexp.MustCompile(`\d+`).FindString(m)
			if len(n) > 0 && ignToInt(n) > 100 {
				return "LIMIT 100"
			}
			return m
		})
	}
	rows, err := store.List(store.DB, sqlText)
	if err != nil {
		WriteAudit(nil, uid, username, "AI_SQL_FAIL", truncateAudit(sqlText))
		return nil, Fail(errcode.ErrDB, "只读查询执行失败: %v", err)
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	cols := []string{}
	if len(rows) > 0 {
		for k := range rows[0] {
			cols = append(cols, k)
		}
	}
	WriteAudit(nil, uid, username, "AI_SQL", fmt.Sprintf("%s → %d 行", truncateAudit(sqlText), len(rows)))
	return &SQLQueryResult{Columns: cols, Rows: rows, Count: len(rows), SQL: sqlText}, nil
}

func truncateAudit(s string) string {
	if len(s) > 480 {
		return s[:480] + "…"
	}
	return s
}

// pickAccount 选定供应商账号（accountID=0 → 首个启用账号;指定 id 须已启用）。
func pickAccount(accountID int64) *llmcfg.Account {
	accts := llmcfg.EnabledAccounts()
	if len(accts) == 0 {
		return nil
	}
	if accountID == 0 {
		return &accts[0]
	}
	for i := range accts {
		if accts[i].ID == accountID {
			return &accts[i]
		}
	}
	return nil
}

// AssistantModels 登录用户可选的模型清单（启用账号 × 模型目录）。
type AssistantModel struct {
	AccountID    int64    `json:"account_id"`
	AccountName  string   `json:"account_name"`
	Provider     string   `json:"provider"`
	Models       []string `json:"models"`
	DefaultModel string   `json:"default_model"`
}

// AssistantModelOptions 助理模型选择器数据源。
func AssistantModelOptions() []AssistantModel {
	accts := llmcfg.EnabledAccounts()
	out := make([]AssistantModel, 0, len(accts))
	for _, a := range accts {
		out = append(out, AssistantModel{
			AccountID: a.ID, AccountName: a.Name, Provider: a.Provider,
			Models: a.ModelList(), DefaultModel: a.DefaultModelResolve(),
		})
	}
	return out
}
