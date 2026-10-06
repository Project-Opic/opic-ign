// ign_recap.go —— 结论判定网关（B-AUTO-12 / R-14；INV-29 / INV-37）。
//
// 剧本汇总（PLAY RECAP）行优先于退出码：汇总存在失败或不可达主机 ⇒ FAILED；
// 无汇总行 ⇒ 以退出码为准。结论依据落 ExecTask.verdictSource（IGN_VERDICT_SOURCE），
// 本域只出"执行事实"这一层的结论，集群是否装好归上游判定（定性一 ②）。
package service

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	ignRecapHeaderRe = regexp.MustCompile(`(?i)^\s*(PLAY|PLAY RECAP|TASK RECAP)\b`)
	ignRecapLineRe   = regexp.MustCompile(`^\s*\S+\s*:\s*ok=\d+`)
	ignFailedRe      = regexp.MustCompile(`failed=(\d+)`)
	ignUnreachRe     = regexp.MustCompile(`unreachable=(\d+)`)
)

// ignRecapBlock 一个汇总块的统计面。
type ignRecapBlock struct {
	hosts  int
	failed int
}

// ignRecapTracker 流式逐行喂入的执行面汇总跟踪器（单任务采集协程内使用）。
type ignRecapTracker struct {
	cur  ignRecapBlock
	last ignRecapBlock
	in   bool
}

// Feed 喂入一行，返回该行是否为"含失败/不可达"的汇总行（供日志行的严重级别标注）。
func (t *ignRecapTracker) Feed(line string) bool {
	if ignRecapHeaderRe.MatchString(line) {
		if t.cur.hosts > 0 {
			t.last = t.cur
		}
		t.cur = ignRecapBlock{}
		t.in = true
		return false
	}
	if !t.in {
		return false
	}
	if !ignRecapLineRe.MatchString(line) {
		t.in = false // 空行或非汇总行即收尾（最后一块为收口结论）
		return false
	}
	t.cur.hosts++
	any := false
	for _, re := range []*regexp.Regexp{ignFailedRe, ignUnreachRe} {
		if m := re.FindStringSubmatch(line); len(m) > 1 {
			if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
				t.cur.failed += n
				any = true
			}
		}
	}
	return any
}

// block 取当前（未收尾）块，为空则取上一个已收尾块。
func (t *ignRecapTracker) block() ignRecapBlock {
	if t.cur.hosts > 0 {
		return t.cur
	}
	return t.last
}

// Seen 是否出现过剧本汇总。
func (t *ignRecapTracker) Seen() bool { return t.block().hosts > 0 }

// Hosts 汇总面主机数。
func (t *ignRecapTracker) Hosts() int { return t.block().hosts }

// Failed 汇总失败数（failed + unreachable）。
func (t *ignRecapTracker) Failed() int { return t.block().failed }

// Summary 结论摘要（不含凭据；仍由调用方掩码兜底）。
func (t *ignRecapTracker) Summary() string {
	b := t.block()
	if b.hosts == 0 {
		return ""
	}
	return "剧本汇总：" + strconv.Itoa(b.hosts) + " 个执行面主机，" + strconv.Itoa(b.failed) + " 个失败/不可达"
}

// ignLocalVerdict 执行位进程退出后的结论判定：
// 返回（终态 IGN_TASK_STATUS，结论依据 IGN_VERDICT_SOURCE，失败原因 IGN_TASK_FAIL_REASON，
// 结论摘要，退出码是否可作为事实登记）。
// hasExit=false 只有一种情形：人为取消且进程留下"无退出码"这一事实本身 —— 不写 0，
// 因为 0 会被读成"命令面正常收口"（INV-30 取消与失败分列的口径延伸）。
func ignLocalVerdict(exit int, tr *ignRecapTracker, cancelled, timedOut bool) (string, string, string, string, bool) {
	seen, summary, failed := tr.Seen(), tr.Summary(), tr.Failed()
	verdict := "EXIT_CODE"
	ok := exit == 0
	if seen {
		verdict = "PLAY_RECAP"
		ok = ok && failed == 0
	}
	switch {
	case cancelled:
		// 取消以事实收口（FUNC-36/R-09）：终态 CANCELLED 与 FAILED 分列（INV-30）
		return "CANCELLED", verdict, "", summary, exit != 0
	case timedOut:
		// 超时归因单列（恢复路径与执行错误不同，INV-30 同源口径）
		return "FAILED", verdict, "TIMEOUT", "单步超时，进程组已终止（REF-15）；" + summary, true
	case ok:
		return "COMPLETED", verdict, "", summary, true
	case seen && failed > 0:
		return "FAILED", verdict, "EXECUTOR_ERROR", summary, true
	default:
		detail := "退出码 " + strconv.Itoa(exit)
		if exit < 0 {
			detail = "执行进程异常终止（信号）"
		}
		return "FAILED", verdict, "EXECUTOR_ERROR", strings.TrimSpace(detail + " " + summary), true
	}
}

// ignLostVerdict 执行痕迹不可存活的结论（B-AUTO-14 / INV-37）：
// LOST 无退出码、必须带 VERDICT_UNRESOLVED —— 不可存活的痕迹不能被伪装成"退出码非零"。
func ignLostVerdict() (string, string, string, *int) {
	return "LOST", "TRACE_LOST", "VERDICT_UNRESOLVED", nil
}
