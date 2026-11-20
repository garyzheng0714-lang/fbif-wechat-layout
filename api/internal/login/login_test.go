package login

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequire(t *testing.T) {
	var got User
	h := Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got, _ = Current(r) }))
	header := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	for _, c := range []struct {
		name, header string
		code         int
	}{
		{"入口认过", header(`{"id":"FJY632B9P3E:ou_a","name":"张三","company_code":"FJY632B9P3E"}`), 200},
		{"没有头", "", 401},
		{"不是 base64url", "%%%", 401},
		{"带 = 的 base64", base64.URLEncoding.EncodeToString([]byte(`{"id":"x"}`)), 401},
		{"没有 id", header(`{"name":"张三"}`), 401},
		{"不是 JSON", header(`张三`), 401},
	} {
		t.Run(c.name, func(t *testing.T) {
			got = User{}
			r := httptest.NewRequest("GET", "/api/tools", nil)
			if c.header != "" {
				r.Header.Set(Header, c.header)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != c.code {
				t.Fatalf("status %d, want %d (%s)", w.Code, c.code, w.Body)
			}
			if c.code == 200 && (got.ID != "FJY632B9P3E:ou_a" || got.Name != "张三") {
				t.Fatalf("user %+v", got)
			}
			if c.code == 401 && w.Body.String() != `{"error":"login_required","login_url":"/auth/login"}`+"\n" {
				t.Fatalf("body %s", w.Body)
			}
		})
	}
}
