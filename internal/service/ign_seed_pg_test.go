// ign_seed_pg_test.go —— 菜单入口权限与页面数据接口一致（第七部分矩阵一 / 8.x 界面归属；DEF-02）。
package service

import "testing"

// TestSeedMenuLogPermission 种子口径：/ign/log 三个页签的数据接口是 FUNC-25~27 与 FUNC-37，
// FUNC-28（对账与纠偏台账）只有平台运维与审计员两格 ✓ —— 拿它当入口门禁会让交付管理员
// 与只读岗位「有数据权限、无侧栏入口」。入口门禁取四岗位共有的最小公共面 FUNC-25。
func TestSeedMenuLogPermission(t *testing.T) {
	found := false
	for _, r := range seedResources {
		if r.Code != "menu-ign-log" {
			continue
		}
		found = true
		if r.PermCode != "ign:FUNC-25" {
			t.Errorf("菜单 ign:FUNC-28 口径 %q，期望 ign:FUNC-25（页面主数据接口）", r.PermCode)
		}
	}
	if !found {
		t.Fatal("种子菜单缺 menu-ign-log")
	}
}

// TestBackfillCorrectsLegacyMenuLogPermission 存量库同样要纠正，但只纠正"仍是旧口径"的行，
// 运维自定义过的权限不覆写。
func TestBackfillCorrectsLegacyMenuLogPermission(t *testing.T) {
	requireDB(t)
	const q = `SELECT permission_code FROM sys_resource WHERE code = ?`

	mustSQL(t, `DELETE FROM sys_resource WHERE code = 'menu-ign-log'`)
	mustSQL(t, `INSERT INTO sys_resource (parent_id, name, code, permission_code, type, path, icon, sort_order, status)
		VALUES (0, '日志与对账', 'menu-ign-log', 'ign:FUNC-28', 'MENU', '/ign/log', 'FileText', 16, 1)`)
	if err := backfillIgnSeed(); err != nil {
		t.Fatal(err)
	}
	if got := Str(mustOne(t, q, "menu-ign-log")["permission_code"]); got != "ign:FUNC-25" {
		t.Fatalf("存量库旧口径未纠正: %q", got)
	}

	mustSQL(t, `UPDATE sys_resource SET permission_code = 'ign:FUNC-31' WHERE code = 'menu-ign-log'`)
	if err := backfillIgnSeed(); err != nil {
		t.Fatal(err)
	}
	if got := Str(mustOne(t, q, "menu-ign-log")["permission_code"]); got != "ign:FUNC-31" {
		t.Errorf("纠偏覆写了非旧口径的自定义值: %q", got)
	}
}
