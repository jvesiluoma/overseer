package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jvesiluoma/overseer/WindowsHost/internal/pairing"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/settings"
)

func newTestRouter(t Transport) (*Router, *Context) {
	ctx := NewContext(settings.Default(), pairing.New(pairing.DefaultConfig(), nil), "1.0", "Test PC")
	return NewRouter(ctx, t, 8443), ctx
}

func get(uri string) Request {
	return Request{Method: "GET", URI: uri, Headers: map[string]string{"host": "192.168.1.2:8443"}, RemoteAddress: "192.168.1.50"}
}

func TestStaticAssetsAndSecurityHeaders(t *testing.T) {
	r, _ := newTestRouter(TLS)
	resp := r.Handle(get("/"))
	if resp.Status != 200 || !strings.Contains(string(resp.Body), "Overscreen") {
		t.Fatalf("index: %d", resp.Status)
	}
	for _, h := range []string{"Content-Security-Policy", "X-Frame-Options", "Cache-Control", "X-Content-Type-Options", "Referrer-Policy"} {
		if resp.Header(h) == "" {
			t.Errorf("missing %s", h)
		}
	}
	if resp.Header("Access-Control-Allow-Origin") != "" {
		t.Error("CORS header present")
	}
	for _, p := range []string{"/../../etc/passwd", "/embed.go", "/client.js/..", "/C:/Windows/win.ini"} {
		if r.Handle(get(p)).Status != 404 {
			t.Errorf("%s not 404", p)
		}
	}
	if r.Handle(get("/client.js")).Header("Content-Type") != "text/javascript; charset=utf-8" {
		t.Error("client.js content type")
	}
}

func TestHostHeaderPolicy(t *testing.T) {
	r, _ := newTestRouter(TLS)
	req := get("/")
	req.Headers["host"] = "evil.example"
	if r.Handle(req).Status != 421 {
		t.Fatal("rebinding host accepted")
	}
}

func TestPairFlow(t *testing.T) {
	r, ctx := newTestRouter(TLS)
	post := func(body, contentType string) Response {
		return r.Handle(Request{Method: "POST", URI: "/api/pair", Body: []byte(body), RemoteAddress: "10.0.0.9",
			Headers: map[string]string{"host": "localhost", "content-type": contentType}})
	}
	if post(`{"code":"x"}`, "text/plain").Status != 415 {
		t.Fatal("non-JSON accepted")
	}
	if post(`nope`, "application/json").Status != 400 {
		t.Fatal("bad JSON accepted")
	}
	wrongResp := post(`{"code":"`+flip(ctx.Pairing.CurrentCode())+`"}`, "application/json")
	if wrongResp.Status != 401 || !strings.Contains(string(wrongResp.Body), `"remainingAttempts":4`) {
		t.Fatalf("wrong code: %d %s", wrongResp.Status, wrongResp.Body)
	}
	ok := post(`{"code":"`+ctx.Pairing.CurrentCode()+`"}`, "application/json; charset=utf-8")
	var body struct{ Token string }
	_ = json.Unmarshal(ok.Body, &body)
	if ok.Status != 200 || !ctx.Pairing.IsValid(body.Token) {
		t.Fatalf("pair: %d %s", ok.Status, ok.Body)
	}
	auth := r.Handle(Request{Method: "GET", URI: "/api/auth", Headers: map[string]string{"host": "localhost", "authorization": "Bearer " + body.Token}})
	if auth.Status != 204 {
		t.Fatalf("auth: %d", auth.Status)
	}
	ws := Request{Method: "GET", URI: "/ws?token=" + body.Token, Headers: map[string]string{"host": "192.168.1.2:8443", "origin": "https://192.168.1.2:8443"}}
	if !r.AuthorizeWebSocket(ws) {
		t.Fatal("websocket refused")
	}
	ws.Headers["origin"] = "https://evil.example"
	if r.AuthorizeWebSocket(ws) {
		t.Fatal("foreign origin accepted")
	}
}

func flip(code string) string {
	if code == "000000" {
		return "111111"
	}
	return "000000"
}

func TestRedirectListener(t *testing.T) {
	r, _ := newTestRouter(RedirectToHTTPS)
	resp := r.Handle(get("/api/pair?x=1"))
	if resp.Status != 307 || resp.Header("Location") != "https://192.168.1.2:8443/api/pair?x=1" {
		t.Fatalf("redirect: %d %s", resp.Status, resp.Header("Location"))
	}
	bad := get("/a\x01b")
	if loc := r.Handle(bad).Header("Location"); loc != "https://192.168.1.2:8443/" {
		t.Fatalf("unsafe path kept: %q", loc)
	}
	if r.AuthorizeWebSocket(get("/ws?token=x")) {
		t.Fatal("websocket on redirect listener")
	}
	r443 := NewRouter(r.ctx, RedirectToHTTPS, 443)
	if loc := r443.Handle(get("/")).Header("Location"); loc != "https://192.168.1.2/" {
		t.Fatalf("default port kept: %s", loc)
	}
}

func TestStatusPageOnlyForLoopback(t *testing.T) {
	r, ctx := newTestRouter(RedirectToHTTPS)
	ctx.LocalPage = func() []byte { return []byte("<p>status</p>") }
	local := Request{Method: "GET", URI: "/host", Headers: map[string]string{"host": "localhost:8080"}, RemoteAddress: "127.0.0.1"}
	resp := r.Handle(local)
	if resp.Status != 200 || !strings.Contains(resp.Header("Content-Security-Policy"), "default-src 'none'") {
		t.Fatalf("local status page: %d %q", resp.Status, resp.Header("Content-Security-Policy"))
	}
	if strings.Count(strings.ToLower(headerNames(resp)), "content-security-policy") != 1 {
		t.Fatal("duplicate CSP")
	}
	remote := local
	remote.RemoteAddress = "192.168.1.50"
	if r.Handle(remote).Status == 200 {
		t.Fatal("status page served to the network")
	}
}

func headerNames(r Response) string {
	var names []string
	for _, h := range r.Headers {
		names = append(names, h.Name)
	}
	return strings.Join(names, ",")
}

func TestInfo(t *testing.T) {
	r, ctx := newTestRouter(TLS)
	ctx.Hub.Register(ConnectedClient{ID: "x", DisplayIndex: 0}, func(string) {})
	var info struct {
		Name            string
		Protocol        int
		PairingRequired bool
		Displays        []struct {
			ID    int
			InUse bool
		}
	}
	if err := json.Unmarshal(r.Handle(get("/api/info")).Body, &info); err != nil {
		t.Fatal(err)
	}
	if info.Name != "Test PC" || info.Protocol != 1 || !info.PairingRequired || len(info.Displays) != 1 || !info.Displays[0].InUse {
		t.Fatalf("info: %+v", info)
	}
	if r.Handle(Request{Method: "DELETE", URI: "/api/info", Headers: map[string]string{"host": "localhost"}}).Status != 405 {
		t.Fatal("DELETE /api/info")
	}
}

func TestHubReplacesSessionOnSameDisplay(t *testing.T) {
	h := NewHub()
	kicked := ""
	h.Register(ConnectedClient{ID: "a", DisplayIndex: 0}, func(r string) { kicked = r })
	if n := h.Register(ConnectedClient{ID: "b", DisplayIndex: 0}, func(string) {}); n != 1 || kicked != "replaced" {
		t.Fatalf("n=%d kicked=%q", n, kicked)
	}
}
