// ign_exec.go —— 本机执行位内核（B-AUTO-11~15；R-13/R-14/R-15；INV-36~INV-40）。
//
// 本文件是"动作码 → argv"的**本域私有映射**所在（Q-69 裁定 ② + Q-70 裁定 ③：映射内容与
// 命令语义不入模型、不出域，门面对上游只暴露动作码与结论）。三条硬纪律：
//  1. 参数独立 argv 直调、绝不经 shell（INV-28/R-12：含拼接风险字符即治理拒绝）；
//  2. 命令面可执行文件与工作根目录只以部署配置值存在（1.6.1）——未挂载即执行位不可用，
//     受理即刻拒绝并记 TOOL_UNAVAILABLE，绝不静默改走别的执行位（R-15）；
//  3. 执行位进程退出 ⇒ 判定网关写终态与结论依据；痕迹不可存活 ⇒ 显式 LOST（B-AUTO-14）。
package service

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"gorm.io/gorm"

	"gitcode.com/opic-ontology/opic-techbase/internal/config"
	"gitcode.com/opic-ontology/opic-techbase/internal/store"
	"gitcode.com/opic-ontology/opic-techbase/pkg/errcode"
	"gitcode.com/opic-ontology/opic-techbase/pkg/logger"
)

// ---------- R-13 私有映射表（发行包整体替换时只改此表与挂载路径） ----------

// ignCmdPlane 动作码 → 命令字（IGN_ACTION 白名单一一对应；15 项）。
var ignCmdPlane = map[string][]string{
	"ezctl.new":             {"new"},
	"ezctl.setup":           {"setup"},
	"ezctl.start":           {"start"},
	"ezctl.stop":            {"stop"},
	"ezctl.add-node":        {"add-node"},
	"ezctl.del-node":        {"del-node"},
	"ezctl.upgrade":         {"upgrade"},
	"ezctl.backup":          {"backup"},
	"ezctl.restore":         {"restore"},
	"ezctl.destroy":         {"destroy"},
	"ezctl.ca-renew":        {"kca-renew"},
	"ezctl.kcfg-adm":        {"kcfg-adm"},
	"scripts.image_prepush": {"scripts/image_prepush.sh"},
	"scripts.health_check":  {"scripts/health_check.sh"},
	"scripts.tls_renew":     {"scripts/tls_renew.sh"},
}

// ignActionPlane 白名单枚举 ≠ 可提交集合（7.3 三层可达面）：
// delivery 人工提交与门面受理均可用；internal 只由 B-AUTO-15 使用；platform 只对系统自动行为开放。
func ignActionPlane(action string) string {
	switch action {
	case "ezctl.new":
		return "internal"
	case "scripts.image_prepush", "scripts.health_check", "scripts.tls_renew":
		return "platform"
	default:
		if _, ok := ignCmdPlane[action]; ok {
			return "delivery"
		}
		return "unknown"
	}
}

// ignCategoryAlias 上游动作类别名 → 本域动作码（O-CLC 的 CLC_TASK_KIND 词汇；R-13 解析）。
var ignCategoryAlias = map[string]string{
	"START":         "ezctl.start",
	"STOP":          "ezctl.stop",
	"UPGRADE":       "ezctl.upgrade",
	"BACKUP":        "ezctl.backup",
	"RESTORE":       "ezctl.restore",
	"DESTROY":       "ezctl.destroy",
	"CERT_RENEW":    "ezctl.ca-renew",
	"ADD_NODE":      "ezctl.add-node",
	"DEL_NODE":      "ezctl.del-node",
	"KCFG_ADM":      "ezctl.kcfg-adm",
	"INSTALL":       "ezctl.setup",
	"RETRY_INSTALL": "ezctl.setup",
}

var (
	ignClusterIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	ignStepCodeRe  = regexp.MustCompile(`^0[1-7]$`) // REF-13/INV-36：本域只认 ezctl.setup 的参数合法域
	// 参数面 token（人工提交的附加参数）：允许选项形态 --k=v / -k，但不得含空格与拼接风险字符。
	ignTokenRe = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._:/@=-]*|-{1,2}[A-Za-z0-9][A-Za-z0-9._:/=@-]*)$`)
	// 节点地址（IP 或主机名）：不得以连接符开头，避免被命令面当成选项。
	ignAddrRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	// 拼接风险字符（R-12）：argv 直调不经 shell，此类输入一律拒绝并审计。
	ignShellMetaRe = regexp.MustCompile("[;&|`$<>()\\[\\]{}*?!~#'\"\\\\\\n\\r\\t]")
)

// ignNodesAction 节点增删类动作（其参数面必须是地址清单，其余动作该字段必空）。
func ignNodesAction(action string) bool {
	return action == "ezctl.add-node" || action == "ezctl.del-node"
}

// ignResolveFacadeAction R-13 受理解析：步骤编码命中 01~07 ⇒ 安装族单步；
// 命中上游类别名 ⇒ 私有映射取单步动作码；两者都不命中、或动作码不属交付动作层 ⇒ 治理拒绝。
func ignResolveFacadeAction(actionCode, stepCode string) (string, string, error) {
	actionCode = strings.TrimSpace(actionCode)
	stepCode = strings.TrimSpace(stepCode)
	if ignStepCodeRe.MatchString(stepCode) {
		if actionCode != "" && actionCode != "ezctl.setup" {
			return "", "", ignGovernanceRefuse(fmt.Sprintf(
				"步骤编码 %s 属安装族参数域、动作码却为 %s（R-13：安装类动作固定为 ezctl.setup）", stepCode, actionCode))
		}
		return "ezctl.setup", stepCode, nil
	}
	if actionCode == "" && stepCode == "" {
		return "", "", ignGovernanceRefuse("步骤编码与执行动作码至少给一个（R-13 受理解析）")
	}
	resolved := actionCode
	if alias, ok := ignCategoryAlias[strings.ToUpper(stepCode)]; ok {
		resolved = alias
	}
	if resolved == "" {
		return "", "", ignGovernanceRefuse(fmt.Sprintf(
			"步骤编码 %s 既不属安装族参数域 01~07、也不属已登记的动作类别名（R-13 命令面私有映射）", stepCode))
	}
	if _, ok := ignCmdPlane[resolved]; !ok {
		return "", "", ignGovernanceRefuse(fmt.Sprintf(
			"执行动作 %s 不在 IGN_ACTION 白名单内（R-12 治理拒绝：白名单外动作一律拒绝并审计）", resolved))
	}
	if actionCode != "" && stepCode != "" && actionCode != resolved {
		return "", "", ignGovernanceRefuse(fmt.Sprintf(
			"动作码 %s 与步骤编码 %s 的解析结果 %s 不一致（R-13）", actionCode, stepCode, resolved))
	}
	if ignActionPlane(resolved) != "delivery" {
		return "", "", ignGovernanceRefuse(fmt.Sprintf(
			"动作码 %s 不属交付动作层（7.3 三层可达面：内部前置 / 平台运维 / 白名单外一律治理拒绝，R-12/R-13）", resolved))
	}
	if ignStepCodeRe.MatchString(stepCode) && resolved != "ezctl.setup" {
		return "", "", ignGovernanceRefuse(fmt.Sprintf(
			"步骤编码 %s 属安装族参数域、解析动作却为 %s（R-13）", stepCode, resolved))
	}
	return resolved, "", nil
}

// ignGovernanceRefuse 治理拒绝（R-12 族：不是表单校验错误，须审计可查）。
func ignGovernanceRefuse(reason string) error {
	return Fail(errcode.ErrForbidden, "%s（R-12/R-13 治理拒绝：拒绝记录可在「日志与对账」台账查询）", reason)
}

// ignBuildArgv 渲染单步命令数组（含 argv[0]=可执行文件；逐项过格式与拼接风险白名单）。
func ignBuildArgv(bin, action, clusterRef, stepCode string, nodes []string, extraArgs string) ([]string, error) {
	words, ok := ignCmdPlane[action]
	if !ok {
		return nil, ignGovernanceRefuse("执行动作 " + action + " 不在命令面映射内（R-13）")
	}
	if strings.TrimSpace(bin) == "" {
		return nil, Fail(errcode.ErrState, "本机执行位不可用：命令面可执行文件未配置（R-15/TOOL_UNAVAILABLE）")
	}
	if strings.TrimSpace(clusterRef) == "" || !ignClusterIDRe.MatchString(clusterRef) {
		return nil, Fail(errcode.ErrParam, "关联集群标识 %q 不符合格式白名单（字母数字与 . _ -，1-64 位；INV-33 跨域只存标识）", clusterRef)
	}
	argv := append([]string{bin}, words...)
	// 安装类：setup <集群> <01..07>（INV-36 参数合法域）
	if action == "ezctl.setup" {
		if !ignStepCodeRe.MatchString(stepCode) {
			return nil, Fail(errcode.ErrRule, "安装类动作的步骤编码须命中 01~07（INV-36/REF-13），收到 %q", stepCode)
		}
		argv = append(argv, clusterRef, stepCode)
	} else {
		if stepCode != "" {
			return nil, Fail(errcode.ErrRule, "步骤编码仅 ezctl.setup 使用（INV-36：其余动作该列必须为空），收到 %q", stepCode)
		}
		argv = append(argv, clusterRef)
	}
	switch {
	case ignNodesAction(action):
		if len(nodes) == 0 {
			return nil, Fail(errcode.ErrParam, "节点增删类动作必须携带节点地址清单（FUNC-34）")
		}
		for _, ip := range nodes {
			if err := ignValidateNodeAddr(ip); err != nil {
				return nil, err
			}
			argv = append(argv, strings.TrimSpace(ip))
		}
	case len(nodes) > 0:
		return nil, Fail(errcode.ErrParam, "非节点增删类动作不得携带节点地址清单（FUNC-34：该字段其余动作必须为空）")
	default:
		for _, tok := range ignArgvTokens(extraArgs) {
			if !ignTokenRe.MatchString(tok) || ignShellMetaRe.MatchString(tok) {
				return nil, Fail(errcode.ErrForbidden, "动作参数 %q 不符合参数面白名单或含拼接风险字符（R-12：代理以独立 argv 无 shell 执行）", tok)
			}
			argv = append(argv, tok)
		}
	}
	for _, a := range argv {
		if ignShellMetaRe.MatchString(a) {
			return nil, Fail(errcode.ErrForbidden, "命令参数 %q 含拼接风险字符（R-12 无 shell 治理）", a)
		}
	}
	return argv, nil
}

// ignArgvTokens 动作参数拆分为独立 argv 元素（空白分隔；人工提交路径）。
func ignArgvTokens(args string) []string {
	return strings.Fields(strings.TrimSpace(args))
}

// ignValidateNodeAddr 节点地址格式白名单（R-13 解析时校验；IP 或合法主机名）。
func ignValidateNodeAddr(v string) error {
	v = strings.TrimSpace(v)
	if v == "" || !ignAddrRe.MatchString(v) || ignShellMetaRe.MatchString(v) {
		return Fail(errcode.ErrForbidden, "节点地址 %q 不符合格式白名单（R-12/R-13：地址非法即治理拒绝）", v)
	}
	return nil
}

// ignTaskArgv 由任务登记事实渲染 argv（执行位唯一入口；不接受任何外部命令面）。
func ignTaskArgv(row map[string]any) ([]string, error) {
	var nodes []string
	action := Str(row["action"])
	args := Str(row["args"])
	if ignNodesAction(action) {
		nodes = ignArgvTokens(args)
		args = ""
	}
	return ignBuildArgv(strings.TrimSpace(config.Config.Delivery.ToolBin),
		action, Str(row["cluster_ref"]), Str(row["step_code"]), nodes, args)
}

// ---------- R-15 执行位可用性与 B-AUTO-15 工作目录幂等前置 ----------

// ignWorkRoot 集群工作目录父目录（配置值；缺省随发行包根目录）。
func ignWorkRoot() string {
	d := config.Config.Delivery
	if s := strings.TrimSpace(d.WorkDir); s != "" {
		return s
	}
	return strings.TrimSpace(d.ToolRoot)
}

// ignClusterDir 该集群的工作目录（<work_root>/clusters/<登记标识>）。
func ignClusterDir(clusterRef string) string {
	return filepath.Join(ignWorkRoot(), "clusters", clusterRef)
}

// ignLocalExecAvailable 执行位可用性探测（R-15：挂载缺失或不可执行 ⇒ 受理即刻拒绝，
// LOCAL 执行机健康列亦取同一派生结论）。返回（是否可用，不可用原因）。
func ignLocalExecAvailable() (bool, string) {
	bin := strings.TrimSpace(config.Config.Delivery.ToolBin)
	if bin == "" {
		return false, "本机执行位不可用：命令面可执行文件未配置（部署配置 delivery.tool_bin 缺失）"
	}
	st, err := os.Stat(bin)
	if err != nil {
		return false, "本机执行位不可用：命令面挂载缺失（" + bin + " 不存在）"
	}
	if st.IsDir() || st.Mode().Perm()&0o111 == 0 {
		return false, "本机执行位不可用：命令面不可执行（" + bin + "）"
	}
	if strings.TrimSpace(ignWorkRoot()) == "" {
		return false, "本机执行位不可用：工作根目录未配置（部署配置 delivery.tool_root/work_dir 缺失）"
	}
	return true, ""
}

// ignEnsureClusterWorkDir 集群工作目录幂等前置（B-AUTO-15）：
// 目录已存在即通过（可重入）；缺失时以本域私有手段对齐骨架（命令族建骨架动作），
// 骨架动作缺席也不静默 —— 目录仍不可建即返回失败，由受理侧记因拒绝（R-15）。
func ignEnsureClusterWorkDir(clusterRef string) error {
	if !ignClusterIDRe.MatchString(clusterRef) {
		return Fail(errcode.ErrParam, "关联集群标识 %q 不符合格式白名单（INV-33）", clusterRef)
	}
	dir := ignClusterDir(clusterRef)
	if ignDirExists(dir) {
		return nil
	}
	if ok, reason := ignLocalExecAvailable(); !ok {
		return Fail(errcode.ErrState, "%s（R-15）", reason)
	}
	// 建骨架属内部前置层动作：不登记任务、不出门面结论（7.3 第 ② 层）
	bin := strings.TrimSpace(config.Config.Delivery.ToolBin)
	cmd := exec.Command(bin, "new", clusterRef) // 参数数组直调，不经 shell
	cmd.Dir = ignWorkRoot()
	cmd.Env = ignToolEnv(clusterRef)
	out, err := cmd.CombinedOutput()
	if !ignDirExists(dir) {
		if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
			return Fail(errcode.ErrState, "集群工作目录不可对齐：%v（%s；R-15 受理即刻拒绝）", mkErr, ignTailLine(string(out)))
		}
		if err != nil {
			return Fail(errcode.ErrState, "集群工作目录幂等前置异常：%v（%s；目录已对齐，可重试本步）", err, ignTailLine(string(out)))
		}
	}
	return nil
}

func ignDirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// ignTailLine 取输出的末行（失败摘要用；已脱敏、限长）。
func ignTailLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			m, _ := ignMaskLine(t)
			return ignTruncateLogLine(m)
		}
	}
	return "无输出"
}

// ignToolEnv 命令面运行环境（C.3.2：注入值由本域登记事实派生，不进字典、不进界面、不由参数下发）。
func ignToolEnv(clusterRef string) []string {
	env := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"LC_ALL=C.UTF-8",
		"LANG=C.UTF-8",
		"HOME=" + ignHomeDir(),
	}
	if d := strings.TrimSpace(config.Config.Delivery.SSHDir); d != "" {
		env = append(env, "IGN_SSH_DIR="+d)
	}
	if p := ignKubeconfigPath(clusterRef); p != "" {
		env = append(env, "KUBECONFIG="+p)
	}
	return env
}

func ignHomeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "/root"
}

// ignKubeconfigPath 控制面 kubeconfig（装配后由发行包自身产出；缺失即不注入）。
func ignKubeconfigPath(clusterRef string) string {
	dir := ignClusterDir(clusterRef)
	exact := filepath.Join(dir, clusterRef+"-m1-kubectl.kubeconfig")
	if ignFileExists(exact) {
		return exact
	}
	matches, err := filepath.Glob(filepath.Join(dir, "*-kubectl.kubeconfig"))
	if err == nil && len(matches) > 0 {
		return matches[0]
	}
	return ""
}

func ignFileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// ---------- B-AUTO-11 本机执行位任务执行 ----------

// ignProc 运行中的执行位进程（取消与超时的进程组终止句柄）。
type ignProc struct {
	pid  int
	kill chan struct{}
}

// ignProcs 在途进程表（"PID 验真"的进程内实现；崩溃后即不存在 ⇒ B-AUTO-14 置 LOST）。
var ignProcs = struct {
	mu sync.Mutex
	m  map[string]*ignProc
}{m: map[string]*ignProc{}}

// ignTryBindProc 协程入口先占位再读库：占位成功才有权处置该任务
// （取消请求据此判断"是否有在途协程持有"，避免取消与派发互相踩空）。
func ignTryBindProc(taskNo string, p *ignProc) bool {
	ignProcs.mu.Lock()
	defer ignProcs.mu.Unlock()
	if _, ok := ignProcs.m[taskNo]; ok {
		return false
	}
	ignProcs.m[taskNo] = p
	return true
}

func ignUnbindProc(taskNo string, p *ignProc) {
	ignProcs.mu.Lock()
	defer ignProcs.mu.Unlock()
	if ignProcs.m[taskNo] == p {
		delete(ignProcs.m, taskNo)
	}
}

func ignProcLive(taskNo string) bool {
	ignProcs.mu.Lock()
	defer ignProcs.mu.Unlock()
	_, live := ignProcs.m[taskNo]
	return live
}

// ignDispatchLocal 受理事务提交后派发（B-AUTO-11：同步返回的引用不依赖执行结果）。
// R-17：同集群只要有更早登记的任务仍在执行位上跑，本任务即"排队中"——状态仍是 RUNNING、
// 不引入新状态、不判失败，由前序任务收口时续派（ignClaimNextQueued）。
func ignDispatchLocal(row map[string]any) {
	taskID := ignToInt(row["id"])
	taskNo := Str(row["task_no"])
	if taskID == 0 || taskNo == "" {
		return
	}
	if ignClusterBusyAhead(Str(row["cluster_ref"]), taskID) {
		ignAppendTaskLog(taskID, "PLATFORM",
			"同集群有更早的在途任务，本任务排队等待续派（R-17：状态仍为执行中，不是失败）")
		return
	}
	go ignRunLocal(row)
}

// ignClusterBusyAhead 同集群是否存在"更早且正在执行位上运行"的任务（排队判定）。
func ignClusterBusyAhead(clusterRef string, selfID int64) bool {
	if strings.TrimSpace(clusterRef) == "" {
		return false
	}
	rows, err := qList(`SELECT task_no, id FROM exec_task
		WHERE flag = 1 AND cluster_ref = ? AND task_status IN ('RUNNING','CANCELLING') AND id <> ?
		ORDER BY id`, clusterRef, selfID)
	if err != nil {
		return false
	}
	for _, r := range rows {
		if ignToInt(r["id"]) > selfID {
			break // 只让更早登记者占位（FIFO，避免后到任务饿死前者）
		}
		if ignProcLive(Str(r["task_no"])) {
			return true
		}
	}
	return false
}

// ignClaimNextQueued 前序任务收口后续派同集群排队任务（R-17 的"由上游轮询自然消化"落点）。
func ignClaimNextQueued(clusterRef string) {
	if strings.TrimSpace(clusterRef) == "" {
		return
	}
	rows, err := qList(`SELECT * FROM exec_task
		WHERE flag = 1 AND cluster_ref = ? AND task_status = 'RUNNING' ORDER BY id`, clusterRef)
	if err != nil {
		return
	}
	for _, r := range rows {
		if ignProcLive(Str(r["task_no"])) {
			continue
		}
		ignDispatchLocal(r)
		return
	}
}

// ignRunLocal 执行位主循环：占位 → 前置核对 → 直调 → 逐行入库 → 判定网关 → 终态回填（R-14）。
func ignRunLocal(row map[string]any) {
	taskID := ignToInt(row["id"])
	taskNo := Str(row["task_no"])
	clusterRef := Str(row["cluster_ref"])

	// ① 先占位：取消请求据此判断"有在途协程持有"（B-AUTO-13 的收口归属）
	proc := &ignProc{kill: make(chan struct{}, 1)}
	if !ignTryBindProc(taskNo, proc) {
		return
	}
	defer ignUnbindProc(taskNo, proc)

	// ② 占位后核对事实：已终态（重复派发/已被取消收口）或已置取消中 ⇒ 绝不起进程
	cur, err := qOne(`SELECT task_status FROM exec_task WHERE id = ? AND flag = 1`, taskID)
	if err != nil {
		ignFinishLocal(taskID, taskNo, ignOutcome{lost: true, reason: "任务事实不可读：" + err.Error()})
		return
	}
	if ignTerminalStatus(Str(cur["task_status"])) {
		return
	}
	if Str(cur["task_status"]) == "CANCELLING" {
		ignFinishLocal(taskID, taskNo, ignCancelledBeforeStart())
		return
	}

	argv, err := ignTaskArgv(row)
	if err != nil {
		// 受理后才出现的执行位不可用（挂载被撤/配置被清）：痕迹从未产生 ⇒ LOST（B-AUTO-14）
		ignFinishLocal(taskID, taskNo, ignOutcome{lost: true, reason: Str(AsBizErr(err).Msg)})
		return
	}
	timeoutSec := ignToInt(row["timeout_seconds"])
	if timeoutSec <= 0 {
		timeoutSec = ignConfigInt(store.DB, ignCfgTaskTimeoutSeconds, 7200) // REF-02
	}
	grace := time.Duration(ignConfigInt(store.DB, ignCfgKillGraceSeconds, 30)) * time.Second // REF-15

	cmd := exec.Command(argv[0], argv[1:]...) // 独立 argv 直调，绝不经 shell（INV-28/R-12）
	cmd.Dir = ignWorkRoot()
	cmd.Env = ignToolEnv(clusterRef)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		ignFinishLocal(taskID, taskNo, ignLostPipeReason("标准输出", err))
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		ignFinishLocal(taskID, taskNo, ignLostPipeReason("标准错误", err))
		return
	}
	if err := cmd.Start(); err != nil {
		ignFinishLocal(taskID, taskNo, ignOutcome{
			lost: true, reason: "执行位进程不可存活：" + err.Error(),
		})
		return
	}
	proc.pid = cmd.Process.Pid

	ignAppendTaskLog(taskID, "PLATFORM",
		"执行位已启动（pid "+strconv.Itoa(proc.pid)+"）：独立 argv 直调，不经 shell")

	// 逐行采集 → 写前脱敏 → 游标单调入库（R-16/INV-38/INV-40）
	lines := make(chan string, 256)
	var wg sync.WaitGroup
	scan := func(r io.Reader, prefix string) {
		defer wg.Done()
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 4*1024), 1024*1024)
		sc.Split(bufio.ScanLines)
		for sc.Scan() {
			if prefix != "" {
				lines <- prefix + " " + sc.Text()
			} else {
				lines <- sc.Text()
			}
		}
	}
	wg.Add(2)
	go scan(stdout, "")
	go scan(stderr, "[stderr]")
	go func() { wg.Wait(); close(lines) }()

	var tr ignRecapTracker
	cursor := ignLogCursor(taskID)
	errCh := make(chan error, 1)
	go func() {
		// 采集须先读到 EOF 再 Wait：否则末行（常是 PLAY RECAP 汇总行）随描述符关闭丢失
		for l := range lines {
			tr.Feed(l)
			masked, hit := ignMaskLine(l)
			ignInsertTaskLog(taskID, &cursor, "TOOL", ignTruncateLogLine(masked), hit)
		}
		errCh <- cmd.Wait()
	}()

	timedOut := false
	var waitErr error
	timer := time.NewTimer(time.Duration(timeoutSec) * time.Second)
	defer timer.Stop()
	select {
	case waitErr = <-errCh:
	case <-proc.kill: // FUNC-24/FUNC-36 撤销：进程组终止链（B-AUTO-13）
		ignKillGroup(proc.pid, grace)
		waitErr = <-errCh
	case <-timer.C: // 超时到点：同一条终止链（B-AUTO-13/REF-15）
		timedOut = true
		ignKillGroup(proc.pid, grace)
		waitErr = <-errCh
	}
	wg.Wait()

	cancelled := ignCancelRequested(taskNo)
	exit := ignExitCode(waitErr)
	status, verdict, failReason, summary, hasExit := ignLocalVerdict(exit, &tr, cancelled, timedOut)
	if strings.TrimSpace(summary) == "" {
		summary = "退出码 " + strconv.Itoa(exit)
	}
	ignFinishLocal(taskID, taskNo, ignOutcome{
		status: status, verdict: verdict, failReason: failReason,
		exit: exit, hasExit: hasExit, summary: summary,
	})
}

// ignLostPipeReason 管道建立失败 ⇒ 痕迹不可存活（B-AUTO-14 家族，不静默）。
func ignLostPipeReason(what string, err error) ignOutcome {
	return ignOutcome{lost: true, reason: what + "管道建立失败：" + err.Error()}
}

// ignOutcome 执行位结论（内部结构；落库形态见 ignFinishLocal）。
type ignOutcome struct {
	status     string
	verdict    string
	failReason string
	exit       int
	hasExit    bool
	summary    string
	reason     string // lost/拒绝时的中文原因
	lost       bool
}

// ignFinishLocal 终态回填：状态 + 结论依据 + 退出码（LOST 无退出码，INV-37）+
// 失败原因 + 计数递减（R-05）+ 平台注记行（INV-39 例外：终态回填属对账留痕）。
// 已终态则整体跳过——终态不可改写（INV-32），重复收口幂等。
func ignFinishLocal(taskID int64, taskNo string, o ignOutcome) {
	if taskID == 0 || taskNo == "" {
		return
	}
	status, verdict, failReason := o.status, o.verdict, o.failReason
	var exitCol any = nil
	note := o.summary
	if o.lost {
		status, verdict, failReason, _ = ignLostVerdict()
		note = o.reason
	} else if o.hasExit {
		exitCol = o.exit
	}
	if strings.TrimSpace(note) == "" {
		note = o.reason
	}
	note = "终态回填：" + status + " / 结论依据 " + verdict + " / " + strings.TrimSpace(note)
	noteMasked, hit := ignMaskLine(ignTruncateLogLine(note))

	var clusterRef string
	var executorID int64
	changed := false
	err := tx(func(db *gorm.DB) error {
		row, err := store.One(db, `
			SELECT id, task_status, executor_id, cluster_ref, started_at FROM exec_task
			WHERE id = ? AND flag = 1 FOR UPDATE`, taskID)
		if err != nil || row == nil {
			return err
		}
		if ignTerminalStatus(Str(row["task_status"])) {
			return nil // INV-32：终态不可改写（重复收口幂等跳过）
		}
		executorID = Int(row["executor_id"])
		clusterRef = Str(row["cluster_ref"])
		now := ignNow()
		var durationCol any
		if st, ok := parseIgnTime(Str(row["started_at"])); ok {
			if et, ok2 := parseIgnTime(now); ok2 {
				durationCol = et.Sub(st).Milliseconds()
			}
		}
		res, err := store.Exec(db, `
			UPDATE exec_task
			SET task_status = ?, verdict_source = ?, exit_code = ?, fail_reason = ?,
			    ended_at = ?, duration_ms = ?,
			    updated_at = to_char(now(),'YYYY-MM-DD HH24:MI:SS')
			WHERE id = ? AND task_status NOT IN ('COMPLETED','FAILED','CANCELLED','LOST')`,
			status, verdict, exitCol, ignNullableStr(failReason), now, durationCol, taskID)
		if err != nil {
			return err
		}
		if res == 0 {
			return nil
		}
		changed = true
		// R-05：任务结束计数原子递减（异常结束也保证不下溢）
		if _, err := store.Exec(db, `
			UPDATE executor SET current_tasks_count = GREATEST(current_tasks_count - 1, 0)
			WHERE id = ? AND flag = 1`, executorID); err != nil {
			return err
		}
		// INV-39 例外：终态注记行（对账留痕；判定网关/失联判定的收口说明）
		// 游标取「已有右界 + 1」——直接复用右界会与最后一行 TOOL 输出撞 uq_ign_log_seq（INV-38）
		cursor, err := txLogCursor(db, taskID)
		if err != nil {
			return err
		}
		if err := txInsertTaskLog(db, taskID, cursor+1, "PLATFORM", noteMasked, hit); err != nil {
			return err
		}
		if status == "LOST" {
			// B-AUTO-14/R-09：痕迹失联显式留痕，不静默（REP-12 可见）
			if _, err := store.Exec(db, `
				INSERT INTO ign_reconcile_log
					(event_type, object_type, object_no, local_value, remote_value, trigger_reason, operator_id, operator_name, created_by)
				VALUES ('TASK_LOST', 'TASK', ?, 'RUNNING', 'LOST', ?, 1, 'system', 1)`,
				taskNo, ignNullableStr(o.reason)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		logger.Warnf("[ign-exec] 任务 %s 终态回填失败: %v", taskNo, err)
		return
	}
	if !changed {
		return
	}
	WriteAudit(nil, 1, "system", "TASK_LOCAL_FINISH",
		"执行位结论回填 "+taskNo+" → "+status+"（"+verdict+"，R-14/B-AUTO-12）")
	publishIgnEvent("task", "completed", map[string]any{
		"task_no": taskNo, "task_status": status, "verdict_source": verdict,
		"cluster_ref": clusterRef,
	})
	ignClaimNextQueued(clusterRef) // R-17：前序收口，续派同集群排队任务
}

// ignTerminalStatus 终态判定（IGN_TASK_STATUS 的三个终态 + v1.1 第四终态 LOST）。
func ignTerminalStatus(st string) bool {
	switch st {
	case "COMPLETED", "FAILED", "CANCELLED", "LOST":
		return true
	}
	return false
}

// ignExitCode 从 Wait 结果取退出码（信号终止记负值）。
func ignExitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if st, ok := ee.Sys().(syscall.WaitStatus); ok && st.Signaled() {
			return -int(st.Signal())
		}
		return ee.ExitCode()
	}
	return -1
}

// ignKillGroup 进程组终止：SIGTERM → REF-15 宽限 → SIGKILL（B-AUTO-13）。
func ignKillGroup(pid int, grace time.Duration) {
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		return
	}
	target := -pgid
	if err := syscall.Kill(target, syscall.SIGTERM); err != nil {
		return
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if syscall.Kill(target, 0) != nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	_ = syscall.Kill(target, syscall.SIGKILL)
}

// ignCancelRequested 该任务是否已被请求取消（RUNNING→CANCELLING 即视为取消意图）。
func ignCancelRequested(taskNo string) bool {
	row, err := qOne(`SELECT task_status FROM exec_task WHERE task_no = ? AND flag = 1`, taskNo)
	return err == nil && row != nil && Str(row["task_status"]) == "CANCELLING"
}

// ignCancelledBeforeStart 未及执行即被撤销（排队中/派发前）：无退出码，结论依据取最接近的
// 既有枚举 EXIT_CODE，失败原因取 ACTION_DENIED —— INV-29 要求终态"退出码或失败原因之一"，
// 而本任务在执行位上没有任何退出码可言（INV-30：取消与失败分列，状态仍是 CANCELLED）。
func ignCancelledBeforeStart() ignOutcome {
	return ignOutcome{status: "CANCELLED", verdict: "EXIT_CODE", failReason: "ACTION_DENIED",
		summary: "撤销请求在命令面启动之前到达：任务未在执行位上开始，无退出码"}
}

// IgnCancelLocal B-AUTO-13 取消入口（FUNC-24 与门面 FUNC-36 共用同一条链）。
// 返回"由在途协程收口"（true）还是"本函数已直接收口"（false）：
// 有在途协程持有 ⇒ 只发终止信号，最终状态仍由执行事实决定（R-09）；
// 无在途协程（排队中/派发前/执行位不可用）⇒ 痕迹不会产生，就地按 CANCELLED 收口。
func IgnCancelLocal(taskID int64, taskNo string) bool {
	if ignRequestCancel(taskNo) {
		return true
	}
	if taskID == 0 {
		row, err := qOne(`SELECT id FROM exec_task WHERE task_no = ? AND flag = 1`, taskNo)
		if err == nil && row != nil {
			taskID = ignToInt(row["id"])
		}
	}
	ignFinishLocal(taskID, taskNo, ignCancelledBeforeStart())
	return false
}

// ignRequestCancel B-AUTO-13 取消入口（FUNC-24 / FUNC-36 共用）：置请求标记唤醒执行位。
// 返回该任务是否由本进程的在途协程持有（否则只有 DB 状态变更，收口由对账/恢复负责）。
func ignRequestCancel(taskNo string) bool {
	ignProcs.mu.Lock()
	p, ok := ignProcs.m[strings.TrimSpace(taskNo)]
	if ok {
		select {
		case p.kill <- struct{}{}:
		default:
		}
	}
	ignProcs.mu.Unlock()
	return ok
}

// IgnRecoverLocalExec 平台启动钩子（B-AUTO-14）：进程内在途表为空 ⇒ 重启前 RUNNING/CANCELLING
// 任务的执行痕迹均不可验真，一律显式 LOST + VERDICT_UNRESOLVED；不猜测成功、不静默重跑。
func IgnRecoverLocalExec() error {
	rows, err := qList(`SELECT id, task_no FROM exec_task
		WHERE flag = 1 AND task_status IN ('RUNNING','CANCELLING') ORDER BY id`)
	if err != nil || len(rows) == 0 {
		return err
	}
	for _, r := range rows {
		taskNo := Str(r["task_no"])
		if ignProcLive(taskNo) {
			continue
		}
		ignFinishLocal(ignToInt(r["id"]), taskNo, ignOutcome{lost: true,
			reason: "平台重启后执行位进程无法验真，执行痕迹不可存活（B-AUTO-14）"})
	}
	return nil
}

// ---------- TaskLogLine 子实体写入（INV-38/INV-39） ----------

// ignLogCursor 任务内游标右界（惰性 MAX+1 分配；只增不改）。
func ignLogCursor(taskID int64) int64 {
	n, err := qCount(`SELECT COALESCE(MAX(seq), 0) FROM ign_task_log WHERE task_id = ? AND flag = 1`, taskID)
	if err != nil {
		return 0
	}
	return n
}

func txLogCursor(db *gorm.DB, taskID int64) (int64, error) {
	row, err := store.One(db, `SELECT COALESCE(MAX(seq), 0) AS c FROM ign_task_log WHERE task_id = ? AND flag = 1`, taskID)
	if err != nil {
		return 0, err
	}
	return Int(row["c"]), nil
}

// ignInsertTaskLog 逐行入库：写前脱敏已由调用方完成；游标唯一索引兜底（INV-38）。
// 入库失败时游标不前进 —— 该行不占号，避免制造伪空洞。
func ignInsertTaskLog(taskID int64, cursor *int64, direction, line string, masked bool) {
	seq := *cursor + 1
	if err := tx(func(db *gorm.DB) error {
		return txInsertTaskLog(db, taskID, seq, direction, line, masked)
	}); err != nil {
		// 日志行入库失败不回滚执行：空洞由上游按游标右界判读（INV-38 口径）
		logger.Warnf("[ign-exec] 任务 %d 日志行入库失败（游标空洞，不阻塞执行）: %v", taskID, err)
		return
	}
	*cursor = seq
}

func txInsertTaskLog(db *gorm.DB, taskID int64, seq int64, direction, line string, masked bool) error {
	_, err := store.Exec(db, `
		INSERT INTO ign_task_log (task_id, seq, direction, log_line, masked, logged_at, created_by)
		VALUES (?, ?, ?, ?, ?, ?, 1)`,
		taskID, seq, direction, line, masked, ignNow())
	return err
}

// ignAppendTaskLog 平台注记行（受理/派发/失联判定；INV-39 的例外路径必须留痕）。
func ignAppendTaskLog(taskID int64, direction, line string) {
	cursor := ignLogCursor(taskID)
	ignInsertTaskLog(taskID, &cursor, direction, ignTruncateLogLine(line), false)
}
