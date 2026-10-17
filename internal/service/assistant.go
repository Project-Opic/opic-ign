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
		"executor_tls_binding, ign_force_remove, ign_reconcile_log, ign_platform_config, ign_task_log\n")
	b.WriteString("关键业务规则:INV-01(版本名未归档唯一);INV-04(组件版本组创建后不可改,集群契约);" +
		"INV-05(已归档登记字段冻结);INV-07(版本状态单向迁移);INV-08(同版本内仓库+标签唯一,预推明细由清单派生);" +
		"INV-12(LOCAL 执行机系统注册至多一行,人工创建拒绝);INV-17(REMOVED 终态冻结);" +
		"INV-19(同执行机至多一条 ACTIVE 凭据);INV-22(令牌密文/nonce/本体不出现在任何查询响应——AI 工具不含凭据表);" +
		"INV-23(同执行机同用途至多一条生效绑定);INV-27(R-11 TLS 降级与绑定互斥);" +
		"INV-28(动作白名单)+R-12(白名单外与拼接风险提交=治理拒绝并审计);" +
		"INV-31(防重放:同执行机摘要唯一,重复提交返回原任务);R-04(引导失败达阈值锁定,解锁=重新生成令牌);" +
		"R-05(任务分发与结束驱动执行机计数原子增减,超上限=容量拒绝);R-07(注销拆除链次序固定);" +
		"R-08(强制注销前置=审批通过);R-10(服务端证书吊销 → 降级,拒绝新分发保护运行中)。\n")
	b.WriteString("v1.1 交付链路规则:INV-34(跨域只存标识,cluster_ref/source_task_no 不直查他域表);" +
		"INV-35(门面幂等键=来源任务标识+来源步骤序号,重复受理返回原任务);INV-36(安装族动作只认 ezctl.setup + 步骤编码 01~07," +
		"步骤状态机权威在上游 O-CLC,本域不判顺序、不聚合整体成败);INV-37(第四终态 LOST=痕迹不可存活,退出码位为空," +
		"结论依据 TRACE_LOST,与 FAILED/CANCELLED 分列);INV-38(日志行游标在任务内单调,子实体不独立编号);" +
		"R-16/INV-40(口令/令牌/私钥写入前不可逆脱敏,masked 标记);R-15(命令面挂载缺失=本机执行位不可用," +
		"受理即刻拒绝并记 TOOL_UNAVAILABLE,绝不静默换执行位);R-17(同集群任务排队,不并发交叉);" +
		"7.3 白名单枚举≠可提交集合(ezctl.new 为 internal、scripts.* 为 platform,人工与门面均不可提交)。\n")
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
			SUM(CASE WHEN task_status='CANCELLED' THEN 1 ELSE 0 END) AS cancelled,
			SUM(CASE WHEN task_status='LOST' THEN 1 ELSE 0 END) AS lost,
			SUM(CASE WHEN source_task_no IS NOT NULL AND source_task_no <> '' THEN 1 ELSE 0 END) AS facade
			FROM exec_task WHERE flag = 1`)
		if err != nil {
			return nil, err
		}
		return &AssistantReply{
			Answer: fmt.Sprintf("任务台账:共 %d 个,其中执行中 %d、完成 %d、失败 %d、已取消 %d、痕迹不可存活 %d;门面受理 %d 个(其余为人工提交)。"+
				"动作必须命中 IGN_ACTION 白名单(INV-28),白名单外与含拼接风险的提交按治理拒绝处理(R-12,403 语义+审计);"+
				"并发超上限即容量拒绝(R-05);同集群按 R-17 排队;终态四值分列 COMPLETED/FAILED/CANCELLED/LOST(INV-30/INV-37),"+
				"每个终态都带结论依据(EXIT_CODE/PLAY_RECAP/AGENT_RECEIPT/TRACE_LOST);执行位挂载缺失时受理即刻拒绝并记 TOOL_UNAVAILABLE(R-15)。",
				Int(row["n"]), Int(row["running"]), Int(row["completed"]), Int(row["failed"]), Int(row["cancelled"]),
				Int(row["lost"]), Int(row["facade"])),
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
	case strings.Contains(q, "对账") || strings.Contains(q, "纠偏") || strings.Contains(q, "拒绝"):
		return &AssistantReply{
			Answer: "对账与纠偏台账(REP-12/REP-14)记录五类事件,受理侧拒绝与执行侧纠偏分列:" +
				"治理拒绝 GOVERNANCE_REFUSE(R-12/R-13:白名单外动作、拼接风险提交、门面越权形态)、" +
				"执行位不可用 TOOL_UNAVAILABLE(R-15:命令面挂载缺失,受理即刻拒绝,不静默换执行位)、" +
				"计数纠偏 COUNT_CORRECT(R-06:心跳计数偏差超过 1,以执行机上报值强制覆写并留痕)、" +
				"状态修正 STATUS_FIX(R-09/R-10)、痕迹不可存活判定 TASK_LOST(INV-37:平台重启后执行位进程无法验真,判定为 LOST 而非失败)。" +
				"全部例外路径必须留痕,可在「日志与对账」页三个页签(任务日志·对账纠偏·门面受理台账)查看。",
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

var forbiddenSQLRe = regexp.MustCompile(`(?i)\b(INSERT|UPDATE|DELETE|DROP|ALTER|CREATE|TRUNCATE|GRANT|REVOKE|COPY|VACUUM|CALL|DO|MERGE|EXECUTE|SET|BEGIN|COMMIT|ROLLBACK|INTO)\b`)
var selectOnlyRe = regexp.MustCompile(`(?is)^\s*(SELECT|WITH)\s.+$`)

// AllowedSQLTables 只读工具可用表(与本体注册表白名单对齐 + 本域扩展/配置表)。
// 刻意不含 bootstrap_credential:INV-22 令牌密文/nonce/本体不得出现在任何查询响应。
var AllowedSQLTables = []string{
	"delivery_version", "image_prepush_item", "executor", "exec_task",
	"executor_tls_binding", "ign_force_remove", "ign_reconcile_log",
	"ign_platform_config", "ign_task_log",
}

// SQLQueryResult 只读 SQL 工具结果。
type SQLQueryResult struct {
	Columns []string         `json:"columns"`
	Rows    []map[string]any `json:"rows"`
	Count   int              `json:"count"`
	SQL     string           `json:"sql"`
}

// ---------- 只读 SQL 词法级表引用校验(不引入 pg 语法解析依赖) ----------

type sqlTokenKind int

const (
	tkWord sqlTokenKind = iota
	tkQuoted
	tkString
	tkPunct
)

type sqlToken struct {
	kind sqlTokenKind
	text string
}

func sqlIdentByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// sqlReservedAfterRef 表引用后不可作为别名/列清单起始的保留字(遇到即结束本次引用解析)。
var sqlReservedAfterRef = map[string]bool{
	"as": true, "cross": true, "except": true, "fetch": true, "for": true,
	"from": true, "full": true, "group": true, "having": true, "inner": true,
	"intersect": true, "join": true, "left": true, "limit": true, "natural": true,
	"offset": true, "on": true, "order": true, "right": true, "select": true,
	"tablesample": true, "union": true, "using": true, "where": true, "window": true, "with": true,
}

// lexSQL 把 SQL 切成词法单元;注释/字符串内容不参与关键词识别,未知字符一律失败闭合。
func lexSQL(s string) ([]sqlToken, bool) {
	toks := make([]sqlToken, 0, 32)
	for i := 0; i < len(s); {
		ch := s[i]
		switch {
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r':
			i++
		case ch == '-' && i+1 < len(s) && s[i+1] == '-':
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case ch == '/' && i+1 < len(s) && s[i+1] == '*':
			depth := 0
			for i < len(s) {
				if i+1 < len(s) && s[i] == '/' && s[i+1] == '*' {
					depth++
					i += 2
					continue
				}
				if i+1 < len(s) && s[i] == '*' && s[i+1] == '/' {
					depth--
					i += 2
					if depth == 0 {
						break
					}
					continue
				}
				i++
			}
			if depth != 0 {
				return nil, false
			}
		case ch == '\'':
			// 标准字符串;拒绝 E'/B'/X' 前缀形态(可引入转义或字节串)
			if i > 0 && strings.IndexByte("eEbBxX", s[i-1]) >= 0 && (i == 1 || !sqlIdentByte(s[i-2])) {
				return nil, false
			}
			i++
			closed := false
			for i < len(s) {
				if s[i] == '\'' {
					if i+1 < len(s) && s[i+1] == '\'' {
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, false
			}
			toks = append(toks, sqlToken{kind: tkString, text: "''"})
		case ch == '"' || ch == '`':
			quote := ch
			var sb strings.Builder
			i++
			closed := false
			for i < len(s) {
				if s[i] == quote {
					if i+1 < len(s) && s[i+1] == quote {
						sb.WriteByte(quote)
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				sb.WriteByte(s[i])
				i++
			}
			if !closed || sb.Len() == 0 {
				return nil, false
			}
			toks = append(toks, sqlToken{kind: tkQuoted, text: sb.String()})
		case sqlIdentByte(ch):
			start := i
			for i < len(s) && sqlIdentByte(s[i]) {
				i++
			}
			toks = append(toks, sqlToken{kind: tkWord, text: s[start:i]})
		case strings.IndexByte("(),.*=<>!+-/%:|?", ch) >= 0:
			toks = append(toks, sqlToken{kind: tkPunct, text: string(ch)})
			i++
		default:
			return nil, false
		}
	}
	return toks, true
}

// normalizeSQLTableName 归一化表名(小写、标识符字符、长度上限、不得数字开头);不合法返回空串。
func normalizeSQLTableName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || len(s) > 64 || (s[0] >= '0' && s[0] <= '9') {
		return ""
	}
	for i := 0; i < len(s); i++ {
		if !sqlIdentByte(s[i]) {
			return ""
		}
	}
	return s
}

func sqlSkipBalanced(toks []sqlToken, i int) (int, bool) {
	depth := 0
	for ; i < len(toks); i++ {
		if toks[i].kind != tkPunct {
			continue
		}
		switch toks[i].text {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				return i + 1, true
			}
			if depth < 0 {
				return 0, false
			}
		}
	}
	return 0, false
}

// sqlCTENames 识别语句开头 WITH [RECURSIVE] 定义的 CTE 名(仅顶层,子查询内 CTE 一律不接受);
// 同时返回各 CTE 体 '(' 的下标,供子查询检查放行。
func sqlCTENames(toks []sqlToken) (map[string]bool, map[int]bool) {
	names := map[string]bool{}
	bodies := map[int]bool{}
	if len(toks) == 0 || toks[0].kind != tkWord || !strings.EqualFold(toks[0].text, "WITH") {
		return names, bodies
	}
	i := 1
	if i < len(toks) && toks[i].kind == tkWord && strings.EqualFold(toks[i].text, "RECURSIVE") {
		i++
	}
	for i < len(toks) {
		nameTok := toks[i]
		if nameTok.kind != tkWord && nameTok.kind != tkQuoted {
			break
		}
		if nameTok.kind != tkQuoted || nameTok.text == strings.ToLower(nameTok.text) {
			if n := normalizeSQLTableName(nameTok.text); n != "" {
				names[n] = true
			}
		}
		i++
		if i < len(toks) && toks[i].kind == tkPunct && toks[i].text == "(" {
			next, ok := sqlSkipBalanced(toks, i)
			if !ok {
				break
			}
			i = next
		}
		if i >= len(toks) || toks[i].kind != tkWord || !strings.EqualFold(toks[i].text, "AS") {
			break
		}
		i++
		if i >= len(toks) || toks[i].kind != tkPunct || toks[i].text != "(" {
			break
		}
		bodies[i] = true
		next, ok := sqlSkipBalanced(toks, i)
		if !ok {
			break
		}
		i = next
		if i < len(toks) && toks[i].kind == tkPunct && toks[i].text == "," {
			i++
			continue
		}
		break
	}
	return names, bodies
}

// sqlParseRefName 解析一个表引用(仅接受单段名,拒绝 schema 限定,避免跨 schema 同名表绕过白名单)。
func sqlParseRefName(toks []sqlToken, i int) (string, int, bool) {
	if i >= len(toks) {
		return "", i, false
	}
	t := toks[i]
	if t.kind != tkWord && t.kind != tkQuoted {
		return "", i, false
	}
	if t.kind == tkQuoted && t.text != strings.ToLower(t.text) {
		return "", i, false // 引号标识符大小写敏感:非全小写可能与未加引号同名表混淆,失败闭合
	}
	i++
	if i < len(toks) && toks[i].kind == tkPunct && toks[i].text == "." {
		return "", i, false
	}
	n := normalizeSQLTableName(t.text)
	if n == "" {
		return "", i, false
	}
	return n, i, true
}

// sqlSkipRefSuffix 跳过表引用后缀:继承星号、[AS] alias、列别名清单、TABLESAMPLE [REPEATABLE]。
func sqlSkipRefSuffix(toks []sqlToken, i int) (int, bool) {
	j := i
	if j < len(toks) && toks[j].kind == tkPunct && toks[j].text == "*" {
		j++
	}
	if j < len(toks) && toks[j].kind == tkWord && strings.EqualFold(toks[j].text, "AS") {
		j++
		if j >= len(toks) || (toks[j].kind != tkWord && toks[j].kind != tkQuoted) {
			return 0, false
		}
		j++
	} else if j < len(toks) && (toks[j].kind == tkQuoted ||
		(toks[j].kind == tkWord && !sqlReservedAfterRef[strings.ToLower(toks[j].text)])) {
		j++
	}
	if j < len(toks) && toks[j].kind == tkPunct && toks[j].text == "(" {
		next, ok := sqlSkipBalanced(toks, j)
		if !ok {
			return 0, false
		}
		j = next
	}
	if j < len(toks) && toks[j].kind == tkWord && strings.EqualFold(toks[j].text, "TABLESAMPLE") {
		j++
		if j >= len(toks) || (toks[j].kind != tkWord && toks[j].kind != tkQuoted) {
			return 0, false
		}
		j++
		if j >= len(toks) || toks[j].kind != tkPunct || toks[j].text != "(" {
			return 0, false
		}
		next, ok := sqlSkipBalanced(toks, j)
		if !ok {
			return 0, false
		}
		j = next
		if j < len(toks) && toks[j].kind == tkWord && strings.EqualFold(toks[j].text, "REPEATABLE") {
			j++
			if j >= len(toks) || toks[j].kind != tkPunct || toks[j].text != "(" {
				return 0, false
			}
			next, ok = sqlSkipBalanced(toks, j)
			if !ok {
				return 0, false
			}
			j = next
		}
	}
	return j, true
}

// sqlSkipJoinQual 跳过 ON 连接条件:仅按括号深度跟踪,深度 0 的逗号/JOIN/子句关键字即结束。
func sqlSkipJoinQual(toks []sqlToken, i int) int {
	depth := 0
	for i < len(toks) {
		t := toks[i]
		if t.kind == tkPunct {
			switch t.text {
			case "(":
				depth++
			case ")":
				if depth == 0 {
					return i
				}
				depth--
			case ",":
				if depth == 0 {
					return i
				}
			}
			i++
			continue
		}
		if depth == 0 && t.kind == tkWord {
			switch strings.ToUpper(t.text) {
			case "JOIN", "INNER", "LEFT", "RIGHT", "FULL", "CROSS", "NATURAL",
				"WHERE", "GROUP", "HAVING", "WINDOW", "ORDER", "LIMIT", "OFFSET",
				"FETCH", "FOR", "UNION", "EXCEPT", "INTERSECT":
				return i
			}
		}
		i++
	}
	return i
}

// sqlParseTableRef 解析单个 from_item 的表引用(含 ONLY/继承星号/别名/TABLESAMPLE 后缀)。
func sqlParseTableRef(toks []sqlToken, i int) (string, int, bool) {
	if i < len(toks) && toks[i].kind == tkWord && strings.EqualFold(toks[i].text, "ONLY") {
		i++
	}
	name, next, ok := sqlParseRefName(toks, i)
	if !ok {
		return "", i, false
	}
	if next < len(toks) && toks[next].kind == tkPunct && toks[next].text == "(" {
		return "", next, false // 表函数/派生表
	}
	next, ok = sqlSkipRefSuffix(toks, next)
	if !ok {
		return "", next, false
	}
	return name, next, true
}

// sqlParseFromItems 解析完整 FROM 子句:from_item { ',' from_item | join ... ON/USING ... }。
func sqlParseFromItems(toks []sqlToken, i int, refs *[]string) (int, bool) {
	needRef := true
	for {
		if i >= len(toks) {
			return i, true
		}
		if needRef {
			name, next, ok := sqlParseTableRef(toks, i)
			if !ok {
				return 0, false
			}
			*refs = append(*refs, name)
			i = next
			needRef = false
			if i >= len(toks) {
				return i, true
			}
		}
		t := toks[i]
		if t.kind == tkPunct && t.text == "," {
			i++
			needRef = true
			continue
		}
		if t.kind == tkWord {
			switch strings.ToUpper(t.text) {
			case "JOIN":
				i++
				needRef = true
				continue
			case "INNER", "LEFT", "RIGHT", "FULL", "CROSS", "NATURAL":
				for i < len(toks) && toks[i].kind == tkWord {
					w := strings.ToUpper(toks[i].text)
					i++
					if w == "JOIN" {
						break
					}
					if w != "INNER" && w != "LEFT" && w != "RIGHT" && w != "FULL" && w != "CROSS" && w != "NATURAL" && w != "OUTER" {
						return 0, false
					}
				}
				if i == 0 || toks[i-1].kind != tkWord || !strings.EqualFold(toks[i-1].text, "JOIN") {
					return 0, false
				}
				needRef = true
				continue
			case "ON":
				i = sqlSkipJoinQual(toks, i+1)
				continue
			case "USING":
				i++
				if i >= len(toks) || toks[i].kind != tkPunct || toks[i].text != "(" {
					return 0, false
				}
				next, ok := sqlSkipBalanced(toks, i)
				if !ok {
					return 0, false
				}
				i = next
				continue
			}
		}
		return i, true
	}
}

// checkReadOnlyRelations 词法级校验所有 FROM/JOIN 表引用:必须可解析且命中白名单(或语句内 CTE)。
// 逗号连接、引号/反引号标识符、注释分隔、表函数/派生表、FROM-less 等形态按真实语义解析;解析失败即拒绝。
func checkReadOnlyRelations(sqlText string) error {
	toks, ok := lexSQL(sqlText)
	if !ok {
		return Fail(errcode.ErrRule, "SQL 无法通过词法校验,已拒绝（只读工具）")
	}
	cte, cteBodies := sqlCTENames(toks)
	// TABLE <name> 是等价于 SELECT * FROM 的查询形态,可绕过 FROM 词法扫描,直接拒绝。
	for _, t := range toks {
		if t.kind == tkWord && strings.EqualFold(t.text, "TABLE") {
			return Fail(errcode.ErrRule, "SQL 不支持 TABLE 语法,已拒绝（只读工具）")
		}
	}
	// 子查询(含标量子查询/派生表/CTE 之外的 WITH)一律拒绝;仅放行已识别的 CTE 体。
	for i := 0; i+1 < len(toks); i++ {
		if toks[i].kind != tkPunct || toks[i].text != "(" || cteBodies[i] {
			continue
		}
		if toks[i+1].kind != tkWord {
			continue
		}
		switch strings.ToUpper(toks[i+1].text) {
		case "SELECT", "WITH", "TABLE", "VALUES":
			return Fail(errcode.ErrRule, "SQL 不支持子查询,已拒绝（只读工具）")
		}
	}
	// FROM 也可出现在 extract/substring/trim/overlay 的表达式语法中(非表引用),整段跳过;
	// 其中的子查询已在上一步被拒,故跳过不影响白名单校验。
	skip := make([]bool, len(toks))
	for i := 1; i < len(toks); i++ {
		if toks[i].kind != tkPunct || toks[i].text != "(" || toks[i-1].kind != tkWord {
			continue
		}
		switch strings.ToLower(toks[i-1].text) {
		case "extract", "substring", "trim", "overlay":
			end, ok := sqlSkipBalanced(toks, i)
			if !ok {
				return Fail(errcode.ErrRule, "SQL 括号不配对,已拒绝（只读工具）")
			}
			for k := i; k < end; k++ {
				skip[k] = true
			}
			i = end - 1
		}
	}
	refs := make([]string, 0, 4)
	kwCount := 0
	for i := 0; i < len(toks); i++ {
		if toks[i].kind != tkWord || skip[i] || !strings.EqualFold(toks[i].text, "FROM") {
			continue
		}
		kwCount++
		end, ok := sqlParseFromItems(toks, i+1, &refs)
		if !ok {
			return Fail(errcode.ErrRule, "SQL 表引用无法解析,已拒绝（只读工具）")
		}
		i = end - 1
	}
	if kwCount == 0 {
		return Fail(errcode.ErrRule, "只读 SQL 必须包含可解析的 FROM/JOIN 表引用")
	}
	allowed := make(map[string]bool, len(AllowedSQLTables))
	for _, t := range AllowedSQLTables {
		allowed[t] = true
	}
	for _, r := range refs {
		if allowed[r] || cte[r] {
			continue
		}
		return Fail(errcode.ErrForbidden, "表 %s 不在只读白名单内（允许:%s）", r, strings.Join(AllowedSQLTables, ","))
	}
	return nil
}

// AssistantRunReadOnlySQL 执行只读查询:
//   - 仅单条 SELECT/WITH 语句(禁止分号尾随多语句、禁止 DML/DDL/INTO 关键词);
//   - 仅白名单表(词法级解析每个 FROM/JOIN 表引用,含逗号连接;解析失败即拒绝);
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
	// 表引用校验:词法级关系集比对(逗号连接/引号/注释/FROM-less 等形态一律失败闭合)
	if err := checkReadOnlyRelations(sqlText); err != nil {
		return nil, err
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
