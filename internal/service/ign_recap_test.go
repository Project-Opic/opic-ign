// ign_recap_test.go —— 结论判定网关：剧本汇总优先于退出码（B-AUTO-12 / R-14 / INV-37）。
package service

import (
	"strings"
	"testing"
)

func TestIgnRecapTracker(t *testing.T) {
	tr := &ignRecapTracker{}
	lines := []string{
		"PLAY [k8s-cluster] *****",
		"TASK [install etcd] ****",
		"10.0.0.11 : ok=12 changed=3 unreachable=0 failed=0 skipped=4 rescued=0 ignored=0",
		"10.0.0.12 : ok=9 changed=2 unreachable=1 failed=0 skipped=0 rescued=0 ignored=0",
		"",
		"some trailing output",
	}
	failLine := false
	for _, l := range lines {
		if tr.Feed(l) {
			failLine = true
		}
	}
	if tr.Seen() {
		t.Fatal("未出现 PLAY RECAP 头不得计为汇总已见")
	}
	if failLine {
		t.Fatal("无 recap 块时不应有失败行")
	}
	// 带汇总头的两次 play：以最后一次为准（同一命令面的收尾结论）
	tr = &ignRecapTracker{}
	for _, l := range []string{
		"PLAY RECAP ****",
		"10.0.0.11 : ok=1 changed=0 unreachable=0 failed=1 skipped=0",
		"",
		"PLAY RECAP ****",
		"10.0.0.11 : ok=20 changed=5 unreachable=0 failed=0 skipped=0",
		"10.0.0.12 : ok=18 changed=4 unreachable=0 failed=0 skipped=2",
	} {
		tr.Feed(l)
	}
	if !tr.Seen() || tr.Hosts() != 2 || tr.Failed() != 0 {
		t.Fatalf("最后一次汇总应为 2 主机 0 失败，实得 %+v", tr)
	}
	if !strings.Contains(tr.Summary(), "2") {
		t.Fatalf("摘要须含主机数: %q", tr.Summary())
	}
}

func TestIgnRecapFailedHosts(t *testing.T) {
	tr := &ignRecapTracker{}
	for _, l := range []string{
		"PLAY RECAP ****",
		"10.0.0.11 : ok=12 changed=3 unreachable=0 failed=2 skipped=4",
		"10.0.0.12 : ok=9 changed=2 unreachable=1 failed=0 skipped=0",
	} {
		tr.Feed(l)
	}
	if tr.Failed() != 3 {
		t.Fatalf("failed 与 unreachable 合计应为 3，实得 %d", tr.Failed())
	}
}

// mark 直接构造汇总面（判定网关用例前置，不经逐行喂入）。
func (t *ignRecapTracker) mark(failed, hosts int) {
	t.cur = ignRecapBlock{hosts: hosts, failed: failed}
}

func TestIgnLocalVerdict(t *testing.T) {
	cases := []struct {
		name         string
		exit         int
		recapSeen    bool
		recapFailed  int
		cancelled    bool
		timedOut     bool
		wantStatus   string
		wantVerdict  string
		wantFailFlag string
	}{
		{"退出码零且汇总无失败", 0, true, 0, false, false, "COMPLETED", "PLAY_RECAP", ""},
		{"汇总有失败即盖过退出码零", 0, true, 2, false, false, "FAILED", "PLAY_RECAP", "EXECUTOR_ERROR"},
		{"汇总无失败但退出码非零", 3, true, 0, false, false, "FAILED", "PLAY_RECAP", "EXECUTOR_ERROR"},
		{"无汇总以退出码为准", 0, false, 0, false, false, "COMPLETED", "EXIT_CODE", ""},
		{"无汇总且退出码非零", 1, false, 0, false, false, "FAILED", "EXIT_CODE", "EXECUTOR_ERROR"},
		{"取消以事实收口", 130, false, 0, true, false, "CANCELLED", "EXIT_CODE", ""},
		{"超时归因 TIMEOUT", -1, false, 0, false, true, "FAILED", "EXIT_CODE", "TIMEOUT"},
		{"超时且有汇总仍按汇总判定", 137, true, 0, false, true, "FAILED", "PLAY_RECAP", "TIMEOUT"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := &ignRecapTracker{}
			if c.recapSeen {
				tr.mark(c.recapFailed, 4)
			}
			st, verdict, fail, _, _ := ignLocalVerdict(c.exit, tr, c.cancelled, c.timedOut)
			if st != c.wantStatus || verdict != c.wantVerdict || fail != c.wantFailFlag {
				t.Fatalf("got %s/%s/%s want %s/%s/%s", st, verdict, fail, c.wantStatus, c.wantVerdict, c.wantFailFlag)
			}
		})
	}
}

// LOST 无退出码、必须有失败原因（INV-37）
func TestIgnLostVerdict(t *testing.T) {
	st, verdict, fail, code := ignLostVerdict()
	if st != "LOST" || verdict != "TRACE_LOST" || fail != "VERDICT_UNRESOLVED" {
		t.Fatalf("got %s/%s/%s", st, verdict, fail)
	}
	if code != nil {
		t.Fatalf("LOST 不得有退出码，实得 %d", *code)
	}
}
