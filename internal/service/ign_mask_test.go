// ign_mask_test.go —— 日志写前不可逆脱敏（R-16 / INV-40）。
package service

import (
	"strings"
	"testing"
)

func TestIgnMaskLine(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantMasked bool
		wantGot    string
	}{
		{"普通输出不改写", "TASK [etcd : start etcd] *************", false, "TASK [etcd : start etcd] *************"},
		{"密码赋值掩码", "etcdctl user add --password=P@ssw0rd root", true, ""},
		{"token 赋值掩码", "bootstrap token: 9n8d7c6b.5a4b3c2d1e0f", true, ""},
		{"下划线合成键名不得漏掩", "root_token=abc123def456", true, ""},
		{"点号前缀的口令字段不得漏掩", "etcd.passwd: S3cr3tValue", true, ""},
		{"私钥头掩码", "-----BEGIN RSA PRIVATE KEY-----", true, ""},
		{"Bearer 掩码", "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.aaaa.bbbb", true, ""},
		{"kubeconfig client-key-data 掩码", "client-key-data: LS0tLS1CRUdJTiBSU0Eg", true, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, masked := ignMaskLine(c.in)
			if masked != c.wantMasked {
				t.Fatalf("masked=%v want %v（%q）", masked, c.wantMasked, c.in)
			}
			if !c.wantMasked {
				if got != c.in {
					t.Fatalf("未命中掩码规则不得改写: %q", got)
				}
				return
			}
			if strings.Contains(got, c.in) && got != c.in {
				t.Fatalf("原文泄漏: %q", got)
			}
			if !strings.Contains(got, ignMaskMark) {
				t.Fatalf("应留掩码标记，实得 %q", got)
			}
			// 幂等：二次掩码不再产生新标记（INV-40 只存一份已脱敏数据）
			again, second := ignMaskLine(got)
			if second {
				t.Fatalf("二次掩码应为不命中，实得 %q", again)
			}
		})
	}
}

func TestIgnTruncateLine(t *testing.T) {
	long := strings.Repeat("字", 5000)
	got := ignTruncateLogLine(long)
	if n := len([]rune(got)); n > ignLogLineMaxRunes+32 {
		t.Fatalf("截断后仍超长: %d", n)
	}
	if !strings.Contains(got, "已截断") {
		t.Fatal("截断须可标注（REF-12）")
	}
}
