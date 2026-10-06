// facade_test.go —— 门面 HTTP 层的响应形态（flat JSON + 非 200）。
//
// 这层是 clc 适配器能否看到真实拒绝原因的唯一依赖：业务错误在本中心一律回 HTTP 200
// （common.FailJSON 信封），门面必须自己把语义错误翻成状态码，否则上游只见空引用（O-CLC 的 INV-46）。
package business

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"gitcode.com/opic-ontology/opic-techbase/internal/service"
	"gitcode.com/opic-ontology/opic-techbase/pkg/errcode"
)

func TestFacadeFailStatusMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name string
		code errcode.ErrCode
		want int
	}{
		{"治理拒绝（Z1）", errcode.ErrForbidden, http.StatusForbidden},
		{"请求形态非法", errcode.ErrParam, http.StatusBadRequest},
		{"规则口径不符", errcode.ErrRule, http.StatusBadRequest},
		{"执行位不可用与容量（Z2）", errcode.ErrState, http.StatusConflict},
		{"流程状态冲突", errcode.ErrFlowState, http.StatusConflict},
		{"唯一性冲突", errcode.ErrDuplicate, http.StatusConflict},
		{"远端引用未命中", errcode.ErrNotFound, http.StatusNotFound},
		{"配置缺失", errcode.ErrConfig, http.StatusInternalServerError},
		{"数据库错误", errcode.ErrDB, http.StatusInternalServerError},
		{"未登记码一律 500", errcode.ErrTaskNotOwner, http.StatusInternalServerError},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(w)
		facadeFail(ctx, service.Fail(c.code, "中文原因：%s", c.name))
		if w.Code != c.want {
			t.Errorf("%s: HTTP %d，期望 %d", c.name, w.Code, c.want)
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: 响应非 JSON: %v", c.name, err)
		}
		if _, ok := body["returnInfo"]; ok {
			t.Errorf("%s: 门面不得回常规信封（上游按 flat JSON 反序列化）", c.name)
		}
		msg, _ := body["error_msg"].(string)
		if !strings.Contains(msg, "中文原因："+c.name) {
			t.Errorf("%s: error_msg 未回中文原因: %q（上游靠它显示拒绝理由）", c.name, msg)
		}
		if code, _ := body["return_code"].(string); code != string(c.code) {
			t.Errorf("%s: return_code %q，期望 %q", c.name, body["return_code"], c.code)
		}
	}
}
