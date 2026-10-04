// Package service —— 点火中心(ignition)公共件。
//
// 对齐《本体模型业务功能开发指导书》：
//   - 编号规则 M3:三段式 {三位前缀}-{8 位日期段}-{4 位流水段}(VER/EXE/BCR/TLS/TSK/RMV),
//     服务层同一事务内 pg_advisory_xact_lock 防重号(事务结束自动释放),唯一索引兜底;
//   - 字典校验:取值集合来自本体注册表(M1 data_dictionaries),模型即唯一语义来源;
//   - 五默认字段由本层赋值 created_by/updated_by,逻辑删除 flag=0;
//   - 事件主题:opic.ign.<area>.<event>(中心级隔离随 CENTER_CODE 派生;M6 流程事件)。
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"gitcode.com/opic-ontology/opic-techbase/internal/config"
	"gitcode.com/opic-ontology/opic-techbase/internal/infra"
	"gitcode.com/opic-ontology/opic-techbase/internal/pkg/ontology"
	"gitcode.com/opic-ontology/opic-techbase/internal/store"
	"gitcode.com/opic-ontology/opic-techbase/pkg/errcode"
)

// publishIgnEvent 发布 ignition 业务事件(旁路:失败仅记日志,不阻塞主流程)。
// 主题约定:opic.ign.<area>.<event>(M6 各流程事件;中心级隔离随 CENTER_CODE 派生)。
func publishIgnEvent(area, event string, payload map[string]any) {
	eb := infra.EB()
	if eb == nil {
		return
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	topic := fmt.Sprintf("opic.%s.%s.%s", config.Config.CenterCode, area, event)
	if err := eb.Publish(context.Background(), topic, raw); err != nil {
		log.Printf("[ign-event] 发布失败 topic=%s: %v", topic, err)
	}
}

// ---------- 业务编号(M3:VER/EXE/BCR/TLS/TSK/RMV-YYYYMMDD-NNNN) ----------

var ignPrefixes = map[string]string{
	"DeliveryVersion":     "VER", // AGG-IGN-01 交付版本
	"Executor":            "EXE", // AGG-IGN-03 执行机
	"BootstrapCredential": "BCR", // AGG-IGN-04 引导凭据
	"ExecutorTlsBinding":  "TLS", // AGG-IGN-05 通道证书绑定
	"ExecTask":            "TSK", // AGG-IGN-06 执行任务
	"ForceRemove":         "RMV", // 强制注销申请载体
}

// genIgnNo 事务内生成业务编号(三位前缀+YYYYMMDD-4 位流水)。
// pg_advisory_xact_lock 防并发重号(事务结束自动释放),唯一索引兜底。
func genIgnNo(db *gorm.DB, alias, table, column string) (string, error) {
	prefix, ok := ignPrefixes[alias]
	if !ok {
		return "", Fail(errcode.ErrConfig, "对象 %s 未登记编号前缀(M3 编号规则)", alias)
	}
	if _, err := store.Exec(db, `SELECT pg_advisory_xact_lock(hashtext('ign_no:' || ?))`, table+"."+column); err != nil {
		return "", err
	}
	day := time.Now().Format("20060102")
	head := prefix + "-" + day + "-"
	row, err := store.One(db, `SELECT COUNT(*) AS n FROM `+table+` WHERE `+column+` LIKE ?`, head+"%")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%04d", head, toInt(row["n"])+1), nil
}

// ---------- 数据字典(模型驱动,M1 data_dictionaries) ----------

// ignDictCodes 取字典启用项 code 集合(来自本体注册表;注册表未加载时返回空集)。
func ignDictCodes(typeCode string) map[string]bool {
	reg := ontology.Load()
	out := map[string]bool{}
	for _, it := range reg.GetDictionaryItems("DICT-"+typeCode, typeCode) {
		out[Str(it["code"])] = true
	}
	return out
}

// ignDictRequire 校验取值命中字典,未命中给中文提示。
func ignDictRequire(typeCode, value, label string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	codes := ignDictCodes(typeCode)
	if len(codes) == 0 {
		return nil // 注册表不可用时不做字典硬校验(不阻塞;由部署检查)
	}
	if !codes[value] {
		return Fail(errcode.ErrRule, "%s 取值 %s 不在 %s 字典内（本域字典为平台预置枚举，不允许运行期新增）", label, value, typeCode)
	}
	return nil
}

// ---------- M3 INV-02 等格式校验 ----------

var (
	ignSemverRe     = regexp.MustCompile(`^\d+\.\d+\.\d+$`)        // INV-02 语义化版本 x.y.z
	ignOrchRe       = regexp.MustCompile(`^v1\.\d+\.\d+$`)         // INV-02 编排引擎 v1.x.y
	ignVersionishRe = regexp.MustCompile(`^[vV]?\d+(\.\d+){0,3}$`) // INV-02 其余组件版本宽松口径
	ignHex64Re      = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)      // 摘要 64 位十六进制
	ignHexRe        = regexp.MustCompile(`^[0-9a-fA-F]+$`)         // 十六进制串
)

// checkSemver INV-02:交付工具版本为语义化版本 x.y.z。
func checkSemver(label, v string) error {
	if !ignSemverRe.MatchString(strings.TrimSpace(v)) {
		return Fail(errcode.ErrRule, "%s 须为语义化版本（形如 2.24.0；INV-02）", label)
	}
	return nil
}

// checkOrchVer INV-02:编排引擎版本为 v1.x.y 格式。
func checkOrchVer(v string) error {
	if !ignOrchRe.MatchString(strings.TrimSpace(v)) {
		return Fail(errcode.ErrRule, "编排引擎版本须为 v1.x.y 格式（形如 v1.30.4；INV-02）")
	}
	return nil
}

// checkComponentVer INV-02:运行时/etcd/CNI 版本/CoreDNS 版本宽松版本口径。
func checkComponentVer(label, v string) error {
	if !ignVersionishRe.MatchString(strings.TrimSpace(v)) {
		return Fail(errcode.ErrRule, "%s 须为版本号（形如 1.7.1；INV-02）", label)
	}
	return nil
}

// checkTarballURL INV-02:发行包地址为合法 URL(http/https)。
func checkTarballURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Fail(errcode.ErrRule, "发行包地址须为合法 URL（http/https；INV-02）")
	}
	return nil
}

// checkSha256Hex R-02/摘要口径:64 位十六进制(提供即强校验)。
func checkSha256Hex(label, v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	if !ignHex64Re.MatchString(v) {
		return Fail(errcode.ErrRule, "%s 须为 64 位十六进制摘要（%s）", label, "R-02")
	}
	return nil
}

// checkPortRange INV-14:服务端口为 1024~65535 的整数。
func checkPortRange(n int64) error {
	if n < 1024 || n > 65535 {
		return Fail(errcode.ErrRule, "服务端口须为 1024~65535 的整数（INV-14）")
	}
	return nil
}

// ignCapabilityList 能力标签拆分(逗号分隔;去空白去重)。
func ignCapabilityList(raw string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, seg := range strings.Split(raw, ",") {
		s := strings.TrimSpace(seg)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// ---------- 时间辅助(统一 YYYY-MM-DD HH:MM:SS,TEXT 列口径) ----------

const ignTimeLayout = "2006-01-02 15:04:05"

func ignNow() string { return time.Now().Format(ignTimeLayout) }

// parseIgnTime 宽松解析(接受 2006-01-02 15:04:05 / RFC3339)。
func parseIgnTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{ignTimeLayout, "2006-01-02T15:04:05", time.RFC3339} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// ---------- 平台配置项(REF-01~12 运行参数 FUNC-30;缺省即 M3 口径) ----------

const (
	ignCfgTokenTTLHours      = "token_ttl_hours"       // REF-01 引导令牌有效期(缺省 24)
	ignCfgTaskTimeoutSeconds = "task_timeout_seconds"  // REF-02 任务默认超时(缺省 7200)
	ignCfgHeartbeatTimeout   = "heartbeat_timeout_sec" // REF-03 心跳超时阈值(缺省 120)
	ignCfgPrepushConcurrent  = "prepush_concurrency"   // REF-04 预推并发上限(缺省 4,热生效)
	ignCfgCertRenewDays      = "cert_renew_days"       // REF-07 证书续期提前天数(缺省 14)
	ignCfgSoftDeleteDays     = "soft_delete_days"      // REF-08 软删归档后天数(缺省 30)
	ignCfgReplayWindowHours  = "replay_window_hours"   // REF-09 防重放窗口(缺省 24)
	ignCfgReconcilePeriodSec = "reconcile_period_sec"  // REF-12 状态对账周期(缺省 60)
)

// defaultIgnConfigs 运行参数缺省(REF-* 口径;FUNC-30 可维护)。
func defaultIgnConfigs() []struct{ Key, Value, Desc string } {
	return []struct{ Key, Value, Desc string }{
		{ignCfgTokenTTLHours, "24", "REF-01 引导令牌有效期（小时）"},
		{ignCfgTaskTimeoutSeconds, "7200", "REF-02 任务默认超时（秒）"},
		{ignCfgHeartbeatTimeout, "120", "REF-03 心跳超时阈值（秒）"},
		{ignCfgPrepushConcurrent, "4", "REF-04 预推并发上限（热生效）"},
		{ignCfgCertRenewDays, "14", "REF-07 证书续期提前天数"},
		{ignCfgSoftDeleteDays, "30", "REF-08 版本软删需归档后超过天数"},
		{ignCfgReplayWindowHours, "24", "REF-09 请求令牌防重放窗口（小时）"},
		{ignCfgReconcilePeriodSec, "60", "REF-12 状态对账周期（秒）"},
	}
}

// upsertIgnConfig 平台配置项 upsert(幂等)。
func upsertIgnConfig(db *gorm.DB, key, value, desc string, userID int64) error {
	exists, err := store.One(db, `SELECT id FROM ign_platform_config WHERE config_key = ?`, key)
	if err != nil {
		return err
	}
	if exists != nil {
		_, err = store.Exec(db, `
			UPDATE ign_platform_config SET config_value=?, description=?, updated_by=?,
			updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`,
			value, desc, userID, toInt(exists["id"]))
		return err
	}
	_, err = store.Exec(db, `
		INSERT INTO ign_platform_config (config_key, config_value, description, created_by)
		VALUES (?, ?, ?, ?)`, key, value, desc, userID)
	return err
}

// ignConfigGet 平台配置项读取(未配置返回空串)。
func ignConfigGet(db *gorm.DB, key string) (string, error) {
	row, err := store.One(db, `SELECT config_value FROM ign_platform_config WHERE config_key = ?`, key)
	if err != nil || row == nil {
		return "", err
	}
	return Str(row["config_value"]), nil
}

// ignConfigInt 平台配置项读取为 int64(空/非法回落缺省;REF-* 口径)。
func ignConfigInt(db *gorm.DB, key string, def int64) int64 {
	v, err := ignConfigGet(db, key)
	if err != nil || strings.TrimSpace(v) == "" {
		return def
	}
	if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && n > 0 {
		return n
	}
	return def
}

// ---------- 取值辅助 ----------

// nullableBigInt 0 → NULL(可空数值列写入;避免 0 被当作有效值)。
func nullableBigInt(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

// timeSinceHours 距今小时数(REF-08 软删时限核算等)。
func timeSinceHours(t time.Time) float64 {
	return time.Since(t).Hours()
}

// ignNullableStr 空串 → NULL(写入可空列;避免把空串当作有效值)。
func ignNullableStr(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// ignToInt 宽松 any→int64:数值型委托 Int()(兼容 pgx int16/int32),
// 字符串型按十进制解析(表单/查询串来源;失败为 0)。
func ignToInt(v any) int64 {
	if s, ok := v.(string); ok {
		n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil {
			return 0
		}
		return n
	}
	return Int(v)
}

// boolOfAny any → bool(容忍 bool/字符串)。
func boolOfAny(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "true" || x == "1"
	case int64, int, float64:
		return ignToInt(v) != 0
	}
	return false
}
