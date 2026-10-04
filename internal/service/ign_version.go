// Package service —— 点火中心(ignition):交付版本聚合(AGG-IGN-01)与镜像预推明细(AGG-IGN-02)。
//
// FUNC-01 版本登记 / FUNC-02 登记信息修改 / FUNC-03 触发发行包下载 / FUNC-04 版本归档 /
// FUNC-05 版本软删除 / FUNC-06 列表(REP-01) / FUNC-07 详情(REP-02) / FUNC-08 跨中心版本查询(REP-03) /
// FUNC-09 触发镜像预推 / FUNC-10 镜像清单人工覆写 / FUNC-11 预推失败重试 / FUNC-12 预推明细查询(REP-04)。
// 规则:INV-01~10;R-01 删除保护、R-02 下载完成判定;REF-08 软删天数。
package service

import (
	"fmt"
	"strings"

	"gorm.io/gorm"

	"gitcode.com/opic-ontology/opic-techbase/internal/store"
	"gitcode.com/opic-ontology/opic-techbase/pkg/errcode"
)

// 版本状态迁移路径(INV-07 单向;FAILED 可从任意非终态进入且重试后回归对应阶段)。
var ignVersionNext = map[string][]string{
	"DRAFT":               {"DOWNLOADING"},
	"DOWNLOADING":         {"READY", "FAILED"},
	"READY":               {"PREPUSHING", "ARCHIVED"},
	"PREPUSHING":          {"PREPUSHED", "PARTIALLY_PREPUSHED", "FAILED"},
	"PREPUSHED":           {"ARCHIVED"},
	"PARTIALLY_PREPUSHED": {"PREPUSHING", "ARCHIVED"},
	"FAILED":              {"DOWNLOADING", "PREPUSHING"}, // 重试后回归对应阶段
}

func canTransVersion(from, to string) bool {
	for _, t := range ignVersionNext[from] {
		if t == to {
			return true
		}
	}
	return false
}

// 组件版本组六项(INV-04 契约字段:创建后不可修改)。
var ignContractFields = []string{"tool_version", "orchestrator_version", "runtime_version", "etcd_version", "cni_plugin", "cni_version", "coredns_version"}

// validateVersionInput 登记输入校验(INV-01~03;保存即校验)。
func validateVersionInput(data map[string]any) (map[string]any, error) {
	name := strings.TrimSpace(Str(data["name"]))
	if name == "" {
		return nil, Fail(errcode.ErrParam, "版本名为必填项")
	}
	if len([]rune(name)) > 64 {
		return nil, Fail(errcode.ErrRule, "版本名不得超过 64 字符")
	}
	tool := strings.TrimSpace(Str(data["tool_version"]))
	orch := strings.TrimSpace(Str(data["orchestrator_version"]))
	runtime := strings.TrimSpace(Str(data["runtime_version"]))
	etcd := strings.TrimSpace(Str(data["etcd_version"]))
	cniPlugin := strings.TrimSpace(Str(data["cni_plugin"]))
	cniVer := strings.TrimSpace(Str(data["cni_version"]))
	coreDNS := strings.TrimSpace(Str(data["coredns_version"]))
	tarball := strings.TrimSpace(Str(data["tarball_url"]))
	if tool == "" || orch == "" || runtime == "" || etcd == "" || cniPlugin == "" || cniVer == "" || coreDNS == "" || tarball == "" {
		return nil, Fail(errcode.ErrParam, "组件版本组六项、CoreDNS 版本与发行包地址均为必填项")
	}
	if err := checkSemver("交付工具版本", tool); err != nil {
		return nil, err
	}
	if err := checkOrchVer(orch); err != nil {
		return nil, err
	}
	if err := checkComponentVer("容器运行时版本", runtime); err != nil {
		return nil, err
	}
	if err := checkComponentVer("etcd 版本", etcd); err != nil {
		return nil, err
	}
	if err := ignDictRequire("IGN_CNI_PLUGIN", cniPlugin, "CNI 插件"); err != nil {
		return nil, err
	}
	if err := checkComponentVer("CNI 版本", cniVer); err != nil {
		return nil, err
	}
	if err := checkComponentVer("CoreDNS 版本", coreDNS); err != nil {
		return nil, err
	}
	if err := checkTarballURL(tarball); err != nil {
		return nil, err
	}
	if err := checkSha256Hex("发行包摘要", Str(data["tarball_sha256"])); err != nil {
		return nil, err
	}
	return map[string]any{
		"name":                 name,
		"display_name":         ignNullableStr(Str(data["display_name"])),
		"tool_version":         tool,
		"orchestrator_version": orch,
		"runtime_version":      runtime,
		"etcd_version":         etcd,
		"cni_plugin":           cniPlugin,
		"cni_version":          cniVer,
		"coredns_version":      coreDNS,
		"tarball_url":          tarball,
		"tarball_sha256":       ignNullableStr(strings.TrimSpace(Str(data["tarball_sha256"]))),
		"image_manifest":       ignDefaultManifest(Str(data["image_manifest"])),
		"default_registry_ref": ignNullableStr(strings.TrimSpace(Str(data["default_registry_ref"]))),
	}, nil
}

// ignDefaultManifest 镜像清单缺省为空数组文本。
func ignDefaultManifest(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "[]"
	}
	return raw
}

// CreateVersion FUNC-01 版本登记(状态置草稿;INV-01 保存即校验)。
func CreateVersion(data map[string]any, user UserInfo) (map[string]any, error) {
	norm, err := validateVersionInput(data)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	err = tx(func(db *gorm.DB) error {
		// INV-01:版本名未归档范围内唯一(唯一索引兜底;此处先行中文提示)
		dup, err := store.Count(db, `
			SELECT COUNT(*) FROM delivery_version WHERE flag = 1 AND version_status <> 'ARCHIVED' AND LOWER(name) = LOWER(?)`, norm["name"])
		if err != nil {
			return err
		}
		if dup > 0 {
			return Fail(errcode.ErrRule, "版本名 %s 在未归档范围内已存在（INV-01：保存即校验；已归档版本不占用名称）", norm["name"])
		}
		versionNo, err := genIgnNo(db, "DeliveryVersion", "delivery_version", "version_no")
		if err != nil {
			return err
		}
		if _, err := store.Exec(db, `
			INSERT INTO delivery_version
				(version_no, name, display_name, tool_version, orchestrator_version, runtime_version,
				 etcd_version, cni_plugin, cni_version, coredns_version, tarball_url, tarball_sha256,
				 image_manifest, default_registry_ref, version_status, created_by)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'DRAFT', ?)`,
			versionNo, norm["name"], norm["display_name"], norm["tool_version"], norm["orchestrator_version"],
			norm["runtime_version"], norm["etcd_version"], norm["cni_plugin"], norm["cni_version"],
			norm["coredns_version"], norm["tarball_url"], norm["tarball_sha256"],
			norm["image_manifest"], norm["default_registry_ref"], user.ID); err != nil {
			return err
		}
		row, err := store.One(db, `SELECT * FROM delivery_version WHERE version_no = ?`, versionNo)
		if err != nil {
			return err
		}
		result = row
		return nil
	})
	if err != nil {
		return nil, err
	}
	WriteAudit(nil, user.ID, user.Username, "VERSION_CREATE", "版本登记 "+Str(result["version_no"]))
	publishIgnEvent("version", "created", map[string]any{"version_no": Str(result["version_no"]), "name": Str(result["name"])})
	return result, nil
}

// GetVersion FUNC-07 版本详情(含镜像清单与预推状态汇总;REP-02)。
func GetVersion(versionID int64) (map[string]any, error) {
	row, err := qOne(`SELECT * FROM delivery_version WHERE id = ? AND flag = 1`, versionID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, NotFound("交付版本")
	}
	items, err := qList(`SELECT * FROM image_prepush_item WHERE version_id = ? AND flag = 1 ORDER BY repo, tag`, versionID)
	if err != nil {
		return nil, err
	}
	if items == nil {
		items = []map[string]any{}
	}
	row["prepush_items"] = items
	row["prepush_summary"] = ignPrepushSummary(versionID)
	row["manifest"] = ParseJSONText(Str(row["image_manifest"]), []any{})
	return row, nil
}

// ignPrepushSummary 预推汇总(按状态计数;REP-01 c5)。
func ignPrepushSummary(versionID int64) map[string]any {
	out := map[string]any{"total": int64(0)}
	rows, err := qList(`
		SELECT prepush_status, COUNT(*) AS n FROM image_prepush_item
		WHERE version_id = ? AND flag = 1 GROUP BY prepush_status`, versionID)
	if err != nil {
		return out
	}
	var total int64
	for _, r := range rows {
		n := toInt(r["n"])
		total += n
		out[Str(r["prepush_status"])] = n
	}
	out["total"] = total
	return out
}

// ListVersions FUNC-06 版本列表(REP-01:编号/版本名/组件版本组/状态/预推汇总/归档时间)。
func ListVersions(page, size int, filters map[string]any) (*Page, error) {
	where := "WHERE v.flag = 1"
	args := []any{}
	if v := Str(filters["status"]); v != "" {
		where += " AND v.version_status = ?"
		args = append(args, v)
	}
	if v := Str(filters["orchestrator_version"]); v != "" {
		where += " AND v.orchestrator_version LIKE ?"
		args = append(args, "%"+v+"%")
	}
	if v := Str(filters["cni_plugin"]); v != "" {
		where += " AND v.cni_plugin = ?"
		args = append(args, v)
	}
	if v := Str(filters["keyword"]); v != "" {
		where += " AND (v.version_no LIKE ? OR v.name LIKE ? OR COALESCE(v.display_name,'') LIKE ?)"
		kw := "%" + v + "%"
		args = append(args, kw, kw, kw)
	}
	total, err := qCount(`SELECT COUNT(*) FROM delivery_version v `+where, args...)
	if err != nil {
		return nil, err
	}
	rows, err := qList(`
		SELECT v.* FROM delivery_version v `+where+` ORDER BY v.id DESC LIMIT ? OFFSET ?`,
		append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	for _, r := range rows {
		r["manifest"] = ParseJSONText(Str(r["image_manifest"]), []any{})
	}
	return &Page{List: rows, Total: total, PageV: page, Size: size}, nil
}

// ListPublicVersions FUNC-08 跨中心版本查询(集群创建视图;REP-03 精简列表;仅已预推/部分预推可选)。
func ListPublicVersions(page, size int) (*Page, error) {
	total, err := qCount(`
		SELECT COUNT(*) FROM delivery_version
		WHERE flag = 1 AND version_status IN ('PREPUSHED','PARTIALLY_PREPUSHED')`)
	if err != nil {
		return nil, err
	}
	rows, err := qList(`
		SELECT id, version_no, display_name, orchestrator_version, runtime_version, cni_plugin,
		       default_registry_ref, version_status
		FROM delivery_version
		WHERE flag = 1 AND version_status IN ('PREPUSHED','PARTIALLY_PREPUSHED')
		ORDER BY id DESC LIMIT ? OFFSET ?`, size, (page-1)*size)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	return &Page{List: rows, Total: total, PageV: page, Size: size}, nil
}

// UpdateVersion FUNC-02 版本登记信息修改(INV-04 组件版本组创建后不可修改;INV-05 已归档冻结)。
func UpdateVersion(versionID int64, data map[string]any, user UserInfo) (map[string]any, error) {
	var result map[string]any
	err := tx(func(db *gorm.DB) error {
		row, err := store.One(db, `SELECT * FROM delivery_version WHERE id = ? AND flag = 1`, versionID)
		if err != nil {
			return err
		}
		if row == nil {
			return NotFound("交付版本")
		}
		status := Str(row["version_status"])
		// INV-05:已归档版本的登记字段冻结(终态冻结)
		if status == "ARCHIVED" {
			return Fail(errcode.ErrState, "版本已归档，登记字段冻结不可修改（INV-05：终态冻结）")
		}
		norm, err := validateVersionInput(data)
		if err != nil {
			return err
		}
		// INV-04:组件版本组(契约字段)创建后不可修改——会被选进集群配置
		for _, f := range ignContractFields {
			if Str(norm[f]) != Str(row[f]) {
				return Fail(errcode.ErrRule, "组件版本组（交付工具/编排引擎/运行时/etcd/CNI 插件与版本/CoreDNS）创建后不可修改（INV-04：会被选进集群配置的契约字段）")
			}
		}
		// INV-01:改名时的唯一性(排除自身)
		if strings.ToLower(Str(norm["name"])) != strings.ToLower(Str(row["name"])) {
			dup, err := store.Count(db, `
				SELECT COUNT(*) FROM delivery_version
				WHERE flag = 1 AND version_status <> 'ARCHIVED' AND LOWER(name) = LOWER(?) AND id <> ?`, norm["name"], versionID)
			if err != nil {
				return err
			}
			if dup > 0 {
				return Fail(errcode.ErrRule, "版本名 %s 在未归档范围内已存在（INV-01）", norm["name"])
			}
		}
		if _, err := store.Exec(db, `
			UPDATE delivery_version SET name=?, display_name=?, tarball_url=?, tarball_sha256=?,
			image_manifest=?, default_registry_ref=?, updated_by=?, updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS')
			WHERE id=?`,
			norm["name"], norm["display_name"], norm["tarball_url"], norm["tarball_sha256"],
			norm["image_manifest"], norm["default_registry_ref"], user.ID, versionID); err != nil {
			return err
		}
		out, err := store.One(db, `SELECT * FROM delivery_version WHERE id = ?`, versionID)
		if err != nil {
			return err
		}
		result = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	WriteAudit(nil, user.ID, user.Username, "VERSION_UPDATE", "版本登记信息修改 "+Str(result["version_no"]))
	return result, nil
}

// DownloadVersion FUNC-03 触发发行包下载(R-02:下载→摘要强校验→解压缓存→置就绪)。
// 跨域留痕:发行包仓库侧实际拉取为外部通道,本域登记状态机与校验结论;摘要不匹配即置失败可重下。
func DownloadVersion(versionID int64, user UserInfo) (map[string]any, error) {
	var result map[string]any
	err := tx(func(db *gorm.DB) error {
		row, err := store.One(db, `SELECT * FROM delivery_version WHERE id = ? AND flag = 1`, versionID)
		if err != nil {
			return err
		}
		if row == nil {
			return NotFound("交付版本")
		}
		status := Str(row["version_status"])
		// INV-07:状态迁移单向(DRAFT/FAILED → DOWNLOADING)
		if !canTransVersion(status, "DOWNLOADING") {
			return Fail(errcode.ErrState, "当前状态 %s 不可触发下载（INV-07：版本状态迁移路径固定单向，仅草稿或失败状态可触发）", status)
		}
		sha := Str(row["tarball_sha256"])
		if sha != "" && !ignHex64Re.MatchString(sha) {
			return Fail(errcode.ErrRule, "发行包摘要须为 64 位十六进制（R-02：提供即强校验）")
		}
		// R-02 实现口径:登记下载动作并置 DOWNLOADING;仓库侧实际拉取完成后的校验结论
		// 由对账任务回填(摘要提供即强校验、不匹配删除已下载文件重下;校验通过才写缓存路径并置就绪)。
		// 本域阶段三不代理真实下载通道,状态置 DOWNLOADING 并在载荷中留痕等待回流,不静默置就绪。
		if _, err := store.Exec(db, `
			UPDATE delivery_version SET version_status='DOWNLOADING', cache_path=NULL,
			updated_by=?, updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`,
			user.ID, versionID); err != nil {
			return err
		}
		out, err := store.One(db, `SELECT * FROM delivery_version WHERE id = ?`, versionID)
		if err != nil {
			return err
		}
		result = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	WriteAudit(nil, user.ID, user.Username, "VERSION_DOWNLOAD", "触发发行包下载 "+Str(result["version_no"])+"（R-02：下载完成判定待对账回流,不静默置就绪）")
	publishIgnEvent("version", "download_started", map[string]any{"version_no": Str(result["version_no"])})
	return result, nil
}

// ArchiveVersion FUNC-04 版本归档(READY/PREPUSHED/PARTIALLY_PREPUSHED → ARCHIVED;登记字段冻结)。
func ArchiveVersion(versionID int64, user UserInfo) (map[string]any, error) {
	var result map[string]any
	err := tx(func(db *gorm.DB) error {
		row, err := store.One(db, `SELECT * FROM delivery_version WHERE id = ? AND flag = 1`, versionID)
		if err != nil {
			return err
		}
		if row == nil {
			return NotFound("交付版本")
		}
		status := Str(row["version_status"])
		if !canTransVersion(status, "ARCHIVED") {
			return Fail(errcode.ErrState, "当前状态 %s 不可归档（INV-07：仅就绪/已预推/部分预推可归档；草稿与下载中版本应先完成或重试）", status)
		}
		// R-01:被引用版本不可删/归档前置——引用存在性由集群域声明、本域不直查他域表;
		// 跨中心引用核对通道阶段三未接入,归档动作如实留痕(归档不等于删除,可回滚)。
		if _, err := store.Exec(db, `
			UPDATE delivery_version SET version_status='ARCHIVED', archived_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS'),
			updated_by=?, updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`,
			user.ID, versionID); err != nil {
			return err
		}
		out, err := store.One(db, `SELECT * FROM delivery_version WHERE id = ?`, versionID)
		if err != nil {
			return err
		}
		result = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	WriteAudit(nil, user.ID, user.Username, "VERSION_ARCHIVE", "版本归档 "+Str(result["version_no"])+"（登记字段自此刻冻结,INV-05）")
	publishIgnEvent("version", "archived", map[string]any{"version_no": Str(result["version_no"])})
	return result, nil
}

// DeleteVersion FUNC-05 版本软删除(REF-08:须已归档且超过平台配置天数;R-01 引用保护提示)。
func DeleteVersion(versionID int64, user UserInfo) (map[string]any, error) {
	var result map[string]any
	err := tx(func(db *gorm.DB) error {
		row, err := store.One(db, `SELECT * FROM delivery_version WHERE id = ? AND flag = 1`, versionID)
		if err != nil {
			return err
		}
		if row == nil {
			return NotFound("交付版本")
		}
		if Str(row["version_status"]) != "ARCHIVED" {
			return Fail(errcode.ErrState, "仅已归档版本可软删除（REF-08：软删除须已归档且超过平台配置天数）")
		}
		archivedAt, ok := parseIgnTime(Str(row["archived_at"]))
		if !ok {
			return Fail(errcode.ErrState, "归档时间缺失，无法核算软删时限（REF-08）")
		}
		days := ignConfigInt(db, ignCfgSoftDeleteDays, 30)
		if elapsed := int64(timeSinceHours(archivedAt) / 24); elapsed < days {
			return Fail(errcode.ErrState, "归档后未满 %d 天，不可软删除（REF-08：当前已归档 %d 天）", days, elapsed)
		}
		// R-01:删除提示仓库侧镜像清理清单(跨域留痕;不阻断但明确提示)
		if _, err := store.Exec(db, `
			UPDATE delivery_version SET flag=0, updated_by=?, updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`,
			user.ID, versionID); err != nil {
			return err
		}
		out := map[string]any{
			"version_no":       row["version_no"],
			"name":             row["name"],
			"cleanup_hint":     "仓库侧镜像清理清单：请按镜像清单逐一核对预推镜像的清理（R-01 删除提示；跨中心清单同步通道未接入）",
			"default_registry": row["default_registry_ref"],
		}
		result = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	WriteAudit(nil, user.ID, user.Username, "VERSION_DELETE", "版本软删除 "+fmt.Sprint(result["version_no"])+"（R-01:已提示仓库侧镜像清理清单）")
	publishIgnEvent("version", "soft_deleted", map[string]any{"version_no": result["version_no"]})
	return result, nil
}

// ---------- 镜像预推(AGG-IGN-02;FUNC-09~12) ----------

// manifestItem 镜像清单条目(repo/tag/digest/size_bytes)。
func parseManifestItems(raw string) []map[string]any {
	parsed := ParseJSONText(raw, []any{})
	arr, _ := parsed.([]any)
	items := []map[string]any{}
	for _, it := range arr {
		if m, ok := it.(map[string]any); ok {
			items = append(items, m)
		}
	}
	return items
}

// StartPrepush FUNC-09 触发镜像预推(前置:版本就绪及以上;清单派生预推明细 INV-08)。
func StartPrepush(versionID int64, user UserInfo) (map[string]any, error) {
	var result map[string]any
	err := tx(func(db *gorm.DB) error {
		row, err := store.One(db, `SELECT * FROM delivery_version WHERE id = ? AND flag = 1`, versionID)
		if err != nil {
			return err
		}
		if row == nil {
			return NotFound("交付版本")
		}
		status := Str(row["version_status"])
		if status != "READY" && status != "PARTIALLY_PREPUSHED" && status != "FAILED" {
			return Fail(errcode.ErrState, "当前状态 %s 不可触发预推（前置阻断：仅就绪/部分预推/失败状态可预推；请先触发发行包下载并等待就绪）", status)
		}
		items := parseManifestItems(Str(row["image_manifest"]))
		if len(items) == 0 {
			return Fail(errcode.ErrRule, "镜像清单为空，无法预推（前置阻断：请先在「镜像清单人工覆写」登记镜像坐标）")
		}
		// INV-08:预推明细由镜像清单派生生成(清单覆写时同步对齐)
		for _, it := range items {
			repo := strings.TrimSpace(Str(it["repo"]))
			tag := strings.TrimSpace(Str(it["tag"]))
			if repo == "" || tag == "" {
				return Fail(errcode.ErrRule, "镜像清单存在缺少仓库或标签的条目（INV-08：仓库 + 标签为镜像坐标必填项）")
			}
			exists, err := store.One(db, `
				SELECT id FROM image_prepush_item WHERE version_id = ? AND repo = ? AND tag = ? AND flag = 1`,
				versionID, repo, tag)
			if err != nil {
				return err
			}
			if exists != nil {
				continue
			}
			if _, err := store.Exec(db, `
				INSERT INTO image_prepush_item (version_id, repo, tag, prepush_status, created_by)
				VALUES (?, ?, ?, 'PENDING', ?)`, versionID, repo, tag, user.ID); err != nil {
				return err
			}
		}
		target := "PREPUSHING"
		if status == "FAILED" {
			// INV-07:FAILED 重试后回归对应阶段
			target = "PREPUSHING"
		}
		if _, err := store.Exec(db, `
			UPDATE delivery_version SET version_status=?, updated_by=?, updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`,
			target, user.ID, versionID); err != nil {
			return err
		}
		out, err := store.One(db, `SELECT * FROM delivery_version WHERE id = ?`, versionID)
		if err != nil {
			return err
		}
		result = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	WriteAudit(nil, user.ID, user.Username, "PREPUSH_START", "触发镜像预推 "+Str(result["version_no"])+"（REF-04 并发上限热生效）")
	publishIgnEvent("prepush", "started", map[string]any{"version_no": Str(result["version_no"])})
	return result, nil
}

// OverwriteManifest FUNC-10 镜像清单人工覆写(INV-08:覆写时同步对齐预推明细)。
func OverwriteManifest(versionID int64, manifestText string, user UserInfo) (map[string]any, error) {
	manifestText = strings.TrimSpace(manifestText)
	if manifestText == "" {
		return nil, Fail(errcode.ErrParam, "镜像清单不得为空（覆写以整份清单为准；如需清空请逐条删除预推明细）")
	}
	if ParseJSONText(manifestText, nil) == nil {
		return nil, Fail(errcode.ErrParam, "镜像清单须为合法 JSON 数组（元素含 repo/tag/digest/size_bytes）")
	}
	var result map[string]any
	err := tx(func(db *gorm.DB) error {
		row, err := store.One(db, `SELECT * FROM delivery_version WHERE id = ? AND flag = 1`, versionID)
		if err != nil {
			return err
		}
		if row == nil {
			return NotFound("交付版本")
		}
		if Str(row["version_status"]) == "ARCHIVED" {
			return Fail(errcode.ErrState, "版本已归档，镜像清单冻结（INV-05：终态冻结）")
		}
		for _, it := range parseManifestItems(manifestText) {
			repo := strings.TrimSpace(Str(it["repo"]))
			tag := strings.TrimSpace(Str(it["tag"]))
			if repo == "" || tag == "" {
				return Fail(errcode.ErrRule, "镜像清单存在缺少仓库或标签的条目（INV-08）")
			}
			exists, err := store.One(db, `
				SELECT id FROM image_prepush_item WHERE version_id = ? AND repo = ? AND tag = ? AND flag = 1`,
				versionID, repo, tag)
			if err != nil {
				return err
			}
			digest := ignNullableStr(strings.TrimSpace(Str(it["digest"])))
			size := ignToInt(it["size_bytes"])
			if exists != nil {
				if _, err := store.Exec(db, `
					UPDATE image_prepush_item SET digest=?, size_bytes=?, updated_by=?,
					updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`,
					digest, nullableBigInt(size), user.ID, toInt(exists["id"])); err != nil {
					return err
				}
				continue
			}
			if _, err := store.Exec(db, `
				INSERT INTO image_prepush_item (version_id, repo, tag, digest, size_bytes, prepush_status, created_by)
				VALUES (?, ?, ?, ?, ?, 'PENDING', ?)`, versionID, repo, tag, digest, nullableBigInt(size), user.ID); err != nil {
				return err
			}
		}
		if _, err := store.Exec(db, `
			UPDATE delivery_version SET image_manifest=?, updated_by=?,
			updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`,
			manifestText, user.ID, versionID); err != nil {
			return err
		}
		out, err := store.One(db, `SELECT * FROM delivery_version WHERE id = ?`, versionID)
		if err != nil {
			return err
		}
		result = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	WriteAudit(nil, user.ID, user.Username, "MANIFEST_OVERWRITE", "镜像清单人工覆写 "+Str(result["version_no"])+"（INV-08:预推明细同步对齐）")
	return result, nil
}

// RetryPrepush FUNC-11 预推失败重试(断点续推只补失败;SKIPPED 视为已完成不重跑)。
func RetryPrepush(versionID int64, user UserInfo) (map[string]any, error) {
	var result map[string]any
	err := tx(func(db *gorm.DB) error {
		row, err := store.One(db, `SELECT * FROM delivery_version WHERE id = ? AND flag = 1`, versionID)
		if err != nil {
			return err
		}
		if row == nil {
			return NotFound("交付版本")
		}
		if Str(row["version_status"]) != "PARTIALLY_PREPUSHED" && Str(row["version_status"]) != "FAILED" {
			return Fail(errcode.ErrState, "仅部分预推或失败状态可断点续推（FUNC-11：只补失败明细，成功与跳过明细不重跑）")
		}
		if _, err := store.Exec(db, `
			UPDATE image_prepush_item SET prepush_status='PENDING', fail_reason=NULL,
			updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS')
			WHERE version_id = ? AND flag = 1 AND prepush_status = 'FAILED'`, versionID); err != nil {
			return err
		}
		if _, err := store.Exec(db, `
			UPDATE delivery_version SET version_status='PREPUSHING', updated_by=?,
			updated_at=to_char(now(),'YYYY-MM-DD HH24:MI:SS') WHERE id=?`, user.ID, versionID); err != nil {
			return err
		}
		out, err := store.One(db, `SELECT * FROM delivery_version WHERE id = ?`, versionID)
		if err != nil {
			return err
		}
		result = out
		return nil
	})
	if err != nil {
		return nil, err
	}
	WriteAudit(nil, user.ID, user.Username, "PREPUSH_RETRY", "预推失败重试（断点续推） "+Str(result["version_no"]))
	publishIgnEvent("prepush", "retried", map[string]any{"version_no": Str(result["version_no"])})
	return result, nil
}

// ListPrepushItems FUNC-12 预推明细查询(REP-04:镜像坐标/摘要/体积/状态/失败原因/最近预推时间)。
func ListPrepushItems(page, size int, filters map[string]any) (*Page, error) {
	where := "WHERE i.flag = 1"
	args := []any{}
	if v := ignToInt(filters["version_id"]); v != 0 {
		where += " AND i.version_id = ?"
		args = append(args, v)
	}
	if v := Str(filters["prepush_status"]); v != "" {
		where += " AND i.prepush_status = ?"
		args = append(args, v)
	}
	if v := Str(filters["version_no"]); v != "" {
		where += " AND EXISTS (SELECT 1 FROM delivery_version v WHERE v.id = i.version_id AND v.version_no = ?)"
		args = append(args, v)
	}
	total, err := qCount(`SELECT COUNT(*) FROM image_prepush_item i `+where, args...)
	if err != nil {
		return nil, err
	}
	rows, err := qList(`
		SELECT i.*, v.version_no, v.name AS version_name FROM image_prepush_item i
		JOIN delivery_version v ON v.id = i.version_id
		`+where+` ORDER BY i.id DESC LIMIT ? OFFSET ?`,
		append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	return &Page{List: rows, Total: total, PageV: page, Size: size}, nil
}
