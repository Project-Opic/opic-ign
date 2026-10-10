package service

import (
	"fmt"
	"strings"
	"testing"
)

// ---------- FUNC-51 跨中心交付执行日志只读通道 ----------

// TestFacadeLogChannelIncrement REF-14 / INV-38：分页游标逐轮续传、行间不重不漏、
// 单次行数按平台配置封顶（请求方给的 limit 超限也按上限封顶并如实回显），
// 游标右界随每次回吐给出（少了几行可判读）；每次调用落一条逐次留痕（来源=上游代理读）。
func TestFacadeLogChannelIncrement(t *testing.T) {
	requireDB(t)
	useDeliveryEnv(t, facadeToolBody(
		`  setup) i=1; while [ $i -le 7 ]; do echo "ezctl progress $i"; i=$((i+1)); done; exit 0 ;;`))
	prev := Str(mustOne(t, `SELECT config_value FROM ign_platform_config WHERE config_key = ?`, ignCfgReceiptLogBatch)["config_value"])
	mustSQL(t, `UPDATE ign_platform_config SET config_value = '3' WHERE config_key = ?`, ignCfgReceiptLogBatch)
	t.Cleanup(func() {
		mustSQL(t, `UPDATE ign_platform_config SET config_value = ? WHERE config_key = ?`, prev, ignCfgReceiptLogBatch)
	})

	src := fmt.Sprintf("CLC-LG-%d", nextSeq())
	got, err := DeliverySubmit(map[string]any{
		"source_task_no": src, "source_step_index": int64(1),
		"cluster_ref": newCluster(t), "step_code": "03", "nodes": facadeInstallNodes(),
	}, facadeUser())
	if err != nil {
		t.Fatal(err)
	}
	taskNo := Str(got["task_no"])
	waitTaskStatus(t, taskNo, "COMPLETED")

	dbLines, err := qList(`SELECT seq, log_line FROM ign_task_log
		WHERE task_id = (SELECT id FROM exec_task WHERE task_no = ?) AND flag = 1 ORDER BY seq`, taskNo)
	if err != nil {
		t.Fatal(err)
	}
	before := auditCount(t, "IGN_EXEC_LOG_READ")

	var (
		seen    []map[string]any
		cursor  int64
		calls   int
		prevSeq int64
	)
	for since := int64(0); ; {
		out, err := FacadeDeliveryLog(taskNo, since, 0, facadeUser())
		if err != nil {
			t.Fatal(err)
		}
		calls++
		lines := out["lines"].([]map[string]any)
		if int64(len(lines)) > 3 {
			t.Fatalf("单次回吐 %d 行 > REF-14 上限 3（limit 缺省应取平台配置）", len(lines))
		}
		if ignToInt(out["limit"]) != 3 {
			t.Errorf("回吐 limit=%v，期望回显封顶后的 3", out["limit"])
		}
		for _, l := range lines {
			s := ignToInt(l["seq"])
			if s <= prevSeq {
				t.Errorf("游标倒挂或重复：seq=%d 上一行 %d（INV-38 严格单调）", s, prevSeq)
			}
			prevSeq = s
		}
		seen = append(seen, lines...)
		next := ignToInt(out["next_seq"])
		cur := ignToInt(out["cursor"])
		if cur < next {
			t.Errorf("游标右界 %d < 本轮末行 %d", cur, next)
		}
		cursor = cur
		if next >= cur {
			break
		}
		since = next
		if calls > 20 {
			t.Fatal("游标不推进（续传死循环）")
		}
	}
	if len(seen) != len(dbLines) {
		t.Fatalf("通道累计回吐 %d 行 ≠ 库内 %d 行（不重不漏）", len(seen), len(dbLines))
	}
	for i := range seen {
		if seen[i]["seq"] != dbLines[i]["seq"] || Str(seen[i]["log_line"]) != Str(dbLines[i]["log_line"]) {
			t.Errorf("第 %d 行与库内不一致：通道 %q / 库内 %q（同一份已掩码数据，不二次改写）",
				i+1, Str(seen[i]["log_line"]), Str(dbLines[i]["log_line"]))
		}
	}
	if audited := auditCount(t, "IGN_EXEC_LOG_READ") - before; int(audited) != calls {
		t.Errorf("逐次留痕 %d 条 ≠ 调用 %d 次（每次调用一条：不留日志副本，但敏感读必须留痕）", audited, calls)
	}
	if cursor == 0 {
		t.Error("游标右界未回吐")
	}
}

// TestFacadeLogChannelMaskAndRefusal INV-40：通道回吐读的就是写前已掩码的那一份，
// 不存在第二条未脱敏通道；limit 越界按上限封顶；引用未命中给明确"任务不存在"，
// 且拒绝同样落一条留痕（谁在找什么任务本身是取证事实）。
func TestFacadeLogChannelMaskAndRefusal(t *testing.T) {
	requireDB(t)
	useDeliveryEnv(t, facadeToolBody(
		`  setup) echo "ezctl setup --password Sup3rSecretValue"; echo "root_token=abc123def456"; echo "plain"; exit 0 ;;`))
	src := fmt.Sprintf("CLC-LGM-%d", nextSeq())
	got, err := DeliverySubmit(map[string]any{
		"source_task_no": src, "source_step_index": int64(1),
		"cluster_ref": newCluster(t), "step_code": "02", "nodes": facadeInstallNodes(),
	}, facadeUser())
	if err != nil {
		t.Fatal(err)
	}
	taskNo := Str(got["task_no"])
	waitTaskStatus(t, taskNo, "COMPLETED")

	out, err := FacadeDeliveryLog(taskNo, 0, 99999, facadeUser())
	if err != nil {
		t.Fatal(err)
	}
	if ignToInt(out["limit"]) > 500 {
		t.Errorf("limit=%v 未按 REF-14 上限封顶", out["limit"])
	}
	var masked int
	for _, l := range out["lines"].([]map[string]any) {
		line := Str(l["log_line"])
		for _, secret := range []string{"Sup3rSecretValue", "abc123def456"} {
			if strings.Contains(line, secret) {
				t.Fatalf("通道回吐出现凭据明文 %q（INV-40 不存在第二条未脱敏通道）", line)
			}
		}
		if boolOfAny(l["masked"]) {
			masked++
			if !strings.Contains(line, ignMaskMark) {
				t.Errorf("masked=true 却无掩码标记: %q", line)
			}
		}
	}
	if masked != 2 {
		t.Errorf("掩码行数 %d，期望 2（命令行选项形态 + 赋值形态）", masked)
	}

	before := auditCount(t, "IGN_EXEC_LOG_READ")
	if _, err := FacadeDeliveryLog("IGN-NO-SUCH-TASK", 0, 0, facadeUser()); err == nil {
		t.Fatal("引用未命中必须给明确拒绝，而不是空回吐冒充现状")
	} else if !strings.Contains(err.Error(), "任务不存在") {
		t.Errorf("拒绝文案 %q（期望与 FUNC-35 同一结论口径）", err.Error())
	}
	if n := auditCount(t, "IGN_EXEC_LOG_READ") - before; n != 1 {
		t.Errorf("拒绝未落留痕（本次 %d 条，期望 1 条）", n)
	}
	if d := latestAuditDetail(t, "IGN_EXEC_LOG_READ"); !strings.Contains(d, "FUNC-51") || !strings.Contains(d, "上游") {
		t.Errorf("留痕缺来源标注（应写明本条读自上游代理通道）：detail=%q", d)
	}
}

func auditCount(t *testing.T, action string) int64 {
	t.Helper()
	n, err := qCount(`SELECT COUNT(*) FROM audit_logs WHERE action = ?`, action)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func latestAuditDetail(t *testing.T, action string) string {
	t.Helper()
	row, err := qOne(`SELECT detail FROM audit_logs WHERE action = ? ORDER BY id DESC LIMIT 1`, action)
	if err != nil {
		t.Fatal(err)
	}
	if row == nil {
		return ""
	}
	return Str(row["detail"])
}
