// ign_param_face.go —— 参数面模板 + 集群交付配置控制器（FUNC-38~50）。
//
// 与既有业务接口同一信封（OKJSON/FailJSON）；唯一例外是 FUNC-50 —— 它挂在门面组，
// 对端是集群中心的服务身份适配器，按 flat JSON 反序列化，失败走 facadeFail（与 FUNC-34~36 同形态）。
// 所有不变量的中文提示都在服务层给出，本层只做入参搬运。
package business

import (
	"strings"

	"github.com/gin-gonic/gin"

	"gitcode.com/opic-ontology/opic-techbase/internal/api/common"
	"gitcode.com/opic-ontology/opic-techbase/internal/service"
)

// ---------- 参数面模板（FUNC-38~44；AGG-IGN-07「参数面模板」屏） ----------

// IgnParamTemplateList GET /api/v1/ign/param-template (FUNC-38;REP-15)
func IgnParamTemplateList(c *gin.Context) {
	page, size := common.PageOf(c)
	p, err := service.ListIgnParamTemplates(page, size, map[string]any{
		"file_face":     c.Query("file_face"),
		"status":        c.Query("status"),
		"is_default":    c.Query("is_default"),
		"show_disabled": c.Query("show_disabled"),
		"keyword":       c.Query("keyword"),
	})
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, p, "")
}

// IgnParamTemplateGet GET /api/v1/ign/param-template/:id (FUNC-39;REP-16 三段分区)
func IgnParamTemplateGet(c *gin.Context) {
	row, err := service.GetIgnParamTemplate(common.PathID(c, "id"))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "")
}

// IgnParamTemplateCreate POST /api/v1/ign/param-template (FUNC-40)
func IgnParamTemplateCreate(c *gin.Context) {
	body := common.BindJSON(c)
	row, err := service.CreateIgnParamTemplate(body, common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "模板已登记（编号生成、状态草稿、默认标记为假；字段行为空不阻塞登记——键域合法性由整组保存与启用把关）")
}

// IgnParamTemplateItemsSave PUT /api/v1/ign/param-template/:id/items (FUNC-41)
func IgnParamTemplateItemsSave(c *gin.Context) {
	body := common.BindJSON(c)
	row, err := service.SaveIgnParamTemplateItems(common.PathID(c, "id"), body, common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "字段行已整组替换（未提交的行即不存在，R-22：列表值整项替换、无元素级增删）")
}

// IgnParamTemplateEnable POST /api/v1/ign/param-template/:id/enable (FUNC-42)
func IgnParamTemplateEnable(c *gin.Context) {
	body := common.BindJSON(c)
	row, err := service.SetIgnParamTemplateState(common.PathID(c, "id"), "enable", common.Trim(body["reason"]), common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "模板已启用（enabledAt 首次回填；第 3 层只认启用态，R-20）")
}

// IgnParamTemplateDisable POST /api/v1/ign/param-template/:id/disable (FUNC-42)
func IgnParamTemplateDisable(c *gin.Context) {
	body := common.BindJSON(c)
	row, err := service.SetIgnParamTemplateState(common.PathID(c, "id"), "disable", common.Trim(body["reason"]), common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "模板已停用（停用只表示「不再被新集群选中」，已生效的取值不作废、覆写行不回收，R-25）")
}

// IgnParamTemplateSetDefault POST /api/v1/ign/param-template/:id/default (FUNC-43)
func IgnParamTemplateSetDefault(c *gin.Context) {
	body := common.BindJSON(c)
	row, err := service.SetIgnParamTemplateDefault(common.PathID(c, "id"), common.Trim(body["reason"]), common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "已设为默认（同面原默认行自动取消标记；默认只作用于上游选择器缺省值，不参与渲染兜底，R-20/INV-43）")
}

// IgnParamTemplateDelete DELETE /api/v1/ign/param-template/:id (FUNC-44)
func IgnParamTemplateDelete(c *gin.Context) {
	body := common.BindJSON(c)
	row, err := service.DeleteIgnParamTemplate(common.PathID(c, "id"), common.Trim(body["reason"]), common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "模板已软删除（字段行随聚合级联，名称随删除释放；软删除是标记不是第四个状态值）")
}

// ---------- 集群交付配置（FUNC-45~49；AGG-IGN-08「集群交付配置」屏） ----------

// IgnClusterRawView GET /api/v1/ign/cluster/raw?cluster_ref=&file_face= (FUNC-45;REP-18)
// 独立权限码、读也留痕：每次访问在服务层写一条审计。
func IgnClusterRawView(c *gin.Context) {
	out, err := service.GetIgnClusterRawFile(
		strings.TrimSpace(c.Query("cluster_ref")),
		strings.ToUpper(strings.TrimSpace(c.Query("file_face"))),
		common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, out, "")
}

// IgnClusterConfigView GET /api/v1/ign/cluster/config?cluster_ref=&file_face= (FUNC-46;REP-17)
func IgnClusterConfigView(c *gin.Context) {
	out, err := service.GetIgnClusterConfigView(
		c.Query("cluster_ref"), c.Query("file_face"), map[string]any{
			"param_template_ref": c.Query("param_template_ref"),
			"version_ref":        c.Query("version_ref"),
		}, common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, out, "")
}

// IgnClusterConfigOverrideSave PUT /api/v1/ign/cluster/config/override (FUNC-47)
func IgnClusterConfigOverrideSave(c *gin.Context) {
	body := common.BindJSON(c)
	out, err := service.SaveIgnClusterConfigOverride(body, common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, out, "覆写已保存（只改「下次将生效」列；不重写已落盘文件、不触发派发，R-21）")
}

// IgnClusterConfigValidate POST /api/v1/ign/cluster/config/validate (FUNC-48 dry-run)
func IgnClusterConfigValidate(c *gin.Context) {
	body := common.BindJSON(c)
	out, err := service.ValidateIgnClusterConfig(body, common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, out, "预检完成（不写盘、不建任务、不占额度、不改行、不留痕；结论与受理一致，R-23）")
}

// IgnClusterConfigRollback POST /api/v1/ign/cluster/config/rollback (FUNC-49)
func IgnClusterConfigRollback(c *gin.Context) {
	body := common.BindJSON(c)
	out, err := service.RollbackIgnClusterConfigOverride(body, common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, out, "覆写已回落（追溯对同步清空，INV-42；行保留以可查曾改过什么）")
}

// IgnFacadeDeliveryConfig GET /api/v1/ign/delivery/config/:clusterRef (FUNC-50 跨中心只读通道)。
// 挂在门面组（middleware.FacadeRequired）：Gin 会在 LoginRequired 组内把 Bearer 当会话 JWT
// 先消费掉，服务令牌在登录组内永远解析不到（routes.go 顶部注释同口径）。flat JSON 回吐。
func IgnFacadeDeliveryConfig(c *gin.Context) {
	out, err := service.ReadIgnDeliveryConfig(
		c.Param("clusterRef"),
		c.Query("file_face"),
		c.Query("param_template_ref"),
		c.Query("version_ref"))
	if err != nil {
		facadeFail(c, err)
		return
	}
	c.JSON(200, out)
}

// IgnFacadeDeliveryConfigRaw GET /api/v1/ign/delivery/config/:clusterRef/raw?file_face= (FUNC-50 原文形态)。
// 与 FUNC-45 人工视图同一份读盘 / 同一套掩码 / 同一逐次审计（服务层共用内核），审计与回吐带来源标注；
// 不新增权限码——身份仍是下游中心服务身份，与 FUNC-34/35/50 同一档。flat JSON 回吐，失败走 facadeFail。
func IgnFacadeDeliveryConfigRaw(c *gin.Context) {
	out, err := service.ReadIgnDeliveryConfigRaw(c.Param("clusterRef"), c.Query("file_face"), common.UserOf(c))
	if err != nil {
		facadeFail(c, err)
		return
	}
	c.JSON(200, out)
}
