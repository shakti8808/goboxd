// Package api contains the HTTP listener and request router.
//
// The router is responsible for the architecture spec's status-code
// partition (Property 3): unrecognised path → HTTP 404; recognised path
// with unsupported method → HTTP 405 with an Allow header; recognised
// path × supported method → handler invocation. The router itself never
// reads the request body, never enqueues work, and never spawns
// subprocesses; it only dispatches.
//
// Server wraps a net.Listener and an *http.Server so the bind happens in
// Listen() (a separately observable step) and Serve() blocks until
// Shutdown is called.
package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Route describes one endpoint binding.
type Route struct {
	// Path is the exact request path to match (e.g. "/healthz").
	Path string

	// Method is the HTTP method to accept (e.g. http.MethodGet).
	Method string

	// Handler is invoked when the request matches both Path and Method.
	Handler http.Handler
}

// NewRouter builds an http.Handler from a list of Routes.
//
// Multiple Routes may share a Path with different Methods; the router
// records every accepted Method per Path so the Allow header on a 405
// response lists every supported Method for that Path (REQ A-3.8).
func NewRouter(routes []Route) http.Handler {
	r := &router{
		methods: map[string]map[string]http.Handler{},
		allow:   map[string]string{},
	}
	for _, rt := range routes {
		if r.methods[rt.Path] == nil {
			r.methods[rt.Path] = map[string]http.Handler{}
		}
		r.methods[rt.Path][rt.Method] = rt.Handler
	}
	for path, methods := range r.methods {
		allowed := make([]string, 0, len(methods))
		for m := range methods {
			allowed = append(allowed, m)
		}
		sort.Strings(allowed)
		r.allow[path] = strings.Join(allowed, ", ")
	}
	return r
}

// router implements http.Handler with the architecture's status-code
// partition.
type router struct {
	methods map[string]map[string]http.Handler
	allow   map[string]string
}

// ServeHTTP implements http.Handler.
func (r *router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	methods, ok := r.methods[req.URL.Path]
	if !ok {
		// Unrecognised path: HTTP 404 (REQ A-3.7, REQ A-3.9).
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"not_found","path":` + jsonString(req.URL.Path) + `}`))
		return
	}
	h, ok := methods[req.Method]
	if !ok {
		// Recognised path, unsupported method: HTTP 405 with Allow
		// header (REQ A-3.8).
		w.Header().Set("Allow", r.allow[req.URL.Path])
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = w.Write([]byte(`{"error":"method_not_allowed"}`))
		return
	}
	h.ServeHTTP(w, req)
}

// jsonString escapes a string for safe inlining inside a JSON literal.
//
// Used by ServeHTTP only when emitting the 404 body, where pulling in
// encoding/json for one path would be overkill. The escape covers the
// minimal set required for JSON string literals.
func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// Server is GoboxD's HTTP listener.
//
// Lifecycle: New → Listen → Serve (blocks) → Shutdown. Listen and Serve
// are split so callers can observe a successful bind before publishing
// readiness, and so a bind failure surfaces synchronously rather than
// inside a goroutine.
type Server struct {
	addr     string
	srv      *http.Server
	listener net.Listener
}

// New constructs a Server.
//
// The router is built from routes via NewRouter. The HTTP server applies
// a 5-second ReadHeaderTimeout to mitigate slow-loris-style attacks; this
// is independent of the per-request body cap, which is enforced inside
// the run handler in Phase 2.
func New(addr string, routes []Route) *Server {
	r := NewRouter(routes)
	return &Server{
		addr: addr,
		srv: &http.Server{
			Addr:              addr,
			Handler:           r,
			ReadHeaderTimeout: 5 * time.Second,
		},
	}
}

// Listen binds the configured TCP address.
//
// Must be called before Serve. Returns the underlying net.Listener error
// when the bind fails.
func (s *Server) Listen() error {
	l, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.listener = l
	return nil
}

// Addr returns the address the listener is bound to.
//
// Useful for tests that bind to "127.0.0.1:0" and need to discover the
// resolved port.
func (s *Server) Addr() string {
	if s.listener == nil {
		return s.addr
	}
	return s.listener.Addr().String()
}

// Serve blocks serving HTTP until Shutdown is called or an error occurs.
//
// Listen must be called first. http.ErrServerClosed is suppressed because
// it is the expected outcome of a clean Shutdown.
func (s *Server) Serve() error {
	if s.listener == nil {
		return errors.New("api: Serve called before Listen")
	}
	if err := s.srv.Serve(s.listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Shutdown gracefully stops the server.
//
// It returns when every in-flight request has completed or the context's
// deadline has elapsed.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}
