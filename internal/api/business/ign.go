// Package business —— O-IGN 点火中心控制器(全部走 OPIC 信封 OKJSON/FailJSON;错误码用 pkg/errcode 既有码)。
//
// 路由前缀 /api/v1/ign(OPIC-ARCH API 前缀约定 /api/v1/{capability}/...);
// 权限码 ign:FUNC-xx 与 M5 PERM-FUNC-xx 一一对应(路由层 RequirePermission 挂载)。
package business

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"gitcode.com/opic-ontology/opic-techbase/internal/api/common"
	"gitcode.com/opic-ontology/opic-techbase/internal/pkg/ontology"
	"gitcode.com/opic-ontology/opic-techbase/internal/service"
	"gitcode.com/opic-ontology/opic-techbase/pkg/errcode"
)

// ---------- 字典(模型驱动) ----------

// IgnDictionaries GET /api/v1/ign/dictionaries —— M1 全量字典(前端下拉/状态映射数据源)。
func IgnDictionaries(c *gin.Context) {
	reg := ontology.Load()
	c.JSON(200, errcode.OK(map[string]any{
		"IGN_VERSION_STATUS":   common.OrEmpty(reg.GetDictionaryItems("DICT-IGN_VERSION_STATUS", "IGN_VERSION_STATUS")),
		"IGN_EXECUTOR_STATUS":  common.OrEmpty(reg.GetDictionaryItems("DICT-IGN_EXECUTOR_STATUS", "IGN_EXECUTOR_STATUS")),
		"IGN_CRED_STATUS":      common.OrEmpty(reg.GetDictionaryItems("DICT-IGN_CRED_STATUS", "IGN_CRED_STATUS")),
		"IGN_TLS_BIND_STATUS":  common.OrEmpty(reg.GetDictionaryItems("DICT-IGN_TLS_BIND_STATUS", "IGN_TLS_BIND_STATUS")),
		"IGN_TASK_STATUS":      common.OrEmpty(reg.GetDictionaryItems("DICT-IGN_TASK_STATUS", "IGN_TASK_STATUS")),
		"IGN_PREPUSH_STATUS":   common.OrEmpty(reg.GetDictionaryItems("DICT-IGN_PREPUSH_STATUS", "IGN_PREPUSH_STATUS")),
		"IGN_TASK_FAIL_REASON": common.OrEmpty(reg.GetDictionaryItems("DICT-IGN_TASK_FAIL_REASON", "IGN_TASK_FAIL_REASON")),
		"IGN_EXECUTOR_TYPE":    common.OrEmpty(reg.GetDictionaryItems("DICT-IGN_EXECUTOR_TYPE", "IGN_EXECUTOR_TYPE")),
		"IGN_CERT_ROLE":        common.OrEmpty(reg.GetDictionaryItems("DICT-IGN_CERT_ROLE", "IGN_CERT_ROLE")),
		"IGN_CNI_PLUGIN":       common.OrEmpty(reg.GetDictionaryItems("DICT-IGN_CNI_PLUGIN", "IGN_CNI_PLUGIN")),
		"IGN_CAPABILITY":       common.OrEmpty(reg.GetDictionaryItems("DICT-IGN_CAPABILITY", "IGN_CAPABILITY")),
		"IGN_ACTION":           common.OrEmpty(reg.GetDictionaryItems("DICT-IGN_ACTION", "IGN_ACTION")),
		// v1.1：安装步骤参数域（INV-36）与结论依据（INV-37）——界面下拉/展示的唯一取值域来源
		"IGN_INSTALL_STEP":   common.OrEmpty(reg.GetDictionaryItems("DICT-IGN_INSTALL_STEP", "IGN_INSTALL_STEP")),
		"IGN_VERDICT_SOURCE": common.OrEmpty(reg.GetDictionaryItems("DICT-IGN_VERDICT_SOURCE", "IGN_VERDICT_SOURCE")),
		// v1.3：参数面模板与集群覆写族的四个枚举 + 值形态（界面下拉合法域的唯一来源，前端不得再抄一份）
		"IGN_CONFIG_FILE_FACE":     common.OrEmpty(reg.GetDictionaryItems("DICT-IGN_CONFIG_FILE_FACE", "IGN_CONFIG_FILE_FACE")),
		"IGN_VALUE_SHAPE":          common.OrEmpty(reg.GetDictionaryItems("DICT-IGN_VALUE_SHAPE", "IGN_VALUE_SHAPE")),
		"IGN_PARAM_TEMPLATE_STATE": common.OrEmpty(reg.GetDictionaryItems("DICT-IGN_PARAM_TEMPLATE_STATE", "IGN_PARAM_TEMPLATE_STATE")),
		"IGN_PARAM_SOURCE":         common.OrEmpty(reg.GetDictionaryItems("DICT-IGN_PARAM_SOURCE", "IGN_PARAM_SOURCE")),
		"IGN_OVERRIDE_STATE":       common.OrEmpty(reg.GetDictionaryItems("DICT-IGN_OVERRIDE_STATE", "IGN_OVERRIDE_STATE")),
		"checkpoint_note":          metaIgnCheckpointNote(),
	}))
}

func metaIgnCheckpointNote() string {
	return "保存即校验:INV-01/02/03/06/08/11/13/14/16/18/19/20/24/27/28;仅提交时强制校验:INV-04/05/07/09/10/12/15/17/21/23/25/26/29/30/31/32/33"
}

// ---------- 交付版本(FUNC-01~08;AGG-IGN-01) ----------

// IgnVersionList GET /api/v1/ign/version (FUNC-06;REP-01)
func IgnVersionList(c *gin.Context) {
	page, size := common.PageOf(c)
	p, err := service.ListVersions(page, size, map[string]any{
		"status":               c.Query("status"),
		"orchestrator_version": c.Query("orchestrator_version"),
		"cni_plugin":           c.Query("cni_plugin"),
		"keyword":              c.Query("keyword"),
	})
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, p, "")
}

// IgnVersionCreate POST /api/v1/ign/version (FUNC-01)
func IgnVersionCreate(c *gin.Context) {
	body := common.BindJSON(c)
	row, err := service.CreateVersion(body, common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "版本已登记，状态置草稿")
}

// IgnVersionGet GET /api/v1/ign/version/:id (FUNC-07;REP-02)
func IgnVersionGet(c *gin.Context) {
	row, err := service.GetVersion(common.PathID(c, "id"))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "")
}

// IgnVersionUpdate PUT /api/v1/ign/version/:id (FUNC-02)
func IgnVersionUpdate(c *gin.Context) {
	body := common.BindJSON(c)
	row, err := service.UpdateVersion(common.PathID(c, "id"), body, common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "版本登记信息已修改（组件版本组契约字段不可改,INV-04）")
}

// IgnVersionDownload POST /api/v1/ign/version/:id/download (FUNC-03)
func IgnVersionDownload(c *gin.Context) {
	row, err := service.DownloadVersion(common.PathID(c, "id"), common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "下载已触发（R-02:摘要提供即强校验;校验通过才写缓存路径并置就绪）")
}

// IgnVersionArchive POST /api/v1/ign/version/:id/archive (FUNC-04)
func IgnVersionArchive(c *gin.Context) {
	row, err := service.ArchiveVersion(common.PathID(c, "id"), common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "版本已归档（登记字段冻结,INV-05）")
}

// IgnVersionDelete DELETE /api/v1/ign/version/:id (FUNC-05)
func IgnVersionDelete(c *gin.Context) {
	row, err := service.DeleteVersion(common.PathID(c, "id"), common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "版本已软删除（R-01:已提示仓库侧镜像清理清单）")
}

// IgnPublicVersions GET /api/v1/ign/version/public (FUNC-08;REP-03 跨中心版本视图)
func IgnPublicVersions(c *gin.Context) {
	page, size := common.PageOf(c)
	p, err := service.ListPublicVersions(page, size)
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, p, "")
}

// ---------- 镜像预推(FUNC-09~12;AGG-IGN-02) ----------

// IgnPrepushStart POST /api/v1/ign/version/:id/prepush (FUNC-09)
func IgnPrepushStart(c *gin.Context) {
	row, err := service.StartPrepush(common.PathID(c, "id"), common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "镜像预推已触发（INV-08:预推明细由清单派生;REF-04 并发上限热生效）")
}

// IgnManifestOverwrite PUT /api/v1/ign/version/:id/manifest (FUNC-10)
func IgnManifestOverwrite(c *gin.Context) {
	body := common.BindJSON(c)
	row, err := service.OverwriteManifest(common.PathID(c, "id"), common.Trim(body["image_manifest"]), common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "镜像清单已覆写（预推明细同步对齐,INV-08）")
}

// IgnPrepushRetry POST /api/v1/ign/version/:id/prepush/retry (FUNC-11)
func IgnPrepushRetry(c *gin.Context) {
	row, err := service.RetryPrepush(common.PathID(c, "id"), common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "断点续推已触发（只补失败明细）")
}

// IgnPrepushList GET /api/v1/ign/prepush (FUNC-12;REP-04)
func IgnPrepushList(c *gin.Context) {
	page, size := common.PageOf(c)
	p, err := service.ListPrepushItems(page, size, map[string]any{
		"version_id":     c.Query("version_id"),
		"prepush_status": c.Query("prepush_status"),
		"version_no":     c.Query("version_no"),
	})
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, p, "")
}

// ---------- 执行机(FUNC-13~19;AGG-IGN-03) ----------

// IgnExecutorList GET /api/v1/ign/executor (FUNC-17;REP-05)
func IgnExecutorList(c *gin.Context) {
	page, size := common.PageOf(c)
	p, err := service.ListExecutors(page, size, map[string]any{
		"status":        c.Query("status"),
		"executor_type": c.Query("executor_type"),
		"capability":    c.Query("capability"),
		"keyword":       c.Query("keyword"),
	})
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, p, "")
}

// IgnExecutorCreate POST /api/v1/ign/executor (FUNC-13)
func IgnExecutorCreate(c *gin.Context) {
	body := common.BindJSON(c)
	row, err := service.RegisterExecutor(body, common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "执行机已注册（引导令牌明文仅此一次展示,INV-22）")
}

// IgnExecutorGet GET /api/v1/ign/executor/:id (FUNC-18;REP-06)
func IgnExecutorGet(c *gin.Context) {
	row, err := service.GetExecutor(common.PathID(c, "id"))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "")
}

// IgnExecutorToken POST /api/v1/ign/executor/:id/token (FUNC-14)
func IgnExecutorToken(c *gin.Context) {
	row, err := service.RegenerateToken(common.PathID(c, "id"), common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "引导令牌已重新生成（旧凭据置吊销;锁定已解锁,INV-19/R-04）")
}

// IgnExecutorRetire DELETE /api/v1/ign/executor/:id (FUNC-15)
func IgnExecutorRetire(c *gin.Context) {
	row, err := service.RetireExecutor(common.PathID(c, "id"), common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "执行机已注销（R-07 拆除链完成）")
}

// IgnExecutorCaps PUT /api/v1/ign/executor/:id/capabilities (FUNC-16)
func IgnExecutorCaps(c *gin.Context) {
	body := common.BindJSON(c)
	row, err := service.UpdateCapabilities(common.PathID(c, "id"), body, common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "能力标签已调整（INV-18 字典校验通过）")
}

// IgnPublicExecutors GET /api/v1/ign/executor/public (FUNC-19;REP-07 跨中心执行机视图)
func IgnPublicExecutors(c *gin.Context) {
	page, size := common.PageOf(c)
	p, err := service.ListPublicExecutors(page, size)
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, p, "")
}

// ---------- 代理侧端点(IGN-ROLE-05 执行机代理;登录即可,服务身份) ----------

// IgnAgentBootstrap POST /api/v1/ign/agent/bootstrap —— 引导回连(令牌校验/双绑定/置就绪,R-03/R-04)
func IgnAgentBootstrap(c *gin.Context) {
	body := common.BindJSON(c)
	row, err := service.IgnAgentBootstrap(common.Trim(body["executor_no"]), common.Trim(body["token"]), common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "引导完成（R-03:双绑定 ACTIVE + 凭据 CONSUMED ⇒ 就绪）")
}

// IgnAgentHeartbeat POST /api/v1/ign/agent/heartbeat —— 心跳回执(REF-03/R-06)
func IgnAgentHeartbeat(c *gin.Context) {
	body := common.BindJSON(c)
	// JSON 数字经 BindJSON 解为 float64;字符串/数值双兼容
	reported := int64(-1)
	switch x := body["current_tasks"].(type) {
	case float64:
		reported = int64(x)
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64); err == nil {
			reported = n
		}
	case int64:
		reported = x
	}
	row, err := service.IgnExecutorHeartbeat(common.Trim(body["executor_no"]), reported, common.Trim(body["payload"]), common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "")
}

// IgnTaskComplete POST /api/v1/ign/task/:taskNo/complete —— 任务回执(计数递减/终态留痕,R-05/R-09)
func IgnTaskComplete(c *gin.Context) {
	body := common.BindJSON(c)
	row, err := service.CompleteTask(c.Param("taskNo"), body, common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "任务回执已登记")
}

// ---------- 通道证书(FUNC-20~22;AGG-IGN-05) ----------

// IgnTlsList GET /api/v1/ign/tls (FUNC-22;REP-08)
func IgnTlsList(c *gin.Context) {
	page, size := common.PageOf(c)
	p, err := service.ListTlsBindings(page, size, map[string]any{
		"executor_id": c.Query("executor_id"),
		"cert_role":   c.Query("cert_role"),
		"bind_status": c.Query("bind_status"),
		"expiring":    c.Query("expiring"),
		"keyword":     c.Query("keyword"),
	})
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, p, "")
}

// IgnTlsRenew POST /api/v1/ign/tls/:id/renew (FUNC-20)
func IgnTlsRenew(c *gin.Context) {
	row, err := service.RenewTlsBinding(common.PathID(c, "id"), common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "续期已执行（续期计数 +1,INV-26）")
}

// IgnTlsDecline POST /api/v1/ign/executor/:id/tls-decline (FUNC-21)
func IgnTlsDecline(c *gin.Context) {
	row, err := service.DeclineTls(common.PathID(c, "id"), common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	if b, ok := row["tls_declined"].(bool); ok && b {
		common.OKJSON(c, row, "TLS 已显式禁用（R-11:声明 + 审计 + 界面红标;再次调用撤销）")
		return
	}
	common.OKJSON(c, row, "TLS 降级声明已撤销")
}

// ---------- 执行任务(FUNC-23~28;AGG-IGN-06) ----------

// IgnTaskSubmit POST /api/v1/ign/task (FUNC-23)
func IgnTaskSubmit(c *gin.Context) {
	body := common.BindJSON(c)
	row, err := service.SubmitTask(body, common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "任务已提交（R-05 计数已递增；本机执行位异步派发 B-AUTO-11，同集群有在途任务则排队 R-17）")
}

// IgnTaskCancel POST /api/v1/ign/task/:taskNo/cancel (FUNC-24)
func IgnTaskCancel(c *gin.Context) {
	row, err := service.CancelTask(c.Param("taskNo"), common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	note, _ := row["cancel_note"].(string)
	if note == "" {
		note = "撤销请求已按幂等处理"
	}
	common.OKJSON(c, row, note)
}

// IgnTaskLogs GET /api/v1/ign/task/:taskNo/logs (FUNC-27;REP-11 流式明细)
// p1=任务编号、p2=游标起点（增量续传，缺省最近 500 行）；分页 500/2000 由服务层按模型收口。
func IgnTaskLogs(c *gin.Context) {
	sinceSeq, _ := strconv.ParseInt(c.Query("since_seq"), 10, 64)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "500"))
	data, err := service.TaskLogs(c.Param("taskNo"), sinceSeq, page, size)
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, data, "")
}

// IgnTaskList GET /api/v1/ign/task (FUNC-25;REP-09)
func IgnTaskList(c *gin.Context) {
	page, size := common.PageOf(c)
	p, err := service.ListExecTasks(page, size, map[string]any{
		"executor_id": c.Query("executor_id"),
		"action":      c.Query("action"),
		"cluster_ref": c.Query("cluster_ref"),
		"task_status": c.Query("task_status"),
		"keyword":     c.Query("keyword"),
	})
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, p, "")
}

// IgnTaskGet GET /api/v1/ign/task/:taskNo (FUNC-26;REP-10)
func IgnTaskGet(c *gin.Context) {
	row, err := service.GetTask(c.Param("taskNo"))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "")
}

// IgnReconcileList GET /api/v1/ign/reconcile (FUNC-28;REP-12)
func IgnReconcileList(c *gin.Context) {
	page, size := common.PageOf(c)
	p, err := service.ListReconcileLogs(page, size, map[string]any{
		"event_type": c.Query("event_type"),
		"keyword":    c.Query("keyword"),
	})
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, p, "")
}

// ---------- 强制注销审批(FUNC-31~33) ----------

// IgnForceRemoveSave POST /api/v1/ign/executor/:id/force-remove (FUNC-31;mode=draft|submit)
func IgnForceRemoveSave(c *gin.Context) {
	body := common.BindJSON(c)
	mode := common.Trim(body["mode"])
	if mode == "" {
		mode = "submit"
	}
	row, err := service.SaveForceRemove(mode, common.PathID(c, "id"), common.Trim(body["reason"]), common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "")
}

// IgnForceRemoveSubmit POST /api/v1/ign/force-remove/:id/submit (FUNC-31 提交审批)
func IgnForceRemoveSubmit(c *gin.Context) {
	row, err := service.SubmitForceRemoveApproval(common.PathID(c, "id"), common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "已提交审批（FLOW-PROC-APP-01）")
}

// IgnForceRemoveWithdraw POST /api/v1/ign/force-remove/:id/withdraw (FUNC-31 撤回)
func IgnForceRemoveWithdraw(c *gin.Context) {
	row, err := service.WithdrawForceRemove(common.PathID(c, "id"), common.UserOf(c))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "已撤回（无写入）")
}

// IgnForceRemoveList GET /api/v1/ign/force-remove (审批弹窗与台账)
func IgnForceRemoveList(c *gin.Context) {
	page, size := common.PageOf(c)
	p, err := service.ListForceRemoves(page, size, map[string]any{
		"status":      c.Query("status"),
		"executor_id": c.Query("executor_id"),
	})
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, p, "")
}

// ---------- 运行观测(FUNC-29/30) ----------

// IgnStats GET /api/v1/ign/stats (FUNC-29;REP-13)
func IgnStats(c *gin.Context) {
	row, err := service.IgnStats()
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, row, "")
}

// IgnParamsGet GET /api/v1/ign/params (FUNC-30 读)
func IgnParamsGet(c *gin.Context) {
	rows, err := service.GetIgnParams()
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, rows, "")
}

// IgnParamsSave PUT /api/v1/ign/params (FUNC-30 写)
func IgnParamsSave(c *gin.Context) {
	body := common.BindJSON(c)
	items, _ := body["items"].([]any)
	mapped := []map[string]any{}
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			mapped = append(mapped, m)
		}
	}
	if err := service.SaveIgnParams(mapped, common.UserOf(c)); err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, gin.H{"saved": len(mapped)}, "运行参数已保存（REF-* 缺省口径可覆盖,热生效）")
}
