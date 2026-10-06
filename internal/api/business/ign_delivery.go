// ign_delivery.go —— 交付门面三接口(FUNC-34~36)+ 门面受理台账(FUNC-37)。
//
// 与其余业务接口的**唯一**差异是响应形态:门面对端是上游中心的服务身份适配器,
// 它按 flat JSON 直接反序列化(remote_task_ref / status / exit_code / summary),
// 且失败必须回**非 200** —— 否则错误原因会被"HTTP 200 + returnCode"吞掉,
// 上游只能看到空引用(O-CLC 的 INV-46 硬失败)而看不到真实拒绝理由(需求 3.2.1 异常路径可诊断)。
// 台账查询(FUNC-37)是给界面看的,仍走常规信封。
package business

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"gitcode.com/opic-ontology/opic-techbase/internal/api/common"
	"gitcode.com/opic-ontology/opic-techbase/internal/service"
	"gitcode.com/opic-ontology/opic-techbase/pkg/errcode"
)

// facadeFail 业务错误 → HTTP 状态码(语义分列:治理拒绝 403 / 形态非法 400 / 状态与容量 409 / 引用未命中 404)。
func facadeFail(c *gin.Context, err error) {
	be := service.AsBizErr(err)
	status := http.StatusInternalServerError
	switch be.Code {
	case errcode.ErrForbidden:
		status = http.StatusForbidden
	case errcode.ErrParam, errcode.ErrRule:
		status = http.StatusBadRequest
	case errcode.ErrState, errcode.ErrFlowState, errcode.ErrDuplicate:
		status = http.StatusConflict
	case errcode.ErrNotFound:
		status = http.StatusNotFound
	case errcode.ErrConfig, errcode.ErrDB, errcode.ErrAuthServer:
		status = http.StatusInternalServerError
	}
	c.JSON(status, gin.H{
		"return_code": string(be.Code),
		"error_msg":   be.Msg,
	})
}

// IgnFacadeSubmit POST /api/v1/ign/delivery/submit (FUNC-34)。
func IgnFacadeSubmit(c *gin.Context) {
	body := common.BindJSON(c)
	out, err := service.DeliverySubmit(body, common.UserOf(c))
	if err != nil {
		facadeFail(c, err)
		return
	}
	taskNo := common.Trim(out["task_no"])
	resp := gin.H{
		"remote_task_ref":   taskNo, // 上游写回 remote_task_ref(O-CLC 的 INV-46 必填)
		"task_no":           taskNo,
		"status":            common.Trim(out["task_status"]), // IGN_TASK_STATUS 字典码
		"source_task_no":    out["source_task_no"],
		"source_step_index": out["source_step_index"],
		"cluster_ref":       out["cluster_ref"],
		"action":            out["action"],
		"step_code":         out["step_code"],
		"reused":            out["facade_reused"] == true, // INV-35 幂等复用
	}
	if note, _ := out["facade_note"].(string); note != "" {
		resp["note"] = note
	}
	c.JSON(http.StatusOK, resp)
}

// IgnFacadeReceipt GET /api/v1/ign/delivery/receipt/:ref?since_seq= (FUNC-35)。
func IgnFacadeReceipt(c *gin.Context) {
	sinceSeq, _ := strconv.ParseInt(c.Query("since_seq"), 10, 64)
	out, err := service.DeliveryReceipt(c.Param("ref"), sinceSeq)
	if err != nil {
		facadeFail(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// IgnFacadeCancel POST /api/v1/ign/delivery/cancel/:ref (FUNC-36)。
func IgnFacadeCancel(c *gin.Context) {
	out, err := service.DeliveryCancel(c.Param("ref"), common.UserOf(c))
	if err != nil {
		facadeFail(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

// IgnFacadeTaskList GET /api/v1/ign/facade/task (FUNC-37;REP-14 门面受理台账)。
func IgnFacadeTaskList(c *gin.Context) {
	page, size := common.PageOf(c)
	p, err := service.ListFacadeTasks(page, size, map[string]any{
		"source_task_no":    strings.TrimSpace(c.Query("source_task_no")),
		"source_step_index": c.Query("source_step_index"),
		"cluster_ref":       c.Query("cluster_ref"),
		"step_code":         c.Query("step_code"),
		"verdict_source":    c.Query("verdict_source"),
		"task_status":       c.Query("task_status"),
	})
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, p, "")
}
