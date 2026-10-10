// ign_cluster_index.go —— FUNC-52 本域交付集群索引（REP-19，v1.5）。
//
// 本域没有集群台账（INV-33：跨域只存标识），"本域服务过哪些集群"这件事的真相散落在
// exec_task.cluster_ref 与 ign_cluster_config_override.cluster_ref 两张表里，答复它不需要
// 新事实：零 DDL、不落表、不加列、不缓存，每次查询现算（Q-127 / 5.21 口径 ②）。
package service

import (
	"strings"

	"gitcode.com/opic-ontology/opic-techbase/pkg/errcode"
)

// 待办筛项值域（9.10.2「待办筛项」四值）：均为对本表既有列的过滤，不引入新查询面。
const (
	ignIndexTodoAll       = "ALL"
	ignIndexTodoRunning   = "RUNNING"
	ignIndexTodoOverride  = "OVERRIDE"
	ignIndexTodoDeviation = "DEVIATION"
)

// ignClusterIndexSources 派生视图的三段 CTE：键集并集 + 两侧聚合 + 最近一次交付。
// UNION（非 ALL）即去重，两侧同名只出一行；last_task 用 DISTINCT ON 取每个标识
// 活动时刻最新的一行 —— 在途任务没有 ended_at，用 COALESCE 退到 started_at，
// 因此"最近一次交付"读作"本域最近的一次交付活动"，不是"最近一次完成"。
const ignClusterIndexSources = `
	WITH keys AS (
		SELECT cluster_ref FROM exec_task WHERE flag = 1 GROUP BY cluster_ref
		UNION
		SELECT cluster_ref FROM ign_cluster_config_override WHERE flag = 1 GROUP BY cluster_ref
	),
	task_agg AS (
		SELECT cluster_ref,
			COUNT(*) AS task_count,
			COUNT(*) FILTER (WHERE task_status IN ('RUNNING','CANCELLING')) AS running_task_count,
			COUNT(*) FILTER (WHERE task_status = 'LOST' OR COALESCE(reconciled_note,'') <> '') AS pending_deviation_count
		FROM exec_task WHERE flag = 1 GROUP BY cluster_ref
	),
	last_task AS (
		SELECT DISTINCT ON (cluster_ref)
			cluster_ref, task_no, task_status, verdict_source, started_at, ended_at
		FROM exec_task WHERE flag = 1
		ORDER BY cluster_ref, COALESCE(ended_at, started_at) DESC NULLS LAST, id DESC
	),
	override_agg AS (
		SELECT cluster_ref,
			COUNT(*) FILTER (WHERE override_status = 'EFFECTIVE') AS effective_override_count,
			COUNT(*) FILTER (WHERE override_status = 'ROLLED_BACK') AS rolled_back_override_count
		FROM ign_cluster_config_override WHERE flag = 1 GROUP BY cluster_ref
	)`

const ignClusterIndexFrom = `
	FROM keys k
	LEFT JOIN task_agg ta ON ta.cluster_ref = k.cluster_ref
	LEFT JOIN last_task l ON l.cluster_ref = k.cluster_ref
	LEFT JOIN override_agg ov ON ov.cluster_ref = k.cluster_ref`

const ignClusterIndexSelect = ignClusterIndexSources + `
	SELECT k.cluster_ref,
		COALESCE(ta.task_count, 0) AS task_count,
		COALESCE(ta.running_task_count, 0) AS running_task_count,
		COALESCE(ta.pending_deviation_count, 0) AS pending_deviation_count,
		COALESCE(ov.effective_override_count, 0) AS effective_override_count,
		COALESCE(ov.rolled_back_override_count, 0) AS rolled_back_override_count,
		l.task_no AS last_task_no, l.task_status AS last_task_status,
		l.verdict_source AS last_verdict_source,
		l.started_at AS last_started_at, l.ended_at AS last_ended_at` + ignClusterIndexFrom

// ignClusterIndexWhere 筛选条件拼装（关键字为包含匹配，不做存在性校验 —— 本域不持有
// 集群有效性判据；待办三档只是对已派生列的收紧）。
func ignClusterIndexWhere(filters map[string]any) (string, []any, bool, error) {
	where := " WHERE 1=1"
	args := []any{}
	filtered := false
	if v := strings.TrimSpace(Str(filters["cluster_ref"])); v != "" {
		where += " AND k.cluster_ref = ?"
		args = append(args, v)
		filtered = true
	}
	if v := strings.TrimSpace(Str(filters["keyword"])); v != "" {
		where += " AND k.cluster_ref LIKE ?"
		args = append(args, "%"+v+"%")
		filtered = true
	}
	switch todo := strings.ToUpper(strings.TrimSpace(Str(filters["pending"]))); todo {
	case "", ignIndexTodoAll:
	case ignIndexTodoRunning:
		where += " AND COALESCE(ta.running_task_count, 0) > 0"
		filtered = true
	case ignIndexTodoOverride:
		where += " AND COALESCE(ov.effective_override_count, 0) > 0"
		filtered = true
	case ignIndexTodoDeviation:
		where += " AND COALESCE(ta.pending_deviation_count, 0) > 0"
		filtered = true
	default:
		return "", nil, false, Fail(errcode.ErrParam, "待办筛项 %q 不在值域内（全部 / 有在跑任务 / 有生效覆写 / 有未决偏差四值）", todo)
	}
	return where, args, filtered, nil
}

// ListDeliveryClusterIndex FUNC-52 本域交付集群索引查询（REP-19）：一行一个本域服务过的
// 集群标识 + 四个即时派生摘要列。默认序 = 最近一次交付结束时间倒序，无结束时间（含从未
// 派发、只有覆写登记）的行按开始时间垫后（9.10.6 单一排序）；上游集群的名称 / 生命周期
// 状态 / 健康一律不出现在返回里（INV-33），本域也没有那份数据可抄。
func ListDeliveryClusterIndex(page, size int, filters map[string]any) (map[string]any, error) {
	where, args, filtered, err := ignClusterIndexWhere(filters)
	if err != nil {
		return nil, err
	}
	if page < 1 {
		page = 1
	}
	if size <= 0 {
		size = 20
	}
	if size > 200 {
		size = 200
	}
	total, err := qCount(ignClusterIndexSources+`
		SELECT COUNT(*)`+ignClusterIndexFrom+where, args...)
	if err != nil {
		return nil, err
	}
	rows, err := qList(ignClusterIndexSelect+where+`
		ORDER BY (l.ended_at IS NULL), l.ended_at DESC NULLS LAST,
			l.started_at DESC NULLS LAST, l.task_no DESC NULLS LAST, k.cluster_ref
		LIMIT ? OFFSET ?`, append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	statsAt := ignNow()
	out := map[string]any{
		"list":     rows,
		"total":    total,
		"page":     page,
		"size":     size,
		"stats_at": statsAt,
		"caliber_note": "摘要截至 " + statsAt +
			"（即时派生，进入台内以台内实时数据为准）",
		"scope_note": "本表只列「本域服务过的」集群：标识取自交付记录与覆写登记的并集去重（零 DDL、不落表不缓存）；" +
			"上游集群的名称 / 生命周期状态 / 健康不在本域，一律不呈现（INV-33）",
	}
	if len(rows) == 0 {
		if filtered {
			out["empty_note"] = "按当前筛选条件没有命中 —— 关键字只做本域标识的包含匹配，不校验集群是否存在（INV-33），换一个关键字或把待办筛项放回「全部」再看"
		} else {
			out["empty_note"] = "本域尚未替任何集群执行过交付 —— 交付任务由上游中心派发或在本域「交付任务」叶提交（本表不断言上游是否存在集群，那是 `O-CLC` 的事实）"
		}
	}
	return out, nil
}
