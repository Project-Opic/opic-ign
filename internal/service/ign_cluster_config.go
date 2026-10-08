// ign_cluster_config.go —— 集群交付配置屏与跨中心只读通道的服务入口（FUNC-45~50，AGG-IGN-08）。
//
// 四层取值一律走 resolveParamFace（R-20 唯一实现），三态判定一律走 ignInventoryState，
// 形态校验一律走面定义内的 Check / ignValidateParamFace / ignValidateDeployParams（R-23）——
// 显示、预检、受理读同一份结论；本文件不复制任何判定逻辑。
//
// 两列纪律（R-21，本批要消灭的就是「已保存 vs 已生效」混淆）：
//   - 「下次将生效」= 四层取值的生效值（覆写只改这一列，不重写已落盘文件、不触发派发）；
//   - 「当前落盘」= clusters/<ref> 工作目录里该键的回读事实（ignFieldOnDisk）；
//   - 两列在任何返回结构里都是两个字段，取数失败显示「不可用 + 原因」，不以空值代替（报表口径 2）。
package service

import (
	"fmt"
	"strings"

	"gorm.io/gorm"

	"gitcode.com/opic-ontology/opic-techbase/internal/store"
	"gitcode.com/opic-ontology/opic-techbase/pkg/errcode"
)

// ignCheckClusterRefForFace 集群标识形态校验（INV-33：跨域只存标识，但标识必须可定位）。
func ignCheckClusterRefForFace(clusterRef string) error {
	if strings.TrimSpace(clusterRef) == "" {
		return Fail(errcode.ErrParam, "必须选定集群（FUNC-46 前置：集群交付配置以集群为天然坐标之一）")
	}
	if !ignClusterIDRe.MatchString(clusterRef) {
		return Fail(errcode.ErrParam, "关联集群标识 %q 不符合格式白名单（INV-33）", clusterRef)
	}
	return nil
}

// ignCellAppliedDisplay 追溯两列的显示口径：无值时显式「未应用」，不给空单元格（REP-17 c5/c6）。
func ignCellAppliedDisplay(cell paramLayerCell) map[string]any {
	if strings.TrimSpace(cell.TaskNo) == "" {
		return map[string]any{"applied_task_no": nil, "applied_at": nil, "applied_display": "未应用"}
	}
	return map[string]any{"applied_task_no": cell.TaskNo, "applied_at": cell.AppliedAt, "applied_display": "已应用"}
}

// ignFieldFaceCells 单字段的四层轨迹（FUNC-50 与 FUNC-46 共用形状：值 / 命中层 / 来源标识 / 说明）。
func ignFieldFaceCells(tr paramFieldTrace) []map[string]any {
	cell := func(layer, label string, c paramLayerCell) map[string]any {
		return map[string]any{
			"layer": layer, "layer_label": label,
			"value": c.Value, "hit": c.Hit, "source_ref": c.SourceRef,
			"note": c.Note, "status": c.Status, "reason": c.Reason,
		}
	}
	return []map[string]any{
		cell(ignSrcOverride, ignSourceLabels[ignSrcOverride], tr.OverrideValue),
		cell(ignSrcVersion, ignSourceLabels[ignSrcVersion], tr.VersionValue),
		cell(ignSrcTemplate, ignSourceLabels[ignSrcTemplate], tr.TemplateValue),
		cell(ignSrcPlatform, ignSourceLabels[ignSrcPlatform], tr.PlatformDefault),
	}
}

// ignTraceSourceRef 命中层的来源标识（覆写坐标 / 模板编号 / 版本引用 / 平台配置键）。
func ignTraceSourceRef(tr paramFieldTrace) string {
	switch tr.EffectiveSource {
	case ignSrcOverride:
		return tr.OverrideValue.SourceRef
	case ignSrcVersion:
		return tr.VersionValue.SourceRef
	case ignSrcTemplate:
		return tr.TemplateValue.SourceRef
	}
	return tr.PlatformDefault.SourceRef
}

// 原文流出来源标识（审计与回吐里区分「人工在本域看」还是「上游代理读」；
// 两者共用同一份读盘 + 同一套掩码 + 同一逐次留痕，只有口径标注不同——FUNC-45/FUNC-50 同源）。
const (
	ignRawOriginLocal  = "本域人工视图(FUNC-45)"
	ignRawOriginFacade = "上游代理只读(FUNC-50)"
)

// ignRawFaceReadout 原文只读视图的共同内核（FUNC-45 人工视图与 FUNC-50 代理通道共用）：
// 同一份落盘文本读取 + 同一套 R-16 凭据掩码（整行掩码，片段掩码仍可拼回）+ 逐次审计。
// origin 只影响留痕标注与回吐 notice，不影响文本口径——两条通道读出的行必须逐字节一致，
// 各写一份读盘/掩码逻辑就必然漂移。文件缺失回 ErrState「尚未生成」结论，不以空文本报现状。
func ignRawFaceReadout(clusterRef, fileFace string, user UserInfo, origin, notice string) (map[string]any, error) {
	if err := ignCheckClusterRefForFace(clusterRef); err != nil {
		return nil, err
	}
	if err := ignCheckTemplateFace(fileFace); err != nil {
		return nil, err
	}
	name := "hosts"
	if fileFace == ignFaceConfig {
		name = "config.yml"
	}
	text, err := ignReadClusterFile(ignClusterDir(clusterRef), name)
	if err != nil {
		return nil, Fail(errcode.ErrState,
			"clusters/%s/%s 不可读（交付配置尚未生成）：原文视图只呈现已落盘事实，请先完成一次交付受理", clusterRef, name)
	}
	rawLines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	lines := make([]map[string]any, 0, len(rawLines))
	var maskedCount int64
	for i, l := range rawLines {
		masked, hit := ignMaskLine(l)
		if hit {
			maskedCount++
		}
		lines = append(lines, map[string]any{"no": i + 1, "text": masked})
	}
	WriteAudit(nil, user.ID, user.Username, "IGN_RAW_FACE_READ", fmt.Sprintf(
		"清单原文只读视图：%s / %s（%d 行，凭据掩码命中 %d 行）来源=%s", clusterRef, fileFace, len(lines), maskedCount, origin))
	return map[string]any{
		"cluster_ref":       clusterRef,
		"file_face":         fileFace,
		"file_face_label":   ignDictLabel("IGN_CONFIG_FILE_FACE", fileFace),
		"file_name":         name,
		"line_count":        len(lines),
		"masked_line_count": maskedCount,
		"content_sha256":    ignSHA256(text), // 摘要取掩码前原文——两条通道对同一份落盘文本必须给出同一摘要
		"lines":             lines,
		"read_origin":       origin,
		"notice":            notice,
	}, nil
}

// GetIgnClusterRawFile FUNC-45 清单原文只读视图：带行号整份文本、可复制、无编辑态、
// 无下载导出；渲染前过 R-16 同一套凭据形态识别，命中即整行掩码并回吐告警计数；
// 每次读取写一条审计（本域唯一「读也留痕」的视图，R-26）。
func GetIgnClusterRawFile(clusterRef, fileFace string, user UserInfo) (map[string]any, error) {
	return ignRawFaceReadout(clusterRef, fileFace, user, ignRawOriginLocal,
		"只读视图：不提供编辑态、不提供下载导出（R-26）；写入口是「集群交付配置」屏的字段级覆写（FUNC-47，另一张牌）；命中 R-16 凭据形态的行已整行掩码")
}

// ReadIgnDeliveryConfigRaw FUNC-50 跨中心原文只读通道（挂在门面组，服务身份调用）：
// 与 FUNC-45 同一份读盘、同一套掩码、同一逐次审计，只多出来源标注；
// file_face 必带且只出一面（越界/缺参给中文提示）；磁盘无文件时回「尚未生成」ErrState 结论。
func ReadIgnDeliveryConfigRaw(clusterRef, fileFace string, user UserInfo) (map[string]any, error) {
	fileFace = strings.ToUpper(strings.TrimSpace(fileFace))
	if fileFace == "" {
		return nil, Fail(errcode.ErrParam,
			"必须携带 file_face 且只出一面：只能是 HOSTS（主机清单原文）或 CONFIG（config.yml 原文）——一次只取一面，缺参即拒（FUNC-50 原文通道口径）")
	}
	return ignRawFaceReadout(clusterRef, fileFace, user, ignRawOriginFacade,
		"跨中心原文只读通道：不提供编辑态；凭据形态的行已在流出前由本域整行掩码（对端不二次解析）；来源已逐次落审计（区分本域人工视图与上游代理读）")
}

// GetIgnClusterConfigView FUNC-46 集群交付配置查看：必须选定集群 + 文件面（一次只看一轴）；
// 逐字段一行、四层并列、生效来源徽章必现；「当前落盘」与「下次将生效」两列不合并；
// 面标识为空 ⇒ 模板列整列显示跳过说明，不回落到默认模板（R-20 分支 H）；
// 冲突态由受理侧同一判定回吐并叠加「已保存 · 未生效」标注（R-24，界面不自行推断）。
func GetIgnClusterConfigView(clusterRef, fileFace string, refs map[string]any, user UserInfo) (map[string]any, error) {
	clusterRef = strings.TrimSpace(clusterRef)
	fileFace = strings.ToUpper(strings.TrimSpace(fileFace))
	if err := ignCheckClusterRefForFace(clusterRef); err != nil {
		return nil, err
	}
	if err := ignCheckTemplateFace(fileFace); err != nil {
		return nil, err
	}
	reqTpl, reqVer := "", ""
	if refs != nil {
		reqTpl = strings.TrimSpace(Str(refs["param_template_ref"]))
		reqVer = strings.TrimSpace(Str(refs["version_ref"]))
	}
	tplRef, verRef := ignFaceRequestContext(clusterRef, reqTpl, reqVer)
	view, err := resolveParamFace(store.DB, clusterRef, fileFace, tplRef, verRef)
	if err != nil {
		return nil, err
	}
	state, stateNote, err := ignInventoryState(clusterRef)
	if err != nil {
		state, stateNote = "", "三态探测不可用："+err.Error()
	}
	fields := make([]map[string]any, 0, len(view.Traces))
	for _, tr := range view.Traces {
		f, _ := ignParamFieldOf(tr.FieldKey)
		onDisk, diskNote := ignFieldOnDisk(clusterRef, f)
		out := map[string]any{
			"field_key":              tr.FieldKey,
			"ref_id":                 tr.RefID,
			"label":                  tr.Label,
			"file_face":              tr.FileFace,
			"file_face_label":        ignDictLabel("IGN_CONFIG_FILE_FACE", tr.FileFace), // 跨中心代理屏(frmClcWizard 配置页签)据此出中文,不再抄第三份轴表
			"value_shape":            tr.ValueShape,
			"platform_default":       tr.PlatformDefault,
			"param_template":         tr.TemplateValue,
			"version_derived":        tr.VersionValue,
			"override":               tr.OverrideValue,
			"next_effective_value":   tr.EffectiveValue, // 「下次将生效」列（R-21）
			"effective_source":       tr.EffectiveSource,
			"effective_source_label": tr.EffectiveSourceLabel,
			"source_ref":             ignTraceSourceRef(tr),
			"on_disk_value":          onDisk, // 「当前落盘」列——与上一列两个字段，不合并
			"on_disk_note":           diskNote,
		}
		for k, v := range ignCellAppliedDisplay(tr.OverrideValue) {
			out[k] = v
		}
		fields = append(fields, out)
	}
	out := map[string]any{
		"cluster_ref":                  clusterRef,
		"file_face":                    fileFace,
		"file_face_label":              ignDictLabel("IGN_CONFIG_FILE_FACE", fileFace),
		"requested_param_template_ref": tplRef,
		"hit_param_template_ref":       view.ParamTemplateRef,
		"param_template_notice":        view.TemplateNotice,
		"requested_version_ref":        verRef,
		"version_notice":               view.VersionNotice,
		"version_status":               view.VersionStatus,
		"inventory_state":              state,
		"inventory_note":               stateNote,
		"fields":                       fields,
	}
	if state == ignStateConflict {
		out["save_effective_note"] = ignConflictUnsaved
	}
	return out, nil
}

// ignResolveOverrideCoord 覆写坐标入参归一（FUNC-47/49 共用）：集群 + 文件面 + 字段键，
// 键必须落在面定义内且与本轴同面（INV-41/INV-42）。
func ignResolveOverrideCoord(data map[string]any) (string, string, ignParamField, error) {
	clusterRef := strings.TrimSpace(Str(data["cluster_ref"]))
	fileFace := strings.ToUpper(strings.TrimSpace(Str(data["file_face"])))
	key := strings.TrimSpace(Str(data["field_key"]))
	if err := ignCheckClusterRefForFace(clusterRef); err != nil {
		return "", "", ignParamField{}, err
	}
	if err := ignCheckTemplateFace(fileFace); err != nil {
		return "", "", ignParamField{}, err
	}
	f, ok := ignParamFieldOf(key)
	if !ok {
		return "", "", ignParamField{}, ignGovernanceRefuse(fmt.Sprintf(
			"字段键 %q 不在参数面定义（REF-17~25）内（INV-41：面外键是治理拒绝，不是表单校验错误）", key))
	}
	if f.FileFace != fileFace {
		return "", "", ignParamField{}, ignGovernanceRefuse(fmt.Sprintf(
			"字段键 %q 属%s，与所选文件面 %s 不同轴（覆写坐标的「集群+文件面+字段键」三元组必须自洽，INV-42）",
			key, ignTemplateFaceLabel(f.FileFace), ignTemplateFaceLabel(fileFace)))
	}
	return clusterRef, fileFace, f, nil
}

// SaveIgnClusterConfigOverride FUNC-47 覆写保存：单字段粒度、事由必填；保存即新建或改写
// 该坐标当前态（EFFECTIVE），已回落行再覆写即回到生效（两态可往复）；不重写已落盘文件、
// 不触发派发——只改「下次将生效」列（R-21），追溯对在真正被消费前保持为空。
func SaveIgnClusterConfigOverride(data map[string]any, user UserInfo) (map[string]any, error) {
	clusterRef, fileFace, f, err := ignResolveOverrideCoord(data)
	if err != nil {
		return nil, err
	}
	reason := strings.TrimSpace(Str(data["reason"]))
	if reason == "" {
		return nil, Fail(errcode.ErrParam, "覆写事由必填（治理事实必须说为什么改，与「显式降级要留痕」同纪律）")
	}
	raw, has := data["value"]
	if body, ok := data["override_value"]; !has && ok {
		raw = body
	}
	value := Str(raw)
	if f.Shape == ignShapeList {
		value = ignListText(ignListItems(value))
	} else {
		value = strings.TrimSpace(value)
	}
	if strings.TrimSpace(value) == "" {
		return nil, Fail(errcode.ErrParam,
			"覆写值不能为空：要让该键回落下层取值请使用「覆写回落」（FUNC-49），保存空值不是回落入口（R-21/R-22：列表值为整体替换后的全量值）")
	}
	if err := ignValidateParamFace([]ignParamField{f}, map[string]string{f.Key: value}); err != nil {
		return nil, err
	}
	var row map[string]any
	now := ignNow()
	err = tx(func(db *gorm.DB) error {
		existing, err := store.One(db, `SELECT * FROM ign_cluster_config_override
			WHERE flag = 1 AND cluster_ref = ? AND file_face = ? AND field_key = ?`,
			clusterRef, fileFace, f.Key)
		if err != nil {
			return err
		}
		if existing != nil {
			// 改写当前态；旧追溯对清空——那是上一次取值被消费的事实，不代表本次覆写
			_, err = store.Exec(db, `UPDATE ign_cluster_config_override
				SET override_value = ?, value_shape = ?, override_status = 'EFFECTIVE', reason = ?,
				    overridden_by = ?, overridden_at = ?, applied_task_no = NULL, applied_at = NULL,
				    rolled_back_at = NULL, updated_by = ?, updated_at = ?
				WHERE id = ?`,
				value, f.Shape, reason, user.Username, now, user.ID, now, toInt(existing["id"]))
			if err != nil {
				return err
			}
			row, err = store.One(db, `SELECT * FROM ign_cluster_config_override WHERE id = ?`, toInt(existing["id"]))
			return err
		}
		if _, err := store.Exec(db, `INSERT INTO ign_cluster_config_override
			(cluster_ref, file_face, field_key, override_value, value_shape, override_status, reason,
			 overridden_by, overridden_at, created_by)
			VALUES (?, ?, ?, ?, ?, 'EFFECTIVE', ?, ?, ?, ?)`,
			clusterRef, fileFace, f.Key, value, f.Shape, reason, user.Username, now, user.ID); err != nil {
			return err
		}
		var e2 error
		row, e2 = store.One(db, `SELECT * FROM ign_cluster_config_override
			WHERE flag = 1 AND cluster_ref = ? AND file_face = ? AND field_key = ?`,
			clusterRef, fileFace, f.Key)
		return e2
	})
	if err != nil {
		return nil, err
	}
	WriteAudit(nil, user.ID, user.Username, "IGN_OVR_SAVE", fmt.Sprintf(
		"覆写保存 %s/%s/%s = %q：事由 %s", clusterRef, fileFace, f.Key, value, reason))
	out := map[string]any{"override": row, "notice": "已保存：只改「下次将生效」列，未重写已落盘文件、未触发派发（R-21）"}
	view, verr := GetIgnClusterConfigView(clusterRef, fileFace, nil, user)
	if verr == nil {
		for _, fld := range view["fields"].([]map[string]any) {
			if Str(fld["field_key"]) == f.Key {
				out["next_effective_value"] = fld["next_effective_value"]
				out["effective_source"] = fld["effective_source"]
				out["effective_source_label"] = fld["effective_source_label"]
				out["on_disk_value"] = fld["on_disk_value"]
				out["on_disk_note"] = fld["on_disk_note"]
				break
			}
		}
		if Str(view["inventory_state"]) == ignStateConflict {
			out["save_effective_note"] = ignConflictUnsaved
		}
	}
	return out, nil
}

// ValidateIgnClusterConfig FUNC-48 校验（dry-run 预检）：判定次序固定 请求形态（R-13）→
// 版本可用（R-19）→ 参数面形态 → 三态归属（R-18）；取值上下文与受理侧同样回落到接管标记快照，
// 放行判定直接调受理侧那个 ignFaceEffectiveUsable（门槛 + 形态收口一处实现），三态调
// ignInventoryState，五不：不写盘 / 不建任务 / 不占并发额度 / 不改覆写行 / 不进受理台账
// （R-23，流程侧 PROC-07 M 分支）。结论与真实受理一致——预检与受理各写一套逻辑必然漂移，
// 漂移后的预检就是误导源。
func ValidateIgnClusterConfig(data map[string]any, user UserInfo) (map[string]any, error) {
	clusterRef := strings.TrimSpace(Str(data["cluster_ref"]))
	fileFace := strings.ToUpper(strings.TrimSpace(Str(data["file_face"])))
	if fileFace == ignFaceHosts || fileFace == ignFaceConfig {
		// 预检的是整张渲染面：file_face 只作输出过滤轴，不参与取值收窄
	} else if fileFace != "" {
		return nil, Fail(errcode.ErrParam, "文件面 %q 非法：只能是 HOSTS / CONFIG 或留空（整张渲染面）", fileFace)
	}
	out := map[string]any{"cluster_ref": clusterRef, "file_face": fileFace, "reasons": []string{}}
	reasons := out["reasons"].([]string)
	refuse := func(err error) {
		reasons = append(reasons, AsBizErr(err).Msg)
	}
	// ① 请求形态（R-13）：集群标识是硬前提，形态即断——与受理侧 ignAccept 的白名单校验同一结论
	if err := ignCheckClusterRefForFace(clusterRef); err != nil {
		out["passed"] = false
		out["stage"] = "请求形态"
		out["reasons"] = append(reasons, AsBizErr(err).Msg)
		out["fields"] = []map[string]any{}
		return out, nil
	}
	tplRef, verRef := ignFaceRequestContext(clusterRef,
		strings.TrimSpace(Str(data["param_template_ref"])), strings.TrimSpace(Str(data["version_ref"])))
	view, err := resolveParamFace(store.DB, clusterRef, "", tplRef, verRef)
	if err != nil {
		return nil, err
	}
	// ③ 参数面形态：待保存的覆写候选（overrides）逐键过面定义校验器（与 FUNC-47 同一实现），
	// 命中即合进生效面副本；收口校验与受理侧共用 ignFaceEffectiveUsable（门槛 + 形态各一处实现），
	// 于是"预检说会生效"与"受理真落盘"对同一输入必然同一结论（R-23）
	p := view.Params
	var pending []map[string]any
	if items, ok := data["overrides"].([]any); ok {
		for _, it := range items {
			m, ok := it.(map[string]any)
			if !ok {
				reasons = append(reasons, "覆写候选必须是对象数组（field_key + value），本次提交形态非法（R-13）")
				break
			}
			key := strings.TrimSpace(Str(m["field_key"]))
			f, ok := ignParamFieldOf(key)
			if !ok {
				reasons = append(reasons, fmt.Sprintf(
					"覆写候选字段键 %q 不在参数面定义（REF-17~25）内（INV-41：面外键是治理拒绝）", key))
				continue
			}
			value := Str(m["value"])
			if f.Shape == ignShapeList {
				value = ignListText(ignListItems(value))
			} else {
				value = strings.TrimSpace(value)
			}
			if err := ignValidateParamFace([]ignParamField{f}, map[string]string{f.Key: value}); err != nil {
				reasons = append(reasons, AsBizErr(err).Msg)
				continue
			}
			f.Set(&p, value)
			pending = append(pending, map[string]any{"field_key": key, "value": value})
		}
	}
	if err := ignFaceEffectiveUsable(view, p); err != nil {
		refuse(err)
	}
	// ④ 三态归属（R-18）：与受理侧同一判定；CONFLICT ⇒ 带清单的重写受理即刻拒绝（结论回吐，不另猜）
	state, stateNote, err := ignInventoryState(clusterRef)
	if err != nil {
		reasons = append(reasons, AsBizErr(err).Msg)
	}
	traces := make([]map[string]any, 0, len(view.Traces))
	for _, tr := range view.Traces {
		if fileFace != "" && tr.FileFace != fileFace {
			continue
		}
		traces = append(traces, map[string]any{
			"field_key": tr.FieldKey, "ref_id": tr.RefID, "label": tr.Label, "file_face": tr.FileFace,
			"value_shape": tr.ValueShape, "effective_value": tr.EffectiveValue,
			"effective_source": tr.EffectiveSource, "effective_source_label": tr.EffectiveSourceLabel,
			"layers": ignFieldFaceCells(tr),
		})
	}
	out["passed"] = len(reasons) == 0
	out["stage"] = "参数面形态"
	out["reasons"] = reasons
	out["fields"] = traces
	out["param_template_ref"] = tplRef
	out["param_template_notice"] = view.TemplateNotice
	out["version_ref"] = verRef
	out["version_notice"] = view.VersionNotice
	out["inventory_state"] = state
	out["inventory_note"] = stateNote
	out["would_reject_on_rewrite"] = state == ignStateConflict
	out["pending_overrides"] = pending
	out["notice"] = "dry-run：未写盘、未建任务、未占并发额度、未改覆写行、未进受理台账（R-23）；判定次序与受理侧一致"
	return out, nil
}

// RollbackIgnClusterConfigOverride FUNC-49 覆写回落：只对生效行执行（已回落的坐标已不在
// 任何层上）；列表值一键回落整项。状态翻面 ROLLED_BACK + 回填 rolledBackAt，同时清空
// applied_task_no/applied_at（INV-42：留着旧追溯对就分不清「曾经生效」与「现在正生效」）；
// 回落不删行——行保留才能让「这个集群曾经改过什么」可查；取值回落到下层（R-20）。
func RollbackIgnClusterConfigOverride(data map[string]any, user UserInfo) (map[string]any, error) {
	clusterRef, fileFace, f, err := ignResolveOverrideCoord(data)
	if err != nil {
		return nil, err
	}
	var row map[string]any
	now := ignNow()
	err = tx(func(db *gorm.DB) error {
		existing, err := store.One(db, `SELECT * FROM ign_cluster_config_override
			WHERE flag = 1 AND cluster_ref = ? AND file_face = ? AND field_key = ?`,
			clusterRef, fileFace, f.Key)
		if err != nil {
			return err
		}
		if existing == nil {
			return NotFound("该坐标的覆写行")
		}
		if Str(existing["override_status"]) != ignOverrideEffective {
			return Fail(errcode.ErrState,
				"该覆写已处于回落态（回落按钮只对「生效」行渲染：已回落的坐标已不在任何层上，重复回落无意义）")
		}
		if _, err := store.Exec(db, `UPDATE ign_cluster_config_override
			SET override_status = 'ROLLED_BACK', rolled_back_at = ?,
			    applied_task_no = NULL, applied_at = NULL,
			    overridden_by = ?, overridden_at = ?, updated_by = ?, updated_at = ?
			WHERE id = ?`,
			now, user.Username, now, user.ID, now, toInt(existing["id"])); err != nil {
			return err
		}
		var e2 error
		row, e2 = store.One(db, `SELECT * FROM ign_cluster_config_override WHERE id = ?`, toInt(existing["id"]))
		return e2
	})
	if err != nil {
		return nil, err
	}
	WriteAudit(nil, user.ID, user.Username, "IGN_OVR_ROLLBACK", fmt.Sprintf(
		"覆写回落 %s/%s/%s（追溯对已同步清空，INV-42；行保留以可查曾改过什么）", clusterRef, fileFace, f.Key))
	out := map[string]any{"override": row,
		"notice": "已回落：取值回落到下层（覆写 > 版本派生 > 模板 > 缺省面次序不破，R-20）；行未删除"}
	view, verr := GetIgnClusterConfigView(clusterRef, fileFace, nil, user)
	if verr == nil {
		for _, fld := range view["fields"].([]map[string]any) {
			if Str(fld["field_key"]) == f.Key {
				out["next_effective_value"] = fld["next_effective_value"]
				out["effective_source"] = fld["effective_source"]
				out["effective_source_label"] = fld["effective_source_label"]
				break
			}
		}
	}
	return out, nil
}

// ignMarkOverridesApplied INV-42 / R-21 的落点：B-AUTO-15 真产出或刷新交付配置后，把本次
// **实际命中并生效**的覆写行追溯对成对回填为当次任务标识。判据只有一条 —— 生效来源是覆写层
// （view.OverrideKeys）：值为空的行根本不参与取值、已回落行被求值器排除、被更高优先层压过的行
// 生效来源不是覆写层，都不回填（留着旧追溯对就分不清「曾经生效」与「现在正生效」）。
// 与 FUNC-49 回落、FUNC-47 再覆写的清空动作合起来，构成 applied_task_no / applied_at 的成对语义。
// 返回未能回填的坐标数（行在受理窗口内被回落或删除）：调用方据此留痕，不静默吞掉。
func ignMarkOverridesApplied(db *gorm.DB, applied *ignFaceApplied, taskNo string) (int, error) {
	if applied == nil || !applied.Wrote || applied.Face == nil {
		return 0, nil
	}
	taskNo = strings.TrimSpace(taskNo)
	if taskNo == "" || len(applied.Face.OverrideKeys) == 0 {
		return 0, nil
	}
	now := ignNow()
	var missing int
	for _, coord := range applied.Face.OverrideKeys {
		fileFace, fieldKey, ok := strings.Cut(coord, "|")
		if !ok || fileFace == "" || fieldKey == "" {
			continue
		}
		n, err := store.Exec(db, `UPDATE ign_cluster_config_override
			SET applied_task_no = ?, applied_at = ?, updated_at = ?
			WHERE flag = 1 AND cluster_ref = ? AND file_face = ? AND field_key = ?
			  AND override_status = 'EFFECTIVE'`,
			taskNo, now, now, applied.ClusterRef, fileFace, fieldKey)
		if err != nil {
			return missing, err
		}
		if n == 0 {
			missing++
		}
	}
	return missing, nil
}

// ReadIgnDeliveryConfig FUNC-50 跨中心交付配置只读通道（挂在门面组，服务身份调用）：
// 上游经本通道读四层取值结果，只出不入、无副作用——不产生任务、不改覆写行、
// 不下发模板内容、不下发原文文本（原文走本域 FUNC-45 的独立权限码）。
// 显示侧语义：面标识 / 版本引用不可用时不拒绝回答，而是把「为什么不命中」随结果回吐。
func ReadIgnDeliveryConfig(clusterRef, fileFace, paramTemplateRef, versionRef string) (map[string]any, error) {
	clusterRef = strings.TrimSpace(clusterRef)
	fileFace = strings.ToUpper(strings.TrimSpace(fileFace))
	if err := ignCheckClusterRefForFace(clusterRef); err != nil {
		return nil, err
	}
	if fileFace != "" {
		if err := ignCheckTemplateFace(fileFace); err != nil {
			return nil, err
		}
	}
	paramTemplateRef, versionRef = ignFaceRequestContext(clusterRef, paramTemplateRef, versionRef)
	view, err := resolveParamFace(store.DB, clusterRef, fileFace, paramTemplateRef, versionRef)
	if err != nil {
		return nil, err
	}
	fields := make([]map[string]any, 0, len(view.Traces))
	for _, tr := range view.Traces {
		applied := ignCellAppliedDisplay(tr.OverrideValue)
		fields = append(fields, map[string]any{
			"field_key": tr.FieldKey,
			"ref_id":    tr.RefID,
			"label":     tr.Label,
			"file_face": tr.FileFace,
			// 跨中心代理屏(frmClcWizard 配置页签及其复用视图)的轴名一律取本字段,下游不抄第二份轴表。
			// FUNC-46 本域视图早已有之;本通道(FUNC-50)曾漏 ⇒ 对端只能回吐码值,补齐两条通道同口径。
			"file_face_label": ignDictLabel("IGN_CONFIG_FILE_FACE", tr.FileFace),
			"value_shape":     tr.ValueShape,
			"value":           tr.EffectiveValue, // 生效值 = 下次受理将生效值（R-21）
			"hit_layer":       tr.EffectiveSource,
			"hit_layer_label": tr.EffectiveSourceLabel,
			"source_ref":      ignTraceSourceRef(tr), // 来源标识（模板编号 / 版本引用 / 覆写坐标 / 平台配置键）
			"next_effective":  true,                  // 本通道回吐的即「下次将生效」取值
			"applied_task_no": applied["applied_task_no"],
			"applied_at":      applied["applied_at"],
			"applied_display": applied["applied_display"],
			"layers":          ignFieldFaceCells(tr), // 逐层：值 / 命中 / 来源标识 / 说明
		})
	}
	out := map[string]any{
		"cluster_ref":           clusterRef,
		"file_face":             fileFace,
		"file_face_label":       ignDictLabel("IGN_CONFIG_FILE_FACE", fileFace),
		"param_template_ref":    paramTemplateRef,
		"param_template_notice": view.TemplateNotice,
		"version_ref":           versionRef,
		"version_notice":        view.VersionNotice,
		"fields":                fields,
		"notice":                "只读通道：反复调用无副作用；本路径只回取值结论、不含模板内容（R-26）；原文形态走同组 /config/:clusterRef/raw（必带 file_face，同一套掩码与逐次留痕）；写入口仅在本域「集群交付配置」屏",
	}
	if view.versionErr != nil {
		out["version_governance_refuse"] = AsBizErr(view.versionErr).Msg
	}
	if view.templateErr != nil {
		out["template_governance_refuse"] = AsBizErr(view.templateErr).Msg
	}
	return out, nil
}
