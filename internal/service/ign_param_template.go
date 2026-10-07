// ign_param_template.go —— 参数面模板治理动作（FUNC-38~44，AGG-IGN-07）的服务入口。
//
// 本文件只做「入口 + 不变量中文提示 + 留痕」，四层取值与面定义一律读 ign_param_face.go
// 的既有实现（resolveParamFace / ignParamFieldOf / ignValidateParamFace / ignTemplateReferences）——
// 判定只有一份实现（R-23），入口层再写一套就必然漂移。
//
// 不变量在本层的中文口径（库里 CHECK/唯一索引只是兜底，报错文案要能指导下一步动作）：
//   - 模板态迁移合法域：草稿→启用、停用→启用、启用→停用；其余迁移即拒（FUNC-42）；
//   - INV-43：同面默认至多一条由 uq_ign_template_default_per_face 兜底（谓词不带状态条件），
//     「默认须启用」由本层校验——停用默认模板即拒，设默认要求目标处于启用态；
//   - R-25：被 clc 面标识声明或本域生效覆写引用的模板不可删，拒绝时列出全部出处；
//   - R-22：字段行只有整组替换，没有单行编辑；启用的模板字段行非空且逐键合法。
package service

import (
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"

	"gitcode.com/opic-ontology/opic-techbase/internal/store"
	"gitcode.com/opic-ontology/opic-techbase/pkg/errcode"
)

// ignTemplateFaceLabel 文件面中文名（报错文案要能读懂是哪一轴）。
func ignTemplateFaceLabel(code string) string {
	switch code {
	case ignFaceHosts:
		return "主机清单面（hosts）"
	case ignFaceConfig:
		return "集群配置面（config.yml）"
	}
	return code
}

// ignCheckTemplateFace 文件面枚举（HOSTS/CONFIG，一张模板只属一轴）。
func ignCheckTemplateFace(face string) error {
	if face != ignFaceHosts && face != ignFaceConfig {
		return Fail(errcode.ErrParam, "文件面 %q 非法：只能是 HOSTS（主机清单面）或 CONFIG（集群配置面）——一张模板只属一轴（R-22）", face)
	}
	return nil
}

// ignParamTemplateLive 按 id 取未删除模板行；不存在即 NotFound。
func ignParamTemplateLive(db *gorm.DB, id int64) (map[string]any, error) {
	row, err := store.One(db, `SELECT * FROM ign_param_template WHERE id = ? AND flag = 1`, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, NotFound("参数面模板")
	}
	return row, nil
}

// ignTemplateClustersReferencing 一张模板的引用集群并集（REP-15 c7 被引用集群数的取数口径：
// ① 上游面标识声明（接管标记快照，FS 扫描）② 本域生效覆写依据该面同键模板行）。
func ignTemplateClustersReferencing(db *gorm.DB, tplID int64, templateNo string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, ref := range ignClustersDeclaringFace(templateNo) {
		if !seen[ref] {
			seen[ref] = true
			out = append(out, ref)
		}
	}
	rows, err := store.List(db, `SELECT DISTINCT o.cluster_ref
		FROM ign_param_template_item i
		JOIN ign_cluster_config_override o ON o.flag = 1 AND o.override_status = 'EFFECTIVE'
			AND o.field_key = i.field_key
		JOIN ign_param_template t ON t.id = ? AND t.flag = 1 AND t.file_face = o.file_face
		WHERE i.template_id = ? AND i.flag = 1`, tplID, tplID)
	if err != nil {
		return out
	}
	for _, r := range rows {
		ref := Str(r["cluster_ref"])
		if !seen[ref] {
			seen[ref] = true
			out = append(out, ref)
		}
	}
	sort.Strings(out)
	return out
}

// ignTemplateRowView 列表/详情共用的模板行视图：删除标记、默认徽标（INV-43 界面侧投影：
// 只在启用态出现）、被引用集群数（让「这张模板会被谁用」在同一屏可见）。
func ignTemplateRowView(db *gorm.DB, row map[string]any) map[string]any {
	live := toInt(row["flag"]) == 1
	status := Str(row["template_status"])
	v := map[string]any{}
	for k, x := range row {
		v[k] = x
	}
	v["deleted"] = !live
	v["default_badge"] = live && status == "ENABLED" && boolOfAny(row["is_default"])
	clusters := ignTemplateClustersReferencing(db, toInt(row["id"]), Str(row["template_no"]))
	v["referenced_cluster_count"] = len(clusters)
	v["referenced_clusters"] = clusters
	return v
}

// ListIgnParamTemplates FUNC-38 参数面模板列表（REP-15）。
// 过滤轴：文件面 + 状态 + 默认标记 + 关键字（只匹配模板名与编号，不检索字段值）；
// 停用态默认筛除、show_disabled=1 可切换显示；软删行默认可见（deleted 列区分）。
func ListIgnParamTemplates(page, size int, filters map[string]any) (*Page, error) {
	var conds []string
	args := []any{}
	if v := strings.ToUpper(strings.TrimSpace(Str(filters["file_face"]))); v != "" {
		conds = append(conds, "file_face = ?")
		args = append(args, v)
	}
	if v := strings.ToUpper(strings.TrimSpace(Str(filters["status"]))); v != "" {
		conds = append(conds, "template_status = ?")
		args = append(args, v)
	} else if Str(filters["show_disabled"]) != "1" {
		conds = append(conds, "(flag = 0 OR template_status <> 'DISABLED')")
	}
	if v := strings.TrimSpace(Str(filters["is_default"])); v != "" {
		want := v == "1" || strings.EqualFold(v, "true")
		conds = append(conds, "is_default = ?")
		args = append(args, want)
	}
	if v := strings.TrimSpace(Str(filters["keyword"])); v != "" {
		conds = append(conds, "(template_no LIKE ? OR LOWER(template_name) LIKE ?)")
		kw := "%" + strings.ToLower(v) + "%"
		args = append(args, "%"+v+"%", kw)
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	total, err := qCount(`SELECT COUNT(*) FROM ign_param_template `+where, args...)
	if err != nil {
		return nil, err
	}
	rows, err := qList(`SELECT * FROM ign_param_template `+where+` ORDER BY flag ASC, id DESC LIMIT ? OFFSET ?`,
		append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, err
	}
	list := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		list = append(list, ignTemplateRowView(store.DB, r))
	}
	return &Page{List: list, Total: total, PageV: page, Size: size}, nil
}

// GetIgnParamTemplate FUNC-39 详情（三段分区：模板头 / 字段行整组 / 被引用出处；REP-16）。
// 字段行按字段键字典序固定排列（整组 diff 可比对）；值列按报表口径 1 过 R-16 掩码；
// 空值显示「让位」而非校验错误（该键交由上层或现场决定）。
func GetIgnParamTemplate(id int64) (map[string]any, error) {
	row, err := qOne(`SELECT * FROM ign_param_template WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, NotFound("参数面模板")
	}
	items, err := qList(`SELECT * FROM ign_param_template_item WHERE template_id = ? AND flag = 1 ORDER BY field_key`, id)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		m := map[string]any{}
		for k, x := range it {
			m[k] = x
		}
		if f, ok := ignParamFieldOf(Str(it["field_key"])); ok {
			m["label"] = f.Label
			m["ref_id"] = f.RefID
			m["axis_file"] = f.AxisFile
			m["disk_key"] = f.DiskKey
		}
		raw := Str(it["field_value"])
		masked, hit := ignMaskLine(raw)
		m["field_value_masked"] = masked
		m["masked"] = hit
		if strings.TrimSpace(raw) == "" {
			m["display_note"] = "让位（空值：该键交由上层取值或现场决定，不是校验错误）"
		}
		out = append(out, m)
	}
	if out == nil {
		out = []map[string]any{}
	}
	refs, err := ignTemplateReferences(store.DB, row)
	if err != nil {
		return nil, err
	}
	view := ignTemplateRowView(store.DB, row)
	view["items"] = out
	view["references"] = refs
	return view, nil
}

// CreateIgnParamTemplate FUNC-40 登记：抢一个编号（TMP-YYYYMMDD-NNNN、生成后只读），
// 状态置草稿、默认标记为假；「保存松」——允许字段行为空，键域合法性由整组保存与启用把关。
func CreateIgnParamTemplate(data map[string]any, user UserInfo) (map[string]any, error) {
	name := strings.TrimSpace(Str(data["template_name"]))
	face := strings.ToUpper(strings.TrimSpace(Str(data["file_face"])))
	desc := strings.TrimSpace(Str(data["description"]))
	if name == "" {
		return nil, Fail(errcode.ErrParam, "模板名称为必填项（重名在未删除范围内即拒绝，软删除释放名称）")
	}
	if err := ignCheckTemplateFace(face); err != nil {
		return nil, err
	}
	if len([]rune(desc)) > 500 {
		return nil, Fail(errcode.ErrParam, "用途说明不得超过 500 字")
	}
	var result map[string]any
	err := tx(func(db *gorm.DB) error {
		dup, err := store.Count(db, `SELECT COUNT(*) FROM ign_param_template WHERE flag = 1 AND LOWER(template_name) = LOWER(?)`, name)
		if err != nil {
			return err
		}
		if dup > 0 {
			return Fail(errcode.ErrRule, "模板名称 %q 在未删除范围内已存在（重名即拒绝；软删除释放名称）", name)
		}
		no, err := genIgnNo(db, "IgnParamTemplate", "ign_param_template", "template_no")
		if err != nil {
			return err
		}
		if _, err := store.Exec(db, `INSERT INTO ign_param_template
			(template_no, template_name, file_face, template_status, is_default, description, created_by)
			VALUES (?, ?, ?, 'DRAFT', FALSE, ?, ?)`, no, name, face, ignNullableStr(desc), user.ID); err != nil {
			return err
		}
		row, err := store.One(db, `SELECT * FROM ign_param_template WHERE template_no = ?`, no)
		if err != nil {
			return err
		}
		result = row
		return nil
	})
	if err != nil {
		return nil, err
	}
	WriteAudit(nil, user.ID, user.Username, "IGN_TPL_CREATE",
		fmt.Sprintf("参数面模板登记 %s（%s / %s）", Str(result["template_no"]), name, face))
	return result, nil
}

// ignNormalizeTemplateItem 单字段行的键域 + 形态判定（FUNC-41 与 FUNC-42 启用前置共用）。
func ignNormalizeTemplateItem(tplFace string, raw map[string]any) (ignParamField, string, error) {
	key := strings.TrimSpace(Str(raw["field_key"]))
	f, ok := ignParamFieldOf(key)
	if !ok {
		return f, "", ignGovernanceRefuse(fmt.Sprintf(
			"字段键 %q 不在参数面定义（REF-17~25）内（INV-41：面外键是治理拒绝，不是表单校验错误——命令面私有不能从台账死角漏出去）", key))
	}
	if f.FileFace != tplFace {
		return f, "", ignGovernanceRefuse(fmt.Sprintf(
			"字段键 %q 属%s，而本模板绑定%s（R-22：一张模板只属一轴，跨面混装会让「整体替换」失去边界）",
			key, ignTemplateFaceLabel(f.FileFace), ignTemplateFaceLabel(tplFace)))
	}
	value := Str(raw["field_value"])
	if f.Shape == ignShapeList {
		value = ignListText(ignListItems(value))
	} else {
		value = strings.TrimSpace(value)
	}
	if err := ignValidateParamFace([]ignParamField{f}, map[string]string{f.Key: value}); err != nil {
		return f, "", err
	}
	return f, value, nil
}

// SaveIgnParamTemplateItems FUNC-41 字段行整体覆写：提交的是整组（无单行编辑入口），
// 未提交的行即不存在；值形态随字段键由面定义带出、不可人工改；治理动作需事由。
func SaveIgnParamTemplateItems(id int64, data map[string]any, user UserInfo) (map[string]any, error) {
	reason := strings.TrimSpace(Str(data["reason"]))
	if reason == "" {
		return nil, Fail(errcode.ErrParam, "字段行整组覆写必须携带事由（治理资产的动作留痕：启用 / 整组替换 / 设默认 / 软删除四类动作同口径）")
	}
	var tpl map[string]any
	var normalized []map[string]any
	err := tx(func(db *gorm.DB) error {
		var e error
		tpl, e = ignParamTemplateLive(db, id)
		if e != nil {
			return e
		}
		face := Str(tpl["file_face"])
		rawItems, _ := data["items"].([]any)
		seen := map[string]bool{}
		normalized = nil
		for _, it := range rawItems {
			m, ok := it.(map[string]any)
			if !ok {
				return Fail(errcode.ErrParam, "字段行必须是对象数组（field_key + field_value）")
			}
			f, value, err := ignNormalizeTemplateItem(face, m)
			if err != nil {
				return err
			}
			if seen[f.Key] {
				return Fail(errcode.ErrParam, "字段键 %q 在本次提交内重复（同面内不重行，FUNC-41 前置）", f.Key)
			}
			seen[f.Key] = true
			normalized = append(normalized, map[string]any{"key": f.Key, "value": value, "shape": f.Shape})
		}
		// 整组替换：先清后写（R-22：未提交的键即不存在，没有元素级增删）
		if _, err := store.Exec(db, `DELETE FROM ign_param_template_item WHERE template_id = ?`, id); err != nil {
			return err
		}
		for _, n := range normalized {
			if _, err := store.Exec(db, `INSERT INTO ign_param_template_item
				(template_id, field_key, field_value, value_shape, created_by)
				VALUES (?, ?, ?, ?, ?)`, id, n["key"], n["value"], n["shape"], user.ID); err != nil {
				return err
			}
		}
		if _, err := store.Exec(db, `UPDATE ign_param_template SET updated_by = ?, updated_at = ? WHERE id = ?`,
			user.ID, ignNow(), id); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	WriteAudit(nil, user.ID, user.Username, "IGN_TPL_ITEMS_SAVE", fmt.Sprintf(
		"参数面模板 %s 字段行整组替换（%d 键）：事由 %s", Str(tpl["template_no"]), len(normalized), reason))
	return GetIgnParamTemplate(id)
}

// SetIgnParamTemplateState FUNC-42 启用 / 停用。迁移合法域：草稿→启用、停用→启用、启用→停用。
// 启用前置：字段行非空且逐键合法；启用回填 enabledAt（首次）。停用默认模板即拒（INV-43：
// 默认须启用——先摘徽标或改指另一张，停用后默认徽标同时消失）。
func SetIgnParamTemplateState(id int64, action, reason string, user UserInfo) (map[string]any, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, Fail(errcode.ErrParam, "启用 / 停用是治理动作，必须携带事由（与整组覆写 / 设默认 / 软删除同口径）")
	}
	if action != "enable" && action != "disable" {
		return nil, Fail(errcode.ErrParam, "未知的模板状态动作 %q（只支持 enable / disable）", action)
	}
	var tpl map[string]any
	err := tx(func(db *gorm.DB) error {
		var e error
		tpl, e = ignParamTemplateLive(db, id)
		if e != nil {
			return e
		}
		status := Str(tpl["template_status"])
		switch action {
		case "enable":
			if status == "ENABLED" {
				return Fail(errcode.ErrState, "模板已处于启用态（草稿 / 停用 → 启用 才是合法迁移）")
			}
			items, err := store.List(db, `SELECT field_key, field_value FROM ign_param_template_item
				WHERE template_id = ? AND flag = 1 ORDER BY field_key`, id)
			if err != nil {
				return err
			}
			if len(items) == 0 {
				return Fail(errcode.ErrState, "启用前置不满足：字段行为空（启用一个空模板只会让第 3 层整层不命中，形同虚设——先用 FUNC-41 保存整组字段行）")
			}
			for _, it := range items {
				if _, _, err := ignNormalizeTemplateItem(Str(tpl["file_face"]), it); err != nil {
					return err
				}
			}
			enabledAt := Str(tpl["enabled_at"])
			if strings.TrimSpace(enabledAt) == "" {
				enabledAt = ignNow()
			}
			if _, err := store.Exec(db, `UPDATE ign_param_template
				SET template_status = 'ENABLED', enabled_at = ?, updated_by = ?, updated_at = ? WHERE id = ?`,
				enabledAt, user.ID, ignNow(), id); err != nil {
				return err
			}
		case "disable":
			if status != "ENABLED" {
				return Fail(errcode.ErrState, "只有启用态模板可以停用（当前状态为「%s」；草稿不涉及停用，重复停用无意义）", ignTemplateStateLabel(status))
			}
			if boolOfAny(tpl["is_default"]) {
				return Fail(errcode.ErrState,
					"默认模板不可直接停用（INV-43：同一文件面内默认模板必须处于启用态）——请先对同面另一张启用态模板执行「设为默认」（FUNC-43 会自动摘除本张徽标），再停用本张")
			}
			if _, err := store.Exec(db, `UPDATE ign_param_template
				SET template_status = 'DISABLED', updated_by = ?, updated_at = ? WHERE id = ?`,
				user.ID, ignNow(), id); err != nil {
				return err
			}
		}
		var e2 error
		tpl, e2 = store.One(db, `SELECT * FROM ign_param_template WHERE id = ?`, id)
		return e2
	})
	if err != nil {
		return nil, err
	}
	verb := "启用"
	if action == "disable" {
		verb = "停用"
	}
	WriteAudit(nil, user.ID, user.Username, "IGN_TPL_STATE", fmt.Sprintf(
		"参数面模板 %s %s：事由 %s", Str(tpl["template_no"]), verb, reason))
	if action == "disable" {
		// 停用只表示「不再被新集群选中」，已生效的取值不作废、覆写行不回收（R-25）
		return tpl, nil
	}
	return tpl, nil
}

// SetIgnParamTemplateDefault FUNC-43 设为默认：目标须处于启用态、事由必填；
// 同面原默认行自动取消标记（INV-43：同面至多一条默认）。默认只作用于上游选择器的缺省值，
// 不参与渲染兜底（R-20 分支 H）——改默认不会让存量集群装机参数静默漂移。
func SetIgnParamTemplateDefault(id int64, reason string, user UserInfo) (map[string]any, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, Fail(errcode.ErrParam, "设为默认必须携带事由（影响面是「后续新建集群缺省挂哪张」，跨集群，与模板内容维护分档）")
	}
	var tpl map[string]any
	err := tx(func(db *gorm.DB) error {
		var e error
		tpl, e = ignParamTemplateLive(db, id)
		if e != nil {
			return e
		}
		if Str(tpl["template_status"]) != "ENABLED" {
			return Fail(errcode.ErrState,
				"设为默认前置不满足：目标模板须处于启用态（当前「%s」；INV-43：默认模板必须处于启用态）",
				ignTemplateStateLabel(Str(tpl["template_status"])))
		}
		if _, err := store.Exec(db, `UPDATE ign_param_template
			SET is_default = FALSE, updated_by = ?, updated_at = ?
			WHERE flag = 1 AND file_face = ? AND is_default = TRUE AND id <> ?`,
			user.ID, ignNow(), Str(tpl["file_face"]), id); err != nil {
			return err
		}
		if _, err := store.Exec(db, `UPDATE ign_param_template
			SET is_default = TRUE, updated_by = ?, updated_at = ? WHERE id = ?`,
			user.ID, ignNow(), id); err != nil {
			return err
		}
		var e2 error
		tpl, e2 = store.One(db, `SELECT * FROM ign_param_template WHERE id = ?`, id)
		return e2
	})
	if err != nil {
		return nil, err
	}
	WriteAudit(nil, user.ID, user.Username, "IGN_TPL_SET_DEFAULT", fmt.Sprintf(
		"参数面模板 %s 设为%s默认（同面原默认行已自动取消标记）：事由 %s",
		Str(tpl["template_no"]), ignTemplateFaceLabel(Str(tpl["file_face"])), reason))
	return tpl, nil
}

// DeleteIgnParamTemplate FUNC-44 软删除：前置引用校验（R-25 两类出处），被引用即拒并列出
// 全部出处（出处即下一步动作的位置）；软删除是标记不是第四个状态值，字段行随聚合级联。
func DeleteIgnParamTemplate(id int64, reason string, user UserInfo) (map[string]any, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, Fail(errcode.ErrParam, "软删除参数面模板必须携带事由（治理资产的动作留痕）")
	}
	tpl, err := qOne(`SELECT * FROM ign_param_template WHERE id = ? AND flag = 1`, id)
	if err != nil {
		return nil, err
	}
	if tpl == nil {
		return nil, NotFound("参数面模板")
	}
	refs, err := ignTemplateReferences(store.DB, tpl)
	if err != nil {
		return nil, err
	}
	if len(refs) > 0 {
		parts := make([]string, 0, len(refs))
		for _, r := range refs {
			parts = append(parts, Str(r["source_label"])+"（集群 "+Str(r["cluster_ref"])+"）")
		}
		return nil, Fail(errcode.ErrForbidden,
			"参数面模板 %s 正被引用，不可删除（R-25 删除前置引用校验）。出处：%s —— 出处即下一步动作的位置，可跳「集群交付配置」屏处理",
			Str(tpl["template_no"]), strings.Join(parts, "；"))
	}
	if err := tx(func(db *gorm.DB) error {
		// 级联：字段行随聚合；默认标记随删除摘除（INV-43 兜底索引只认 flag=1 行）
		if _, err := store.Exec(db, `UPDATE ign_param_template_item SET flag = 0, updated_by = ?, updated_at = ?
			WHERE template_id = ? AND flag = 1`, user.ID, ignNow(), id); err != nil {
			return err
		}
		_, err := store.Exec(db, `UPDATE ign_param_template
			SET flag = 0, is_default = FALSE, updated_by = ?, updated_at = ? WHERE id = ?`,
			user.ID, ignNow(), id)
		return err
	}); err != nil {
		return nil, err
	}
	WriteAudit(nil, user.ID, user.Username, "IGN_TPL_DELETE", fmt.Sprintf(
		"参数面模板 %s 软删除（字段行随聚合级联，名称随删除释放）：事由 %s", Str(tpl["template_no"]), reason))
	row, err := qOne(`SELECT * FROM ign_param_template WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	return row, nil
}
