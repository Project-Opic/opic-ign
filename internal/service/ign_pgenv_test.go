// ign_pgenv_test.go —— 本机 PostgreSQL 服务端工具集的环境自举（仅测试构建）。
//
// 两个实测坑（与装置结构解耦，故单列一文件）：
//  1. PATH 上的 initdb 可能来自 libpq（客户端 formula）符号链接，同目录无 postgres 服务端
//     二进制 ⇒ initdb 直接失败；需优先选中带完整服务端工具集的目录。
//  2. pg_ctl 由本进程（多线程 Go 进程）派生且无有效 locale 环境变量时，
//     postmaster 以「became multithreaded during startup」终止 ⇒ 预置 LC_ALL。
package service

import (
	"os"
	"path/filepath"
	"sort"
)

var pgServerToolset = []string{"initdb", "pg_ctl", "createdb", "postgres"}

func init() {
	if os.Getenv("IGN_TEST_PG_DSN") != "" {
		return // 指向既有实例，无需本机装置
	}
	if os.Getenv("LC_ALL") == "" {
		_ = os.Setenv("LC_ALL", "C")
	}
	if pgHasToolsetIn(pgDirOf(pgFirstOnPath("initdb"))) {
		return
	}
	for _, dir := range pgCandidateDirs() {
		if pgHasToolsetIn(dir) {
			_ = os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			return
		}
	}
}

func pgDirOf(exe string) string {
	if exe == "" {
		return ""
	}
	return filepath.Dir(exe)
}

func pgFirstOnPath(name string) string {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			dir = "."
		}
		if p := filepath.Join(dir, name); pgExecutable(p) {
			return p
		}
	}
	return ""
}

func pgExecutable(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode().Perm()&0o111 != 0
}

// pgHasToolsetIn initdb 与 postgres 必须同目录，否则 initdb 运行期找不到 postgres。
func pgHasToolsetIn(dir string) bool {
	if dir == "" {
		return false
	}
	for _, name := range pgServerToolset {
		if !pgExecutable(filepath.Join(dir, name)) {
			return false
		}
	}
	return true
}

func pgCandidateDirs() []string {
	globs := []string{
		"/opt/homebrew/opt/postgresql@*/bin",
		"/opt/homebrew/opt/postgresql/bin",
		"/opt/homebrew/Cellar/postgresql@*/*/bin",
		"/opt/homebrew/Cellar/postgresql/*/*/bin",
		"/usr/local/opt/postgresql@*/bin",
		"/usr/local/Cellar/postgresql@*/*/bin",
		"/Library/PostgreSQL/*/bin",
	}
	var out []string
	seen := map[string]bool{}
	for _, g := range globs {
		matches, err := filepath.Glob(g)
		if err != nil {
			continue
		}
		sort.Strings(matches)
		for _, m := range matches {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	return out
}
