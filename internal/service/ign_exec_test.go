// ign_exec_test.go —— 本机执行位的命令面私有映射与 argv 渲染（R-13 / INV-36 / R-15）。
package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gitcode.com/opic-ontology/opic-techbase/internal/config"
)

// withDeliveryConfig 临时替换部署配置中的命令面挂载值（1.6.1：路径只以配置值存在）。
func withDeliveryConfig(t *testing.T, bin, root string) {
	t.Helper()
	old := config.Config.Delivery
	config.Config.Delivery.ToolBin = bin
	config.Config.Delivery.ToolRoot = root
	config.Config.Delivery.WorkDir = root
	t.Cleanup(func() { config.Config.Delivery = old })
}

func TestResolveFacadeAction(t *testing.T) {
	cases := []struct {
		name       string
		actionCode string
		stepCode   string
		wantAction string
		wantStep   string
		wantReject bool
	}{
		{"安装类步骤编码即安装族单步", "", "03", "ezctl.setup", "03", false},
		{"安装类与动作码同源自洽", "ezctl.setup", "05", "ezctl.setup", "05", false},
		{"安装类与动作码冲突即治理拒绝", "ezctl.start", "05", "", "", true},
		{"步骤编码 08 越界即治理拒绝", "", "08", "", "", true},
		{"上游类别名映射单步动作", "", "DESTROY", "ezctl.destroy", "", false},
		{"上游类别名映射加减节点", "", "ADD_NODE", "ezctl.add-node", "", false},
		{"本域动作码直传", "ezctl.ca-renew", "CERT_RENEW", "ezctl.ca-renew", "", false},
		{"类别名与动作码不一致即拒绝", "ezctl.stop", "DESTROY", "", "", true},
		{"两者皆空即拒绝", "", "", "", "", true},
		{"白名单外动作即治理拒绝", "rm -rf", "", "", "", true},
		// R-13：白名单内但不属交付动作层 ⇒ 同样拒绝（内部前置层 / 平台运维层）
		{"内部前置层不出现在门面", "ezctl.new", "", "", "", true},
		{"平台运维层不出现在门面", "scripts.image_prepush", "", "", "", true},
		{"工作目录骨架不可被上游请求", "", "NEW", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			action, step, err := ignResolveFacadeAction(c.actionCode, c.stepCode)
			if c.wantReject {
				if err == nil {
					t.Fatalf("应治理拒绝，实得 action=%q step=%q", action, step)
				}
				if !strings.Contains(err.Error(), "R-13") && !strings.Contains(err.Error(), "R-12") {
					t.Fatalf("拒绝理由须登记规则号，实得 %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("受理解析失败: %v", err)
			}
			if action != c.wantAction || step != c.wantStep {
				t.Fatalf("got %q/%q want %q/%q", action, step, c.wantAction, c.wantStep)
			}
		})
	}
}

func TestBuildArgv(t *testing.T) {
	bin := "/opt/kubeasz/ezctl"
	t.Run("安装类为 setup 集群 步骤", func(t *testing.T) {
		got, err := ignBuildArgv(bin, "ezctl.setup", "prod-01", "02", nil, "")
		if err != nil {
			t.Fatal(err)
		}
		want := []string{bin, "setup", "prod-01", "02"}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Fatalf("got %v want %v", got, want)
		}
	})
	t.Run("节点增删携带地址清单", func(t *testing.T) {
		got, err := ignBuildArgv(bin, "ezctl.add-node", "prod-01", "", []string{"10.0.0.7", "10.0.0.8"}, "")
		if err != nil {
			t.Fatal(err)
		}
		want := []string{bin, "add-node", "prod-01", "10.0.0.7", "10.0.0.8"}
		if strings.Join(got, " ") != strings.Join(want, " ") {
			t.Fatalf("got %v want %v", got, want)
		}
	})
	t.Run("节点增删缺地址清单即拒绝", func(t *testing.T) {
		if _, err := ignBuildArgv(bin, "ezctl.del-node", "prod-01", "", nil, ""); err == nil {
			t.Fatal("节点增删类动作必须携带地址清单")
		}
	})
	t.Run("非法地址即拒绝", func(t *testing.T) {
		if _, err := ignBuildArgv(bin, "ezctl.add-node", "prod-01", "", []string{"10.0.0.7; rm -rf /"}, ""); err == nil {
			t.Fatal("非法节点地址应被拒绝")
		}
	})
	t.Run("非法集群标识即拒绝", func(t *testing.T) {
		if _, err := ignBuildArgv(bin, "ezctl.setup", "../etc", "01", nil, ""); err == nil {
			t.Fatal("非法集群标识应被拒绝")
		}
	})
	t.Run("非节点类动作不得带地址清单", func(t *testing.T) {
		if _, err := ignBuildArgv(bin, "ezctl.start", "prod-01", "", []string{"10.0.0.7"}, ""); err == nil {
			t.Fatal("非节点类动作该字段必须为空")
		}
	})
	t.Run("安装类步骤编码越界即拒绝", func(t *testing.T) {
		if _, err := ignBuildArgv(bin, "ezctl.setup", "prod-01", "09", nil, ""); err == nil {
			t.Fatal("步骤编码须命中 01~07（INV-36）")
		}
	})
	t.Run("人工路径附加参数逐项校验", func(t *testing.T) {
		got, err := ignBuildArgv(bin, "ezctl.kcfg-adm", "prod-01", "", nil, "--user=ops")
		if err != nil {
			t.Fatal(err)
		}
		if got[len(got)-1] != "--user=ops" {
			t.Fatalf("白名单内参数应逐项进入 argv，实得 %v", got)
		}
		if _, err := ignBuildArgv(bin, "ezctl.kcfg-adm", "prod-01", "", nil, "a|b"); err == nil {
			t.Fatal("含拼接风险字符的参数须治理拒绝（R-12）")
		}
	})
}

func TestLocalExecAvailable(t *testing.T) {
	t.Run("未配置挂载即可用性为假（R-15）", func(t *testing.T) {
		withDeliveryConfig(t, "", "")
		ok, reason := ignLocalExecAvailable()
		if ok || !strings.Contains(reason, "未配置") {
			t.Fatalf("got %v/%q", ok, reason)
		}
	})
	t.Run("路径不存在即可用性为假", func(t *testing.T) {
		withDeliveryConfig(t, filepath.Join(t.TempDir(), "nope"), "")
		if ok, _ := ignLocalExecAvailable(); ok {
			t.Fatal("挂载缺失应判不可用")
		}
	})
	t.Run("非可执行即可用性为假", func(t *testing.T) {
		bin := filepath.Join(t.TempDir(), "ezctl")
		if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		withDeliveryConfig(t, bin, filepath.Dir(bin))
		if ok, _ := ignLocalExecAvailable(); ok {
			t.Fatal("不可执行应判不可用")
		}
	})
	t.Run("可执行即可用", func(t *testing.T) {
		dir := t.TempDir()
		bin := filepath.Join(dir, "ezctl")
		if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		withDeliveryConfig(t, bin, dir)
		if ok, reason := ignLocalExecAvailable(); !ok {
			t.Fatalf("应判可用，实得 %v", reason)
		}
	})
}

func TestEnsureClusterWorkDir(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "ezctl")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	withDeliveryConfig(t, bin, dir)
	if err := ignEnsureClusterWorkDir("prod-01"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "clusters", "prod-01")); err != nil {
		t.Fatalf("工作目录未按登记事实对齐: %v", err)
	}
	// 幂等可重入（B-AUTO-15）：第二次不再触发骨架命令
	if err := ignEnsureClusterWorkDir("prod-01"); err != nil {
		t.Fatal(err)
	}
}
