// cmd/techbase —— opic-techbase v3 组合根。
//
// 装配顺序:配置 → 日志 → Redis(可选) → PostgreSQL(goose 迁移) → 本体注册表 →
// 种子 → metrics → Gin(路由 + /health 系列 + /metrics + 前端静态托管) → 优雅退出。
//
// 注:dbedec0 提交声明「组合根 cmd/techbase/main.go」但树中缺失本文件,导致仓库
// 不可编译——本文件为该缺口的补齐(装配仅调用既有组件,不新增语义)。
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"gitcode.com/opic-ontology/opic-techbase/internal/api"
	"gitcode.com/opic-ontology/opic-techbase/internal/config"
	"gitcode.com/opic-ontology/opic-techbase/internal/infra"
	"gitcode.com/opic-ontology/opic-techbase/internal/middleware"
	"gitcode.com/opic-ontology/opic-techbase/internal/pkg/ontology"
	"gitcode.com/opic-ontology/opic-techbase/internal/service"
	"gitcode.com/opic-ontology/opic-techbase/internal/store"
	"gitcode.com/opic-ontology/opic-techbase/pkg/graceful"
	"gitcode.com/opic-ontology/opic-techbase/pkg/llmcfg"
	"gitcode.com/opic-ontology/opic-techbase/pkg/logger"
	"gitcode.com/opic-ontology/opic-techbase/pkg/metrics"
	"gitcode.com/opic-ontology/opic-techbase/pkg/redisx"
)

func main() {
	cfgPath := flag.String("config", "", "配置文件路径(默认 ./configs/config.yml)")
	flag.Parse()

	config.Init(*cfgPath)
	_ = logger.InitLogger(config.Config.Log.Level, config.Config.Log.Output, config.Config.Log.Dir)

	// Redis(可选):令牌吊销 jti 黑名单;未启用时 token 包静默降级
	if config.Config.Redis.Enabled {
		if err := redisx.Init(true, config.Config.Redis.Host, config.Config.Redis.Port,
			config.Config.Redis.Password, config.Config.Redis.DB); err != nil {
			logger.Warnf("redis 初始化失败,令牌吊销降级为内存态: %v", err)
		}
	}

	// PostgreSQL(含 goose 迁移)
	if err := store.Open(); err != nil {
		logger.Fatalf("[致命] %v", err)
	}

	// AI 模型配置表（管理控制台可配；幂等）
	if err := llmcfg.EnsureTable(); err != nil {
		logger.Warnf("ai_model_config 建表失败: %v", err)
	}
	// AI 供应商账号表（多账号模型设置;幂等）
	if err := llmcfg.EnsureAccountsTable(); err != nil {
		logger.Warnf("ai_provider_account 建表失败: %v", err)
	}

	// 本体注册表 + 种子
	ontology.Load()
	if err := service.EnsureSeed(); err != nil {
		logger.Fatalf("[致命] 种子数据失败: %v", err)
	}

	// 本机执行位启动清扫(B-AUTO-14 / INV-37):重启前在途但进程已不存活的 RUNNING/CANCELLING
	// 任务保守判 LOST(无退出码 + VERDICT_UNRESOLVED),并续派同集群排队任务(R-17)。
	if err := service.IgnRecoverLocalExec(); err != nil {
		logger.Warnf("执行痕迹启动清扫失败(LOST 判定未推进,需人工对账): %v", err)
	}

	// metrics
	metrics.SetEnabled(config.Config.Metrics.Enabled)
	// 接真实 DB 连接池统计(GORM→database/sql Stats;对齐 gopherforge 连接池指标)
	if sqlDB, err := store.DB.DB(); err == nil {
		metrics.SetDBStats(func() (open, idle, inUse, wait, waitMS uint64) {
			s := sqlDB.Stats()
			return uint64(s.OpenConnections), uint64(s.Idle), uint64(s.InUse),
				uint64(s.WaitCount), uint64(s.WaitDuration.Milliseconds())
		})
	}

	if !strings.EqualFold(os.Getenv("APP_ENV"), "development") {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	// 来源地址可信性:限速与验证码来源绑定依赖 ClientIP;未显式声明可信代理时
	// 只信任回环,直接暴露时忽略可伪造的 X-Forwarded-For。
	trustedProxies := []string{"127.0.0.1", "::1"}
	if v := strings.TrimSpace(os.Getenv("TRUSTED_PROXIES")); v != "" {
		trustedProxies = nil
		for _, part := range strings.Split(v, ",") {
			if p := strings.TrimSpace(part); p != "" {
				trustedProxies = append(trustedProxies, p)
			}
		}
	}
	if err := r.SetTrustedProxies(trustedProxies); err != nil {
		logger.Warnf("set trusted proxies failed, source headers ignored: %v", err)
	}
	r.Use(gin.Recovery())
	r.Use(metrics.Middleware()) // HTTP 指标采集(请求计数/延迟直方图;须先于路由注册)
	api.SetupRoutes(r)

	// /health 系列(存活/就绪/检查)
	r.GET("/health", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	r.GET("/health/live", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "live"}) })
	r.GET("/health/ready", func(c *gin.Context) {
		if err := pingDB(); err != nil {
			logger.Warnf("[health] db ping 失败: %v", err)
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not-ready"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})
	r.GET("/health/check", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "time": time.Now().Format(time.RFC3339)})
	})
	// /metrics 仅在显式启用时注册(默认 true);对外暴露面由部署侧路由收敛,见 docker-compose 标签
	if metrics.Enabled() {
		r.GET("/metrics", metrics.Handler())
	}

	addr := ":" + config.Config.App.Port
	logger.Infof("opic-techbase(v3) listening %s", addr)
	srv := &http.Server{Addr: addr, Handler: r}

	sd := graceful.New(15 * time.Second)
	// step1 基础设施客户端（eventbus/workflow；失败降级 nil = 纯 v1 行为）
	infra.Init()
	if infra.TemporalDriver() {
		// temporal worker：承载 flowGuardian 看护工作流（阻塞运行，随 graceful 停止）
		if runner, err := infra.NewTemporalWorker(config.Config.Workflow.Address, config.Config.Workflow.Namespace); err == nil {
			infra.RegisterTemporalFlows(runner)
			wctx, wcancel := context.WithCancel(context.Background())
			go func() { _ = runner.Run(wctx) }()
			sd.Register("workflow-worker", func(ctx context.Context) error { wcancel(); time.Sleep(500 * time.Millisecond); return nil })
		} else {
			logger.Warnf("temporal worker 启动失败，SLA 看护降级为无")
		}
	}

	sd.Register("http", func(ctx context.Context) error { return srv.Shutdown(ctx) })
	sd.Register("redis", func(ctx context.Context) error { redisx.Close(); return nil })
	sd.Register("infra", func(ctx context.Context) error { infra.Close(); return nil })

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatalf("HTTP 服务退出: %v", err)
		}
	}()
	sd.WaitAndShutdown()
	logger.Infof("opic-techbase 已优雅退出")
}

func pingDB() error {
	if store.DB == nil {
		return fmt.Errorf("db not initialized")
	}
	sqlDB, err := store.DB.DB()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return sqlDB.PingContext(ctx)
}

var _ = middleware.LoginRequired // 引用保持(路由组内使用)
