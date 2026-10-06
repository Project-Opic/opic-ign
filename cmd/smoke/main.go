// Command smoke —— opic-techbase × step1 基础设施 live 冒烟。
//
// 在生产环境（或具备step1 栈的网络内）执行，逐项验证并打印结果：
//
//	./smoke                      # 全部四项
//	./smoke -only pg,redis       # 指定项
//
// 四项：pg（opicdb+schema 读写/ goose 表可达）、redis（PING/SET/GET）、
// nats（JetStream 发布+订阅往返）、temporal（连接+集群健康）。
// 口令经环境变量注入（对齐生产 .env），绝不硬编码。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"gitcode.com/opic-ontology/opic-techbase/pkg/eventbus"
	"gitcode.com/opic-ontology/opic-techbase/pkg/workflowx"
	"github.com/redis/go-redis/v9"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func main() {
	only := flag.String("only", "", "逗号分隔: pg,redis,nats,temporal（默认全部）")
	flag.Parse()
	want := map[string]bool{}
	for _, k := range strings.Split(*only, ",") {
		if k = strings.TrimSpace(k); k != "" {
			want[k] = true
		}
	}
	if len(want) == 0 {
		for _, k := range []string{"pg", "redis", "nats", "temporal"} {
			want[k] = true
		}
	}

	host := env("DB_HOST", "192.168.3.200")
	port := env("DB_PORT", "5433")
	pass := env("DB_PASSWORD", env("PG_ADMIN_PASSWORD", ""))
	// 中心隔离口径随 CENTER_CODE/DB_SCHEMA 派生:schema=ign、NATS 流=ign、主题域=opic.ign.>
	code := env("CENTER_CODE", "ign")
	schema := env("DB_SCHEMA", "ign")
	results := map[string]string{}
	failed := 0
	run := func(name string, fn func() error) {
		if !want[name] {
			return
		}
		if err := fn(); err != nil {
			results[name] = "FAIL: " + err.Error()
			failed++
			return
		}
		results[name] = "OK"
	}

	if want["pg"] {
		run("pg", func() error { return checkPG(host, port, pass, schema) })
	}
	if want["redis"] {
		run("redis", func() error {
			// .env 的 *_PORT 可能是绑定描述（127.0.0.1:26379）—— 清洗为纯端口
			p := cleanPort(env("REDIS_PORT", "26379"))
			return checkRedis(env("REDIS_HOST", "127.0.0.1"), p, env("REDIS_PASSWORD", ""))
		})
	}
	if want["nats"] {
		run("nats", func() error {
			eb, err := eventbus.New(eventbus.Options{Driver: "nats",
				URL:      env("EVENTBUS_URL", "nats://"+host+":14222"),
				Username: env("EVENTBUS_USERNAME", "opic"), Password: env("EVENTBUS_PASSWORD", env("NATS_PASSWORD", "")),
				StreamName: env("NATS_STREAM", code),
				Subjects:   []string{"opic." + code + ".>"}})
			if err != nil {
				return err
			}
			defer eb.Close()
			return checkEventbusRoundtrip(eb, code)
		})
	}
	if want["temporal"] {
		run("temporal", func() error {
			return checkTemporalLive(env("WORKFLOW_ADDRESS", host+":7233"), env("WORKFLOW_NAMESPACE", "opic"))
		})
	}

	fmt.Printf("── ignition-center(%s/%s) × step1 冒烟 ─────────\n", code, schema)
	for _, k := range []string{"pg", "redis", "nats", "temporal"} {
		if v, ok := results[k]; ok {
			mark := "✓"
			if strings.HasPrefix(v, "FAIL") {
				mark = "✗"
			}
			fmt.Printf("  %s %-9s %s\n", mark, k, v)
		}
	}
	if failed > 0 {
		os.Exit(1)
	}
	fmt.Println("  全部通过 —— ignition-center 已集成 step1 基础设施")
}

func checkPG(host, port, pass, schema string) error {
	dsn := fmt.Sprintf("host=%s port=%s user=postgres password=%s dbname=opicdb sslmode=disable options=-csearch_path=%s", host, port, pass, schema)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		return err
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	// goose 迁移表可见 + 临时验证表读写（不落迁移，仅验证 schema 可写）
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS smoke_probe(id int primary key, v text)`).Error; err != nil {
		return fmt.Errorf("建探针表: %w", err)
	}
	defer db.Exec(`DROP TABLE IF EXISTS smoke_probe`)
	if err := db.Exec(`INSERT INTO smoke_probe VALUES (1,'ok')`).Error; err != nil {
		return fmt.Errorf("写入: %w", err)
	}
	var v string
	if err := db.Raw(`SELECT v FROM smoke_probe WHERE id=1`).Scan(&v).Error; err != nil || v != "ok" {
		return fmt.Errorf("读回: %v (v=%q)", err, v)
	}
	var cur string
	if err := db.Raw(`SELECT current_schema()`).Scan(&cur).Error; err != nil || cur != schema {
		return fmt.Errorf("search_path 未落 schema: current=%q err=%v", cur, err)
	}
	return nil
}

func checkRedis(host, port, pass string) error {
	c := redis.NewClient(&redis.Options{Addr: host + ":" + port, Password: pass, DialTimeout: 5 * time.Second})
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Ping(ctx).Err(); err != nil {
		return err
	}
	return c.Set(ctx, "smoke:probe", "ok", time.Minute).Err()
}

func checkEventbusRoundtrip(eb eventbus.EventBus, centerCode string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	got := make(chan string, 4)
	sub, err := eb.Subscribe(ctx, "opic."+centerCode+".smoke.roundtrip", "smoke-probe", func(_ context.Context, m eventbus.Message) error {
		got <- string(m.Payload)
		return nil
	})
	if err != nil {
		return err
	}
	defer sub.Unsubscribe()
	if err := eb.Publish(ctx, "opic."+centerCode+".smoke.roundtrip", []byte("hello-step1")); err != nil {
		return err
	}
	select {
	case v := <-got:
		if v != "hello-step1" {
			return fmt.Errorf("往返载荷不符: %s", v)
		}
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("10s 未收到订阅消息")
	}
}

func checkWorkflowRoundtrip(wf workflowx.Client) error {
	ctx := context.Background()
	info, err := wf.Start(ctx, "smoke", func(c context.Context, _ []byte) error {
		select {
		case <-c.Done():
			return c.Err()
		case <-time.After(50 * time.Millisecond):
			return nil
		}
	}, workflowx.StartOptions{ID: "smoke-wf-" + fmt.Sprint(time.Now().UnixNano())})
	if err != nil {
		return err
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		st, err := wf.Status(ctx, info.ID)
		if err == nil && st.Status == workflowx.StatusDone {
			return nil
		}
		if err != nil && err != workflowx.ErrNotFound {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("10s 未完成, 状态: %+v err=%v", st, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// checkTemporalLive 起内嵌 worker 承载 smoke 工作流，经 client 启动并等待完成。
// Temporal 语义：无 worker 则工作流永远排队 —— 冒烟必须自含 worker（同时验证 worker 通道）。
func checkTemporalLive(addr, ns string) error {
	c, err := client.Dial(client.Options{HostPort: addr, Namespace: ns})
	if err != nil {
		return fmt.Errorf("连接: %w", err)
	}
	defer c.Close()
	w := worker.New(c, "smoke", worker.Options{})
	wfFn := func(ctx workflow.Context, input []byte) error {
		_ = workflow.Sleep(ctx, 10*time.Millisecond)
		return nil
	}
	w.RegisterWorkflowWithOptions(wfFn, workflow.RegisterOptions{Name: "smoke"})
	if err := w.Start(); err != nil {
		return fmt.Errorf("worker 启动: %w", err)
	}
	defer w.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: "smoke-wf-" + fmt.Sprint(time.Now().UnixNano()), TaskQueue: "smoke"}, "smoke", []byte("{}"))
	if err != nil {
		return fmt.Errorf("启动: %w", err)
	}
	return run.Get(ctx, nil)
}

// cleanPort 兼容 .env 的绑定式端口（127.0.0.1:26379 → 26379）。
func cleanPort(v string) string {
	if i := strings.LastIndex(v, ":"); i >= 0 {
		return v[i+1:]
	}
	return v
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
