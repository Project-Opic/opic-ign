// Package middleware —— 交付门面通道鉴权（FUNC-34~36 双重鉴权的第二层：服务身份）。
//
// 上游中心（O-CLC）以 Bearer 服务令牌调用门面；本中间件把令牌解析到本域登记的
// **服务账号**（actor_type=SERVICE、绑 IGN-ROLE-05），随后仍走 RequirePermission(ign:FUNC-34~36)
// —— 即需求文档 3.2.1「鉴权：双重鉴权（上游业务权限 + 本域执行权限）」的本域一侧。
// 服务账号落到 ctx 后，submittedBy 与审计留痕均记服务身份（FUNC-34 前置条件）。
package middleware

import (
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"gitcode.com/opic-ontology/opic-techbase/internal/config"
	"gitcode.com/opic-ontology/opic-techbase/internal/service"
	"gitcode.com/opic-ontology/opic-techbase/pkg/errcode"
)

// facadeRefuse 鉴权拒绝也回 flat JSON —— 与 business.facadeFail 同一形态:
// 门面对端按 {return_code, error_msg} 直接反序列化拒绝理由,信封会把理由吞进嵌套字段。
func facadeRefuse(c *gin.Context, status int, code errcode.ErrCode, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"return_code": string(code), "error_msg": msg})
}

// FacadeRequired 门面服务身份鉴权：delivery.facade_token 常量时间比较。
// 令牌未配置 ⇒ 门面整体关闭（401），不存在"无令牌也能受理"的第三条路。
func FacadeRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		want := config.Config.Delivery.FacadeToken
		if want == "" {
			facadeRefuse(c, http.StatusUnauthorized, errcode.ErrUnAuth,
				"交付门面未启用：部署配置 delivery.facade_token 缺失")
			return
		}
		got := bearerToken(c)
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			facadeRefuse(c, http.StatusUnauthorized, errcode.ErrToken,
				"交付门面令牌无效（服务身份不被承认）")
			return
		}
		uid, username := service.FacadeServiceAccount()
		if uid == 0 {
			facadeRefuse(c, http.StatusUnauthorized, errcode.ErrUnAuth,
				"门面服务账号未登记："+config.Config.Delivery.FacadeUser)
			return
		}
		setUser(c, uid, username)
		c.Next()
	}
}

// bearerToken 取 Authorization: Bearer <token>（大小写与多余空白容忍）。
func bearerToken(c *gin.Context) string {
	h := strings.TrimSpace(c.GetHeader("Authorization"))
	const scheme = "bearer"
	if len(h) < len(scheme)+2 || !strings.EqualFold(h[:len(scheme)], scheme) ||
		(h[len(scheme)] != ' ' && h[len(scheme)] != '\t') {
		return ""
	}
	return strings.TrimSpace(h[len(scheme):])
}
