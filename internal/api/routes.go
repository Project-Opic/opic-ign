// Package api —— Gin 路由注册(gopherforge internal/api 模式:公开组 + 登录组 + 权限中间件按路由挂载)。
//
// 点火中心(ignition)fork 改造:底座客户申请示例路由(/api/customer)移除;
// 业务路由按 OPIC-ARCH 前缀约定挂 /api/v1/ign/*,权限码 ign:FUNC-xx 对齐 M5 PERM-FUNC-xx。
// 注意:gin 重复注册同一路径会 panic——静态段(/version/public)与参数段(/version/:id)已核对无冲突。
package api

import (
	"github.com/gin-gonic/gin"

	"gitcode.com/opic-ontology/opic-techbase/internal/api/adminconsole"
	"gitcode.com/opic-ontology/opic-techbase/internal/api/auth"
	"gitcode.com/opic-ontology/opic-techbase/internal/api/business"
	"gitcode.com/opic-ontology/opic-techbase/internal/api/flow"
	"gitcode.com/opic-ontology/opic-techbase/internal/api/meta"
	"gitcode.com/opic-ontology/opic-techbase/internal/api/system"
	"gitcode.com/opic-ontology/opic-techbase/internal/middleware"
)

// perm 权限码 + 处理器(路由层挂载 ign:/system:/flow: 权限码)。
func perm(code string, handler gin.HandlerFunc) []gin.HandlerFunc {
	return []gin.HandlerFunc{middleware.RequirePermission(code), handler}
}

// audited 同 perm,并追加操作审计(用户/角色/权限/资源及其绑定等授予面写操作)。
func audited(code string, handler gin.HandlerFunc) []gin.HandlerFunc {
	return []gin.HandlerFunc{middleware.RequirePermission(code), middleware.OperationAudit(), handler}
}

// SetupRoutes 挂载 /api 路由。
func SetupRoutes(r *gin.Engine) {
	api := r.Group("/api")

	// ---- 公开路由(认证入口) ----
	pub := api.Group("")
	{
		pub.POST("/auth/login", auth.Login)
		pub.GET("/auth/mode", auth.AuthMode)
		pub.GET("/auth/captcha", auth.Captcha)
		pub.GET("/auth/login-url", auth.ZitadelLoginURL)
		pub.GET("/auth/callback", auth.ZitadelCallback)
		// 用户工作台 SSO（OPIC-SSO-01：Casdoor）
		pub.GET("/auth/casdoor/login-url", auth.CasdoorLoginURL)
		pub.GET("/auth/casdoor/callback", auth.CasdoorCallback)
	}

	// ---- 交付门面通道(/api/v1/ign/delivery,FUNC-34~36) ----
	// 必须挂在 LoginRequired 组**之外**:Gin 会把 Authorization: Bearer 当会话 JWT 先消费掉,
	// 上游服务令牌就永远解析不到。鉴权由 FacadeRequired(服务身份)+ RequirePermission
	// (ign:FUNC-34~36,本域执行权限)两层构成 —— 需求 3.2.1「双重鉴权」的两条腿缺一不可。
	facade := api.Group("/v1/ign/delivery", middleware.FacadeRequired())
	{
		facade.POST("/submit", perm("ign:FUNC-34", business.IgnFacadeSubmit)...)
		facade.GET("/receipt/:ref", perm("ign:FUNC-35", business.IgnFacadeReceipt)...)
		facade.POST("/cancel/:ref", perm("ign:FUNC-36", business.IgnFacadeCancel)...)
		// FUNC-50 跨中心交付配置只读通道：调用方同为 ignition 服务令牌（集群中心代理读），
		// 必须与门面三接口同组——挂进 authed 的 ign 组会让服务令牌解析不到（本文件顶部注释）。
		facade.GET("/config/:clusterRef", perm("ign:FUNC-50", business.IgnFacadeDeliveryConfig)...)
		// FUNC-50 原文形态（hosts / config.yml 逐字只读）：同一套掩码 + 逐次审计，来源标为上游代理读；
		// 不新增权限码——身份与取值通道同一档（需求 FUNC-50「字段级视图 / 四层来源 / 覆写台账 / 原文」的原文腿）。
		facade.GET("/config/:clusterRef/raw", perm("ign:FUNC-50", business.IgnFacadeDeliveryConfigRaw)...)
		// FUNC-51 跨中心交付执行日志只读通道（ezctl/ansible 原文腿，集群中心向导日志代理直展）：
		// 独立发牌 ign:FUNC-51，不与回执 ign:FUNC-35 合并——回执可见不等于原文可见。
		facade.GET("/log/:ref", perm("ign:FUNC-51", business.IgnFacadeDeliveryLog)...)
	}

	// ---- 登录路由组 ----
	authed := api.Group("", middleware.LoginRequired())
	{
		// 认证
		authed.POST("/auth/logout", auth.Logout)
		authed.GET("/auth/info", auth.Info)

		// 元数据(登录即可)
		authed.GET("/meta/dictionaries", meta.Dictionaries)
		authed.GET("/meta/ign-status", meta.IgnStatus)
		authed.POST("/assistant/chat", business.AssistantChat)
		authed.POST("/assistant/query", business.AssistantQuery) // AI 只读 SQL 工具(白名单+审计)
		authed.GET("/assistant/models", business.AssistantModels)
		authed.GET("/meta/rules", meta.Rules)
		// 参数面字段目录(登录即可;面定义的只读投影,界面下拉合法域的唯一来源)
		authed.GET("/meta/ign-param-face", meta.ParamFace)

		// 审批中心(登录即可;归属校验在服务层)
		authed.GET("/workbench/todo", business.WorkbenchTodo)
		authed.GET("/workbench/done", business.WorkbenchDone)
		authed.GET("/workbench/requested", business.WorkbenchRequested)
		authed.POST("/workbench/todo/:id/approve", business.WorkbenchApprove)
		authed.POST("/workbench/todo/:id/reject", business.WorkbenchReject)
		authed.POST("/workbench/todo/:id/return", business.WorkbenchReturn)

		// 流程查询(登录即可)
		authed.GET("/flow/definitions", flow.DefinitionList)
		authed.GET("/flow/definitions/:id", flow.DefinitionGet)
		authed.GET("/flow/definitions/:id/graph", flow.DefinitionGraph)
		authed.GET("/flow/instances", flow.InstanceList)
		authed.GET("/flow/instances/:id", flow.InstanceGet)
		authed.GET("/flow/tasks", flow.TaskList)

		// ---- 点火中心业务路由(/api/v1/ign,权限码挂载) ----
		ign := authed.Group("/v1/ign")
		{
			// 字典(登录即可)
			ign.GET("/dictionaries", business.IgnDictionaries)

			// 交付版本(FUNC-01~08)
			ign.GET("/version/public", perm("ign:FUNC-08", business.IgnPublicVersions)...) // 跨中心版本视图(执行机代理可查)
			ign.GET("/version", perm("ign:FUNC-06", business.IgnVersionList)...)
			ign.POST("/version", perm("ign:FUNC-01", business.IgnVersionCreate)...)
			ign.GET("/version/:id", perm("ign:FUNC-07", business.IgnVersionGet)...)
			ign.PUT("/version/:id", perm("ign:FUNC-02", business.IgnVersionUpdate)...)
			ign.POST("/version/:id/download", perm("ign:FUNC-03", business.IgnVersionDownload)...)
			ign.POST("/version/:id/archive", perm("ign:FUNC-04", business.IgnVersionArchive)...)
			ign.DELETE("/version/:id", perm("ign:FUNC-05", business.IgnVersionDelete)...)
			ign.POST("/version/:id/prepush", perm("ign:FUNC-09", business.IgnPrepushStart)...)
			ign.POST("/version/:id/prepush/retry", perm("ign:FUNC-11", business.IgnPrepushRetry)...)
			ign.PUT("/version/:id/manifest", perm("ign:FUNC-10", business.IgnManifestOverwrite)...)

			// 镜像预推明细(FUNC-12)
			ign.GET("/prepush", perm("ign:FUNC-12", business.IgnPrepushList)...)

			// 执行机(FUNC-13~19;31 走 force-remove)
			ign.GET("/executor/public", perm("ign:FUNC-19", business.IgnPublicExecutors)...) // 跨中心执行机视图(执行机代理可查)
			ign.GET("/executor", perm("ign:FUNC-17", business.IgnExecutorList)...)
			ign.POST("/executor", perm("ign:FUNC-13", business.IgnExecutorCreate)...)
			ign.GET("/executor/:id", perm("ign:FUNC-18", business.IgnExecutorGet)...)
			ign.POST("/executor/:id/token", perm("ign:FUNC-14", business.IgnExecutorToken)...)
			ign.DELETE("/executor/:id", perm("ign:FUNC-15", business.IgnExecutorRetire)...)
			ign.PUT("/executor/:id/capabilities", perm("ign:FUNC-16", business.IgnExecutorCaps)...)
			ign.POST("/executor/:id/tls-decline", perm("ign:FUNC-21", business.IgnTlsDecline)...)
			ign.POST("/executor/:id/force-remove", perm("ign:FUNC-31", business.IgnForceRemoveSave)...)

			// 强制注销申请(提交/撤回 FUNC-31;列表登录即可,审批动作走 workbench)
			ign.GET("/force-remove", business.IgnForceRemoveList)
			ign.POST("/force-remove/:id/submit", perm("ign:FUNC-31", business.IgnForceRemoveSubmit)...)
			ign.POST("/force-remove/:id/withdraw", perm("ign:FUNC-31", business.IgnForceRemoveWithdraw)...)

			// 通道证书(FUNC-20/22)
			ign.GET("/tls", perm("ign:FUNC-22", business.IgnTlsList)...)
			ign.POST("/tls/:id/renew", perm("ign:FUNC-20", business.IgnTlsRenew)...)

			// 执行任务(FUNC-23~28)
			ign.POST("/task", perm("ign:FUNC-23", business.IgnTaskSubmit)...)
			ign.GET("/task", perm("ign:FUNC-25", business.IgnTaskList)...)
			ign.GET("/task/:taskNo", perm("ign:FUNC-26", business.IgnTaskGet)...)
			ign.GET("/task/:taskNo/logs", perm("ign:FUNC-27", business.IgnTaskLogs)...)
			ign.POST("/task/:taskNo/cancel", perm("ign:FUNC-24", business.IgnTaskCancel)...)

			// 对账与纠偏台账(FUNC-28)
			ign.GET("/reconcile", perm("ign:FUNC-28", business.IgnReconcileList)...)

			// 门面受理台账(FUNC-37;REP-14,固定过滤来源任务标识非空)
			ign.GET("/facade/task", perm("ign:FUNC-37", business.IgnFacadeTaskList)...)

			// 运行观测(FUNC-29/30)
			ign.GET("/stats", perm("ign:FUNC-29", business.IgnStats)...)
			ign.GET("/params", perm("ign:FUNC-30", business.IgnParamsGet)...)
			ign.PUT("/params", perm("ign:FUNC-30", business.IgnParamsSave)...)

			// 参数面模板(FUNC-38~44;AGG-IGN-07「参数面模板」屏)
			ign.GET("/param-template", perm("ign:FUNC-38", business.IgnParamTemplateList)...)
			ign.POST("/param-template", perm("ign:FUNC-40", business.IgnParamTemplateCreate)...)
			ign.GET("/param-template/:id", perm("ign:FUNC-39", business.IgnParamTemplateGet)...)
			ign.PUT("/param-template/:id/items", perm("ign:FUNC-41", business.IgnParamTemplateItemsSave)...)
			ign.POST("/param-template/:id/enable", perm("ign:FUNC-42", business.IgnParamTemplateEnable)...)
			ign.POST("/param-template/:id/disable", perm("ign:FUNC-42", business.IgnParamTemplateDisable)...)
			ign.POST("/param-template/:id/default", perm("ign:FUNC-43", business.IgnParamTemplateSetDefault)...)
			ign.DELETE("/param-template/:id", perm("ign:FUNC-44", business.IgnParamTemplateDelete)...)

			// 本域交付集群索引(FUNC-52;REP-19,v1.5 侧栏「集群交付」叶的取数口)
			ign.GET("/cluster/index", perm("ign:FUNC-52", business.IgnClusterIndexList)...)

			// 集群交付配置(FUNC-45~49;AGG-IGN-08「集群交付配置」屏)
			// FUNC-45 原文视图独立权限码(R-26:判据是内容敏感度)——不随 FUNC-46 发牌附带
			ign.GET("/cluster/raw", perm("ign:FUNC-45", business.IgnClusterRawView)...)
			ign.GET("/cluster/config", perm("ign:FUNC-46", business.IgnClusterConfigView)...)
			ign.PUT("/cluster/config/override", perm("ign:FUNC-47", business.IgnClusterConfigOverrideSave)...)
			ign.POST("/cluster/config/validate", perm("ign:FUNC-48", business.IgnClusterConfigValidate)...)
			ign.POST("/cluster/config/rollback", perm("ign:FUNC-49", business.IgnClusterConfigRollback)...)
		}

		// ---- 代理侧端点(IGN-ROLE-05 执行机代理服务身份;登录即可,写审计留痕) ----
		authed.POST("/v1/ign/agent/bootstrap", business.IgnAgentBootstrap)     // 引导回连(R-03/R-04)
		authed.POST("/v1/ign/agent/heartbeat", business.IgnAgentHeartbeat)     // 心跳回执(REF-03/R-06)
		authed.POST("/v1/ign/task/:taskNo/complete", business.IgnTaskComplete) // 任务回执(R-05/R-09)

		// ---- 管理控制台补齐(gopherforge 基准):日志审计/在线用户/公告/错误码/系统监控 ----
		admin := api.Group("/admin", middleware.LoginRequired(), middleware.OperationAudit())
		{
			g := admin.Group("", middleware.RequirePermission("system:manage"))
			adminconsole.RegisterTemporalFlow(g)
			g.GET("/llm-config", adminconsole.LLMConfigGet)
			g.PUT("/llm-config", adminconsole.LLMConfigSave)
			// AI 模型设置:多供应商账号管理
			g.GET("/ai/catalog", adminconsole.AICatalog)
			g.GET("/ai/accounts", adminconsole.AIAccountList)
			g.POST("/ai/accounts", adminconsole.AIAccountCreate)
			g.PUT("/ai/accounts/:id", adminconsole.AIAccountUpdate)
			g.DELETE("/ai/accounts/:id", adminconsole.AIAccountDelete)
			g.POST("/ai/accounts/:id/test", adminconsole.AIAccountTest)
			g.GET("/ai/upstream-models", adminconsole.AIUpstreamModels)
			g.GET("/isolation", adminconsole.IsolationInfo)
			g.GET("/logs/operation", adminconsole.OperationLogs)
			g.GET("/logs/login", adminconsole.LoginLogs)
			g.GET("/logs/audit", adminconsole.AuditLogs)
			g.GET("/online-users", adminconsole.OnlineUsers)
			g.GET("/notice", adminconsole.NoticeList)
			g.POST("/notice", adminconsole.NoticeSave)
			g.PUT("/notice/:id", adminconsole.NoticeSave)
			g.DELETE("/notice/:id", adminconsole.NoticeDelete)
			g.GET("/errcodes", adminconsole.ErrCodes)
			g.GET("/system-stats", adminconsole.SystemStats)
		}

		// 系统查询(登录即可)
		authed.GET("/users", system.UserList)
		authed.GET("/users/options", system.UserOptions)
		authed.GET("/roles", system.RoleList)
		authed.GET("/permissions", system.PermissionList)
		authed.GET("/permissions/all", system.PermissionAll)
		authed.GET("/resources", system.ResourceList)
		authed.GET("/resources/tree", system.ResourceTree)

		// ---- 写操作:按权限码挂载中间件(perm 已在业务路由段定义) ----

		// 用户管理
		authed.POST("/users", audited("system:user:add", system.UserCreate)...)
		authed.PUT("/users/:id", audited("system:user:edit", system.UserUpdate)...)
		authed.DELETE("/users/:id", audited("system:user:delete", system.UserDelete)...)
		authed.PUT("/users/:id/roles", audited("system:user:assign-role", system.UserAssignRoles)...)
		authed.PUT("/users/:id/reset-pwd", audited("system:user:reset-pwd", system.UserResetPwd)...)

		// 角色管理
		authed.POST("/roles", audited("system:role:add", system.RoleCreate)...)
		authed.PUT("/roles/:id", audited("system:role:edit", system.RoleUpdate)...)
		authed.DELETE("/roles/:id", audited("system:role:delete", system.RoleDelete)...)
		authed.PUT("/roles/:id/permissions", audited("system:role:assign", system.RoleAssignPermissions)...)
		authed.PUT("/roles/:id/resources", audited("system:role:assign", system.RoleAssignResources)...)

		// 权限管理
		authed.POST("/permissions", audited("system:permission:add", system.PermissionCreate)...)
		authed.PUT("/permissions/:id", audited("system:permission:edit", system.PermissionUpdate)...)
		authed.DELETE("/permissions/:id", audited("system:permission:delete", system.PermissionDelete)...)

		// 资源管理
		authed.POST("/resources", audited("system:resource:add", system.ResourceCreate)...)
		authed.PUT("/resources/:id", audited("system:resource:edit", system.ResourceUpdate)...)
		authed.DELETE("/resources/:id", audited("system:resource:delete", system.ResourceDelete)...)

		// 流程管理写操作
		authed.POST("/flow/definitions", perm("flow:definition:add", flow.DefinitionCreate)...)
		authed.PUT("/flow/definitions/:id", perm("flow:definition:edit", flow.DefinitionUpdate)...)
		authed.POST("/flow/definitions/:id/publish", perm("flow:definition:publish", flow.DefinitionPublish)...)
		authed.PUT("/flow/instances/:id/terminate", perm("flow:instance:terminate", flow.InstanceTerminate)...)
		authed.PUT("/flow/tasks/:id/transfer", perm("flow:task:transfer", flow.TaskTransfer)...)
		authed.PUT("/flow/tasks/:id/urge", perm("flow:task:urge", flow.TaskUrge)...)
	}
}
