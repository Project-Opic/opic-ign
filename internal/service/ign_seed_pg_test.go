// ign_seed_pg_test.go —— 菜单种子与 v1.5 收敛（第七部分矩阵一 / 8.3.1 叶可见性；DEF-02 同族）。
package service

import (
	"slices"
	"testing"
)

var ignV15LeafCodes = []string{"menu-ign-cluster", "menu-ign-version", "menu-ign-executor", "menu-ign-task", "menu-ign-ops"}

// TestSeedMenusV15 空库首建形态（MU application.menus v1.5）：本域侧栏 = 一级五叶、0 二级，
// 每叶的入口门禁取该屏主数据接口，退出侧栏的五叶不再作为可见行存在。
func TestSeedMenusV15(t *testing.T) {
	got := map[string]struct{ perm, path, parent string }{}
	order := []string{}
	for _, r := range seedResources {
		if !isIgnDomainCode(r.Code) {
			continue
		}
		if r.Type != "MENU" {
			t.Errorf("%s 仍登记为 %s：v1.5 本域侧栏没有目录节点", r.Code, r.Type)
		}
		if r.Parent != "" {
			t.Errorf("%s 仍挂在 %s 下：侧栏扁平为一级叶", r.Code, r.Parent)
		}
		got[r.Code] = struct{ perm, path, parent string }{r.PermCode, r.Path, r.Parent}
		order = append(order, r.Code)
	}
	if len(order) != 5 {
		t.Fatalf("本域叶 %d 个，期望 5（%v）", len(order), order)
	}
	for _, retired := range menusV15Retired {
		if _, ok := got[retired]; ok {
			t.Errorf("%s 已退出侧栏却仍在种子可见行里", retired)
		}
	}
	want := map[string]string{
		"menu-ign-cluster":  "ign:FUNC-52",
		"menu-ign-version":  "ign:FUNC-06",
		"menu-ign-executor": "ign:FUNC-17",
		"menu-ign-task":     "ign:FUNC-25",
		"menu-ign-ops":      "ign:FUNC-29",
	}
	for code, perm := range want {
		if got[code].perm != perm {
			t.Errorf("%s 入口门禁 %q，期望 %q（菜单只管入口，取本屏主数据接口）", code, got[code].perm, perm)
		}
	}
	// 顺序 = MU 菜单序（集群交付居首，它是纵看主线的起点）
	if !slices.Equal(order, ignV15LeafCodes) {
		t.Errorf("种子叶序 %v，期望 %v", order, ignV15LeafCodes)
	}
}

func resourceID(t *testing.T, code string) int64 {
	t.Helper()
	return toInt(mustOne(t, `SELECT id FROM sys_resource WHERE code = ?`, code)["id"])
}

// ignSeedCodes 本域侧栏涉及的种子 code（五叶 + 五退出叶 + 两目录），平台段不参与收敛。
var ignSeedCodes = append(append(append([]string{}, menusV15Kept...), menusV15Retired...), menusV15Dirs...)

func isIgnDomainCode(code string) bool { return slices.Contains(ignSeedCodes, code) }

// TestConvergeMenusV15 存量库幂等收敛：旧形态（两目录 + 九叶）收成一级五叶，退出侧栏的叶
// 置不可见而非物理删除，缺的新叶补插，运维自定义不动。
func TestConvergeMenusV15(t *testing.T) {
	requireDB(t)
	codes := append(append([]string{}, menusV15Kept...), menusV15Retired...)
	codes = append(codes, menusV15Dirs...)
	codes = append(codes, "menu-ign-custom")
	for _, c := range codes {
		mustSQL(t, `DELETE FROM sys_resource WHERE code = ?`, c)
	}
	t.Cleanup(func() {
		for _, c := range codes {
			softSQL(`DELETE FROM sys_resource WHERE code = ?`, c)
		}
	})

	// 重组前的旧形状（v1.4 种子口径）
	mustSQL(t, `INSERT INTO sys_resource (parent_id, name, code, permission_code, type, path, icon, sort_order, status)
		VALUES (0, '交付管理', 'menu-ign-delivery', '', 'DIRECTORY', '', 'DeploymentUnit', 10, 1)`)
	mustSQL(t, `INSERT INTO sys_resource (parent_id, name, code, permission_code, type, path, icon, sort_order, status)
		VALUES (0, '运行观测', 'menu-ign-obs', '', 'DIRECTORY', '', 'Audit', 20, 1)`)
	dirDelivery := resourceID(t, "menu-ign-delivery")
	dirObs := resourceID(t, "menu-ign-obs")
	insertLegacy := func(name, code string, parent int64, perm, path string, sort int) {
		mustSQL(t, `INSERT INTO sys_resource (parent_id, name, code, permission_code, type, path, icon, sort_order, status)
			VALUES (?, ?, ?, ?, 'MENU', ?, 'Appstore', ?, 1)`, parent, name, code, perm, path, sort)
	}
	insertLegacy("版本目录", "menu-ign-version", dirDelivery, "ign:FUNC-06", "/ign/version", 11)
	insertLegacy("发行包与镜像", "menu-ign-image", dirDelivery, "ign:FUNC-12", "/ign/image", 12)
	insertLegacy("执行机", "menu-ign-executor", dirDelivery, "ign:FUNC-17", "/ign/executor", 13)
	insertLegacy("通道安全", "menu-ign-tls", dirDelivery, "ign:FUNC-22", "/ign/tls", 14)
	insertLegacy("任务桥接", "menu-ign-task", dirDelivery, "ign:FUNC-25", "/ign/task", 15)
	insertLegacy("参数面模板", "menu-ign-template", dirDelivery, "ign:FUNC-38", "/ign/template", 15)
	insertLegacy("集群交付配置", "menu-ign-config", dirDelivery, "ign:FUNC-46", "/ign/config", 15)
	insertLegacy("日志与对账", "menu-ign-log", dirDelivery, "ign:FUNC-25", "/ign/log", 16)
	insertLegacy("运行观测", "menu-ign-ops", dirObs, "ign:FUNC-29", "/ign/ops", 21)
	// 运维自定义：挂在种子目录下的自有叶 ⇒ 该目录不得被"无可见子项"隐去
	mustSQL(t, `INSERT INTO sys_resource (parent_id, name, code, permission_code, type, path, icon, sort_order, status)
		VALUES (?, '运维自有台账', 'menu-ign-custom', '', 'MENU', '/ign/custom', 'List', 99, 1)`, dirObs)

	if err := backfillIgnSeed(); err != nil {
		t.Fatal(err)
	}
	q := `SELECT parent_id, name, permission_code, status FROM sys_resource WHERE code = ?`
	for _, code := range menusV15Retired {
		row := mustOne(t, q, code)
		if row == nil {
			t.Fatalf("%s 被物理删除（MU menuNote：置不可见而非删除）", code)
		}
		if toInt(row["status"]) != 0 {
			t.Errorf("%s 仍可见，期望 status=0", code)
		}
	}
	names := map[string]string{"menu-ign-version": "交付资产", "menu-ign-executor": "执行位", "menu-ign-task": "交付任务"}
	for _, code := range menusV15Kept {
		row := mustOne(t, q, code)
		if row == nil {
			t.Fatalf("保留叶 %s 缺失", code)
		}
		if toInt(row["status"]) != 1 || toInt(row["parent_id"]) != 0 {
			t.Errorf("%s 未升到一级或不可见: %v", code, row)
		}
		if want, ok := names[code]; ok && Str(row["name"]) != want {
			t.Errorf("%s 名称 %q，期望跟 MU 走（%q）", code, Str(row["name"]), want)
		}
	}
	if got := Str(mustOne(t, q, "menu-ign-cluster")["permission_code"]); got != "ign:FUNC-52" {
		t.Errorf("集群交付叶门禁 %q，期望 ign:FUNC-52（新叶由收敛补插）", got)
	}
	if toInt(mustOne(t, q, "menu-ign-delivery")["status"]) != 0 {
		t.Errorf("交付管理目录已无可见子项却仍可见")
	}
	// 运行观测目录的种子子项全部退出侧栏，但运维自定义叶仍挂在下面 ⇒ 目录保住可见，
	// "无可见 MENU 子项"的隐去条件不能被误用成"种子子项全退了就隐"。
	if toInt(mustOne(t, q, "menu-ign-obs")["status"]) != 1 {
		t.Errorf("运行观测目录因挂着运维自定义可见叶被隐去")
	}
	// 幂等：再跑一次形状不变，自定义行不动
	if err := backfillIgnSeed(); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, `SELECT COUNT(*) FROM sys_resource WHERE code = ?`, "menu-ign-cluster"); n != 1 {
		t.Errorf("集群交付叶被重复补插 %d 行", n)
	}
	if toInt(mustOne(t, q, "menu-ign-custom")["status"]) != 1 {
		t.Errorf("运维自定义叶被动了")
	}
	if toInt(mustOne(t, q, "menu-ign-template")["parent_id"]) == 0 {
		t.Errorf("退出侧栏的叶被改了挂载位（只该动可见性）")
	}
}
