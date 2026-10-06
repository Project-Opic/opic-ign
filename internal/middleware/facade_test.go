// facade_test.go —— 门面服务身份鉴权（双重鉴权的本域一侧）。
//
// 只测"令牌未配置"与"令牌不符"两条关闭路径：它们不得触达数据库，且必须早于任何业务处理；
// 令牌通过后的服务账号解析属 service 层（需真实库），由 internal/service 的门面用例覆盖。
package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gitcode.com/opic-ontology/opic-techbase/internal/config"
)

// newFacadeCtx 构造一次门面调用（Authorization 为空即不带头）。
func newFacadeCtx(t *testing.T, authorization string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/ign/delivery/submit", nil)
	if authorization != "" {
		c.Request.Header.Set("Authorization", authorization)
	}
	return c, w
}

func TestBearerTokenParsing(t *testing.T) {
	cases := []struct{ header, want string }{
		{"Bearer abc123", "abc123"},
		{"bearer abc123", "abc123"},
		{"   BEARER  abc123  ", "abc123"},
		{"Bearer\tabc123", "abc123"},
		{"Bearertoken", ""}, // 方案与令牌之间必须有分隔符，否则整个串被当成令牌
		{"token abc123", ""},
		{"Bearer", ""},
		{"", ""},
	}
	for _, c := range cases {
		ctx, _ := newFacadeCtx(t, c.header)
		if got := bearerToken(ctx); got != c.want {
			t.Errorf("Authorization=%q ⇒ %q，期望 %q", c.header, got, c.want)
		}
	}
}

// TestFacadeRequiredClosesWhenTokenUnset 令牌未配置 ⇒ 门面整体关闭：不存在"无令牌也能受理"的第三条路。
func TestFacadeRequiredClosesWhenTokenUnset(t *testing.T) {
	prev := config.Config.Delivery.FacadeToken
	config.Config.Delivery.FacadeToken = ""
	t.Cleanup(func() { config.Config.Delivery.FacadeToken = prev })

	c, w := newFacadeCtx(t, "Bearer anything")
	FacadeRequired()(c)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("HTTP %d，期望 401", w.Code)
	}
	if !strings.Contains(w.Body.String(), "交付门面未启用") {
		t.Errorf("中文口径缺失: %s", w.Body.String())
	}
	assertFlatRefuse(t, w.Body.String())
	if !c.IsAborted() {
		t.Error("门面未启用必须中止链路（不得继续进业务处理）")
	}
}

// assertFlatRefuse 拒绝体必须是 flat {return_code, error_msg}：上游适配器按这两字段取拒绝理由，
// 落成 {returnInfo:{...}} 信封会让它只看到空串（= 拒绝原因不可诊断）。
func assertFlatRefuse(t *testing.T, body string) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("拒绝体不是 JSON: %v (%s)", err, body)
	}
	if _, ok := m["return_code"]; !ok {
		t.Errorf("拒绝体缺 return_code: %s", body)
	}
	if _, ok := m["error_msg"]; !ok {
		t.Errorf("拒绝体缺 error_msg: %s", body)
	}
	if _, nested := m["returnInfo"]; nested {
		t.Errorf("拒绝体不得是统一信封（上游只解析 flat 字段）: %s", body)
	}
}

// TestFacadeRequiredRejectsWrongToken 令牌不符/缺失/方案错误一律 401，且不泄漏配置态。
func TestFacadeRequiredRejectsWrongToken(t *testing.T) {
	prev := config.Config.Delivery.FacadeToken
	config.Config.Delivery.FacadeToken = "correct-horse-battery"
	t.Cleanup(func() { config.Config.Delivery.FacadeToken = prev })

	for _, h := range []string{"", "Bearer wrong", "correct-horse-battery", "Bearer corect-horse-battery"} {
		c, w := newFacadeCtx(t, h)
		FacadeRequired()(c)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("Authorization=%q ⇒ HTTP %d，期望 401", h, w.Code)
		}
		if !c.IsAborted() {
			t.Errorf("Authorization=%q 未中止链路", h)
		}
		if strings.Contains(w.Body.String(), "未启用") {
			t.Errorf("Authorization=%q 泄漏配置态: %s", h, w.Body.String())
		}
		assertFlatRefuse(t, w.Body.String())
	}
}
