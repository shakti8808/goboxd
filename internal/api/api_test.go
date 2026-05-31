package api_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thesouldev/goboxd/internal/api"
)

// fixedHandler returns an http.HandlerFunc that writes a known body so
// tests can distinguish handler invocations from router-level responses.
func fixedHandler(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

// newTestServer wires a minimal route set into NewRouter and an httptest
// server, returning the server and a teardown closure.
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	routes := []api.Route{
		{Path: "/healthz", Method: http.MethodGet, Handler: fixedHandler(http.StatusOK, `{"status":"ok"}`)},
		{Path: "/run", Method: http.MethodPost, Handler: fixedHandler(http.StatusServiceUnavailable, `{"error":"not_implemented"}`)},
	}
	srv := httptest.NewServer(api.NewRouter(routes))
	t.Cleanup(srv.Close)
	return srv
}

// TestRouterStatusPartition walks the cells of the routing partition
// (Property 3): recognised path × supported method → handler invocation;
// recognised path × unsupported method → 405 with Allow header;
// unrecognised path → 404 regardless of method.
func TestRouterStatusPartition(t *testing.T) {
	srv := newTestServer(t)

	cases := []struct {
		name     string
		method   string
		path     string
		status   int
		allow    string
		bodyPart string
	}{
		{"healthz GET ok", http.MethodGet, "/healthz", http.StatusOK, "", `"status":"ok"`},
		{"healthz POST 405", http.MethodPost, "/healthz", http.StatusMethodNotAllowed, "GET", `"method_not_allowed"`},
		{"run POST 503", http.MethodPost, "/run", http.StatusServiceUnavailable, "", `"not_implemented"`},
		{"run GET 405", http.MethodGet, "/run", http.StatusMethodNotAllowed, "POST", `"method_not_allowed"`},
		{"unknown path 404", http.MethodGet, "/nope", http.StatusNotFound, "", `"not_found"`},
		{"unknown path POST 404", http.MethodPost, "/nope", http.StatusNotFound, "", `"not_found"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(tc.method, srv.URL+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != tc.status {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.status)
			}
			if tc.allow != "" {
				got := resp.Header.Get("Allow")
				if got != tc.allow {
					t.Errorf("Allow = %q, want %q", got, tc.allow)
				}
			}
			body, _ := io.ReadAll(resp.Body)
			if tc.bodyPart != "" && !strings.Contains(string(body), tc.bodyPart) {
				t.Errorf("body %q missing substring %q", body, tc.bodyPart)
			}
		})
	}
}

// TestRouter404PathEcho confirms the 404 body includes the offending path.
func TestRouter404PathEcho(t *testing.T) {
	srv := newTestServer(t)
	resp, err := srv.Client().Get(srv.URL + "/path%20with/spaces")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	if !strings.Contains(string(body), `"path":"/path with/spaces"`) {
		t.Errorf("404 body missing path field: %s", body)
	}
}

// TestServerListenServeShutdown drives the full lifecycle of *Server.
func TestServerListenServeShutdown(t *testing.T) {
	routes := []api.Route{
		{Path: "/healthz", Method: http.MethodGet, Handler: fixedHandler(http.StatusOK, `{"status":"ok"}`)},
	}
	server := api.New("127.0.0.1:0", routes)
	if err := server.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr := server.Addr()
	if _, _, err := net.SplitHostPort(addr); err != nil {
		t.Fatalf("Addr() = %q, not host:port: %v", addr, err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve() }()

	// Issue a real HTTP request against the bound address.
	url := "http://" + addr + "/healthz"
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Errorf("Serve returned error: %v", err)
	}
}

// TestServeWithoutListenIsError asserts the lifecycle ordering invariant.
func TestServeWithoutListenIsError(t *testing.T) {
	server := api.New("127.0.0.1:0", nil)
	err := server.Serve()
	if err == nil {
		t.Fatal("Serve() before Listen() should error")
	}
}
