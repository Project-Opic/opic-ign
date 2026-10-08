package service

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gitcode.com/opic-ontology/opic-techbase/internal/config"
	"gitcode.com/opic-ontology/opic-techbase/internal/pkg/auth"
	"gitcode.com/opic-ontology/opic-techbase/internal/pkg/ontology"
	"gitcode.com/opic-ontology/opic-techbase/internal/store"
)

// GetRoleByCode 按编码取角色。
func GetRoleByCode(code string) (map[string]any, error) {
	return qOne(`SELECT * FROM sys_role WHERE code = ?`, code)
}

// SyncUserRolesByCodes 按角色编码全量同步用户角色(ZITADEL claim → 本地角色)。
// 仅映射本地已存在的角色;返回实际生效的本地角色编码。
func SyncUserRolesByCodes(userID int64, roleCodes []string) error {
	var roleIDs []any
	applied := map[string]bool{}
	for _, code := range roleCodes {
		code = strings.TrimSpace(code)
		if code == "" || applied[code] {
			continue
		}
		role, err := GetRoleByCode(code)
		if err != nil || role == nil {
			continue
		}
		applied[code] = true
		roleIDs = append(roleIDs, Int(role["id"]))
	}
	if len(roleIDs) == 0 {
		return nil
	}
	return SetUserRoles(userID, roleIDs)
}

// ---------- 种子数据(交付执行域 M5:m5-actor-model.yaml + m2-behavior-model.yaml 派生) ----------

// bizPermPrefix 业务权限码前缀:sys_permission.code = "<域码>:<m2 行为号>"，
// 与路由层 RequirePermission("ign:FUNC-nn") 同口径;m5 permissions[].permissionId(PERM-FUNC-nn)
// 是模型侧编号，不落库。
const bizPermPrefix = "ign"

// adminRoleCode 底座超管位角色码(不在 m5 模型内，由代码注入)。
const adminRoleCode = "admin"

// seedRoleDesc m5 roles[].roleId → 角色说明列(m5 只给 roleId/name/permissions，说明是展示文案)。
var seedRoleDesc = map[string]string{
	"admin":       "系统内置超级管理员",
	"IGN-ROLE-01": "交付版本编目与上架、执行机注册与引导、任务提交与取消(M5 ACTOR-IGN-ROLE-01)",
	"IGN-ROLE-02": "运行参数维护、TLS 降级声明、审批执行机强制注销(M5 ACTOR-IGN-ROLE-02)",
	"IGN-ROLE-03": "交付执行域全过程取证;全部菜单只读可见(零写操作)(M5 ACTOR-IGN-ROLE-03)",
	"IGN-ROLE-04": "目录/台账/日志只读查询(M5 ACTOR-IGN-ROLE-04)",
	"IGN-ROLE-05": "服务身份(非岗位):引导回连/跨中心视图/任务回执(M5 ACTOR-IGN-ROLE-05)",
}

// sortedKeys map 键升序切片(种子入库顺序稳定，结果可复算)。
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// seedPerm m5 permissions[] 对应的一行 sys_permission。
type seedPerm struct {
	Code, Name, TargetType, TargetRef, DataScope string
}

// buildSeedPermissions 由本体注册表展开 m5 permissions 全集，权限名取 m2 行为名。
// 模型是唯一语义来源:m5 增删权限码只改 YAML。此前此处硬编码 37 条权限名，
// 与 m5 的条数/口径各改各的，是种子漂移的源头。
func buildSeedPermissions() []seedPerm {
	reg := ontology.Load()
	out := make([]seedPerm, 0, len(reg.Permissions))
	for _, p := range reg.Permissions {
		ref := Str(p["targetRef"])
		name := ref
		if b, ok := reg.Behaviors[ref]; ok {
			name = Str(b["name"])
		}
		tt := Str(p["targetType"])
		if tt == "" {
			tt = "BEHAVIOR"
		}
		scope := Str(p["dataScope"])
		if scope == "" {
			scope = "ALL"
		}
		out = append(out, seedPerm{
			Code:       bizPermPrefix + ":" + ref,
			Name:       name,
			TargetType: tt,
			TargetRef:  ref,
			DataScope:  scope,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// m5RoleNames m5 roles[].roleId → name，并补 admin 超管位。
func m5RoleNames() map[string]string {
	reg := ontology.Load()
	out := make(map[string]string, len(reg.Roles)+1)
	for id, role := range reg.Roles {
		out[id] = Str(role["name"])
	}
	out[adminRoleCode] = "系统管理员"
	return out
}

// buildSeedRolePermissions m5 roles[].permissions 全量映射成运行期权限码
// (admin 超管位无模型行，不参与绑定;PERM-FUNC-nn 按 m5 permissions[] 换回 ign:FUNC-nn)。
func buildSeedRolePermissions() map[string][]string {
	reg := ontology.Load()
	out := make(map[string][]string, len(reg.Roles))
	for id, role := range reg.Roles {
		var codes []string
		if list, ok := role["permissions"].([]any); ok {
			for _, v := range list {
				pc := Str(v)
				ref := Str(reg.Permissions[pc]["targetRef"])
				codes = append(codes, bizPermPrefix+":"+ref)
			}
		}
		sort.Strings(codes)
		out[id] = codes
	}
	return out
}

// seedPermissions 平台权限(管理控制台/流程引擎;fork 保留)。
var seedPermissions = []struct{ Code, Name, TargetType, TargetRef string }{
	{"system:user:add", "用户新增", "BEHAVIOR", "User_Add"},
	{"system:user:edit", "用户编辑", "BEHAVIOR", "User_Edit"},
	{"system:user:delete", "用户删除", "BEHAVIOR", "User_Delete"},
	{"system:user:assign-role", "分配角色", "BEHAVIOR", "User_AssignRole"},
	{"system:user:reset-pwd", "重置密码", "BEHAVIOR", "User_ResetPassword"},
	{"system:role:add", "角色新增", "BEHAVIOR", "Role_Add"},
	{"system:role:edit", "角色编辑", "BEHAVIOR", "Role_Edit"},
	{"system:role:delete", "角色删除", "BEHAVIOR", "Role_Delete"},
	{"system:role:assign", "分配权限资源", "BEHAVIOR", "Role_Assign"},
	{"system:permission:add", "权限新增", "BEHAVIOR", "Permission_Add"},
	{"system:permission:edit", "权限编辑", "BEHAVIOR", "Permission_Edit"},
	{"system:permission:delete", "权限删除", "BEHAVIOR", "Permission_Delete"},
	{"system:resource:add", "资源新增", "BEHAVIOR", "Resource_Add"},
	{"system:resource:edit", "资源编辑", "BEHAVIOR", "Resource_Edit"},
	{"system:resource:delete", "资源删除", "BEHAVIOR", "Resource_Delete"},
	{"flow:definition:add", "流程新增", "BEHAVIOR", "Flow_Add"},
	{"flow:definition:edit", "流程编辑", "BEHAVIOR", "Flow_Edit"},
	{"flow:definition:publish", "流程发布", "BEHAVIOR", "Flow_Publish"},
	{"flow:instance:terminate", "强制终止", "BEHAVIOR", "Flow_Terminate"},
	{"flow:task:transfer", "任务转办", "BEHAVIOR", "Flow_Transfer"},
	{"flow:task:urge", "任务催办", "BEHAVIOR", "Flow_Urge"},
	// 管理控制台入口(菜单 permission_code)
	{"system:manage", "管理控制台", "MENU", "Admin_Console"},
}

// seedResources 资源/菜单(mu-ui-model menus:交付管理 + 运行观测 + 审批中心 + 系统管理)。
var seedResources = []struct {
	Parent, Name, Code, PermCode, Type, Path, Icon string
	Sort                                           int
}{
	// L1 交付管理(mu menu-l1-01)
	{"", "交付管理", "menu-ign-delivery", "", "DIRECTORY", "", "DeploymentUnit", 10},
	{"menu-ign-delivery", "版本目录", "menu-ign-version", "ign:FUNC-06", "MENU", "/ign/version", "Appstore", 11},
	{"menu-ign-delivery", "发行包与镜像", "menu-ign-image", "ign:FUNC-12", "MENU", "/ign/image", "Cube", 12},
	{"menu-ign-delivery", "执行机", "menu-ign-executor", "ign:FUNC-17", "MENU", "/ign/executor", "Cluster", 13},
	{"menu-ign-delivery", "通道安全", "menu-ign-tls", "ign:FUNC-22", "MENU", "/ign/tls", "SafetyCertificate", 14},
	{"menu-ign-delivery", "任务桥接", "menu-ign-task", "ign:FUNC-25", "MENU", "/ign/task", "Thunderbolt", 15},
	// MU v1.3 menu-ign-template「参数面模板」：入口门禁取本屏主数据接口 FUNC-38（四岗位共有的列表查询发牌）。
	// sort=15 与任务桥接同值，GetMenus 按 (sort_order,id) 升序天然落在任务桥接之后、日志与对账(16)之前，与 MU 菜单序一致。
	{"menu-ign-delivery", "参数面模板", "menu-ign-template", "ign:FUNC-38", "MENU", "/ign/template", "Profile", 15},
	// MU v1.3 menu-ign-config「集群交付配置」：入口门禁取本屏主数据接口 FUNC-46（与模板屏取 FUNC-38 同一口径：
	// 列表/主数据查询的最小公共面，不取只覆盖单个页签的行为码）。sort=15 依 (sort_order,id) 升序落在
	// 参数面模板之后、日志与对账(16)之前，与 MU 菜单序（…task → template → config → log）一致。
	{"menu-ign-delivery", "集群交付配置", "menu-ign-config", "ign:FUNC-46", "MENU", "/ign/config", "Settings", 15},
	// 入口门禁取页面主数据接口 FUNC-25（三页签四岗位共有的最小公共面），不取只覆盖单个页签的 FUNC-28。
	{"menu-ign-delivery", "日志与对账", "menu-ign-log", "ign:FUNC-25", "MENU", "/ign/log", "FileText", 16},
	// L1 运行观测(mu menu-l1-02)
	{"", "运行观测", "menu-ign-obs", "", "DIRECTORY", "", "Audit", 20},
	{"menu-ign-obs", "运行观测", "menu-ign-ops", "ign:FUNC-29", "MENU", "/ign/ops", "Dashboard", 21},
	// L1 审批中心(FLOW-PROC-APP-01 待办入口)
	{"", "审批中心", "menu-workbench", "", "DIRECTORY", "", "Audit", 30},
	{"menu-workbench", "我的待办", "menu-workbench-todo", "", "MENU", "/workbench/todo", "Inbox", 31},
	{"menu-workbench", "我的已办", "menu-workbench-done", "", "MENU", "/workbench/done", "CheckSquare", 32},
	{"menu-workbench", "我的申请", "menu-workbench-requested", "", "MENU", "/workbench/requested", "FileText", 33},
	// L1 系统管理(admin 控制台入口;permission_code=system:manage)
	{"", "系统管理", "menu-system", "system:manage", "DIRECTORY", "", "Settings", 40},
	{"menu-system", "用户管理", "menu-system-user", "system:manage", "MENU", "/admin/system/users", "User", 41},
	{"menu-system", "角色管理", "menu-system-role", "system:manage", "MENU", "/admin/system/roles", "Shield", 42},
	{"menu-system", "权限管理", "menu-system-permission", "system:manage", "MENU", "/admin/system/permissions", "KeySquare", 43},
	{"menu-system", "资源管理", "menu-system-resource", "system:manage", "MENU", "/admin/system/resources", "Menu", 44},
}

var seedUsers = []struct{ Username, Password, RealName, RoleCode string }{
	{"admin", "admin123", "管理员", "admin"},
	{"ignadmin", "admin123", "交付管理员", "IGN-ROLE-01"},
	{"opsadmin", "admin123", "平台运维管理员", "IGN-ROLE-02"},
	{"auditor", "admin123", "交付审计员", "IGN-ROLE-03"},
	{"ignviewer", "admin123", "交付只读用户", "IGN-ROLE-04"},
}

// ensureRoleID 幂等取角色 id:先按 code 命中，其次按 name 命中并把 code 补成模型口径。
// goose 的 m5 种子迁移(000006_m5_seed.sql)与 EnsureSeed 写的是同一组 (code, name)，
// 两条路径谁先跑都不能撞 sys_role 的 name/code UNIQUE。
func ensureRoleID(code, name, desc string) (int64, error) {
	row, err := qOne(`SELECT id FROM sys_role WHERE code = ?`, code)
	if err != nil {
		return 0, err
	}
	if row != nil {
		return toInt(row["id"]), nil
	}
	row, err = qOne(`SELECT id FROM sys_role WHERE name = ?`, name)
	if err != nil {
		return 0, err
	}
	if row != nil {
		id := toInt(row["id"])
		if _, err := qExec(`UPDATE sys_role SET code = ? WHERE id = ?`, code, id); err != nil {
			return 0, err
		}
		return id, nil
	}
	if _, err := qExec(`INSERT INTO sys_role (name, code, parent_id, description, status) VALUES (?, ?, 0, ?, 1)`,
		name, code, desc); err != nil {
		return 0, err
	}
	row, err = qOne(`SELECT id FROM sys_role WHERE code = ?`, code)
	if err != nil {
		return 0, err
	}
	if row == nil {
		return 0, fmt.Errorf("角色入库后未取到 id: %s", code)
	}
	return toInt(row["id"]), nil
}

// ensurePermissionID 幂等取权限 id，并把已有行校正回 m5/m2 口径。
// 校正不可省:goose 的 m5 种子迁移曾把权限码写成 m5 permissionId(PERM-FUNC-nn)、target_ref 也写成码本身，
// 而运行期鉴权用的是 ign:FUNC-nn + target_ref=FUNC-nn;只 INSERT 不校正，存量库会一直带着错值。
func ensurePermissionID(p seedPerm) (int64, error) {
	row, err := qOne(`SELECT id, name, target_type, target_ref, data_scope FROM sys_permission WHERE code = ?`, p.Code)
	if err != nil {
		return 0, err
	}
	if row != nil {
		id := toInt(row["id"])
		if Str(row["target_ref"]) == p.TargetRef && Str(row["target_type"]) == p.TargetType &&
			Str(row["name"]) == p.Name && Str(row["data_scope"]) == p.DataScope {
			return id, nil
		}
		if _, err := qExec(`
			UPDATE sys_permission SET name = ?, target_type = ?, target_ref = ?, data_scope = ? WHERE id = ?`,
			p.Name, p.TargetType, p.TargetRef, p.DataScope, id); err != nil {
			return 0, err
		}
		return id, nil
	}
	if _, err := qExec(`
		INSERT INTO sys_permission (code, name, target_type, target_ref, data_scope, status)
		VALUES (?, ?, ?, ?, ?, 1)`, p.Code, p.Name, p.TargetType, p.TargetRef, p.DataScope); err != nil {
		return 0, err
	}
	row, err = qOne(`SELECT id FROM sys_permission WHERE code = ?`, p.Code)
	if err != nil {
		return 0, err
	}
	if row == nil {
		return 0, fmt.Errorf("权限入库后未取到 id: %s", p.Code)
	}
	return toInt(row["id"]), nil
}

// ensureRolePermission 幂等绑定角色-权限。
func ensureRolePermission(roleID, permID int64) error {
	row, err := qOne(`SELECT role_id FROM sys_role_permission WHERE role_id = ? AND permission_id = ?`, roleID, permID)
	if err != nil || row != nil {
		return err
	}
	_, err = qExec(`INSERT INTO sys_role_permission (role_id, permission_id) VALUES (?, ?)`, roleID, permID)
	return err
}

// syncM5Authorization m5 角色/权限/角色权限绑定入库(幂等)，返回角色码 → id。
// 空库首建与存量库回填共用这一条路径:m5 加行为发牌只改 YAML，种子自己跟上，
// 与 goose 的 m5 种子迁移互为重复执行。
func syncM5Authorization() (map[string]int64, error) {
	names := m5RoleNames()
	roleIDs := map[string]int64{}
	for _, code := range sortedKeys(names) {
		id, err := ensureRoleID(code, names[code], seedRoleDesc[code])
		if err != nil {
			return nil, err
		}
		roleIDs[code] = id
	}
	permIDs := map[string]int64{}
	for _, p := range buildSeedPermissions() {
		id, err := ensurePermissionID(p)
		if err != nil {
			return nil, err
		}
		permIDs[p.Code] = id
	}
	bindings := buildSeedRolePermissions()
	for _, roleCode := range sortedKeys(bindings) {
		roleID, ok := roleIDs[roleCode]
		if !ok {
			continue
		}
		for _, pc := range bindings[roleCode] {
			permID, ok := permIDs[pc]
			if !ok {
				return nil, fmt.Errorf("角色 %s 绑定的 %s 不在 m5 permissions 全集", roleCode, pc)
			}
			if err := ensureRolePermission(roleID, permID); err != nil {
				return nil, err
			}
		}
	}
	return roleIDs, nil
}

// syncPlatformPermissions 平台权限(控制台/流程引擎;不在 m5 模型内，fork 自带口径)。
func syncPlatformPermissions() error {
	for _, p := range seedPermissions {
		if err := ensureRow("sys_permission", "code", p.Code,
			`INSERT INTO sys_permission (code, name, target_type, target_ref, data_scope, status)
			 VALUES (?, ?, ?, ?, 'ALL', 1)`, p.Code, p.Name, p.TargetType, p.TargetRef); err != nil {
			return err
		}
	}
	return nil
}

// ensureAdminRole 内置超管角色(代码层 GetPermissionCodes 对 code=admin 注入 "*";幂等)。
func ensureAdminRole() error {
	if err := ensureRow("sys_role", "code", "admin",
		`INSERT INTO sys_role (name, code, parent_id, description, status) VALUES (?, ?, 0, ?, 1)`,
		"系统管理员", "admin", "系统内置超级管理员"); err != nil {
		return err
	}
	user, err := qOne(`SELECT id FROM sys_user WHERE username=?`, "admin")
	if err != nil || user == nil {
		return err
	}
	role, err := qOne(`SELECT id FROM sys_role WHERE code=?`, "admin")
	if err != nil || role == nil {
		return err
	}
	n, err := qCount(`SELECT COUNT(*) FROM sys_user_role WHERE user_id=? AND role_id=?`, toInt(user["id"]), toInt(role["id"]))
	if err != nil {
		return err
	}
	if n == 0 {
		_, err = qExec(`INSERT INTO sys_user_role (user_id, role_id) VALUES (?, ?)`, toInt(user["id"]), toInt(role["id"]))
	}
	return err
}

// ensureRow 通用幂等插入(按唯一列判存在)。
func ensureRow(table, keyCol string, keyExpr any, insertSQL string, args ...any) error {
	row, err := qOne(`SELECT id FROM `+table+` WHERE `+keyCol+` = ?`, keyExpr)
	if err != nil {
		return err
	}
	if row != nil {
		return nil
	}
	_, err = qExec(insertSQL, args...)
	return err
}

// EnsureSeed 种子(幂等)。权限矩阵(角色/权限/发牌)两条路径都按 m5+m2 模型同步一次，
// 空库再补菜单、账号与流程定义。
func EnsureSeed() error {
	count, err := qCount(`SELECT COUNT(*) FROM sys_user`)
	if err != nil {
		return err
	}
	// 权限矩阵(m5 + 平台权限):空库与存量库同一条路径
	roleIDs, err := syncM5Authorization()
	if err != nil {
		return err
	}
	if err := syncPlatformPermissions(); err != nil {
		return err
	}
	if count > 0 {
		if err := ensureAdminRole(); err != nil {
			return err
		}
		// 存量库幂等回填:配置键/LOCAL 执行机/门面服务账号
		// （EnsureSeed 主路径只在空库执行，不回填则执行位无从登记）
		if err := backfillIgnSeed(); err != nil {
			return err
		}
		return ensureFlowDefinitions()
	}
	// 资源/菜单
	resIDs := map[string]int64{}
	for _, r := range seedResources {
		parentID := resIDs[r.Parent]
		if _, err := qExec(`
			INSERT INTO sys_resource (parent_id, name, code, permission_code, type, path, component, icon, sort_order, status)
			VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?, 1)`,
			parentID, r.Name, r.Code, r.PermCode, r.Type, r.Path, r.Icon, r.Sort); err != nil {
			return err
		}
		row, err := qOne(`SELECT id FROM sys_resource WHERE code = ?`, r.Code)
		if err != nil {
			return err
		}
		resIDs[r.Code] = toInt(row["id"])
	}
	// 用户(先落用户,再按查得的 ID 绑定角色——账号-角色绑定顺序)
	for _, u := range seedUsers {
		if _, err := qExec(`
			INSERT INTO sys_user (username, password, real_name, actor_type, status)
			VALUES (?, ?, ?, 'HUMAN', 1)`, u.Username, HashPassword(u.Password), u.RealName); err != nil {
			return err
		}
		row, err := qOne(`SELECT id FROM sys_user WHERE username = ?`, u.Username)
		if err != nil {
			return err
		}
		uid := toInt(row["id"])
		if _, err := qExec(`INSERT INTO sys_user_role (user_id, role_id) VALUES (?, ?)`, uid, roleIDs[u.RoleCode]); err != nil {
			return err
		}
	}
	// 运行参数缺省(REF-*;FUNC-30 可维护)——只在缺失时落缺省值，不覆盖运维改过的值
	for _, c := range defaultIgnConfigs() {
		if err := seedIgnConfigIfAbsent(store.DB, c.Key, c.Value, c.Desc, 1); err != nil {
			return err
		}
	}
	// LOCAL 执行机系统自动注册(INV-12/REF-10)
	if err := EnsureLocalExecutor(); err != nil {
		return err
	}
	// 交付门面服务账号（FUNC-34~36 的本域身份；M5 IGN-ROLE-05 非人工身份那一格）
	if err := EnsureFacadeServiceUser(); err != nil {
		return err
	}
	// 内置超管角色与 admin 账号绑定(用户落库之后执行)
	if err := ensureAdminRole(); err != nil {
		return err
	}
	return ensureFlowDefinitions()
}

// ensureFlowDefinitions 从本体注册表 M6 流程定义导入(幂等;仅导入带 nodeGraph 的可执行审批流,
// COLLABORATION 协作流程为叙述视图,不作为可执行定义导入——与 O-CRT/O-AST 口径一致)。
func ensureFlowDefinitions() error {
	reg := ontology.Load()
	for code, flow := range reg.Flows {
		if _, ok := flow["nodeGraph"]; !ok {
			continue
		}
		if exists, err := qOne(`SELECT id FROM flow_definition WHERE code = ?`, code); err != nil {
			return err
		} else if exists != nil {
			continue
		}
		name, _ := flow["name"].(string)
		if name == "" {
			name = code
		}
		flowType, _ := flow["flowType"].(string)
		if flowType == "" {
			flowType = "APPROVAL"
		}
		triggerType := "MANUAL"
		triggerBehavior := ""
		if trigger, ok := flow["trigger"].(map[string]any); ok {
			if v, ok := trigger["triggerType"].(string); ok && v != "" {
				triggerType = v
			}
			if v, ok := trigger["behaviorRef"].(string); ok {
				triggerBehavior = v
			}
		}
		desc, _ := flow["description"].(string)
		nodeGraph := "null"
		if ng := flow["nodeGraph"]; ng != nil {
			raw, err := json.Marshal(ng)
			if err == nil {
				nodeGraph = string(raw)
			}
		}
		if _, err := qExec(`
			INSERT INTO flow_definition (code, name, flow_type, trigger_type, trigger_behavior, description, node_graph, version, status, created_by)
			VALUES (?, ?, ?, ?, ?, ?, ?, 1, 1, 1)`,
			code, name, flowType, triggerType, triggerBehavior, desc, nodeGraph); err != nil {
			return err
		}
	}
	return nil
}

// backfillIgnSeed 存量库幂等回填（权限矩阵已由 syncM5Authorization 在两条路径上统一入库，
// 这里只剩本域特有的:菜单入口口径纠正、运行参数缺省、LOCAL 执行机与门面服务账号）。
func backfillIgnSeed() error {
	// 菜单入口口径纠正：旧种子把 /ign/log 挂在 FUNC-28（三页签之一的权限），交付管理员与只读岗位
	// 有数据权限却无侧栏入口；只纠正"仍是旧口径"的行，运维自定义过的一律不动。
	if _, err := qExec(`UPDATE sys_resource SET permission_code = ? WHERE code = ? AND permission_code = ?`,
		"ign:FUNC-25", "menu-ign-log", "ign:FUNC-28"); err != nil {
		return err
	}
	// v1.3 新屏菜单挂口（MU menu-ign-template「参数面模板」/ menu-ign-config「集群交付配置」）：
	// 存量库只在 code 缺失时补插一条，已存在（含运维改过）不动。
	for _, menuCode := range []string{"menu-ign-template", "menu-ign-config"} {
		if err := backfillV13Menu(menuCode); err != nil {
			return err
		}
	}
	for _, c := range defaultIgnConfigs() {
		if err := seedIgnConfigIfAbsent(store.DB, c.Key, c.Value, c.Desc, 1); err != nil {
			return err
		}
	}
	if err := EnsureLocalExecutor(); err != nil {
		return err
	}
	return EnsureFacadeServiceUser()
}

// backfillV13Menu 按 seedResources 登记口径补插一条缺失菜单；父目录不在（被删/改名）时不强造树。
func backfillV13Menu(code string) error {
	exists, err := qOne(`SELECT id FROM sys_resource WHERE code = ?`, code)
	if err != nil || exists != nil {
		return err
	}
	for _, r := range seedResources {
		if r.Code != code {
			continue
		}
		parent, err := qOne(`SELECT id FROM sys_resource WHERE code = ?`, r.Parent)
		if err != nil || parent == nil {
			return err
		}
		_, err = qExec(`INSERT INTO sys_resource (parent_id, name, code, permission_code, type, path, component, icon, sort_order, status)
			VALUES (?, ?, ?, ?, ?, ?, NULL, ?, ?, 1)`,
			toInt(parent["id"]), r.Name, r.Code, r.PermCode, r.Type, r.Path, r.Icon, r.Sort)
		return err
	}
	return nil
}

// EnsureFacadeServiceUser 交付门面服务账号（IGN-ROLE-05 非人工身份那一格；幂等）。
// 口令为随机不可用值：门面对上游只认通道令牌（delivery.facade_token），不存在密码登录路径。
func EnsureFacadeServiceUser() error {
	name := strings.TrimSpace(config.Config.Delivery.FacadeUser)
	if name == "" {
		name = "ignition-agent"
	}
	row, err := qOne(`SELECT id FROM sys_user WHERE username = ?`, name)
	if err != nil {
		return err
	}
	if row == nil {
		uid, err := CreateUser(map[string]any{
			"username":   name,
			"password":   "facade-managed:" + auth.NewState(),
			"real_name":  "交付门面服务身份（O-CLC 等下游中心）",
			"actor_type": "SERVICE",
			"status":     float64(1),
		})
		if err != nil {
			return err
		}
		row = map[string]any{"id": uid}
	}
	role, err := qOne(`SELECT id FROM sys_role WHERE code = 'IGN-ROLE-05'`)
	if err != nil || role == nil {
		return err
	}
	n, err := qCount(`SELECT COUNT(*) FROM sys_user_role WHERE user_id = ? AND role_id = ?`,
		toInt(row["id"]), toInt(role["id"]))
	if err != nil || n > 0 {
		return err
	}
	_, err = qExec(`INSERT INTO sys_user_role (user_id, role_id) VALUES (?, ?)`,
		toInt(row["id"]), toInt(role["id"]))
	return err
}

// FacadeServiceAccount 门面服务账号（中间件把通道令牌解析到本域身份；未登记返回 0）。
func FacadeServiceAccount() (int64, string) {
	name := strings.TrimSpace(config.Config.Delivery.FacadeUser)
	if name == "" {
		return 0, ""
	}
	row, err := qOne(`SELECT id FROM sys_user WHERE username = ? AND status = 1`, name)
	if err != nil || row == nil {
		return 0, ""
	}
	return toInt(row["id"]), name
}
