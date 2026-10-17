package auth

import (
	"bytes"
	"image/png"
	"testing"
	"time"
)

func TestVerifyCaptcha(t *testing.T) {
	const ip = "203.0.113.7"
	id := randomCode(16)
	captchaStore.m[id] = captchaEntry{answer: "Ab3d", expires: time.Now().Add(captchaTTL), ip: ip}

	// 大小写不敏感 + 首尾空白容忍
	if !VerifyCaptcha(id, " ab3D ", ip) {
		t.Fatal("正确验证码应通过(大小写不敏感)")
	}
	// 一次性:销毁后同码重放必须失败
	if VerifyCaptcha(id, "Ab3d", ip) {
		t.Fatal("验证码应一次性,重放须失败")
	}

	// 来源绑定:非领取来源提交必须失败
	id2 := randomCode(16)
	captchaStore.m[id2] = captchaEntry{answer: "XY7K", expires: time.Now().Add(captchaTTL), ip: ip}
	if VerifyCaptcha(id2, "XY7K", "203.0.113.8") {
		t.Fatal("来源不一致须失败")
	}

	// 过期
	id3 := randomCode(16)
	captchaStore.m[id3] = captchaEntry{answer: "XY7K", expires: time.Now().Add(-time.Minute), ip: ip}
	if VerifyCaptcha(id3, "XY7K", ip) {
		t.Fatal("过期验证码须失败")
	}

	// 空入参
	if VerifyCaptcha("", "ABCD", ip) || VerifyCaptcha("someid", "", ip) || VerifyCaptcha("someid", "ABCD", "") {
		t.Fatal("空 id/空码/空来源须失败")
	}
}

func TestCaptchaPNG(t *testing.T) {
	raw := renderCaptchaPNG("AB23")
	// 交付物为像素图:PNG 魔数 + 固定尺寸;答案不以任何文本节点/属性形态出现
	if len(raw) < 8 || !bytes.HasPrefix(raw, []byte{0x89, 'P', 'N', 'G'}) {
		t.Fatal("验证码交付物应为 PNG 像素图")
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("PNG 解码失败: %v", err)
	}
	if b := img.Bounds(); b.Dx() != 120 || b.Dy() != 40 {
		t.Fatalf("验证码尺寸=%v,应为 120x40", b)
	}
	if got := randomCode(4); len(got) != 4 {
		t.Fatalf("randomCode 长度=%d,应为 4", len(got))
	}
}

func TestRateWindow(t *testing.T) {
	w := newRateWindow(2, time.Minute)
	if w.exceeded("k") {
		t.Fatal("空窗口不应判超限")
	}
	if n := w.record("k"); n != 1 {
		t.Fatalf("计数=%d,应为 1", n)
	}
	w.record("k")
	if !w.exceeded("k") {
		t.Fatal("达到上限应判超限")
	}
	w.reset("k")
	if w.exceeded("k") {
		t.Fatal("reset 后不应超限")
	}
}
