// ign_menus_pg_test.go —— 菜单树装载（8.1.1 侧栏形状 / 矩阵一）。
//
// v1.5 侧栏扁平为「一级五叶、0 二级」：本域叶从此是挂在根上的 MENU 行（parent_id=0、无目录节点）。
// 而 GetMenus 的 v1.4 形状只把 DIRECTORY 端出去、MENU 一律当子项聚合 ⇒ 一级叶 whole-scale 消失，
// 界面上一整条本域侧栏是空的（种子建了行、菜单接口不吐）。这里把根级 MENU 的装载钉成用例。
package service

import (
	"slices"
	"strings"
	"testing"

	"gitcode.com/opic-ontology/opic-techbase/internal/store"
)

// menuNames 把菜单树压成 "name[/首子项名]" 序列，便于按侧栏实际顺序断言。
// 只取 menu-tree- 前缀的夹具行：库里种子/平台自带的菜单不参与本用例判据。
func menuNames(menus []map[string]any) []string {
	out := []string{}
	for _, m := range menus {
		if !strings.HasPrefix(Str(m["code"]), "menu-tree-") {
			continue
		}
		label := Str(m["name"])
		if kids, ok := m["children"].([]map[string]any); ok && len(kids) > 0 {
			label += "/" + Str(kids[0]["name"])
		}
		out = append(out, label)
	}
	return out
}

// TestGetMenusRootLevelLeaves 根级 MENU 叶要端出去、按权限码与可见性裁切、与目录节点混排时按 sort_order。
func TestGetMenusRootLevelLeaves(t *testing.T) {
	requireDB(t)
	roleCode := "IGN-ROLE-TREE-A"
	userName := "ign_tree_holder"
	dirCode := "menu-tree-dir"
	l1Code := "menu-tree-l1"
	heldPerm := "ign:FUNC-TREE-A"
	otherPerm := "ign:FUNC-TREE-B"

	t.Cleanup(func() {
		softSQL(`DELETE FROM sys_user_role WHERE user_id IN (SELECT id FROM sys_user WHERE username = ?)`, userName)
		softSQL(`DELETE FROM sys_user WHERE username = ?`, userName)
		softSQL(`DELETE FROM sys_role_permission WHERE role_id IN (SELECT id FROM sys_role WHERE code = ?)`, roleCode)
		softSQL(`DELETE FROM sys_role WHERE code = ?`, roleCode)
		softSQL(`DELETE FROM sys_permission WHERE code IN (?, ?)`, heldPerm, otherPerm)
		softSQL(`DELETE FROM sys_resource WHERE code LIKE 'menu-tree-%'`)
	})
	// 上一轮残留（同 code 复用）先清干净，用例只认自己插的那几行
	mustSQL(t, `DELETE FROM sys_user_role WHERE user_id IN (SELECT id FROM sys_user WHERE username = ?)`, userName)
	mustSQL(t, `DELETE FROM sys_user WHERE username = ?`, userName)
	mustSQL(t, `DELETE FROM sys_role_permission WHERE role_id IN (SELECT id FROM sys_role WHERE code = ?)`, roleCode)
	mustSQL(t, `DELETE FROM sys_role WHERE code = ?`, roleCode)
	mustSQL(t, `DELETE FROM sys_permission WHERE code IN (?, ?)`, heldPerm, otherPerm)
	mustSQL(t, `DELETE FROM sys_resource WHERE code LIKE 'menu-tree-%'`)

	mustSQL(t, `INSERT INTO sys_role (name, code, status) VALUES ('用例角色', ?, 1)`, roleCode)
	roleID := toInt(mustOne(t, `SELECT id FROM sys_role WHERE code = ?`, roleCode)["id"])
	mustSQL(t, `INSERT INTO sys_user (username, password, real_name, actor_type, status) VALUES (?, 'x', '用例', 'HUMAN', 1)`, userName)
	userID := toInt(mustOne(t, `SELECT id FROM sys_user WHERE username = ?`, userName)["id"])
	mustSQL(t, `INSERT INTO sys_user_role (user_id, role_id) VALUES (?, ?)`, userID, roleID)
	insertPerm := func(code, name string) int64 {
		mustSQL(t, `INSERT INTO sys_permission (code, name, target_type, target_ref, data_scope, status)
			VALUES (?, ?, 'FUNC', ?, 'ALL', 1)`, code, name, code)
		return toInt(mustOne(t, `SELECT id FROM sys_permission WHERE code = ?`, code)["id"])
	}
	permID := insertPerm(heldPerm, "用例权限")
	mustSQL(t, `INSERT INTO sys_role_permission (role_id, permission_id) VALUES (?, ?)`, roleID, permID)
	insertPerm(otherPerm, "用例另一权限")

	ins := func(parent int64, name, code, perm, path, rtype string, status, sort int) int64 {
		mustSQL(t, `INSERT INTO sys_resource (parent_id, name, code, permission_code, type, path, icon, sort_order, status)
			VALUES (?, ?, ?, ?, ?, ?, 'List', ?, ?)`, parent, name, code, perm, rtype, path, sort, status)
		return toInt(mustOne(t, `SELECT id FROM sys_resource WHERE code = ?`, code)["id"])
	}
	// 一级叶：持牌可见（对应 v1.5 的「集群交付」叶）
	ins(0, "一级持牌叶", l1Code, heldPerm, "/ign/cluster", "MENU", 1, 10)
	// 一级叶：无门禁（平台自带叶同形）⇒ 对所有登录身份开放
	ins(0, "一级无门禁叶", "menu-tree-open", "", "/ign/open", "MENU", 1, 11)
	// 一级叶：牌不在手上 ⇒ 不出现（8.3.1 判据）
	ins(0, "一级无权叶", "menu-tree-secret", otherPerm, "/ign/secret", "MENU", 1, 12)
	// 一级叶：行在库里但已置不可见（v1.5 退出侧栏的落库口径）⇒ 不出现
	ins(0, "一级退出叶", "menu-tree-hidden", heldPerm, "/ign/hidden", "MENU", 0, 13)
	dirID := ins(0, "用例分组", dirCode, "", "", "DIRECTORY", 1, 20)
	ins(dirID, "分组子叶", "menu-tree-child", heldPerm, "/ign/child", "MENU", 1, 21)

	menus, err := GetMenus(store.DB, userID)
	if err != nil {
		t.Fatal(err)
	}
	got := menuNames(menus)
	want := []string{"一级持牌叶", "一级无门禁叶", "用例分组/分组子叶"}
	if !slices.Equal(got, want) {
		t.Fatalf("菜单树 %v，期望 %v（一级叶要端出去，无权/不可见的不端，且与目录按 sort_order 混排）", got, want)
	}
}
