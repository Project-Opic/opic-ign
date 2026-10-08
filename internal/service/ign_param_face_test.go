// ign_param_face_test.go —— v1.3 参数面治理（FUNC-38~50）服务入口语义用例。
//
// 覆盖口径：每条行为至少一条语义用例 + 服务层中文不变量提示逐条断言。
// 断言的错误文案只匹配关键片段（中文口径会润色，键名/编号/规则号才是可断言的事实）。
package service

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gitcode.com/opic-ontology/opic-techbase/internal/config"
	"gitcode.com/opic-ontology/opic-techbase/internal/pkg/ontology"
	"gitcode.com/opic-ontology/opic-techbase/internal/store"
)

var faceTestUser = UserInfo{ID: 1, Username: "face-tester"}

// faceTestSeq 用例间隔离：模板名/编号带自增后缀，避免跨用例撞唯一索引。
var faceTestSeq int

func faceName(tag string) string {
	faceTestSeq++
	return fmt.Sprintf("tpl-%s-%d", tag, faceTestSeq)
}

// faceTestTemplate 登记 + 整组保存 + 启用一条龙。
func faceTestTemplate(t *testing.T, tag, face string, items map[string]string) map[string]any {
	t.Helper()
	tpl, err := CreateIgnParamTemplate(map[string]any{
		"template_name": faceName(tag), "file_face": face, "description": "测试模板 " + tag,
	}, faceTestUser)
	if err != nil {
		t.Fatalf("登记模板失败: %v", err)
	}
	list := []any{}
	reason := "用例初始化"
	for k, v := range items {
		list = append(list, map[string]any{"field_key": k, "field_value": v})
	}
	detail, err := SaveIgnParamTemplateItems(toInt(tpl["id"]), map[string]any{
		"items": list, "reason": reason,
	}, faceTestUser)
	if err != nil {
		t.Fatalf("整组保存字段行失败: %v", list)
	}
	enabled, err := SetIgnParamTemplateState(toInt(tpl["id"]), "enable", "用例启用", faceTestUser)
	if err != nil {
		t.Fatalf("启用模板失败: %v", err)
	}
	_ = detail
	return enabled
}

// faceWorkRoot 把集群工作目录指到临时目录（用例结束自动还原）。
func faceWorkRoot(t *testing.T) string {
	t.Helper()
	prev := config.Config.Delivery.WorkDir
	root := t.TempDir()
	config.Config.Delivery.WorkDir = root
	t.Cleanup(func() { config.Config.Delivery.WorkDir = prev })
	mustSQL(t, `DELETE FROM ign_cluster_config_override`)
	return root
}

func errText(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatalf("期望报错，实为 nil")
	}
	return AsBizErr(err).Msg
}

func wantErrContains(t *testing.T, err error, frags ...string) string {
	t.Helper()
	msg := errText(t, err)
	for _, f := range frags {
		if !strings.Contains(msg, f) {
			t.Fatalf("报错文案应含 %q，实际: %s", f, msg)
		}
	}
	return msg
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("非期望错误: %v", err)
	}
}

// ---------- FUNC-40 登记 ----------

func TestFunc40CreateTemplate(t *testing.T) {
	requireDB(t)
	mustSQL(t, `DELETE FROM ign_param_template_item`)
	mustSQL(t, `DELETE FROM ign_param_template`)

	tpl, err := CreateIgnParamTemplate(map[string]any{
		"template_name": faceName("c40"), "file_face": "HOSTS",
	}, faceTestUser)
	mustNoErr(t, err)
	no := Str(tpl["template_no"])
	if !strings.HasPrefix(no, "TMP-") {
		t.Fatalf("编号应为 TMP-YYYYMMDD-NNNN 形态，实际 %q", no)
	}
	if Str(tpl["template_status"]) != "DRAFT" || boolOfAny(tpl["is_default"]) {
		t.Fatalf("登记后置草稿且默认标记为假: %v/%v", tpl["template_status"], tpl["is_default"])
	}
	if _, err := CreateIgnParamTemplate(map[string]any{
		"template_name": Str(tpl["template_name"]), "file_face": "HOSTS",
	}, faceTestUser); err == nil {
		t.Fatalf("同面重名登记应被拒绝")
	} else {
		wantErrContains(t, err, "已存在")
	}
	if _, err := CreateIgnParamTemplate(map[string]any{
		"template_name": faceName("badface"), "file_face": "MIXED",
	}, faceTestUser); err == nil {
		t.Fatalf("非法文件面应被拒绝")
	} else {
		wantErrContains(t, err, "HOSTS")
	}
	// m1 required=true ⇒ 库侧 NOT NULL（迁移 000007 修正项）
	if err := store.DB.Exec(`INSERT INTO ign_param_template (template_name, file_face) VALUES ('x-no-no', 'HOSTS')`).Error; err == nil {
		t.Fatalf("template_no 缺 NOT NULL 约束")
	}
}

// ---------- FUNC-41 字段行整体覆写 ----------

func TestFunc41ItemsWholeReplace(t *testing.T) {
	requireDB(t)
	mustSQL(t, `DELETE FROM ign_param_template_item`)
	mustSQL(t, `DELETE FROM ign_param_template`)

	tpl, err := CreateIgnParamTemplate(map[string]any{
		"template_name": faceName("c41"), "file_face": "HOSTS",
	}, faceTestUser)
	mustNoErr(t, err)
	id := toInt(tpl["id"])

	if _, err := SaveIgnParamTemplateItems(id, map[string]any{
		"items": []any{map[string]any{"field_key": ignCfgDeployServiceCIDR, "field_value": "10.68.0.0/16"}},
	}, faceTestUser); err == nil {
		t.Fatalf("缺事由的整组覆写应被拒绝（治理动作留痕）")
	} else {
		wantErrContains(t, err, "事由")
	}
	if _, err := SaveIgnParamTemplateItems(id, map[string]any{
		"reason": "r",
		"items":  []any{map[string]any{"field_key": "made_up_key", "field_value": "x"}},
	}, faceTestUser); err == nil {
		t.Fatalf("面外键应是治理拒绝")
	} else {
		wantErrContains(t, err, "不在参数面定义")
	}
	if _, err := SaveIgnParamTemplateItems(id, map[string]any{
		"reason": "r",
		"items":  []any{map[string]any{"field_key": ignCfgDeployInstallSource, "field_value": "online"}},
	}, faceTestUser); err == nil {
		t.Fatalf("CONFIG 面键放进 HOSTS 面模板应被拒绝（一张模板只属一轴）")
	} else {
		wantErrContains(t, err, "只属一轴")
	}
	if _, err := SaveIgnParamTemplateItems(id, map[string]any{
		"reason": "r",
		"items": []any{
			map[string]any{"field_key": ignCfgDeployServiceCIDR, "field_value": "10.68.0.0/16"},
			map[string]any{"field_key": ignCfgDeployServiceCIDR, "field_value": "10.69.0.0/16"},
		},
	}, faceTestUser); err == nil {
		t.Fatalf("同次提交重键应被拒绝（同面内不重行）")
	} else {
		wantErrContains(t, err, "重复")
	}
	if _, err := SaveIgnParamTemplateItems(id, map[string]any{
		"reason": "r",
		"items":  []any{map[string]any{"field_key": ignCfgDeployPodCIDR, "field_value": "not-a-cidr"}},
	}, faceTestUser); err == nil {
		t.Fatalf("值应过受理侧同一套形态校验器（R-23）")
	} else {
		wantErrContains(t, err, "不是 CIDR 形态")
	}

	_, err = SaveIgnParamTemplateItems(id, map[string]any{
		"reason": "首组",
		"items": []any{
			map[string]any{"field_key": ignCfgDeployServiceCIDR, "field_value": "10.68.0.0/16"},
			map[string]any{"field_key": ignCfgDeployPodCIDR, "field_value": "10.200.0.0/16"},
		},
	}, faceTestUser)
	mustNoErr(t, err)
	detail, err := GetIgnParamTemplate(id)
	mustNoErr(t, err)
	if len(detail["items"].([]map[string]any)) != 2 {
		t.Fatalf("应有 2 条字段行")
	}
	// 整组替换：未提交的行即不存在（R-22）
	_, err = SaveIgnParamTemplateItems(id, map[string]any{
		"reason": "整组替换",
		"items":  []any{map[string]any{"field_key": ignCfgDeployServiceCIDR, "field_value": "10.70.0.0/16"}},
	}, faceTestUser)
	mustNoErr(t, err)
	detail, err = GetIgnParamTemplate(id)
	mustNoErr(t, err)
	items := detail["items"].([]map[string]any)
	if len(items) != 1 || Str(items[0]["field_key"]) != ignCfgDeployServiceCIDR {
		t.Fatalf("整组替换后未提交的键即不存在，实际 %d 行", len(items))
	}
	if countRows(t, `SELECT COUNT(*) FROM ign_param_template_item WHERE template_id = ?`, id) != 1 {
		t.Fatalf("旧行应随整组替换物理清掉（唯一索引按 flag=1 计数）")
	}
}

// ---------- FUNC-42 启用 / 停用（迁移合法域 + 启用前置） ----------

func TestFunc42StateTransitions(t *testing.T) {
	requireDB(t)
	mustSQL(t, `DELETE FROM ign_param_template_item`)
	mustSQL(t, `DELETE FROM ign_param_template`)

	tpl, err := CreateIgnParamTemplate(map[string]any{
		"template_name": faceName("c42"), "file_face": "CONFIG",
	}, faceTestUser)
	mustNoErr(t, err)
	id := toInt(tpl["id"])
	if _, err := SetIgnParamTemplateState(id, "enable", "空模板不该启用", faceTestUser); err == nil {
		t.Fatalf("启用前置：字段行非空")
	} else {
		wantErrContains(t, err, "字段行为空")
	}
	if _, err := SetIgnParamTemplateState(id, "disable", "草稿无停用", faceTestUser); err == nil {
		t.Fatalf("草稿 → 停用不是合法迁移")
	} else {
		wantErrContains(t, err, "只有启用态模板可以停用")
	}
	mustSQL(t, `INSERT INTO ign_param_template_item (template_id, field_key, field_value, value_shape)
		VALUES (?, ?, 'offline', 'SCALAR')`, id, ignCfgDeployInstallSource)
	enabled, err := SetIgnParamTemplateState(id, "enable", "启用", faceTestUser)
	mustNoErr(t, err)
	if Str(enabled["template_status"]) != "ENABLED" || strings.TrimSpace(Str(enabled["enabled_at"])) == "" {
		t.Fatalf("启用应置 ENABLED 并回填 enabledAt: %v", enabled["enabled_at"])
	}
	if _, err := SetIgnParamTemplateState(id, "enable", "重复启用", faceTestUser); err == nil {
		t.Fatalf("重复启用应拒（迁移合法域外）")
	}
	disabled, err := SetIgnParamTemplateState(id, "disable", "停用", faceTestUser)
	mustNoErr(t, err)
	if Str(disabled["template_status"]) != "DISABLED" {
		t.Fatalf("应为停用态")
	}
	again, err := SetIgnParamTemplateState(id, "enable", "停用可往复", faceTestUser)
	mustNoErr(t, err)
	if Str(again["template_status"]) != "ENABLED" {
		t.Fatalf("停用 → 启用是合法迁移")
	}
	// 停用默认模板必须先摘徽标（INV-43：默认须启用——库里谓词已不带状态条件，
	// 「默认必须启用」由本层校验）
	if _, err := SetIgnParamTemplateDefault(id, "设默认", faceTestUser); err != nil {
		t.Fatalf("设默认失败: %v", err)
	}
	if _, err := SetIgnParamTemplateState(id, "disable", "停用默认", faceTestUser); err == nil {
		t.Fatalf("默认模板不可直接停用")
	} else {
		wantErrContains(t, err, "默认模板不可直接停用")
	}
}

// ---------- FUNC-43 设为默认（同面至多一条 + 目标须启用） ----------

func TestFunc43SetDefault(t *testing.T) {
	requireDB(t)
	mustSQL(t, `DELETE FROM ign_param_template_item`)
	mustSQL(t, `DELETE FROM ign_param_template`)

	draft, err := CreateIgnParamTemplate(map[string]any{
		"template_name": faceName("c43-draft"), "file_face": "HOSTS",
	}, faceTestUser)
	mustNoErr(t, err)
	if _, err := SetIgnParamTemplateDefault(toInt(draft["id"]), "草稿不能设默认", faceTestUser); err == nil {
		t.Fatalf("设默认前置：目标须启用态")
	} else {
		wantErrContains(t, err, "须处于启用态")
	}
	if _, err := SetIgnParamTemplateDefault(toInt(draft["id"]), "", faceTestUser); err == nil {
		t.Fatalf("设默认事由必填")
	}
	a := faceTestTemplate(t, "c43a", "HOSTS", map[string]string{ignCfgDeployServiceCIDR: "10.68.0.0/16"})
	b := faceTestTemplate(t, "c43b", "HOSTS", map[string]string{ignCfgDeployServiceCIDR: "10.70.0.0/16"})
	if _, err := SetIgnParamTemplateDefault(toInt(a["id"]), "甲为默认", faceTestUser); err != nil {
		t.Fatalf("设默认失败: %v", err)
	}
	ba, err := SetIgnParamTemplateDefault(toInt(b["id"]), "改指乙", faceTestUser)
	mustNoErr(t, err)
	if !boolOfAny(ba["is_default"]) {
		t.Fatalf("乙应为默认")
	}
	if n := countRows(t, `SELECT COUNT(*) FROM ign_param_template WHERE flag = 1 AND is_default = TRUE AND file_face = 'HOSTS'`); n != 1 {
		t.Fatalf("同面默认至多一条（原默认应被自动摘除），实际 %d", n)
	}
	// 库侧兜底：不带状态条件的部分唯一索引 ⇒ 连「第二条默认（哪怕草稿态）」都插不进去
	err = store.DB.Exec(`INSERT INTO ign_param_template (template_no, template_name, file_face, template_status, is_default)
		VALUES ('TMP-99999999-9999', 'x-idx-probe', 'HOSTS', 'DRAFT', TRUE)`).Error
	if err == nil {
		t.Fatalf("uq_ign_template_default_per_face 谓词仍带状态条件，削弱了 INV-43 第一句")
	}
	mustSQL(t, `DELETE FROM ign_param_template WHERE template_no = 'TMP-99999999-9999'`)
}

// ---------- FUNC-44 软删除（被引用不可删 + 级联 + 名称释放） ----------

func TestFunc44DeleteAndNameRelease(t *testing.T) {
	requireDB(t)
	mustSQL(t, `DELETE FROM ign_cluster_config_override`)
	mustSQL(t, `DELETE FROM ign_param_template_item`)
	mustSQL(t, `DELETE FROM ign_param_template`)

	name := faceName("c44")
	tpl, err := CreateIgnParamTemplate(map[string]any{"template_name": name, "file_face": "HOSTS"}, faceTestUser)
	mustNoErr(t, err)
	id := toInt(tpl["id"])
	if _, err := DeleteIgnParamTemplate(id, "", faceTestUser); err == nil {
		t.Fatalf("软删除事由必填")
	}
	if _, err := DeleteIgnParamTemplate(id, "无引用即删", faceTestUser); err != nil {
		t.Fatalf("未被引用的模板应可软删除: %v", err)
	}
	if countRows(t, `SELECT COUNT(*) FROM ign_param_template WHERE id = ? AND flag = 0`, id) != 1 {
		t.Fatalf("软删除是标记不是物理删除")
	}
	// 名称随删除释放（唯一索引只认 flag=1）
	re, err := CreateIgnParamTemplate(map[string]any{"template_name": name, "file_face": "HOSTS"}, faceTestUser)
	mustNoErr(t, err)
	// 被生效覆写引用的模板不可删（R-25 ②），拒绝时列出出处
	faceWorkRoot(t)
	if _, err := SaveIgnParamTemplateItems(toInt(re["id"]), map[string]any{
		"reason": "r",
		"items":  []any{map[string]any{"field_key": ignCfgDeployServiceCIDR, "field_value": "10.68.0.0/16"}},
	}, faceTestUser); err != nil {
		t.Fatalf("保存字段行失败: %v", err)
	}
	if _, err := SetIgnParamTemplateState(toInt(re["id"]), "enable", "启用", faceTestUser); err != nil {
		t.Fatalf("启用失败: %v", err)
	}
	if _, err := SaveIgnClusterConfigOverride(map[string]any{
		"cluster_ref": "kasz-ref-44", "file_face": "HOSTS",
		"field_key": ignCfgDeployServiceCIDR, "value": "10.99.0.0/16", "reason": "引用来源②",
	}, faceTestUser); err != nil {
		t.Fatalf("覆写保存失败: %v", err)
	}
	if _, err := DeleteIgnParamTemplate(toInt(re["id"]), "应被拒绝", faceTestUser); err == nil {
		t.Fatalf("被 clc 引用（生效覆写依据模板行）的模板不可删")
	} else {
		wantErrContains(t, err, "正被引用", "kasz-ref-44")
	}
}

// ---------- FUNC-47 覆写保存 ----------

func TestFunc47OverrideSave(t *testing.T) {
	requireDB(t)
	faceWorkRoot(t)
	mustSQL(t, `DELETE FROM ign_cluster_config_override`)

	base := map[string]any{
		"cluster_ref": "kasz-ref-47", "file_face": "HOSTS", "field_key": ignCfgDeployServiceCIDR,
	}
	if _, err := SaveIgnClusterConfigOverride(map[string]any{
		"cluster_ref": "kasz-ref-47", "file_face": "HOSTS", "field_key": "unknown_key",
		"value": "x", "reason": "r",
	}, faceTestUser); err == nil {
		t.Fatalf("面外键覆写应治理拒绝")
	}
	if _, err := SaveIgnClusterConfigOverride(map[string]any{
		"cluster_ref": "kasz-ref-47", "file_face": "CONFIG", "field_key": ignCfgDeployServiceCIDR,
		"value": "10.68.0.0/16", "reason": "r",
	}, faceTestUser); err == nil {
		t.Fatalf("键与轴不同面应拒绝（坐标三元组必须自洽）")
	}
	if _, err := SaveIgnClusterConfigOverride(map[string]any{
		"cluster_ref": base["cluster_ref"], "file_face": base["file_face"], "field_key": base["field_key"],
		"value": "10.68.0.0/16",
	}, faceTestUser); err == nil {
		t.Fatalf("覆写事由必填")
	} else {
		wantErrContains(t, err, "事由必填")
	}
	if _, err := SaveIgnClusterConfigOverride(map[string]any{
		"cluster_ref": base["cluster_ref"], "file_face": base["file_face"], "field_key": base["field_key"],
		"value": "", "reason": "想清空",
	}, faceTestUser); err == nil {
		t.Fatalf("空值不是覆写入口（回落走 FUNC-49）")
	} else {
		wantErrContains(t, err, "FUNC-49")
	}
	if _, err := SaveIgnClusterConfigOverride(map[string]any{
		"cluster_ref": base["cluster_ref"], "file_face": base["file_face"], "field_key": base["field_key"],
		"value": "10.68.0.0", "reason": "形态错误",
	}, faceTestUser); err == nil {
		t.Fatalf("覆写值应过受理侧同一套形态校验器（R-23）")
	} else {
		wantErrContains(t, err, "不是 CIDR 形态")
	}
	out, err := SaveIgnClusterConfigOverride(map[string]any{
		"cluster_ref": base["cluster_ref"], "file_face": base["file_face"], "field_key": base["field_key"],
		"value": "10.99.0.0/16", "reason": "网段避让",
	}, faceTestUser)
	mustNoErr(t, err)
	row := out["override"].(map[string]any)
	if Str(row["override_status"]) != "EFFECTIVE" || Str(row["overridden_by"]) != "face-tester" {
		t.Fatalf("保存即生效当前态: %v", row)
	}
	// 坐标唯一：同坐标再保存是改写不是插行；追溯对随新覆写清空
	mustSQL(t, `UPDATE ign_cluster_config_override SET applied_task_no = 'TSK-0001', applied_at = 'x' WHERE id = ?`, toInt(row["id"]))
	out2, err := SaveIgnClusterConfigOverride(map[string]any{
		"cluster_ref": base["cluster_ref"], "file_face": base["file_face"], "field_key": base["field_key"],
		"value": "10.100.0.0/16", "reason": "再改一次",
	}, faceTestUser)
	mustNoErr(t, err)
	row2 := out2["override"].(map[string]any)
	if toInt(row2["id"]) != toInt(row["id"]) {
		t.Fatalf("同坐标应复用行（INV-42 坐标唯一）")
	}
	if strings.TrimSpace(Str(row2["applied_task_no"])) != "" {
		t.Fatalf("再覆写后旧追溯对应清空（不代表本次取值被消费）")
	}
	if got := Str(out2["effective_source"]); got != ignSrcOverride {
		t.Fatalf("覆写后生效来源应为 OVERRIDE，实际 %q", got)
	}
}

// ---------- FUNC-49 覆写回落（INV-42 追溯对成对清空） ----------

func TestFunc49Rollback(t *testing.T) {
	requireDB(t)
	faceWorkRoot(t)
	mustSQL(t, `DELETE FROM ign_cluster_config_override`)

	coord := map[string]any{
		"cluster_ref": "kasz-ref-49", "file_face": "HOSTS", "field_key": ignCfgDeployPodCIDR,
	}
	if _, err := RollbackIgnClusterConfigOverride(coord, faceTestUser); err == nil {
		t.Fatalf("无行可回落应报不存在")
	}
	if _, err := SaveIgnClusterConfigOverride(map[string]any{
		"cluster_ref": coord["cluster_ref"], "file_face": coord["file_face"], "field_key": coord["field_key"],
		"value": "10.233.0.0/16", "reason": "覆写",
	}, faceTestUser); err != nil {
		t.Fatalf("覆写失败: %v", err)
	}
	mustSQL(t, `UPDATE ign_cluster_config_override SET applied_task_no = 'TSK-0002', applied_at = 'y'
		WHERE flag = 1 AND cluster_ref = ?`, "kasz-ref-49")
	out, err := RollbackIgnClusterConfigOverride(coord, faceTestUser)
	mustNoErr(t, err)
	row := out["override"].(map[string]any)
	if Str(row["override_status"]) != ignOverrideRolledBack || strings.TrimSpace(Str(row["rolled_back_at"])) == "" {
		t.Fatalf("回落应翻面并回填 rolledBackAt")
	}
	if Str(row["applied_task_no"]) != "" || Str(row["applied_at"]) != "" {
		t.Fatalf("回落必须同时清空 applied_task_no/applied_at（INV-42）")
	}
	if Str(out["effective_source"]) == ignSrcOverride {
		t.Fatalf("回落后的生效来源不得再是覆写（R-20 取值回落下层）")
	}
	if _, err := RollbackIgnClusterConfigOverride(coord, faceTestUser); err == nil {
		t.Fatalf("重复回落应拒（按钮只对生效行渲染）")
	} else {
		wantErrContains(t, err, "已处于回落态")
	}
	// 库侧 CHECK：ROLLED_BACK 却留着追溯对的行必须插不进去
	err = store.DB.Exec(`UPDATE ign_cluster_config_override SET applied_task_no = 'TSK-9999'
		WHERE override_status = 'ROLLED_BACK' AND cluster_ref = ?`, "kasz-ref-49").Error
	if err == nil {
		t.Fatalf("ck_ign_override_trace_pair 未生效")
	}
	// 两态可往复：已回落行再覆写即回到生效
	back, err := SaveIgnClusterConfigOverride(map[string]any{
		"cluster_ref": coord["cluster_ref"], "file_face": coord["file_face"], "field_key": coord["field_key"],
		"value": "10.234.0.0/16", "reason": "再来一次",
	}, faceTestUser)
	mustNoErr(t, err)
	if Str(back["override"].(map[string]any)["override_status"]) != ignOverrideEffective {
		t.Fatalf("已回落行再覆写应回到生效态")
	}
}

// ---------- FUNC-46 集群交付配置查看（两列不合并 + 来源必现） ----------

func TestFunc46ConfigView(t *testing.T) {
	requireDB(t)
	root := faceWorkRoot(t)
	mustSQL(t, `DELETE FROM ign_cluster_config_override`)
	mustSQL(t, `DELETE FROM ign_param_template_item`)
	mustSQL(t, `DELETE FROM ign_param_template`)

	ref := "kasz-ref-46"
	if _, err := GetIgnClusterConfigView(ref, "", nil, faceTestUser); err == nil {
		t.Fatalf("FUNC-46 必须选定文件面才出表（一次只看一轴）")
	}

	tpl := faceTestTemplate(t, "c46", "HOSTS", map[string]string{ignCfgDeployNetworkPlugin: "calico"})
	view, err := GetIgnClusterConfigView(ref, "HOSTS", map[string]any{
		"param_template_ref": Str(tpl["template_no"]),
	}, faceTestUser)
	mustNoErr(t, err)
	fields := view["fields"].([]map[string]any)
	if len(fields) != 7 {
		t.Fatalf("HOSTS 面应有 7 个字段键，实际 %d", len(fields))
	}
	byKey := map[string]map[string]any{}
	for _, f := range fields {
		byKey[Str(f["field_key"])] = f
	}
	// 命中模板层：键在模板行集内才命中（同键还有版本派生竞争 ⇒ 键换成 runtime）
	if got := Str(byKey[ignCfgDeployNetworkPlugin]["effective_source"]); got != ignSrcTemplate {
		t.Fatalf("模板命中键的来源应为 PARAM_TEMPLATE，实际 %q", got)
	}
	if got := Str(byKey[ignCfgDeployRuntime]["effective_source"]); got != ignSrcPlatform {
		t.Fatalf("未命中键应落平台缺省面，实际 %q", got)
	}
	for _, f := range fields {
		if Str(f["effective_source_label"]) == "" {
			t.Fatalf("来源徽章必现（R-20 禁止空徽章）: %s", Str(f["field_key"]))
		}
		if _, ok := f["next_effective_value"]; !ok {
			t.Fatalf("必须有独立的「下次将生效」列")
		}
		if !strings.Contains(Str(f["on_disk_note"]), "不可用") {
			t.Fatalf("无落盘文件时「当前落盘」应显示不可用+原因（报表口径 2），实际 %q", Str(f["on_disk_note"]))
		}
	}
	// 落盘面：写一个 hosts（SERVICE_CIDR 与「下次将生效」不同值）⇒ 两列并列不合并
	dir := filepath.Join(root, "clusters", ref)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	hosts := "[etcd]\n172.28.0.11\n\n[kube_master]\n172.28.0.11\n\n[kube_node]\n172.28.0.11\n\n[all:vars]\nCONTAINER_RUNTIME=\"containerd\"\nCLUSTER_NETWORK=\"flannel\"\nSERVICE_CIDR=\"10.111.0.0/16\"\n"
	if err := os.WriteFile(filepath.Join(dir, "hosts"), []byte(hosts), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte("INSTALL_SOURCE: \"online\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ignInventoryManifest),
		[]byte(`{"managed":true,"hosts_sha256":"deadbeef","config_sha256":"abc"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	view, err = GetIgnClusterConfigView(ref, "HOSTS", map[string]any{"param_template_ref": Str(tpl["template_no"])}, faceTestUser)
	mustNoErr(t, err)
	for _, f := range view["fields"].([]map[string]any) {
		if Str(f["field_key"]) != ignCfgDeployServiceCIDR {
			continue
		}
		if Str(f["on_disk_value"]) != "10.111.0.0/16" {
			t.Fatalf("「当前落盘」应回读 hosts 实值，实际 %q", Str(f["on_disk_value"]))
		}
		if Str(f["next_effective_value"]) != "10.68.0.0/16" {
			t.Fatalf("「下次将生效」取平台缺省面，实际 %q", Str(f["next_effective_value"]))
		}
		if Str(f["on_disk_value"]) == Str(f["next_effective_value"]) {
			t.Fatalf("示例数据里两列必须分立（合并即重新制造「已保存 vs 已生效」混淆）")
		}
	}
	if Str(view["inventory_state"]) != ignStateConflict {
		t.Fatalf("有清单而无标记 ⇒ 三态应为 CONFLICT，实际 %q", Str(view["inventory_state"]))
	}
	if Str(view["save_effective_note"]) != ignConflictUnsaved {
		t.Fatalf("冲突态必须叠加「已保存 · 未生效」标注（R-24）")
	}

	// 覆写后来源翻到 OVERRIDE；回落后再落回模板层
	if _, err := SaveIgnClusterConfigOverride(map[string]any{
		"cluster_ref": ref, "file_face": "HOSTS", "field_key": ignCfgDeployNetworkPlugin,
		"value": "cilium", "reason": "团队标准",
	}, faceTestUser); err != nil {
		t.Fatalf("覆写失败: %v", err)
	}
	view, err = GetIgnClusterConfigView(ref, "HOSTS", map[string]any{"param_template_ref": Str(tpl["template_no"])}, faceTestUser)
	mustNoErr(t, err)
	for _, f := range view["fields"].([]map[string]any) {
		if Str(f["field_key"]) != ignCfgDeployNetworkPlugin {
			continue
		}
		if Str(f["effective_source"]) != ignSrcOverride || Str(f["next_effective_value"]) != "cilium" {
			t.Fatalf("覆写应压过模板层: %v", f["effective_source"])
		}
		if Str(f["applied_display"]) != "未应用" {
			t.Fatalf("追溯无值必须显式「未应用」，实际 %q", Str(f["applied_display"]))
		}
	}
	if _, err := RollbackIgnClusterConfigOverride(map[string]any{
		"cluster_ref": ref, "file_face": "HOSTS", "field_key": ignCfgDeployNetworkPlugin,
	}, faceTestUser); err != nil {
		t.Fatalf("回落失败: %v", err)
	}
	view, err = GetIgnClusterConfigView(ref, "HOSTS", map[string]any{"param_template_ref": Str(tpl["template_no"])}, faceTestUser)
	mustNoErr(t, err)
	for _, f := range view["fields"].([]map[string]any) {
		if Str(f["field_key"]) == ignCfgDeployNetworkPlugin && Str(f["effective_source"]) != ignSrcTemplate {
			t.Fatalf("回落后取值应回到模板层，实际 %q", Str(f["effective_source"]))
		}
	}

	// 面标识为空 ⇒ 第 3 层整体跳过，绝不回落默认模板（R-20 分支 H）——即使存在默认模板
	if _, err := SetIgnParamTemplateDefault(toInt(tpl["id"]), "设为默认", faceTestUser); err != nil {
		t.Fatalf("设默认失败: %v", err)
	}
	view, err = GetIgnClusterConfigView(ref, "HOSTS", nil, faceTestUser)
	mustNoErr(t, err)
	if !strings.Contains(Str(view["param_template_notice"]), "默认模板不参与渲染兜底") {
		t.Fatalf("空面标识的模板列结论应为「本层跳过（默认模板不参与渲染兜底）」，实际 %q", Str(view["param_template_notice"]))
	}
	for _, f := range view["fields"].([]map[string]any) {
		if Str(f["effective_source"]) == ignSrcTemplate {
			t.Fatalf("面标识为空时不得命中模板层: %s", Str(f["field_key"]))
		}
	}
}

// ---------- FUNC-48 dry-run 预检（五不 + 结论与受理一致） ----------

func TestFunc48DryRunNoSideEffects(t *testing.T) {
	requireDB(t)
	faceWorkRoot(t)
	mustSQL(t, `DELETE FROM ign_cluster_config_override`)
	mustSQL(t, `DELETE FROM ign_param_template_item`)
	mustSQL(t, `DELETE FROM ign_param_template`)

	// 夹具先建（版本行 + 停用模板），再取基线计数——否则基线会被夹具自身的审计留痕污染
	if _, err := store.Exec(nil, `INSERT INTO delivery_version
		(version_no, name, tool_version, orchestrator_version, runtime_version, etcd_version,
		 cni_plugin, cni_version, coredns_version, tarball_url, version_status)
		VALUES ('VER-19700101-0001','dryrun-bad','v3.5.5','v1.30.4','1.6.8','3.5.4','calico','v3.27.3','v1.11.1','https://example.invalid/k.tgz','ARCHIVED')`); err != nil {
		t.Fatalf("夹具版本行插入失败: %v", err)
	}
	t.Cleanup(func() { mustSQL(t, `DELETE FROM delivery_version WHERE version_no = 'VER-19700101-0001'`) })
	stopTpl := faceTestTemplate(t, "c48stop", "HOSTS", map[string]string{ignCfgDeployRuntime: "docker"})
	if _, err := SetIgnParamTemplateState(toInt(stopTpl["id"]), "disable", "停用", faceTestUser); err != nil {
		t.Fatalf("停用失败: %v", err)
	}

	beforeOvr := countRows(t, `SELECT COUNT(*) FROM ign_cluster_config_override`)
	beforeTask := countRows(t, `SELECT COUNT(*) FROM exec_task`)
	beforeAudit := countRows(t, `SELECT COUNT(*) FROM audit_logs`)
	beforeTpl := countRows(t, `SELECT COUNT(*) FROM ign_param_template`)

	out, err := ValidateIgnClusterConfig(map[string]any{"cluster_ref": "kasz-ref-48"}, faceTestUser)
	mustNoErr(t, err)
	if out["passed"] != true {
		t.Fatalf("干净现场应预检通过: %v", out["reasons"])
	}
	if Str(out["inventory_state"]) != ignStateAbsent {
		t.Fatalf("无清单 ⇒ ABSENT，实际 %q", Str(out["inventory_state"]))
	}

	// 不合法版本引用（R-19/INV-33）⇒ 与受理同一结论
	out, err = ValidateIgnClusterConfig(map[string]any{"cluster_ref": "kasz-ref-48", "version_ref": "VER-19700101-0001"}, faceTestUser)
	mustNoErr(t, err)
	if out["passed"] == true {
		t.Fatalf("归档版本应预检不通过（R-19）")
	}

	// 待保存覆写候选走同一套校验器，但不写行
	out, err = ValidateIgnClusterConfig(map[string]any{
		"cluster_ref": "kasz-ref-48",
		"overrides":   []any{map[string]any{"field_key": ignCfgDeployServiceCIDR, "value": "bad-cidr"}},
	}, faceTestUser)
	mustNoErr(t, err)
	if out["passed"] == true {
		t.Fatalf("非法覆写候选应预检不通过")
	}
	if !strings.Contains(fmt.Sprint(out["reasons"]), "不是 CIDR 形态") {
		t.Fatalf("逐字段原因应给出形态文案: %v", out["reasons"])
	}
	// 受理侧门槛（面标识指向停用模板）⇒ 同一拒绝结论随 dry-run 回吐
	out, err = ValidateIgnClusterConfig(map[string]any{
		"cluster_ref": "kasz-ref-48", "param_template_ref": Str(stopTpl["template_no"]),
	}, faceTestUser)
	mustNoErr(t, err)
	if out["passed"] == true || !strings.Contains(fmt.Sprint(out["reasons"]), "非启用态不参与取值") {
		t.Fatalf("停用模板引用应与受理同一口径被拒: %v", out["reasons"])
	}

	if n := countRows(t, `SELECT COUNT(*) FROM ign_cluster_config_override`); n != beforeOvr {
		t.Fatalf("dry-run 不得改覆写行（五不）")
	}
	if n := countRows(t, `SELECT COUNT(*) FROM exec_task`); n != beforeTask {
		t.Fatalf("dry-run 不得建任务（五不）")
	}
	if n := countRows(t, `SELECT COUNT(*) FROM audit_logs`); n != beforeAudit {
		t.Fatalf("dry-run 不得留痕（五不）")
	}
	if n := countRows(t, `SELECT COUNT(*) FROM ign_param_template`); n != beforeTpl {
		t.Fatalf("dry-run 本体不产模板行")
	}
}

// ---------- FUNC-45 清单原文只读视图（掩码 + 读也留痕） ----------

func TestFunc45RawViewMasksAndAudits(t *testing.T) {
	requireDB(t)
	root := faceWorkRoot(t)
	before := countRows(t, `SELECT COUNT(*) FROM audit_logs WHERE action = 'IGN_RAW_FACE_READ'`)

	if _, err := GetIgnClusterRawFile("kasz-ref-45", "HOSTS", faceTestUser); err == nil {
		t.Fatalf("文件不存在应回不可读错误")
	}
	dir := filepath.Join(root, "clusters", "kasz-ref-45")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "[kube_master]\n172.28.0.11\n[all:vars]\nETCDCTL_KEY_FILE=\"/etc/kubeasz/keys/admin-key.pem\"\nk8s_password: Sup3rS3cret!\n"
	if err := os.WriteFile(filepath.Join(dir, "hosts"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := GetIgnClusterRawFile("kasz-ref-45", "HOSTS", faceTestUser)
	mustNoErr(t, err)
	blob := fmt.Sprint(out["lines"])
	if strings.Contains(blob, "Sup3rS3cret") {
		t.Fatalf("命中 R-16 形态的行必须整行掩码（片段掩码仍可拼回）")
	}
	if !strings.Contains(blob, ignMaskMark) {
		t.Fatalf("掩码标记应出现在回吐文本")
	}
	if out["masked_line_count"].(int64) < 1 {
		t.Fatalf("应回吐掩码告警计数")
	}
	// 「可复制、无编辑态、无导出」——返回结构里没有整份原文字段
	if _, ok := out["content"]; ok {
		t.Fatalf("不得回吐未掩码的整份原文")
	}
	if n := countRows(t, `SELECT COUNT(*) FROM audit_logs WHERE action = 'IGN_RAW_FACE_READ'`); n != before+1 {
		t.Fatalf("原文视图每次访问都要留审计（本域唯一读也留痕的视图）")
	}
}

// ---------- FUNC-50 原文腿（与 FUNC-45 同一份读盘 / 同一套掩码 / 逐次留痕，来源可区分） ----------

func TestFunc50RawChannelParityAndAudit(t *testing.T) {
	requireDB(t)
	root := faceWorkRoot(t)
	facadeUser := UserInfo{ID: 2, Username: "clc-service-proxy"}
	ref := "kasz-ref-50raw"

	// 磁盘无文件 ⇒ 明确「尚未生成」结论，不返回空文本报现状
	if _, err := ReadIgnDeliveryConfigRaw(ref, "HOSTS", facadeUser); err == nil {
		t.Fatalf("未生成过交付配置时应给状态类拒绝，而不是空原文")
	} else {
		wantErrContains(t, err, "尚未生成")
	}
	// file_face 必带、越界即拒（一次只出一面）
	if _, err := ReadIgnDeliveryConfigRaw(ref, "", facadeUser); err == nil {
		t.Fatalf("file_face 必带")
	} else {
		wantErrContains(t, err, "必须携带 file_face")
	}
	if _, err := ReadIgnDeliveryConfigRaw(ref, "HOSTS,CONFIG", facadeUser); err == nil {
		t.Fatalf("越界文件面应被拒绝")
	} else {
		wantErrContains(t, err, "只能是 HOSTS")
	}

	dir := filepath.Join(root, "clusters", ref)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "[kube_master]\n172.28.0.11\n[all:vars]\nETCDCTL_KEY_FILE=\"/etc/kubeasz/keys/admin-key.pem\"\nk8s_password: Sup3rS3cret!\n"
	if err := os.WriteFile(filepath.Join(dir, "hosts"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}

	manual, err := GetIgnClusterRawFile(ref, "HOSTS", faceTestUser)
	mustNoErr(t, err)
	// 小写入参由服务层归一——同一份文本、同一套掩码：逐字节一致
	proxied, err := ReadIgnDeliveryConfigRaw(ref, "hosts", facadeUser)
	mustNoErr(t, err)
	if Str(proxied["content_sha256"]) != Str(manual["content_sha256"]) {
		t.Fatalf("两条通道对同一份落盘文本必须给出同一摘要")
	}
	if !reflect.DeepEqual(manual["lines"], proxied["lines"]) {
		t.Fatalf("代理通道读出的掩码文本必须与人工视图逐字节一致: %v vs %v", manual["lines"], proxied["lines"])
	}
	blob := fmt.Sprint(proxied["lines"])
	if strings.Contains(blob, "Sup3rS3cret") {
		t.Fatalf("凭据类值必须在流出前由本域整行掩码（对端不二次解析）")
	}
	if !strings.Contains(blob, ignMaskMark) || proxied["masked_line_count"].(int64) < 1 {
		t.Fatalf("掩码标记与告警计数必须随原文回吐")
	}
	if _, ok := proxied["content"]; ok {
		t.Fatalf("不得回吐未掩码的整份原文")
	}
	if !strings.Contains(Str(proxied["read_origin"]), "上游代理只读") {
		t.Fatalf("代理通道回吐要带来源标识，实际 %q", Str(proxied["read_origin"]))
	}

	// 逐次留痕 + 来源可区分：每次调用审计 +1，人工/代理两条腿的标识互不冒领
	beforeTask := countRows(t, `SELECT COUNT(*) FROM exec_task`)
	beforeAll := countRows(t, `SELECT COUNT(*) FROM audit_logs WHERE action = 'IGN_RAW_FACE_READ'`)
	beforeProxy := countRows(t, `SELECT COUNT(*) FROM audit_logs WHERE action = 'IGN_RAW_FACE_READ' AND detail LIKE '%上游代理只读(FUNC-50)%'`)
	beforeLocal := countRows(t, `SELECT COUNT(*) FROM audit_logs WHERE action = 'IGN_RAW_FACE_READ' AND detail LIKE '%本域人工视图(FUNC-45)%'`)
	if _, err := ReadIgnDeliveryConfigRaw(ref, "CONFIG", facadeUser); err == nil {
		t.Fatalf("CONFIG 面未落盘，应给「尚未生成」")
	}
	if _, err := ReadIgnDeliveryConfigRaw(ref, "HOSTS", facadeUser); err != nil {
		t.Fatalf("代理读失败: %v", err)
	}
	if _, err := GetIgnClusterRawFile(ref, "HOSTS", faceTestUser); err != nil {
		t.Fatalf("人工视图读失败: %v", err)
	}
	if n := countRows(t, `SELECT COUNT(*) FROM audit_logs WHERE action = 'IGN_RAW_FACE_READ'`); n != beforeAll+2 {
		t.Fatalf("两条腿都要逐次留痕（成功的读各一条），实际增量 %d", n-beforeAll)
	}
	if n := countRows(t, `SELECT COUNT(*) FROM audit_logs WHERE action = 'IGN_RAW_FACE_READ' AND detail LIKE '%上游代理只读(FUNC-50)%'`); n != beforeProxy+1 {
		t.Fatalf("代理读的留痕要能标识来源，实际增量 %d", n-beforeProxy)
	}
	if n := countRows(t, `SELECT COUNT(*) FROM audit_logs WHERE action = 'IGN_RAW_FACE_READ' AND detail LIKE '%本域人工视图(FUNC-45)%'`); n != beforeLocal+1 {
		t.Fatalf("人工视图的留痕要与代理读可区分，实际增量 %d", n-beforeLocal)
	}
	// 原文腿不写本域对象、不产生事件（与取值腿同一「只出不入」纪律）；上面 +2 计数即口径：被拒绝的读不留痕
	if n := countRows(t, `SELECT COUNT(*) FROM exec_task`); n != beforeTask {
		t.Fatalf("只读腿不得产生任务")
	}
}

// ---------- FUNC-50 跨中心只读通道 ----------

func TestFunc50ReadChannel(t *testing.T) {
	requireDB(t)
	faceWorkRoot(t)
	mustSQL(t, `DELETE FROM ign_cluster_config_override`)
	mustSQL(t, `DELETE FROM ign_param_template_item`)
	mustSQL(t, `DELETE FROM ign_param_template`)

	if _, err := ReadIgnDeliveryConfig("", "", "", ""); err == nil {
		t.Fatalf("通道要求集群标识必填且形态合法")
	}
	if _, err := store.Exec(nil, `INSERT INTO delivery_version
		(version_no, name, tool_version, orchestrator_version, runtime_version, etcd_version,
		 cni_plugin, cni_version, coredns_version, tarball_url, version_status)
		VALUES ('VER-19700101-0002','chan-good','v3.5.5','v1.30.4','1.6.8','3.5.4','calico','v3.27.3','v1.11.1','https://example.invalid/k2.tgz','READY')`); err != nil {
		t.Fatalf("夹具版本行插入失败: %v", err)
	}
	t.Cleanup(func() { mustSQL(t, `DELETE FROM delivery_version WHERE version_no = 'VER-19700101-0002'`) })
	tpl := faceTestTemplate(t, "c50", "HOSTS", map[string]string{ignCfgDeployRuntime: "containerd"})

	beforeTask := countRows(t, `SELECT COUNT(*) FROM exec_task`)
	out, err := ReadIgnDeliveryConfig("kasz-ref-50", "", Str(tpl["template_no"]), "VER-19700101-0002")
	mustNoErr(t, err)
	fields := out["fields"].([]map[string]any)
	byKey := map[string]map[string]any{}
	for _, f := range fields {
		byKey[Str(f["field_key"])] = f
	}
	if got := Str(byKey[ignCfgDeployNetworkPlugin]["hit_layer"]); got != ignSrcVersion {
		t.Fatalf("版本派生层应命中 CNI 键，实际 %q", got)
	}
	if got := Str(byKey[ignCfgDeployRuntime]["hit_layer"]); got != ignSrcTemplate {
		t.Fatalf("模板层应命中 runtime 键，实际 %q", got)
	}
	if got := Str(byKey[ignCfgDeployServiceCIDR]["hit_layer"]); got != ignSrcPlatform {
		t.Fatalf("未命中键应落平台缺省面，实际 %q", got)
	}
	// 覆写后通道来源翻 OVERRIDE；layers 里逐层带值 / 命中 / 来源标识
	if _, err := SaveIgnClusterConfigOverride(map[string]any{
		"cluster_ref": "kasz-ref-50", "file_face": "HOSTS", "field_key": ignCfgDeployDNSDomain,
		"value": "cluster.local2", "reason": "域名避让",
	}, faceTestUser); err != nil {
		t.Fatalf("覆写失败: %v", err)
	}
	out, err = ReadIgnDeliveryConfig("kasz-ref-50", "HOSTS", Str(tpl["template_no"]), "VER-19700101-0002")
	mustNoErr(t, err)
	for _, f := range out["fields"].([]map[string]any) {
		if Str(f["field_key"]) != ignCfgDeployDNSDomain {
			continue
		}
		if Str(f["hit_layer"]) != ignSrcOverride || Str(f["value"]) != "cluster.local2" {
			t.Fatalf("覆写层应压过其余层: %v", f)
		}
		if !strings.Contains(Str(f["source_ref"]), "kasz-ref-50/HOSTS/") {
			t.Fatalf("来源标识应为覆写坐标，实际 %q", Str(f["source_ref"]))
		}
		layers := f["layers"].([]map[string]any)
		if len(layers) != 4 {
			t.Fatalf("逐字段必须给出四层轨迹")
		}
		hits := 0
		for _, l := range layers {
			if l["hit"] == true {
				hits++
			}
		}
		if hits == 0 {
			t.Fatalf("layers 必须标注命中层")
		}
	}
	// 跨中心通道的轴名标签必须随字段一并回吐：对端代理屏(clc frmClcWizard 配置页签)刻意不抄第二份轴表，
	// 缺这一列它只能显示码值(HOSTS/CONFIG)——正是 DEF-05 判据要堵的口径。
	if lbl := Str(out["file_face_label"]); lbl == "" || lbl == "HOSTS" {
		t.Fatalf("通道头级须回吐取自模型字典的轴标签，实际 %q", lbl)
	}
	for _, f := range out["fields"].([]map[string]any) {
		if lbl := Str(f["file_face_label"]); lbl == "" || lbl == Str(f["file_face"]) {
			t.Fatalf("字段行缺轴标签或与码值同形: %v", f)
		}
	}
	// 只出不入、无副作用；不下发模板内容与原文
	if n := countRows(t, `SELECT COUNT(*) FROM exec_task`); n != beforeTask {
		t.Fatalf("只读通道不得产生任务")
	}
	for _, k := range []string{"content", "lines", "template_items", "items"} {
		if _, ok := out[k]; ok {
			t.Fatalf("通道不得下发 %s（原文走 FUNC-45 独立权限码）", k)
		}
	}
}

// ---------- 四层求值器与列表整项替换（R-20/R-22 语义抽查） ----------

func TestResolveParamFacePrecedenceAndListReplace(t *testing.T) {
	requireDB(t)
	faceWorkRoot(t)
	mustSQL(t, `DELETE FROM ign_cluster_config_override`)
	mustSQL(t, `DELETE FROM ign_param_template_item`)
	mustSQL(t, `DELETE FROM ign_param_template`)

	tpl := faceTestTemplate(t, "prio", "CONFIG", map[string]string{ignCfgDeployImageMirror: "harbor.tpl:80"})
	view, err := resolveParamFace(store.DB, "kasz-prio", "", Str(tpl["template_no"]), "")
	mustNoErr(t, err)
	got := view.Params.MirrorHostList()
	if len(got) != 1 || got[0] != "harbor.tpl:80" {
		t.Fatalf("模板层列表值应整项生效: %v", got)
	}
	if Str(view.Traces[0].EffectiveValue) == "" {
		t.Fatalf("轨迹首行不得为空值来源缺失")
	}
	// 覆写层整项替换（列表不做元素级增删）
	if _, err := SaveIgnClusterConfigOverride(map[string]any{
		"cluster_ref": "kasz-prio", "file_face": "CONFIG", "field_key": ignCfgDeployImageMirror,
		"value": "a.example:80, b.example:80", "reason": "双仓库",
	}, faceTestUser); err != nil {
		t.Fatalf("列表覆写失败: %v", err)
	}
	view, err = resolveParamFace(store.DB, "kasz-prio", "", Str(tpl["template_no"]), "")
	mustNoErr(t, err)
	got = view.Params.MirrorHostList()
	if len(got) != 2 || got[0] != "a.example:80" {
		t.Fatalf("列表覆写应是整体替换后的全量值: %v", got)
	}
	for _, tr := range view.Traces {
		if tr.FieldKey == ignCfgDeployImageMirror && tr.EffectiveSource != ignSrcOverride {
			t.Fatalf("覆写层必须压过模板层")
		}
	}
}

// ---------- REF-26 界面日志自动跟随间隔（config 键 log_follow_interval_sec） ----------

func TestLogFollowIntervalClamp(t *testing.T) {
	requireDB(t)
	prev, _ := ignConfigGet(store.DB, ignCfgLogFollowIntervalSec)
	t.Cleanup(func() {
		if prev != "" {
			mustSQL(t, `UPDATE ign_platform_config SET config_value = ? WHERE config_key = ?`, prev, ignCfgLogFollowIntervalSec)
		}
	})
	mustSQL(t, `UPDATE ign_platform_config SET config_value = '3' WHERE config_key = ?`, ignCfgLogFollowIntervalSec)
	n, note := ignLogFollowIntervalSec(store.DB)
	if n != 3 || note != "" {
		t.Fatalf("区间内值应原样生效: %d %q", n, note)
	}
	mustSQL(t, `UPDATE ign_platform_config SET config_value = '9' WHERE config_key = ?`, ignCfgLogFollowIntervalSec)
	n, note = ignLogFollowIntervalSec(store.DB)
	if n != ignLogFollowMax || !strings.Contains(note, "越界") {
		t.Fatalf("越界值应夹到边界并提示: %d %q", n, note)
	}
	mustSQL(t, `UPDATE ign_platform_config SET config_value = 'two' WHERE config_key = ?`, ignCfgLogFollowIntervalSec)
	n, note = ignLogFollowIntervalSec(store.DB)
	if n != ignLogFollowDefault || !strings.Contains(note, "不是整数") {
		t.Fatalf("非整数值应回缺省并提示: %d %q", n, note)
	}
}

// ---------- FUNC-38 列表（徽标投影 / 软删可见 / 停用默认筛除） ----------

func TestFunc38ListProjection(t *testing.T) {
	requireDB(t)
	mustSQL(t, `DELETE FROM ign_param_template_item`)
	mustSQL(t, `DELETE FROM ign_param_template`)

	a := faceTestTemplate(t, "c38a", "HOSTS", map[string]string{ignCfgDeployServiceCIDR: "10.68.0.0/16"})
	if _, err := SetIgnParamTemplateDefault(toInt(a["id"]), "设默认", faceTestUser); err != nil {
		t.Fatalf("设默认失败: %v", err)
	}
	draft, err := CreateIgnParamTemplate(map[string]any{"template_name": faceName("c38d"), "file_face": "CONFIG"}, faceTestUser)
	mustNoErr(t, err)
	// 脏态直插（CONFIG 面此时无默认）：草稿态即便挂着 is_default，徽标也不得出现
	mustSQL(t, `UPDATE ign_param_template SET is_default = TRUE WHERE id = ?`, toInt(draft["id"]))

	stopped := faceTestTemplate(t, "c38s", "CONFIG", map[string]string{ignCfgDeployInstallSource: "offline"})
	if _, err := SetIgnParamTemplateState(toInt(stopped["id"]), "disable", "停用", faceTestUser); err != nil {
		t.Fatalf("停用失败: %v", err)
	}

	p, err := ListIgnParamTemplates(1, 50, nil)
	mustNoErr(t, err)
	var badges, drafts int
	for _, r := range p.List {
		if r["default_badge"] == true {
			badges++
			if Str(r["template_status"]) != "ENABLED" {
				t.Fatalf("默认徽标只能在启用态出现（INV-43 界面侧投影）")
			}
		}
		if Str(r["template_status"]) == "DRAFT" {
			drafts++
		}
	}
	if badges != 1 {
		t.Fatalf("同面视图至多一行带徽标，实际 %d", badges)
	}
	if drafts != 1 {
		t.Fatalf("草稿行应可见（默认只筛停用态）")
	}
	// 停用默认筛除、可切换显示；关键字只匹配名称与编号
	if _, err := ListIgnParamTemplates(1, 50, map[string]any{"file_face": "CONFIG"}); err != nil {
		t.Fatalf("列表查询失败: %v", err)
	}
	pAll, err := ListIgnParamTemplates(1, 50, map[string]any{"file_face": "CONFIG", "show_disabled": "1"})
	mustNoErr(t, err)
	var seenStopped bool
	for _, r := range pAll.List {
		if Str(r["template_no"]) == Str(stopped["template_no"]) {
			seenStopped = true
		}
	}
	if !seenStopped {
		t.Fatalf("show_disabled=1 应可切换显示停用态")
	}
	pKw, err := ListIgnParamTemplates(1, 50, map[string]any{"keyword": Str(a["template_no"])})
	mustNoErr(t, err)
	if len(pKw.List) != 1 || Str(pKw.List[0]["template_no"]) != Str(a["template_no"]) {
		t.Fatalf("关键字应只按编号/名称命中")
	}
	// 被引用集群数（R-25 ②口径，本域可直查）
	if _, err := SaveIgnParamTemplateItems(toInt(a["id"]), map[string]any{
		"reason": "补键", "items": []any{map[string]any{"field_key": ignCfgDeployServiceCIDR, "field_value": "10.68.0.0/16"}},
	}, faceTestUser); err != nil {
		t.Fatalf("补键失败: %v", err)
	}
	faceWorkRoot(t)
	if _, err := SaveIgnClusterConfigOverride(map[string]any{
		"cluster_ref": "kasz-ref-38", "file_face": "HOSTS", "field_key": ignCfgDeployServiceCIDR,
		"value": "10.88.0.0/16", "reason": "引用来源②",
	}, faceTestUser); err != nil {
		t.Fatalf("覆写失败: %v", err)
	}
	p, err = ListIgnParamTemplates(1, 50, map[string]any{"file_face": "HOSTS"})
	mustNoErr(t, err)
	for _, r := range p.List {
		if Str(r["template_no"]) == Str(a["template_no"]) && r["referenced_cluster_count"].(int) < 1 {
			t.Fatalf("列表行应带被引用集群数（这张模板会被谁用，同屏可见）")
		}
	}
}

// TestParamFaceCatalogParityWithFaceDefinition DEF-05：界面下拉的合法域就是面定义表本身。
// 判据两条：① 投影逐条对得上面定义（条数、键集、规则依据一一对应，不多不少不重复）；
// ② 出口只有键的形态与归属（key/label/file_face/value_shape/ref_id/axis_file 六项），
//
//	落盘键、校验器与任何取值都不许出现（INV-08：界面拿到值即等于在本域四层之外自算一层）。
func TestParamFaceCatalogParityWithFaceDefinition(t *testing.T) {
	cat := ParamFaceCatalog()
	fields := ignParamFaceFields()
	if len(fields) == 0 {
		t.Fatal("面定义表为空，目录无从对照")
	}
	if len(cat) != len(fields) {
		t.Fatalf("目录条数须与面定义表一致，得到 %d / %d", len(cat), len(fields))
	}
	allowed := map[string]bool{"key": true, "label": true, "file_face": true,
		"value_shape": true, "ref_id": true, "axis_file": true}
	remaining := map[string]bool{}
	for _, f := range fields {
		remaining[f.Key] = true
	}
	refs := map[string]bool{}
	for i, e := range cat {
		key := Str(e["key"])
		if !remaining[key] {
			t.Fatalf("目录第 %d 条的键 %q 不在面定义表内", i+1, key)
		}
		delete(remaining, key)
		ref := Str(e["ref_id"])
		if refs[ref] {
			t.Fatalf("规则依据 %s 在目录里重复出现", ref)
		}
		refs[ref] = true
		for k := range e {
			if !allowed[k] {
				t.Fatalf("目录条目多出投影外字段 %s（面定义对外只出形态与归属）", k)
			}
		}
		if f := Str(e["file_face"]); f != ignFaceHosts && f != ignFaceConfig {
			t.Fatalf("键 %s 的所属文件面 %q 不是两轴之一", key, f)
		}
		if s := Str(e["value_shape"]); s != ignShapeScalar && s != ignShapeList {
			t.Fatalf("键 %s 的值形态 %q 不是两值之一", key, s)
		}
		if Str(e["label"]) == "" || Str(e["axis_file"]) == "" {
			t.Fatalf("键 %s 的中文名或落盘文件名为空", key)
		}
	}
	if len(remaining) > 0 {
		t.Fatalf("面定义有键未进目录：%v", remaining)
	}
	if len(refs) != len(fields) {
		t.Fatalf("规则依据须逐键一对一，得到 %d 条覆盖 %d 个键", len(refs), len(fields))
	}
	// 字典轴（IGN_CONFIG_FILE_FACE / IGN_VALUE_SHAPE）与面定义用的是同一套码值：
	// 界面走元数据档通道取轴与形态的中文名，键域仍来自本目录，两路不得给出互斥的取值。
	for _, e := range cat {
		if Str(e["file_face"]) == ignFaceHosts && Str(e["axis_file"]) != "hosts" {
			t.Fatalf("主机清单面的落盘文件名应为 hosts，得到 %q", Str(e["axis_file"]))
		}
		if Str(e["file_face"]) == ignFaceConfig && Str(e["axis_file"]) != "config.yml" {
			t.Fatalf("集群配置面的落盘文件名应为 config.yml，得到 %q", Str(e["axis_file"]))
		}
	}
}

// TestDictLabelComesFromModelNotFromLocalCopy —— 中文标签的唯一事实源是 M1 字典（DEF-05 的读侧对偶）。
//
// 判据：跨中心代理屏（frmClcWizard 的配置页签）与对端自己的两屏用的是同一份标签，
// 因此标签必须由服务层随视图一起给出；一旦有实现把轴中文写回代码里当副本，
// 这里"标签 != code"与"目录里每个轴都能解析"两条会同时失败。
func TestDictLabelComesFromModelNotFromLocalCopy(t *testing.T) {
	reg := ontology.Load()
	for _, typeCode := range []string{"IGN_CONFIG_FILE_FACE", "IGN_VALUE_SHAPE"} {
		items := reg.GetDictionaryItems("DICT-"+typeCode, typeCode)
		if len(items) == 0 {
			t.Fatalf("字典 %s 未注册（模型 m1 data_dictionaries 是唯一标签源，取不到即视图无中文可回吐）", typeCode)
		}
		for _, it := range items {
			code, label := Str(it["code"]), Str(it["label"])
			if code == "" || strings.TrimSpace(label) == "" {
				t.Fatalf("%s 的字典项缺 code 或 label：code=%q label=%q", typeCode, code, label)
			}
			if got := ignDictLabel(typeCode, code); got != strings.TrimSpace(label) {
				t.Fatalf("%s/%s 的标签应与模型一致：model=%q helper=%q", typeCode, code, label, got)
			}
			if got := ignDictLabel(typeCode, code); got == code {
				t.Fatalf("%s/%s 的中文标签回吐成了 code，模型标签没生效：%q", typeCode, code, got)
			}
		}
	}
	// 未命中不编造中文（宁可显式英文码，也不在进程里留一份会漂的副本）
	if got := ignDictLabel("IGN_CONFIG_FILE_FACE", "NOT_A_FACE"); got != "NOT_A_FACE" {
		t.Fatalf("未命中字典项应原样回吐 code，得到 %q", got)
	}
	if got := ignDictLabel("IGN_CONFIG_FILE_FACE", "  "); got != "" {
		t.Fatalf("空 code 应回空串，得到 %q", got)
	}
	// 面目录里出现的每个轴码都必须能被字典解析成中文（两轴之外不得有第三种取值）
	for _, e := range ParamFaceCatalog() {
		face := Str(e["file_face"])
		if got := ignDictLabel("IGN_CONFIG_FILE_FACE", face); got == "" || got == face {
			t.Fatalf("面目录的轴 %q 在字典里取不到中文标签（回吐 %q）", face, got)
		}
	}
}
