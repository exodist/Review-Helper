package review

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The access layer and the static routes, which serve no review data, so
// a server with no review file answers them.
func testHandler(token string, hosts ...string) http.Handler {
	s := &Server{access: NewAccess("Tess", token, 4321, hosts)}
	return s.Handler()
}

func request(h http.Handler, method, target, host, remote string, header ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	r.Host = host
	r.RemoteAddr = remote
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

const loopback = "127.0.0.1:5555"

func TestHostCheck(t *testing.T) {
	h := testHandler("", "Dev.Example.com", "[FE80:0:0::1]")
	for host, want := range map[string]int{
		"127.0.0.1":                     200,
		"127.0.0.1:8080":                200,
		"localhost":                     200,
		"LocalHost:1":                   200,
		"[::1]":                         200,
		"[::1]:9":                       200,
		"[0:0:0:0:0:0:0:1]:9":           200,
		"[0000::0001]":                  200,
		"dev.example.com:80":            200,
		"DEV.EXAMPLE.COM":               200,
		"[fe80::1]:3000":                200,
		"[fe80:0000::0:1]":              200,
		"::1":                           403,
		"evil.com":                      403,
		"localhost.evil.com":            403,
		"127.0.0.1.evil.com":            403,
		"[::2]":                         403,
		"localhost:port":                403,
		"":                              403,
		"[::1]x":                        403,
		"127.0.0.1:8080:9":              403,
		"[::ffff:127.0.0.1]":            403,
		"localhost:8080@evil.com":       403,
		"dev.example.com.attacker.test": 403,
	} {
		if got := request(h, "GET", "/static/css/review.css", host, loopback).Code; got != want {
			t.Errorf("Host %q: got %d, want %d", host, got, want)
		}
	}
}

func TestToken(t *testing.T) {
	const token = "0123456789abcdef"
	h := testHandler(token)
	remote := "192.0.2.7:4000"

	if w := request(h, "GET", "/", "localhost", loopback); w.Code != 200 {
		t.Errorf("loopback without a token: got %d", w.Code)
	}
	if w := request(h, "GET", "/", "localhost", "[::1]:4000"); w.Code != 200 {
		t.Errorf("IPv6 loopback without a token: got %d", w.Code)
	}
	w := request(h, "GET", "/api/review", "localhost", remote)
	if w.Code != 401 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("no token: got %d %q", w.Code, w.Header().Get("Content-Type"))
	}
	if w := request(h, "GET", "/?token=wrong", "localhost", remote); w.Code != 401 {
		t.Errorf("a wrong token: got %d", w.Code)
	}
	if w := request(h, "GET", "/?token="+token+"x", "localhost", remote); w.Code != 401 {
		t.Errorf("a longer token: got %d", w.Code)
	}

	w = request(h, "GET", "/static/js/app.js?x=1&token="+token, "localhost", remote)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
		t.Fatalf("the token URL: got %d to %q", w.Code, w.Header().Get("Location"))
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("the token URL set %d cookies", len(cookies))
	}
	c := cookies[0]
	if c.Name != "rh_token_4321" || c.Value != token || c.Path != "/" || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
		t.Errorf("the cookie: %+v", c)
	}
	if set := w.Header().Get("Set-Cookie"); !strings.Contains(set, "SameSite=Lax") || !strings.Contains(set, "HttpOnly") {
		t.Errorf("Set-Cookie: %s", set)
	}

	if w := request(h, "GET", "/static/css/review.css", "localhost", remote, "Cookie", "rh_token_4321="+token); w.Code != 200 {
		t.Errorf("with the cookie: got %d", w.Code)
	}
	if w := request(h, "GET", "/static/css/review.css", "localhost", remote, "Cookie", "rh_token_1234="+token); w.Code != 401 {
		t.Errorf("another port's cookie: got %d", w.Code)
	}
	if w := request(h, "GET", "/", "evil.com", remote, "Cookie", "rh_token_4321="+token); w.Code != 403 {
		t.Errorf("the host check comes first: got %d", w.Code)
	}
}

func TestStaticPaths(t *testing.T) {
	h := testHandler("")
	for target, want := range map[string]int{
		"/":                            200,
		"/static/css/review.css":       200,
		"/static/js/app.js":            200,
		"/static/html/index.html":      200,
		"/static/js/":                  404,
		"/static/js":                   404,
		"/static/t/logic.test.mjs":     404,
		"/static/AGENTS.md":            404,
		"/static/js//app.js":           404,
		"/static/js/./app.js":          404,
		"/static/js/../css/review.css": 404,
		"/static/../ux/AGENTS.md":      404,
		"/static/js/%2e%2e/app.js":     404,
		"/static/js%5capp.js":          404,
		"/static/js/app.js%00":         404,
		"/static/js/nope.js":           404,
		"/static/js/highlight":         404,
		"/index.html":                  404,
		"/api/nope":                    404,
	} {
		w := request(h, "GET", target, "localhost", loopback)
		if w.Code != want {
			t.Errorf("%s: got %d, want %d", target, w.Code, want)
		}
		if want == 404 && !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
			t.Errorf("%s: a 404 is JSON, got %q", target, w.Header().Get("Content-Type"))
		}
	}
	w := request(h, "GET", "/static/js/app.js", "localhost", loopback)
	if ct := w.Header().Get("Content-Type"); ct != "text/javascript; charset=utf-8" {
		t.Errorf("app.js: Content-Type %q", ct)
	}
	if w := request(h, "POST", "/static/js/app.js", "localhost", loopback); w.Code != 404 {
		t.Errorf("POST to a GET route: got %d", w.Code)
	}
}

func TestPostChecks(t *testing.T) {
	h := testHandler("")
	for _, c := range []struct {
		header []string
		want   int
	}{
		{nil, 415},
		{[]string{"Content-Type", "text/plain"}, 415},
		{[]string{"Content-Type", "application/jsonx"}, 415},
		{[]string{"Content-Type", "application/json", "Origin", "http://evil.com"}, 403},
		{[]string{"Content-Type", "application/json", "Origin", "http://localhost:1"}, 403},
		{[]string{"Content-Type", "application/json", "Origin", "null"}, 403},
	} {
		if w := request(h, "POST", "/api/close", "localhost:9", loopback, c.header...); w.Code != c.want {
			t.Errorf("%v: got %d, want %d", c.header, w.Code, c.want)
		}
	}
}
