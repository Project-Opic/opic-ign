// AI 智能助理端点(登录即可;只读)。chat 支持选择模型账号/模型/温度;query 为只读 SQL 工具。
package business

import (
	"strings"

	"github.com/gin-gonic/gin"

	"gitcode.com/opic-ontology/opic-techbase/internal/api/common"
	"gitcode.com/opic-ontology/opic-techbase/internal/service"
	"gitcode.com/opic-ontology/opic-techbase/pkg/errcode"
)

type chatReq struct {
	Question    string  `json:"question"`
	AccountID   int64   `json:"account_id"`  // 0=默认账号
	Model       string  `json:"model"`       // 空=账号默认模型
	Temperature float64 `json:"temperature"` // <=0=账号默认
}

// AssistantChat POST /api/assistant/chat
func AssistantChat(c *gin.Context) {
	var body chatReq
	if err := c.ShouldBindJSON(&body); err != nil || strings.TrimSpace(body.Question) == "" {
		c.JSON(200, errcode.Error(errcode.ErrParam, "question 不能为空"))
		return
	}
	uid := service.FromCtx(c.Request.Context()).ID
	reply, err := service.AssistantChat(uid, body.Question, body.AccountID, body.Model, body.Temperature)
	if err != nil {
		c.JSON(200, errcode.Error(errcode.ErrAuthServer, "%s", err.Error()))
		return
	}
	c.JSON(200, errcode.OK(reply))
}

// AssistantModels GET /api/assistant/models —— 模型选择器数据源(启用账号×模型目录)。
func AssistantModels(c *gin.Context) {
	c.JSON(200, errcode.OK(gin.H{"models": service.AssistantModelOptions()}))
}

// AssistantQuery POST /api/assistant/query —— 只读 SQL 工具(仅 SELECT/WITH + ign_* 白名单 + 强制 LIMIT + 审计)
func AssistantQuery(c *gin.Context) {
	body := common.BindJSON(c)
	u := common.UserOf(c)
	out, err := service.AssistantRunReadOnlySQL(u.ID, u.Username, common.Trim(body["sql"]))
	if err != nil {
		common.FailJSON(c, err)
		return
	}
	common.OKJSON(c, out, "")
}
