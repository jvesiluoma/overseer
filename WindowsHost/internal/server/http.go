package server

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// HTTPServer is one listener (HTTPS, plain, or redirect-only) in front of the router.
type HTTPServer struct {
	ctx       *Context
	engine    MediaEngine
	transport Transport
	tlsConfig *tls.Config
	httpsPort int
	server    *http.Server
	listener  net.Listener
	Port      int
}

// NewHTTPServer: tlsConfig is required for TLS; httpsPort is where RedirectToHTTPS points.
func NewHTTPServer(ctx *Context, engine MediaEngine, transport Transport, tlsConfig *tls.Config, httpsPort int) (*HTTPServer, error) {
	if transport == TLS && tlsConfig == nil {
		return nil, errors.New("TLS transport needs a certificate")
	}
	return &HTTPServer{ctx: ctx, engine: engine, transport: transport, tlsConfig: tlsConfig, httpsPort: httpsPort}, nil
}

// Start listens on the first port that is free and returns it.
func (s *HTTPServer) Start(host string, ports []int) (int, error) {
	var lastErr error
	for _, port := range ports {
		l, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			lastErr = err
			continue
		}
		if s.transport == TLS {
			l = &tlsListener{Listener: l, config: s.tlsConfig}
		}
		s.listener = l
		s.Port = l.Addr().(*net.TCPAddr).Port
		router := NewRouter(s.ctx, s.transport, s.httpsPort)
		s.server = &http.Server{
			Handler:           &handler{server: s, router: router},
			ReadHeaderTimeout: 10 * time.Second,
			MaxHeaderBytes:    16 * 1024,
			ErrorLog:          log.New(io.Discard, "", 0), // TLS handshake noise from refused certificates
		}
		s.server.SetKeepAlivesEnabled(false) // one response per connection, like the macOS host
		go func() { _ = s.server.Serve(l) }()
		return s.Port, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no ports given")
	}
	return 0, lastErr
}

func (s *HTTPServer) Stop() {
	if s.server != nil {
		_ = s.server.Close()
	}
}

// tlsListener hands out connections that are not *tls.Conn, so net/http neither answers plain
// HTTP on the TLS port with a "400 client sent an HTTP request to an HTTPS server" (it just drops
// the connection) nor needs ServeTLS.
type tlsListener struct {
	net.Listener
	config *tls.Config
}

type tlsConn struct{ net.Conn }

func (l *tlsListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return tlsConn{tls.Server(c, l.config)}, nil
}

type handler struct {
	server *HTTPServer
	router *Router
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	headers := make(map[string]string, len(r.Header)+1)
	for name, values := range r.Header {
		if len(values) > 0 {
			headers[strings.ToLower(name)] = values[0]
		}
	}
	if r.Host != "" {
		headers["host"] = r.Host
	}
	remote, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		remote = r.RemoteAddr
	}
	body, _ := io.ReadAll(io.LimitReader(r.Body, MaxBodyBytes+1))
	req := Request{Method: r.Method, URI: r.RequestURI, Headers: headers, Body: body, RemoteAddress: remote}

	if req.Path() == "/ws" && strings.EqualFold(r.Header.Get("Upgrade"), "websocket") && h.router.AuthorizeWebSocket(req) {
		// Origin was already checked against Host by the router.
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		runSignaling(h.server.ctx, h.server.engine, conn, remote)
		return
	}

	resp := h.router.Handle(req)
	for _, hd := range resp.Headers {
		w.Header().Add(hd.Name, hd.Value)
	}
	w.Header().Set("Connection", "close")
	if r.Method != "HEAD" {
		w.Header().Set("Content-Length", strconv.Itoa(len(resp.Body)))
	}
	w.WriteHeader(resp.Status)
	if r.Method != "HEAD" {
		_, _ = w.Write(resp.Body)
	}
}

func withTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
