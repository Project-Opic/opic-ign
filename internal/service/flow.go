package service

import (
	"encoding/json"

	"gorm.io/gorm"

	"gitcode.com/opic-ontology/opic-techbase/internal/store"
	"gitcode.com/opic-ontology/opic-techbase/pkg/errcode"
)

// ---------- 流程管理(flow_service.py) ----------

// ListDefinitions 流程定义列表。
func ListDefinitions(page, size int, keyword string) (*Page, error) {
	where := ""
	args := []any{}
	if keyword != "" {
		where = "WHERE name LIKE ? OR code LIKE ?"
		kw := "%" + keyword + "%"
		args = append(args, kw, kw)
	}
	total, err := qCount(`SELECT COUNT(*) FROM flow_definition `+where, args...)
	if err != nil {
		return nil, err
	}
	rows, err := qList(`SELECT * FROM flow_definition `+where+` ORDER BY id DESC LIMIT ? OFFSET ?`,
		append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	return &Page{List: rows, Total: total, PageV: page, Size: size}, nil
}

// GetDefinition 流程定义详情(node_graph 解析为对象)。
func GetDefinition(defID int64) (map[string]any, error) {
	row, err := qOne(`SELECT * FROM flow_definition WHERE id = ?`, defID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, NotFound("流程定义")
	}
	row["node_graph"] = ParseJSONText(Str(row["node_graph"]), map[string]any{})
	return row, nil
}

// GetDefinitionGraph 流程图。
func GetDefinitionGraph(defID int64) (any, error) {
	row, err := qOne(`SELECT node_graph FROM flow_definition WHERE id = ?`, defID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, NotFound("流程定义")
	}
	return ParseJSONText(Str(row["node_graph"]), map[string]any{}), nil
}

// CreateDefinition 新建流程定义。
func CreateDefinition(data map[string]any, userID int64) (int64, error) {
	code := Str(data["code"])
	if code == "" {
		return 0, Fail(errcode.ErrParam, "流程编码必填")
	}
	if exists, err := qOne(`SELECT id FROM flow_definition WHERE code = ?`, code); err != nil {
		return 0, err
	} else if exists != nil {
		return 0, DupErr("流程编码")
	}
	nodeGraph := data["node_graph"]
	if nodeGraph == nil {
		nodeGraph = map[string]any{"nodes": []any{}, "edges": []any{}}
	}
	flowType := Str(data["flow_type"])
	if flowType == "" {
		flowType = "APPROVAL"
	}
	triggerType := Str(data["trigger_type"])
	if triggerType == "" {
		triggerType = "MANUAL"
	}
	if _, err := qExec(`
		INSERT INTO flow_definition (code, name, flow_type, trigger_type, trigger_behavior, description, node_graph, version, status, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, 1, 0, ?)`,
		code, data["name"], flowType, triggerType, data["trigger_behavior"], data["description"],
		MarshalJSONVal(nodeGraph), userID); err != nil {
		return 0, err
	}
	created, err := qOne(`SELECT id FROM flow_definition WHERE code = ?`, code)
	if err != nil {
		return 0, err
	}
	return toInt(created["id"]), nil
}

// UpdateDefinition 编辑流程定义(已发布不可改)。
func UpdateDefinition(defID int64, data map[string]any) error {
	existing, err := qOne(`SELECT * FROM flow_definition WHERE id = ?`, defID)
	if err != nil {
		return err
	}
	if existing == nil {
		return NotFound("流程定义")
	}
	if toInt(existing["status"]) == 1 {
		return Fail(errcode.ErrState, "已发布流程不可直接编辑，请停用后修改或新建版本")
	}
	nodeGraph := data["node_graph"]
	if nodeGraph == nil {
		nodeGraph = ParseJSONText(Str(existing["node_graph"]), map[string]any{})
	}
	pick := func(key string) any {
		if v, ok := data[key]; ok && v != nil {
			return v
		}
		return existing[key]
	}
	_, err = qExec(`
		UPDATE flow_definition SET name=?, flow_type=?, trigger_type=?, trigger_behavior=?, description=?, node_graph=?,
		updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS')
		WHERE id=?`,
		pick("name"), pick("flow_type"), pick("trigger_type"), pick("trigger_behavior"),
		pick("description"), MarshalJSONVal(nodeGraph), defID)
	return err
}

// PublishDefinition 发布(含图校验)。
func PublishDefinition(defID int64) error {
	existing, err := qOne(`SELECT * FROM flow_definition WHERE id = ?`, defID)
	if err != nil {
		return err
	}
	if existing == nil {
		return NotFound("流程定义")
	}
	graph, _ := ParseJSONText(Str(existing["node_graph"]), map[string]any{}).(map[string]any)
	if err := ValidateGraph(graph); err != nil {
		return err
	}
	_, err = qExec(`UPDATE flow_definition SET status = 1, updated_at = to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id = ?`, defID)
	return err
}

// ValidateGraph 校验流程图(开始唯一/结束至少一/审批节点须配角色)。
func ValidateGraph(graph map[string]any) error {
	nodes, _ := graph["nodes"].([]any)
	startCount, endCount := 0, 0
	for _, n := range nodes {
		nm, ok := n.(map[string]any)
		if !ok {
			continue
		}
		switch nm["type"] {
		case "start":
			startCount++
		case "end":
			endCount++
		case "approval_task", "user_task":
			if Str(nm["role_ref"]) == "" {
				return Fail(errcode.ErrGraph, "节点「%v」必须配置角色 role_ref", nm["name"])
			}
		}
	}
	if startCount != 1 {
		return Fail(errcode.ErrGraph, "流程必须包含且仅包含一个开始节点")
	}
	if endCount < 1 {
		return Fail(errcode.ErrGraph, "流程必须包含至少一个结束节点")
	}
	return nil
}

// ListInstances 实例列表。
func ListInstances(page, size int, filters map[string]any) (*Page, error) {
	where := "WHERE 1=1"
	args := []any{}
	if status := Str(filters["status"]); status != "" {
		where += " AND i.status = ?"
		args = append(args, status)
	}
	if kw := Str(filters["keyword"]); kw != "" {
		where += " AND (i.business_key LIKE ?)"
		args = append(args, "%"+kw+"%")
	}
	total, err := qCount(`SELECT COUNT(*) FROM flow_instance i JOIN flow_definition d ON d.id = i.def_id `+where, args...)
	if err != nil {
		return nil, err
	}
	rows, err := qList(`
		SELECT i.*, d.name AS def_name, d.code AS def_code
		FROM flow_instance i JOIN flow_definition d ON d.id = i.def_id
		`+where+` ORDER BY i.id DESC LIMIT ? OFFSET ?`,
		append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	return &Page{List: rows, Total: total, PageV: page, Size: size}, nil
}

// GetInstanceDetail 实例详情(含 definition/tasks/history)。
func GetInstanceDetail(instanceID int64) (map[string]any, error) {
	inst, err := qOne(`SELECT * FROM flow_instance WHERE id = ?`, instanceID)
	if err != nil {
		return nil, err
	}
	if inst == nil {
		return nil, NotFound("流程实例")
	}
	definition, err := qOne(`SELECT * FROM flow_definition WHERE id = ?`, toInt(inst["def_id"]))
	if err != nil {
		return nil, err
	}
	tasks, err := qList(`SELECT * FROM flow_task WHERE instance_id = ? ORDER BY id`, instanceID)
	if err != nil {
		return nil, err
	}
	history, err := qList(`SELECT * FROM flow_history WHERE instance_id = ? ORDER BY id`, instanceID)
	if err != nil {
		return nil, err
	}
	inst["definition"] = map[string]any{
		"id":         definition["id"],
		"name":       definition["name"],
		"code":       definition["code"],
		"node_graph": ParseJSONText(Str(definition["node_graph"]), map[string]any{}),
	}
	if tasks == nil {
		tasks = []map[string]any{}
	}
	if history == nil {
		history = []map[string]any{}
	}
	inst["tasks"] = tasks
	inst["history"] = history
	return inst, nil
}

// TerminateInstance 终止实例。
func TerminateInstance(instanceID int64, user UserInfo) error {
	inst, err := qOne(`SELECT * FROM flow_instance WHERE id = ?`, instanceID)
	if err != nil {
		return err
	}
	if inst == nil {
		return NotFound("流程实例")
	}
	if inst["status"] != "RUNNING" {
		return Fail(errcode.ErrState, "仅运行中的实例可终止")
	}
	return tx(func(db *gorm.DB) error {
		if _, err := txExec(db, `
			UPDATE flow_instance SET status = 'TERMINATED', ended_at = to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id = ?`, instanceID); err != nil {
			return err
		}
		if _, err := txExec(db, `UPDATE flow_task SET status = 'CANCEL' WHERE instance_id = ? AND status = 'TODO'`, instanceID); err != nil {
			return err
		}
		_, err := txExec(db, `
			INSERT INTO flow_history (instance_id, operator_id, operator_name, action, comment) VALUES (?, ?, ?, 'TERMINATE', ?)`,
			instanceID, user.ID, user.Username, "强制终止")
		return err
	})
}

// ListTasks 任务列表。
func ListTasks(page, size int, filters map[string]any) (*Page, error) {
	where := "WHERE 1=1"
	args := []any{}
	if status := Str(filters["status"]); status != "" {
		where += " AND t.status = ?"
		args = append(args, status)
	}
	total, err := qCount(`SELECT COUNT(*) FROM flow_task t `+where, args...)
	if err != nil {
		return nil, err
	}
	rows, err := qList(`
		SELECT t.*, i.business_key, d.name AS def_name
		FROM flow_task t
		JOIN flow_instance i ON i.id = t.instance_id
		JOIN flow_definition d ON d.id = i.def_id
		`+where+` ORDER BY t.id DESC LIMIT ? OFFSET ?`,
		append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	return &Page{List: rows, Total: total, PageV: page, Size: size}, nil
}

// TransferTask 转办。
func TransferTask(taskID int64, assigneeID int64) error {
	task, err := qOne(`SELECT * FROM flow_task WHERE id = ?`, taskID)
	if err != nil {
		return err
	}
	if task == nil {
		return NotFound("任务")
	}
	if task["status"] != "TODO" {
		return Fail(errcode.ErrFlowState, "仅待办任务可转办")
	}
	if assigneeID <= 0 {
		return Fail(errcode.ErrParam, "被转办人必填")
	}
	assignee, err := qOne(`SELECT id, real_name, username FROM sys_user WHERE id = ?`, assigneeID)
	if err != nil {
		return err
	}
	if assignee == nil {
		return NotFound("被转办用户")
	}
	name := Str(assignee["real_name"])
	if name == "" {
		name = Str(assignee["username"])
	}
	_, err = qExec(`
		UPDATE flow_task SET assignee_id=?, assignee_name=?, claimed_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`,
		assigneeID, name, taskID)
	return err
}

// UrgeTask 催办(写历史记录)。
func UrgeTask(taskID int64, user UserInfo) error {
	task, err := qOne(`SELECT * FROM flow_task WHERE id = ?`, taskID)
	if err != nil {
		return err
	}
	if task == nil {
		return NotFound("任务")
	}
	if task["status"] != "TODO" {
		return Fail(errcode.ErrState, "仅待办任务可催办")
	}
	_, err = qExec(`
		INSERT INTO flow_history (instance_id, activity_id, activity_name, operator_id, operator_name, action, comment)
		VALUES (?, ?, ?, ?, ?, 'URGE', ?)`,
		task["instance_id"], task["activity_id"], task["activity_name"], user.ID, user.Username, "催办提醒")
	return err
}

// toInt any→int64。注意:PostgreSQL INTEGER/SMALLINT 列经 pgx 返回 int32/int16,
// 仅处理 int64/int/float64 会静默得 0(DBS/AST/REG 均已踩过),统一委托 Int()。
func toInt(v any) int64 {
	return Int(v)
}

// ---------- 执行机强制注销申请同步(FLOW-PROC-APP-01;R-08) ----------

// SyncForceRemoveFromInstance 实例状态→强制注销申请状态(审批通过/驳回;进行中节点不做副写——
// 状态由 workbench 审批动作驱动,通过时直接进入拆除链)。
func SyncForceRemoveFromInstance(db *gorm.DB, instanceID int64) (string, error) {
	inst, err := store.One(db, `SELECT * FROM flow_instance WHERE id = ?`, instanceID)
	if err != nil || inst == nil {
		return "", err
	}
	apply, err := store.One(db, `SELECT * FROM ign_force_remove WHERE instance_id = ? AND flag = 1`, instanceID)
	if err != nil || apply == nil {
		return "", err
	}
	var status string
	switch Str(inst["status"]) {
	case "APPROVED":
		status = "已通过"
	case "REJECTED":
		status = "已驳回"
	}
	if status != "" && status != Str(apply["status"]) {
		_, err = store.Exec(db, `
			UPDATE ign_force_remove SET status=?, updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`,
			status, apply["id"])
		return status, err
	}
	return status, nil
}

var _ = json.Marshal // 占位保持导入(实际经 MarshalJSONVal)
