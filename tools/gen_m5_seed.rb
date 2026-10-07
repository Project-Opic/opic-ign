# 由 m5-actor-model.yaml + m2-behavior-model.yaml 生成 goose 迁移的 M5 种子 Up 段。
# 用法: ruby tools/gen_m5_seed.rb <models_dir> [perm_code] > 新 Up 段
#   perm_code = perm_id(默认，sys_permission.code 直接取 m5 permissionId)
#             | prefix:<域码>(code 取 "<域码>:<m2 行为号>"，与路由层 RequirePermission 口径一致)
require 'yaml'

dir = ARGV[0] or abort('usage: gen_m5_seed.rb <models_dir> [perm_code]')
mode = ARGV[1] || 'perm_id'
prefix = nil
if mode =~ /\Aprefix:(.+)\z/
  prefix = Regexp.last_match(1)
elsif mode != 'perm_id'
  abort("unknown perm_code: #{mode}")
end

m2 = YAML.load_file(File.join(dir, 'm2-behavior-model.yaml'))
m5 = YAML.load_file(File.join(dir, 'm5-actor-model.yaml'))
bname = {}
m2['behaviors'].each { |b| bname[b['id']] = b['name'] }

def q(s)
  "'" + s.to_s.gsub("'", "''") + "'"
end

# perm_code m5 permissionId → sys_permission.code(运行期鉴权用的就是这一列)
def perm_code(p, prefix)
  return p['permissionId'] if prefix.nil?

  "#{prefix}:#{p['targetRef']}"
end

puts '-- +goose Up'
puts '-- M5 种子:业务权限码 + 业务角色 + 角色权限绑定(本段由 m5-actor-model.yaml + m2-behavior-model.yaml 生成，勿手改)'
puts '-- 编号说明:本件原误编 000005，与同序号的交付内核 DDL(000005_*)同号，goose 收集迁移时直接 panic'
puts '-- (duplicate version)，干净库首次部署起不来;种子逻辑上晚于交付内核 DDL，故顺排为 000006。'
puts '-- target_ref 取 m5 permissions[].targetRef(即 m2 行为号 FUNC-nn)，不是权限码本身;'
if prefix
  puts "-- sys_permission.code = \"#{prefix}:<m2 行为号>\"(路由层 RequirePermission 与本文件的绑定行都按这一列匹配)，"
  puts '-- m5 permissions[].permissionId 仍是 PERM-FUNC-nn(模型侧编号，不落库)。'
end
puts '-- 运行期 internal/service/seed.go 的 syncM5Authorization 走同一口径，两边互为幂等重复。'
puts ''
puts '-- 角色(m5 roles[])'
m5['roles'].sort_by { |r| r['roleId'] }.each do |r|
  puts "INSERT INTO sys_role (code, name) SELECT #{q(r['roleId'])}, #{q(r['name'])} WHERE NOT EXISTS (SELECT 1 FROM sys_role WHERE code=#{q(r['roleId'])} OR name=#{q(r['name'])});"
end
puts ''
puts '-- 权限码(m5 permissions[]，权限名取 m2 行为名)'
m5['permissions'].sort_by { |p| p['permissionId'] }.each do |p|
  ref = p['targetRef']
  name = bname[ref] || ref
  scope = p['dataScope'] || 'ALL'
  code = perm_code(p, prefix)
  puts "INSERT INTO sys_permission (code, name, target_type, target_ref, data_scope) SELECT #{q(code)}, #{q(name)}, #{q(p['targetType'] || 'BEHAVIOR')}, #{q(ref)}, #{q(scope)} WHERE NOT EXISTS (SELECT 1 FROM sys_permission WHERE code=#{q(code)});"
end
puts ''
puts '-- 角色权限绑定(m5 roles[].permissions)'
by_id = {}
m5['permissions'].each { |p| by_id[p['permissionId']] = p }
m5['roles'].sort_by { |r| r['roleId'] }.each do |r|
  (r['permissions'] || []).sort.each do |pc|
    p = by_id[pc] or abort("角色 #{r['roleId']} 绑定的 #{pc} 不在 m5 permissions[] 全集")
    code = perm_code(p, prefix)
    puts "INSERT INTO sys_role_permission (role_id, permission_id) SELECT r.id, p.id FROM sys_role r, sys_permission p WHERE r.code=#{q(r['roleId'])} AND p.code=#{q(code)} AND NOT EXISTS (SELECT 1 FROM sys_role_permission rp WHERE rp.role_id=r.id AND rp.permission_id=p.id);"
  end
end
