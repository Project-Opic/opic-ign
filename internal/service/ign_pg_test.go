// ign_pg_test.go —— 临时 PostgreSQL 测试实例装置（initdb/pg_ctl 自起自停，或 IGN_TEST_PG_DSN 指向既有实例）
// + goose 真实迁移；门面受理/执行位/判定网关等 DB 语义用例依赖本装置。
package service

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"gitcode.com/opic-ontology/opic-techbase/internal/config"
	"gitcode.com/opic-ontology/opic-techbase/internal/store"
)

var testDBReady bool

func TestMain(m *testing.M) {
	cleanup, err := startTestDB()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[ign_pg_test] 测试库不可用，DB 依赖用例跳过: %v\n", err)
	} else {
		testDBReady = true
		defer cleanup()
	}
	code := m.Run()
	os.Exit(code)
}

func startTestDB() (func(), error) {
	dsn := os.Getenv("IGN_TEST_PG_DSN")
	if dsn == "" {
		initdb, err1 := exec.LookPath("initdb")
		pgCtl, err2 := exec.LookPath("pg_ctl")
		createdbBin, err3 := exec.LookPath("createdb")
		if err1 != nil || err2 != nil || err3 != nil {
			return nil, fmt.Errorf("缺少 initdb/pg_ctl/createdb（本地装 PostgreSQL 或设 IGN_TEST_PG_DSN）")
		}
		base, err := os.MkdirTemp("", "ign-test-pg-")
		if err != nil {
			return nil, err
		}
		data := filepath.Join(base, "data")
		if out, err := runCmd(append(os.Environ(), "LC_ALL=C"), initdb,
			"-D", data, "-U", "postgres", "--auth=trust", "-E", "UTF8", "--no-sync"); err != nil {
			os.RemoveAll(base)
			return nil, fmt.Errorf("initdb 失败: %v\n%s", err, out)
		}
		port, err := freeTCPPort()
		if err != nil {
			os.RemoveAll(base)
			return nil, err
		}
		logFile := filepath.Join(base, "pg.log")
		if out, err := runCmd(nil, pgCtl, "-D", data, "-l", logFile, "-w", "start",
			"-o", fmt.Sprintf("-p %d -c listen_addresses=127.0.0.1 -c unix_socket_directories=%s", port, base)); err != nil {
			out2, _ := os.ReadFile(logFile)
			os.RemoveAll(base)
			return nil, fmt.Errorf("pg_ctl start 失败: %v\n%s\n%s", err, out, out2)
		}
		cleanup := func() {
			_, _ = runCmd(nil, pgCtl, "-D", data, "-m", "fast", "stop")
			_ = os.RemoveAll(base)
		}
		if out, err := runCmd(nil, createdbBin, "-h", "127.0.0.1", "-p", fmt.Sprint(port), "-U", "postgres", "opicdb"); err != nil {
			cleanup()
			return nil, fmt.Errorf("createdb 失败: %v\n%s", err, out)
		}
		dsn = fmt.Sprintf("host=127.0.0.1 port=%d user=postgres dbname=opicdb sslmode=disable", port)
		dbCleanup, err := openAndMigrate(dsn)
		if err != nil {
			cleanup()
			return nil, err
		}
		return func() { dbCleanup(); cleanup() }, nil
	}
	return openAndMigrate(dsn)
}

func openAndMigrate(dsn string) (func(), error) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	if err != nil {
		return nil, fmt.Errorf("连接测试库失败: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(8)
	goose.SetDialect("postgres")
	goose.SetLogger(goose.NopLogger())
	if err := goose.UpContext(context.Background(), sqlDB, filepath.Join("..", "..", "migrations")); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("goose 迁移失败: %w", err)
	}
	store.DB = db
	config.Config.Ontology.ModelsDir = filepath.Join("..", "..", "models")
	return func() { _ = sqlDB.Close() }, nil
}

func runCmd(env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	if env != nil {
		cmd.Env = env
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func freeTCPPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// ---------- 用例助手 ----------

var testSeq int64

func nextSeq() int64 { return atomic.AddInt64(&testSeq, 1) }

func requireDB(t *testing.T) {
	t.Helper()
	if !testDBReady {
		t.Skip("测试库不可用，跳过 DB 用例")
	}
}

func mustSQL(t *testing.T, sql string, args ...any) int64 {
	t.Helper()
	res := store.DB.Exec(sql, args...)
	if res.Error != nil {
		t.Fatalf("SQL 失败: %v\n%s", res.Error, sql)
	}
	return res.RowsAffected
}

func mustOne(t *testing.T, sql string, args ...any) map[string]any {
	t.Helper()
	row, err := qOne(sql, args...)
	if err != nil {
		t.Fatalf("查询失败: %v\n%s", err, sql)
	}
	return row
}

func mustList(t *testing.T, sql string, args ...any) []map[string]any {
	t.Helper()
	rows, err := qList(sql, args...)
	if err != nil {
		t.Fatalf("查询失败: %v\n%s", err, sql)
	}
	return rows
}

func countRows(t *testing.T, sql string, args ...any) int64 {
	t.Helper()
	n, err := qCount(sql, args...)
	if err != nil {
		t.Fatalf("计数失败: %v\n%s", err, sql)
	}
	return n
}

// writeExecTool 生成一个可执行的命令面夹具（0755 shell 脚本；模拟 ezctl 的行为与输出）。
func writeExecTool(t *testing.T, body string) (bin, root string) {
	t.Helper()
	root = t.TempDir()
	bin = filepath.Join(root, "ezctl")
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "clusters"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, root
}

// waitTaskStatus 轮询等待任务落终态（执行位异步收口）。
// 超时即把最后观测与末三条日志一并回吐：异步链路失败必须可从断言里读出原因，不靠人再查库。
func waitTaskStatus(t *testing.T, taskNo string, want ...string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var last map[string]any
	for time.Now().Before(deadline) {
		row := mustOne(t, `SELECT * FROM exec_task WHERE task_no = ? AND flag = 1`, taskNo)
		if row != nil {
			last = row
			for _, w := range want {
				if Str(row["task_status"]) == w {
					return row
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	tail := "（无日志行）"
	if last != nil {
		if rows, err := qList(`SELECT seq, direction, log_line FROM ign_task_log
			WHERE task_id = ? AND flag = 1 ORDER BY seq DESC LIMIT 3`, Str(last["id"])); err == nil && len(rows) > 0 {
			parts := make([]string, 0, len(rows))
			for i := len(rows) - 1; i >= 0; i-- {
				parts = append(parts, Str(rows[i]["seq"])+" "+Str(rows[i]["direction"])+": "+Str(rows[i]["log_line"]))
			}
			tail = strings.Join(parts, "\n")
		}
	}
	t.Fatalf("任务 %s 未在期限内进入 %v\n最后观测: 状态=%s 结论依据=%v 退出码=%v 失败原因=%v\n末三条日志:\n%s",
		taskNo, want, Str(last["task_status"]), last["verdict_source"], last["exit_code"], last["fail_reason"], tail)
	return nil
}
