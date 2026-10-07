# encoding: utf-8
# 七模型一致性门禁（M1/M2/M3/M5/M6/M7/MU 交叉引用复算）
#   用法: ruby tools/check_model_gate.rb <models_dir> [新屏 screenId,逗号分隔]
#   第 2 个参数是"本轮新增的屏"—— 它们的 dataBinding 首段必须是 M1 alias（硬性 FAIL）；
#   未列出的屏沿用历史字段名风格，只出 WARN 不误伤既有交付链。
require 'yaml'

dir = ARGV[0]
m1 = YAML.load_file(File.join(dir, 'm1-object-model.yaml'))
m2 = YAML.load_file(File.join(dir, 'm2-behavior-model.yaml'))
m3 = YAML.load_file(File.join(dir, 'm3-rule-model.yaml'))
m5 = YAML.load_file(File.join(dir, 'm5-actor-model.yaml'))
m6 = YAML.load_file(File.join(dir, 'm6-flow-model.yaml'))
m7 = YAML.load_file(File.join(dir, 'm7-report-model.yaml'))
mu = YAML.load_file(File.join(dir, 'mu-ui-model.yaml'))

puts "versions: m1=#{m1['version']} m2=#{m2['version']} m3=#{m3['version']} m5=#{m5['version']} m6=#{m6['version']} m7=#{m7['version']} mu=#{mu['version']}"

behaviors = m2['behaviors']
b_ids = behaviors.map { |b| b['id'] }
rule_ids = m3['rules'].map { |r| r['id'] }
perm_ids = m5['permissions'].map { |p| p['permissionId'] }
role_ids = m5['roles'].map { |r| r['roleId'] }
rep_ids = m7['query_reports'].map { |r| r['id'] }
agg_ids = m1['aggregates'].map { |a| a['id'] }
ent_names = []
m1['aggregates'].each do |a|
  ent_names << a['alias']
  (a['entities'] || []).each { |e| ent_names << e['alias'] }
end
flow_ids = m6['flows'].map { |f| f['id'] }

fails = []

# --- M2 self checks
dup = b_ids.group_by { |x| x }.select { |_, v| v.size > 1 }.keys
fails << "M2 duplicate behavior ids: #{dup.join(',')}" unless dup.empty?
ua = behaviors.select { |b| b['triggerType'] == 'USER_ACTION' }
qy = behaviors.select { |b| b['behaviorType'] == 'QUERY' }
puts "M2: behaviors=#{b_ids.size} USER_ACTION=#{ua.size} SYSTEM=#{behaviors.size - ua.size} QUERY=#{qy.size} COMMAND=#{behaviors.size - qy.size}"

# --- MU
mu_acts = []
mu['screens'].each { |s| (s['actions'] || []).each { |a| mu_acts << [s['screenId'], a] } }
mu_brefs = mu_acts.map { |_, a| a['behaviorRef'] }
mu_bref_set = {}
mu_brefs.each { |r| mu_bref_set[r] = true }
missing_b = mu_brefs.uniq.reject { |r| b_ids.include?(r) }
fails << "MU behaviorRef missing in M2: #{missing_b.join(',')}" unless missing_b.empty?
uncovered = ua.map { |b| b['id'] }.reject { |id| mu_bref_set.key?(id) }
fails << "M2 USER_ACTION not referenced by MU: #{uncovered.join(',')}" unless uncovered.empty?
missing_perm = mu_acts.flat_map { |_, a| a['permissionRef'] || [] }.uniq.reject { |p| perm_ids.include?(p) }
fails << "MU permissionRef missing in M5: #{missing_perm.join(',')}" unless missing_perm.empty?
screen_ids = mu['screens'].map { |s| s['screenId'] }
menu_refs = []
mu['application']['menus'].each do |m|
  (m['children'] || []).each { |c| menu_refs << c['screenRef'] }
end
bad_menu = menu_refs.reject { |s| screen_ids.include?(s) }
fails << "menu screenRef missing: #{bad_menu.join(',')}" unless bad_menu.empty?
orphan = screen_ids.reject { |s| menu_refs.include?(s) || s.start_with?('frmPublic') }
puts "MU: screens=#{screen_ids.size} actions=#{mu_acts.size} menuLeafRefs=#{menu_refs.size} unmenued=#{orphan.join(',')}"
bad_ds = mu['screens'].flat_map { |s| (s['elements'] || []).map { |e| [s['screenId'], e['id'], e['dataSource']] } }
bad_ds = bad_ds.select { |_, _, d| d && !d.to_s.empty? }.reject { |_, _, d| rep_ids.include?(d) }
fails << "MU dataSource not in M7: #{bad_ds.map { |s, e, d| "#{s}/#{e}=#{d}" }.join(',')}" unless bad_ds.empty?

# --- M2 -> M1 / M3 / M5
bad_owner = behaviors.reject { |b| agg_ids.include?(b['ownerEntity']) || b['ownerEntity'].to_s.start_with?('AGG-DERIVED') }
fails << "M2 ownerEntity not an M1 aggregate: #{bad_owner.map { |b| "#{b['id']}=#{b['ownerEntity']}" }.join(',')}" unless bad_owner.empty?
bad_rules = behaviors.flat_map { |b| (b['appliedRules'] || []).map { |r| [b['id'], r] } }.reject { |_, r| rule_ids.include?(r) }
fails << "M2 appliedRules missing in M3: #{bad_rules.map { |b, r| "#{b}=#{r}" }.join(',')}" unless bad_rules.empty?
bad_perms = behaviors.flat_map { |b| (b['requiredPermissions'] || []).map { |p| [b['id'], p] } }.reject { |_, p| perm_ids.include?(p) }
fails << "M2 requiredPermissions missing in M5: #{bad_perms.map { |b, p| "#{b}=#{p}" }.join(',')}" unless bad_perms.empty?

# --- M5 -> M2 / roles
dangling = m5['permissions'].reject { |p| b_ids.include?(p['targetRef']) }
fails << "M5 permission targetRef missing in M2: #{dangling.map { |p| p['permissionId'] }.join(',')}" unless dangling.empty?
ungranted = m5['permissions'].select { |p| !m5['roles'].any? { |r| (r['permissions'] || []).include?(p['permissionId']) } }
puts "M5: perms=#{perm_ids.size} ungranted=#{ungranted.map { |p| p['permissionId'] }.join(',')}(expect FUNC-28 family none)" unless ungranted.empty?
bad_role_perm = m5['roles'].flat_map { |r| (r['permissions'] || []).map { |p| [r['roleId'], p] } }.reject { |_, p| perm_ids.include?(p) }
fails << "M5 role permission missing: #{bad_role_perm.join(',')}" unless bad_role_perm.empty?
bad_actor_role = m5['actors'].flat_map { |a| (a['roles'] || []).map { |r| [a['actorId'], r] } }.reject { |_, r| role_ids.include?(r) }
fails << "M5 actor role missing: #{bad_actor_role.join(',')}" unless bad_actor_role.empty?
# role-perm target exists in M2, and cross-check role grants vs behavior perms
grant_by_b = {}
m5['permissions'].each { |p| grant_by_b[p['targetRef']] = p['permissionId'] }
no_grant = ua.map { |b| b['id'] }.reject { |b| grant_by_b.key?(b) }
fails << "M2 USER_ACTION behaviors with no PERM defined: #{no_grant.join(',')}" unless no_grant.empty?

# --- M7 <-> M2 (1:1)
rep_by_behavior = {}
m7['query_reports'].each do |r|
  fails << "M7 #{r['id']} duplicate behaviorRef #{r['behaviorRef']}" if rep_by_behavior.key?(r['behaviorRef'])
  rep_by_behavior[r['behaviorRef']] = r['id']
end
bad_m7_b = rep_by_behavior.keys.reject { |b| b_ids.include?(b) }
fails << "M7 behaviorRef missing in M2: #{bad_m7_b.join(',')}" unless bad_m7_b.empty?
nonquery = rep_by_behavior.keys.select { |b| behaviors.find { |x| x['id'] == b }['behaviorType'] != 'QUERY' }
fails << "M7 bound to non-QUERY behavior: #{nonquery.join(',')}" unless nonquery.empty?
declared = {}
qy.each do |b|
  ref = b['queryReportRef']
  if ref.nil? || ref.to_s.empty?
    puts "NOTE: #{b['id']} declares no report (exception registered: #{b.key?('reportException')})"
  else
    declared[b['id']] = ref
    fails << "#{b['id']} queryReportRef #{ref} not in M7" unless rep_ids.include?(ref)
    fails << "#{b['id']} says #{ref} but M7 #{ref} points back to #{rep_by_behavior[b['id']].inspect}" if rep_by_behavior[b['id']] != ref
  end
end
unbound = rep_ids.reject { |r| declared.values.include?(r) }
fails << "M7 reports not bound by any M2: #{unbound.join(',')}" unless unbound.empty?
puts "M7: reports=#{rep_ids.size} bound=#{declared.size} QUERY behaviors=#{qy.size}"

# --- M6 references + acyclicity
m6_bad_role = []; m6_bad_beh = []; m6_bad_flow = []; m6_bad_rule = []
m6['flows'].each do |f|
  (f['roleRefs'] || []).each { |r| m6_bad_role << "#{f['id']}:#{r}" unless role_ids.include?(r) }
  (f['businessObjectRefs'] || []).each { |o| fails << "M6 #{f['id']} bad boRef #{o}" unless agg_ids.include?(o) }
  (f['activities'] || []).each do |a|
    m6_bad_role << "#{f['id']}/#{a['activityId']}:#{a['roleRef']}" if a['roleRef'] && !role_ids.include?(a['roleRef'])
    m6_bad_beh << "#{f['id']}/#{a['activityId']}:#{a['behaviorRef']}" if a['behaviorRef'] && !b_ids.include?(a['behaviorRef'])
    m6_bad_flow << "#{f['id']}/#{a['activityId']}:#{a['subFlowRef']}" if a['subFlowRef'] && !flow_ids.include?(a['subFlowRef'])
    m6_bad_rule << "#{f['id']}/#{a['activityId']}:#{a['ruleRef']}" if a['ruleRef'] && !rule_ids.include?(a['ruleRef'])
  end
end
fails << "M6 roleRef unresolved: #{m6_bad_role.join(',')}" unless m6_bad_role.empty?
fails << "M6 behaviorRef unresolved: #{m6_bad_beh.join(',')}" unless m6_bad_beh.empty?
fails << "M6 subFlowRef unresolved: #{m6_bad_flow.join(',')}" unless m6_bad_flow.empty?
fails << "M6 ruleRef unresolved: #{m6_bad_rule.join(',')}" unless m6_bad_rule.empty?
# activity internal refs
m6['flows'].each do |f|
  acts = (f['activities'] || []).map { |a| a['activityId'] }
  d = acts.group_by { |x| x }.select { |_, v| v.size > 1 }.keys
  fails << "M6 #{f['id']} duplicate activityId: #{d.join(',')}" unless d.empty?
  (f['activities'] || []).each do |a|
    nxt = (a['nextActivities'] || []) + (a['branches'] || []).map { |br| br['targetActivity'] }.compact
    bad = nxt.reject { |t| acts.include?(t) }
    fails << "M6 #{f['id']}/#{a['activityId']} unresolved next: #{bad.join(',')}" unless bad.empty?
  end
  [f['startActivity'], *(f['endActivities'] || [])].each do |t|
    fails << "M6 #{f['id']} endpoint #{t} not in activities" unless acts.include?(t)
  end
end
# subflow graph cycle
sub = {}
m6['flows'].each do |f|
  lst = []
  (f['activities'] || []).each { |a| lst << a['subFlowRef'] if a['subFlowRef'] }
  sub[f['id']] = lst.uniq
end
cyc = []
visit = lambda do |node, stack|
  return if stack.include?(node) && false
  sub[node].each do |t|
    if stack.include?(t)
      cyc << (stack[stack.index(t)..-1].push(t)).join(' -> ')
    else
      visit.call(t, stack + [t])
    end
  end
end
sub.each_key { |k| visit.call(k, [k]) }
fails << "M6 subflow cycle: #{cyc.uniq.join(' | ')}" unless cyc.empty?
puts "M6: flows=#{m6['flows'].size} activities=#{m6['flows'].sum { |f| (f['activities'] || []).size }}"
sub.each { |k, v| puts "  #{k} -> [#{v.join(', ')}]" unless v.empty? }

# --- M3 self + M1
dupr = rule_ids.group_by { |x| x }.select { |_, v| v.size > 1 }.keys
fails << "M3 duplicate rule ids: #{dupr.join(',')}" unless dupr.empty?
bad_rtype = m3['rules'].reject { |r| %w[FORMAT INVARIANT CROSS_OBJECT].include?(r['ruleType']) }
fails << "M3 bad ruleType: #{bad_rtype.map { |r| r['id'] }.join(',')}" unless bad_rtype.empty?
m2_used = behaviors.flat_map { |b| b['appliedRules'] || [] }.uniq
orphan_rules = rule_ids.reject { |r| m2_used.include?(r) }
m6_used = []
m6['flows'].each { |f| (f['activities'] || []).each { |a| m6_used << a['ruleRef'] if a['ruleRef'] } }
orphan_rules = orphan_rules.reject { |r| m6_used.include?(r) }
puts "M3: rules=#{rule_ids.size} unreferenced=#{orphan_rules.size} [#{orphan_rules.join(',')}]"
# M1 checks
dupa = agg_ids.group_by { |x| x }.select { |_, v| v.size > 1 }.keys
fails << "M1 duplicate aggregate ids: #{dupa.join(',')}" unless dupa.empty?
dict_ids = (m1['data_dictionaries'] || []).map { |d| d['id'] }
puts "M1: aggs=#{agg_ids.size} dicts=#{dict_ids.size} associations=#{(m1['aggregate_associations'] || []).size}"
# attribute refs used in MU dataBinding top-level object must exist
bind_objs = mu['screens'].flat_map { |s| (s['elements'] || []).map { |e| [s['screenId'], e['dataBinding'].to_s.split('.')[0]] } }
bind_objs = bind_objs.uniq.reject { |_, o| o.nil? || o.empty? }
ascii_bind = bind_objs.select { |_, o| o =~ /\A[A-Za-z][A-Za-z0-9_]*\z/ }
bad_bind = ascii_bind.reject { |_, o| ent_names.include?(o) }
new_screens = (ARGV[1] || '').split(',')
hard = bad_bind.select { |s, _| new_screens.include?(s) }
warn = bad_bind.reject { |s, _| new_screens.include?(s) }
fails << "MU dataBinding alias unknown in M1 (new screens): #{hard.map { |s, o| "#{s}=#{o}" }.join(',')}" unless hard.empty?
puts "WARN legacy bindings (v1.0/v1.1 inherited, field-name style): #{warn.map { |s, o| "#{s}=#{o}" }.join(', ')}" unless warn.empty?
prose_bind = bind_objs.size - ascii_bind.size
puts "M1: aliases=#{ent_names.uniq.size}; MU bindings ascii=#{ascii_bind.size} prose=#{prose_bind} unresolved=#{bad_bind.size}"

puts '----'
if fails.empty?
  puts 'GATE: PASS (no unresolved reference)'
else
  puts "GATE: FAIL #{fails.size}"
  fails.each { |f| puts " - #{f}" }
end
