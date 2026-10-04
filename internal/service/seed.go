package service

import (
	"encoding/json"
	"strings"

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

// ---------- 种子数据(seed.py 对应;交付执行域 M5 定制) ----------

var seedRoles = []struct{ Code, Name, Desc string }{
	{"admin", "系统管理员", "系统内置超级管理员"},
	{"IGN-ROLE-01", "交付管理员", "交付版本编目与上架、执行机注册与引导、任务提交与取消(M5 ACTOR-IGN-ROLE-01)"},
	{"IGN-ROLE-02", "平台运维管理员", "运行参数维护、TLS 降级声明、审批执行机强制注销(M5 ACTOR-IGN-ROLE-02)"},
	{"IGN-ROLE-03", "交付审计员", "交付执行域全过程取证;全部菜单只读可见(零写操作)(M5 ACTOR-IGN-ROLE-03)"},
	{"IGN-ROLE-04", "交付只读用户", "目录/台账/日志只读查询(M5 ACTOR-IGN-ROLE-04)"},
	{"IGN-ROLE-05", "执行机代理", "服务身份(非岗位):引导回连/跨中心视图/任务回执(M5 ACTOR-IGN-ROLE-05)"},
}

// seedIgnPermissions 业务权限(FUNC-01..33 与 M5 PERM-FUNC-xx 一一对应;权限码 = ign:FUNC-xx)。
var seedIgnPermissions = []struct{ Code, Name, TargetRef string }{
	{"ign:FUNC-01", "版本登记", "FUNC-01"},
	{"ign:FUNC-02", "版本登记信息修改", "FUNC-02"},
	{"ign:FUNC-03", "触发发行包下载", "FUNC-03"},
	{"ign:FUNC-04", "版本归档", "FUNC-04"},
	{"ign:FUNC-05", "版本软删除", "FUNC-05"},
	{"ign:FUNC-06", "版本列表查询", "FUNC-06"},
	{"ign:FUNC-07", "版本详情查询", "FUNC-07"},
	{"ign:FUNC-08", "跨中心版本查询（集群创建视图）", "FUNC-08"},
	{"ign:FUNC-09", "触发镜像预推", "FUNC-09"},
	{"ign:FUNC-10", "镜像清单人工覆写", "FUNC-10"},
	{"ign:FUNC-11", "预推失败重试（断点续推）", "FUNC-11"},
	{"ign:FUNC-12", "预推明细查询", "FUNC-12"},
	{"ign:FUNC-13", "注册远程执行机", "FUNC-13"},
	{"ign:FUNC-14", "重新生成引导令牌（含锁定解锁）", "FUNC-14"},
	{"ign:FUNC-15", "执行机注销（空闲）", "FUNC-15"},
	{"ign:FUNC-16", "执行机能力标签调整", "FUNC-16"},
	{"ign:FUNC-17", "执行机列表查询（含健康视图）", "FUNC-17"},
	{"ign:FUNC-18", "执行机详情查询", "FUNC-18"},
	{"ign:FUNC-19", "跨中心执行机查询（集群创建视图）", "FUNC-19"},
	{"ign:FUNC-20", "手动触发证书续期", "FUNC-20"},
	{"ign:FUNC-21", "TLS 显式降级声明（实验环境）", "FUNC-21"},
	{"ign:FUNC-22", "证书绑定清单查询（含临期视图）", "FUNC-22"},
	{"ign:FUNC-23", "提交执行任务", "FUNC-23"},
	{"ign:FUNC-24", "取消执行任务", "FUNC-24"},
	{"ign:FUNC-25", "任务台账查询", "FUNC-25"},
	{"ign:FUNC-26", "任务详情查询", "FUNC-26"},
	{"ign:FUNC-27", "任务日志查询（中继回流视图）", "FUNC-27"},
	{"ign:FUNC-28", "对账与纠偏台账查询", "FUNC-28"},
	{"ign:FUNC-29", "统计总览查询", "FUNC-29"},
	{"ign:FUNC-30", "运行参数维护", "FUNC-30"},
	{"ign:FUNC-31", "强制注销 · 提交申请", "FUNC-31"},
	{"ign:FUNC-32", "强制注销 · 通过", "FUNC-32"},
	{"ign:FUNC-33", "强制注销 · 驳回", "FUNC-33"},
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
	{"menu-ign-delivery", "日志与对账", "menu-ign-log", "ign:FUNC-28", "MENU", "/ign/log", "FileText", 16},
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

// seedRolePermissions 角色→权限(M5 roles[].permissions;admin 超管位由代码层注入,不落数据)。
var seedRolePermissions = map[string][]string{
	"IGN-ROLE-01": {
		"ign:FUNC-01", "ign:FUNC-02", "ign:FUNC-03", "ign:FUNC-04", "ign:FUNC-05",
		"ign:FUNC-06", "ign:FUNC-07", "ign:FUNC-09", "ign:FUNC-10", "ign:FUNC-11", "ign:FUNC-12",
		"ign:FUNC-13", "ign:FUNC-14", "ign:FUNC-15", "ign:FUNC-16", "ign:FUNC-17", "ign:FUNC-18",
		"ign:FUNC-20", "ign:FUNC-22",
		"ign:FUNC-23", "ign:FUNC-24", "ign:FUNC-25", "ign:FUNC-26", "ign:FUNC-27",
		"ign:FUNC-29", "ign:FUNC-31",
	},
	"IGN-ROLE-02": {
		"ign:FUNC-06", "ign:FUNC-07", "ign:FUNC-12", "ign:FUNC-17", "ign:FUNC-18",
		"ign:FUNC-21", "ign:FUNC-22",
		"ign:FUNC-25", "ign:FUNC-26", "ign:FUNC-27", "ign:FUNC-28", "ign:FUNC-29", "ign:FUNC-30",
		"ign:FUNC-32", "ign:FUNC-33",
	},
	"IGN-ROLE-03": {
		"ign:FUNC-06", "ign:FUNC-07", "ign:FUNC-12", "ign:FUNC-17", "ign:FUNC-18",
		"ign:FUNC-22",
		"ign:FUNC-25", "ign:FUNC-26", "ign:FUNC-27", "ign:FUNC-28", "ign:FUNC-29",
	},
	"IGN-ROLE-04": {
		"ign:FUNC-06", "ign:FUNC-07", "ign:FUNC-12", "ign:FUNC-17", "ign:FUNC-18",
		"ign:FUNC-22", "ign:FUNC-25", "ign:FUNC-26", "ign:FUNC-27", "ign:FUNC-29",
	},
	// IGN-ROLE-05 执行机代理:PERM-FUNC-08/19/23(服务身份;任务回执与心跳为登录即可端点)
	"IGN-ROLE-05": {"ign:FUNC-08", "ign:FUNC-19", "ign:FUNC-23"},
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

// EnsureSeed 种子(幂等;users 非空则补齐超管角色与流程定义)。
func EnsureSeed() error {
	count, err := qCount(`SELECT COUNT(*) FROM sys_user`)
	if err != nil {
		return err
	}
	if count > 0 {
		if err := ensureAdminRole(); err != nil {
			return err
		}
		return ensureFlowDefinitions()
	}
	// 角色
	roleIDs := map[string]int64{}
	for _, r := range seedRoles {
		if _, err := qExec(`INSERT INTO sys_role (name, code, parent_id, description, status) VALUES (?, ?, 0, ?, 1)`,
			r.Name, r.Code, r.Desc); err != nil {
			return err
		}
		row, err := qOne(`SELECT id FROM sys_role WHERE code = ?`, r.Code)
		if err != nil {
			return err
		}
		roleIDs[r.Code] = toInt(row["id"])
	}
	// 权限:平台权限 + ignition 业务权限(INSERT 列序与值序对齐)
	for _, p := range seedPermissions {
		if _, err := qExec(`
			INSERT INTO sys_permission (code, name, target_type, target_ref, data_scope, status)
			VALUES (?, ?, ?, ?, 'ALL', 1)`, p.Code, p.Name, p.TargetType, p.TargetRef); err != nil {
			return err
		}
	}
	for _, p := range seedIgnPermissions {
		if _, err := qExec(`
			INSERT INTO sys_permission (code, name, target_type, target_ref, data_scope, status)
			VALUES (?, ?, 'BEHAVIOR', ?, 'ALL', 1)`, p.Code, p.Name, p.TargetRef); err != nil {
			return err
		}
	}
	perms, err := qList(`SELECT id, code FROM sys_permission`)
	if err != nil {
		return err
	}
	permIDByCode := map[string]any{}
	for _, p := range perms {
		permIDByCode[Str(p["code"])] = p["id"]
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
	// 角色权限(admin 超管位由 GetPermissionCodes 代码层注入,不落数据)
	for roleCode, permCodes := range seedRolePermissions {
		roleID := roleIDs[roleCode]
		for _, pc := range permCodes {
			if _, err := qExec(`INSERT INTO sys_role_permission (role_id, permission_id) VALUES (?, ?)`,
				roleID, permIDByCode[pc]); err != nil {
				return err
			}
		}
	}
	// 运行参数缺省(REF-*;FUNC-30 可维护)
	for _, c := range defaultIgnConfigs() {
		if err := upsertIgnConfig(store.DB, c.Key, c.Value, c.Desc, 1); err != nil {
			return err
		}
	}
	// LOCAL 执行机系统自动注册(INV-12/REF-10)
	if err := EnsureLocalExecutor(); err != nil {
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
