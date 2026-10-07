// ign_m5_seed_model_test.go —— M5 种子(角色/权限/发牌)必须由本体模型派生。
// 口径与 goose 的 000006_m5_seed.sql 一致:sys_permission.code = "ign:<m2 行为号>"，
// target_ref 取 m5 permissions[].targetRef(FUNC-nn)，权限名取 m2 行为名。
package service

import (
	"testing"

	"gitcode.com/opic-ontology/opic-techbase/internal/pkg/ontology"
)

func TestSeedPermissionsDerivedFromM5Model(t *testing.T) {
	requireDB(t)
	reg := ontology.Load()
	if err := EnsureSeed(); err != nil {
		t.Fatalf("种子入库失败: %v", err)
	}
	if err := EnsureSeed(); err != nil {
		t.Fatalf("种子二次执行不幂等: %v", err)
	}
	if n, want := countRows(t, `SELECT COUNT(*) FROM sys_permission WHERE code LIKE 'ign:FUNC-%'`),
		int64(len(reg.Permissions)); n != want {
		t.Fatalf("业务权限行数应等于 m5 permissions 全集: model=%d db=%d", want, n)
	}
	if n := countRows(t, `SELECT COUNT(*) FROM sys_permission WHERE code LIKE 'PERM-FUNC-%'`); n != 0 {
		t.Fatalf("库里还有 %d 行把 m5 permissionId 当成权限码(运行期鉴权口径是 ign:FUNC-nn)", n)
	}
	for _, p := range buildSeedPermissions() {
		ref := p.TargetRef
		row := mustOne(t, `SELECT name, target_type, target_ref, data_scope FROM sys_permission WHERE code=?`, p.Code)
		if row == nil {
			t.Fatalf("G3b:%s 未入库(m5 permissions)", p.Code)
		}
		if Str(row["target_type"]) != p.TargetType || Str(row["target_ref"]) != ref ||
			Str(row["data_scope"]) != p.DataScope {
			t.Fatalf("G3b:%s 口径不符: model(%s,%s,%s) db=%+v", p.Code,
				p.TargetType, ref, p.DataScope, row)
		}
		b := reg.Behaviors[ref]
		if b == nil {
			t.Fatalf("G3b:%s 指向的 m2 行为 %s 不存在", p.Code, ref)
		}
		if Str(row["name"]) != p.Name || p.Name != Str(b["name"]) {
			t.Fatalf("G3b:%s 权限名应取 m2 行为名 %q,实际 %q", p.Code, Str(b["name"]), Str(row["name"]))
		}
	}
	if n, want := countRows(t, `SELECT COUNT(*) FROM sys_role`), int64(len(reg.Roles)+1); n != want {
		t.Fatalf("角色行数应等于 m5 roles + admin 超管位: want=%d db=%d", want, n)
	}
	actual := map[string]map[string]bool{}
	for _, r := range mustList(t, `
		SELECT ro.code AS role_code, p.code AS perm_code FROM sys_role_permission rp
		JOIN sys_role ro ON ro.id=rp.role_id JOIN sys_permission p ON p.id=rp.permission_id`) {
		rc := Str(r["role_code"])
		if actual[rc] == nil {
			actual[rc] = map[string]bool{}
		}
		actual[rc][Str(r["perm_code"])] = true
	}
	for roleCode, codes := range buildSeedRolePermissions() {
		want := map[string]bool{}
		for _, c := range codes {
			want[c] = true
		}
		got := actual[roleCode]
		for pc := range want {
			if !got[pc] {
				t.Fatalf("G3b:%s 缺绑定 %s(m5 roles[].permissions)", roleCode, pc)
			}
		}
		for pc := range got {
			if !want[pc] {
				t.Fatalf("G3b:%s 多绑 %s(模型里没有此权限码)", roleCode, pc)
			}
		}
	}
}
