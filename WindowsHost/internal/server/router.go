package server

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"

	webclient "github.com/jvesiluoma/overseer/WebClient"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/netinfo"
	"github.com/jvesiluoma/overseer/WindowsHost/internal/protocol"
)

const MaxBodyBytes = 4096

// Transport is how a listener talks to browsers.
type Transport int

const (
	// Plain is unencrypted: the pairing code, token and signaling can be read on the network.
	Plain Transport = iota
	TLS
	// RedirectToHTTPS is an unencrypted listener that only sends browsers to the HTTPS port.
	RedirectToHTTPS
)

type Request struct {
	Method string
	// URI is the request target as sent (path + optional query).
	URI           string
	Headers       map[string]string // canonical (lower-case) names
	Body          []byte
	RemoteAddress string
}

func (r Request) Header(name string) (string, bool) {
	v, ok := r.Headers[strings.ToLower(name)]
	return v, ok
}

func (r Request) Path() string {
	p, _, _ := strings.Cut(r.URI, "?")
	if p == "" {
		return "/"
	}
	return p
}

func (r Request) Query() map[string]string {
	_, q, ok := strings.Cut(r.URI, "?")
	result := map[string]string{}
	if !ok {
		return result
	}
	for _, pair := range strings.Split(q, "&") {
		if pair == "" {
			continue
		}
		k, v, _ := strings.Cut(pair, "=")
		key, err := url.PathUnescape(k)
		if err != nil {
			continue
		}
		value, err := url.QueryUnescape(v)
		if err != nil {
			value = ""
		}
		if _, seen := result[key]; !seen {
			result[key] = value
		}
	}
	return result
}

type Header struct{ Name, Value string }

type Response struct {
	Status  int
	Headers []Header
	Body    []byte
}

func (r Response) Header(name string) string {
	for _, h := range r.Headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

// Router is the pure request -> response logic, independent of net/http so it can be tested.
type Router struct {
	ctx       *Context
	transport Transport
	httpsPort int
}

func NewRouter(ctx *Context, transport Transport, httpsPort int) *Router {
	return &Router{ctx: ctx, transport: transport, httpsPort: httpsPort}
}

var securityHeaders = []Header{
	{"Cache-Control", "no-store"},
	{"X-Content-Type-Options", "nosniff"},
	{"X-Frame-Options", "DENY"},
	{"Referrer-Policy", "no-referrer"},
	{"Content-Security-Policy", "default-src 'self'; connect-src 'self' ws: wss:; img-src 'self' data:; media-src 'self' blob: mediastream:; " +
		"style-src 'self'; script-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"},
}

func (rt *Router) Handle(req Request) Response {
	resp := rt.route(req)
	for _, h := range securityHeaders {
		if resp.Header(h.Name) == "" { // the status page brings its own CSP
			resp.Headers = append(resp.Headers, h)
		}
	}
	if req.Method == "HEAD" {
		resp.Body = nil
	}
	return resp
}

// AuthorizeWebSocket decides whether a WebSocket upgrade request may proceed.
func (rt *Router) AuthorizeWebSocket(req Request) bool {
	if rt.transport == RedirectToHTTPS || req.Path() != "/ws" || req.Method != "GET" {
		return false
	}
	host, ok := req.Header("Host")
	if !ok || !netinfo.HostAllowed(host, rt.ctx.Settings().ExtraHostNames) {
		return false
	}
	origin, hasOrigin := req.Header("Origin")
	if !netinfo.OriginMatchesHost(origin, host, hasOrigin) {
		return false
	}
	token, ok := req.Query()["token"]
	return ok && rt.ctx.Pairing.IsValid(token)
}

func (rt *Router) route(req Request) Response {
	host, ok := req.Header("Host")
	if !ok || !netinfo.HostAllowed(host, rt.ctx.Settings().ExtraHostNames) {
		return text(421, "Misdirected request")
	}
	path := req.Path()
	// Loopback traffic cannot be sniffed, so the status page skips the HTTPS redirect.
	if path == "/host" && rt.ctx.LocalPage != nil && isLoopback(req.RemoteAddress) && (req.Method == "GET" || req.Method == "HEAD") {
		return Response{Status: 200, Body: rt.ctx.LocalPage(), Headers: []Header{
			{"Content-Type", "text/html; charset=utf-8"},
			{"Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; img-src data:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"},
		}}
	}
	if rt.transport == RedirectToHTTPS {
		return redirect(host, rt.httpsPort, req.URI)
	}
	if strings.HasPrefix(path, "/api/") {
		return rt.api(req, path)
	}
	if path == "/ws" {
		// Reaching the router means the upgrade was refused (bad token, origin or not an upgrade).
		return jsonResponse(401, map[string]any{"error": "unauthorized"})
	}
	if req.Method != "GET" && req.Method != "HEAD" {
		r := text(405, "Method not allowed")
		r.Headers = append(r.Headers, Header{"Allow", "GET, HEAD"})
		return r
	}
	name := strings.TrimPrefix(path, "/")
	if path == "/" {
		name = "index.html"
	}
	// Assets come from a fixed in-memory table: no filesystem access, no traversal.
	asset, ok := webclient.Files[name]
	if !ok {
		return text(404, "Not found")
	}
	return Response{Status: 200, Headers: []Header{{"Content-Type", asset.ContentType}}, Body: asset.Body}
}

func (rt *Router) api(req Request, path string) Response {
	switch {
	case path == "/api/info" && (req.Method == "GET" || req.Method == "HEAD"):
		s := rt.ctx.Settings()
		displays := make([]map[string]any, 0, len(s.Displays))
		for _, d := range s.Displays {
			displays = append(displays, map[string]any{"id": d.ID, "name": d.Name, "inUse": rt.ctx.Hub.IsInUse(d.ID)})
		}
		return jsonResponse(200, map[string]any{
			"name": rt.ctx.HostName, "version": rt.ctx.AppVersion, "protocol": protocol.Version,
			"pairingRequired": s.RequirePairing, "displays": displays,
		})
	case path == "/api/pair" && req.Method == "POST":
		return rt.pair(req)
	case path == "/api/auth" && req.Method == "GET":
		auth, _ := req.Header("Authorization")
		token, ok := strings.CutPrefix(auth, "Bearer ")
		if !ok || !rt.ctx.Pairing.IsValid(token) {
			return jsonResponse(401, map[string]any{"error": "unauthorized"})
		}
		return Response{Status: 204}
	case path == "/api/info" || path == "/api/pair" || path == "/api/auth":
		return jsonResponse(405, map[string]any{"error": "method_not_allowed"})
	}
	return jsonResponse(404, map[string]any{"error": "not_found"})
}

func (rt *Router) pair(req Request) Response {
	// Requiring JSON makes this a non-simple CORS request, so other sites cannot post blindly.
	ct, _ := req.Header("Content-Type")
	if !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		return jsonResponse(415, map[string]any{"error": "unsupported_media_type"})
	}
	if len(req.Body) > MaxBodyBytes {
		return jsonResponse(413, map[string]any{"error": "too_large"})
	}
	if !rt.ctx.Settings().RequirePairing {
		return jsonResponse(200, map[string]any{"token": rt.ctx.Pairing.IssueUnauthenticatedToken()})
	}
	var body struct {
		Code *string `json:"code"`
	}
	if json.Unmarshal(req.Body, &body) != nil || body.Code == nil || len(*body.Code) > 16 {
		return jsonResponse(400, map[string]any{"error": "bad_request"})
	}
	result := rt.ctx.Pairing.Attempt(*body.Code, req.RemoteAddress)
	switch {
	case result.OK():
		return jsonResponse(200, map[string]any{"token": result.Token})
	case result.RetryAfter > 0:
		seconds := int(math.Ceil(result.RetryAfter.Seconds()))
		r := jsonResponse(429, map[string]any{"error": "locked_out", "retryAfter": seconds})
		r.Headers = append(r.Headers, Header{"Retry-After", strconv.Itoa(seconds)})
		return r
	default:
		return jsonResponse(401, map[string]any{"error": "invalid_code", "remainingAttempts": result.RemainingAttempts})
	}
}

func redirect(host string, httpsPort int, uri string) Response {
	name := netinfo.Hostname(host)
	if strings.Contains(name, ":") {
		name = "[" + name + "]"
	}
	authority := name
	if httpsPort != 443 {
		authority = fmt.Sprintf("%s:%d", name, httpsPort)
	}
	// Only keep a plain path, so the Location header cannot be abused.
	path := "/"
	if strings.HasPrefix(uri, "/") && printableASCII(uri) {
		path = uri
	}
	// 307 rather than a permanent redirect: browsers would cache that forever.
	r := text(307, "Use https://"+authority+"/")
	r.Headers = append(r.Headers, Header{"Location", "https://" + authority + path})
	return r
}

func isLoopback(address string) bool {
	ip := net.ParseIP(address)
	return ip != nil && ip.IsLoopback()
}

func printableASCII(s string) bool {
	for _, c := range []byte(s) {
		if c <= 0x20 || c >= 0x7F {
			return false
		}
	}
	return true
}

func jsonResponse(status int, object map[string]any) Response {
	data, err := json.Marshal(object) // maps marshal with sorted keys
	if err != nil {
		data = []byte("{}")
	}
	return Response{Status: status, Headers: []Header{{"Content-Type", "application/json"}}, Body: data}
}

func text(status int, body string) Response {
	return Response{Status: status, Headers: []Header{{"Content-Type", "text/plain; charset=utf-8"}}, Body: []byte(body)}
}
