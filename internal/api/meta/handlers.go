// Package meta —— 元数据控制器(字典/执行机状态清单/M3 规则;数据源为本体注册表)。
//
// 点火中心(ignition)fork 改造:底座客户状态清单(customer-status)已移除,
// 替代为执行机状态清单(ign-status);字典接口改由 /api/v1/ign/dictionaries 提供(11 类枚举)。
package meta

import (
	"strings"

	"github.com/gin-gonic/gin"

	"gitcode.com/opic-ontology/opic-techbase/internal/api/common"
	"gitcode.com/opic-ontology/opic-techbase/internal/pkg/ontology"
	"gitcode.com/opic-ontology/opic-techbase/internal/service"
	"gitcode.com/opic-ontology/opic-techbase/pkg/errcode"
)

// ignExecutorStatuses 执行机治理状态清单(IGN_EXECUTOR_STATUS;状态类字典取值不允许停用,Q-45)。
var ignExecutorStatuses = []string{"ENROLLING", "READY", "LOCKED", "DEGRADED", "RENEWAL_PENDING", "REMOVED"}

// IgnCheckpointNote 模型校验时机说明(保存即校验 vs 仅提交时强制校验;M3 各 INV 描述)。
var IgnCheckpointNote = "保存即校验:INV-01/02/03/06/08/11/13/14/16/18/19/20/24/27/28;仅提交时强制校验:INV-04/05/07/09/10/12/15/17/21/23/25/26/29/30/31/32/33"

// Dictionaries GET /api/meta/dictionaries 字典项(兼容入口;ign 全量字典见 /api/v1/ign/dictionaries)。
func Dictionaries(c *gin.Context) {
	reg := ontology.Load()
	c.JSON(200, errcode.OK(map[string]any{
		"IGN_EXECUTOR_STATUS": common.OrEmpty(reg.GetDictionaryItems("DICT-IGN_EXECUTOR_STATUS", "IGN_EXECUTOR_STATUS")),
		"IGN_TASK_STATUS":     common.OrEmpty(reg.GetDictionaryItems("DICT-IGN_TASK_STATUS", "IGN_TASK_STATUS")),
	}))
}

// IgnStatus GET /api/meta/ign-status 执行机治理状态清单。
func IgnStatus(c *gin.Context) {
	c.JSON(200, errcode.OK(ignExecutorStatuses))
}

// Rules GET /api/meta/rules M3 规则清单。
func Rules(c *gin.Context) {
	reg := ontology.Load()
	var out []map[string]any
	for _, r := range reg.Rules {
		out = append(out, map[string]any{
			"id":           r["id"],
			"name":         orDefaultStr(r["name"], r["id"]),
			"description":  r["description"],
			"expression":   strings.TrimSpace(service.Str(r["expression"])),
			"rule_type":    r["ruleType"],
			"input_params": common.OrEmpty(r["inputParams"]),
		})
	}
	if out == nil {
		out = []map[string]any{}
	}
	c.JSON(200, errcode.OK(out))
}

// ParamFace GET /api/meta/ign-param-face 参数面字段目录（面定义表的只读投影）。
// 与 /meta/dictionaries、/meta/rules 同一档：登录即可、不新建权限码、不建 M2 行为（它是元数据，不是业务读）。
// 出口只有键的形态与归属，没有任何取值（INV-08）——界面因此拿不到"第二份真相"。
func ParamFace(c *gin.Context) {
	c.JSON(200, errcode.OK(service.ParamFaceCatalog()))
}

func orDefaultStr(v any, def any) any {
	if v == nil || service.Str(v) == "" {
		return def
	}
	return v
}
