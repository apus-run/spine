package http

// Migration behavior probes (PRD §22 / Phase 1).
//
// This file records the routing behavior baseline of the HTTP transport on
// the CURRENT engine. It was first captured while the engine was Gorilla
// Mux v1.8.1 (commit 2fdc2115, go1.27.3 darwin/arm64); results are recorded
// in docs/router-migration-audit.md. As the ServeMux migration lands
// (Phases 2-3), expectations are updated to the decided D1-D8 behavior and
// each change is noted in the audit document.
//
// Probes are driven through Server.ServeHTTP with httptinrecorder to avoid
// network flakiness; redirect probes inspect Location/Allow directly.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/apus-run/spine/v3/transport"
)

// probeServer builds a server with the same default configuration as the
// production NewServer plus the routes needed by the B-1..B-15 matrix.
type probeServer struct {
	srv *Server
	// transportInFilter reports whether a server Transport is visible in the
	// global filter (runs before route matching for every request).
	transportInFilter *bool
	// operationSeen records Transport.Operation() observed by handlers.
	operationSeen *string
}

func newProbeServer(t *testing.T) *probeServer {
	t.Helper()
	p := &probeServer{
		transportInFilter: new(bool),
		operationSeen:     new(string),
	}
	transportSeen := p.transportInFilter
	op := p.operationSeen
	srv := NewServer(Filter(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, ok := transport.FromServerContext(r.Context())
			*transportSeen = ok
			next.ServeHTTP(w, r)
		})
	}))
	route := srv.Route("/api")
	route.GET("/users/{id}", func(ctx Context) error {
		if tr, ok := transport.FromServerContext(ctx); ok {
			*op = tr.Operation()
		}
		return ctx.String(http.StatusOK, "user")
	})
	p.srv = srv
	return p
}

func probeDo(srv *Server, method, path string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w
}

// B-1: path exists, method does not match.
func TestProbeB1_MethodMismatch(t *testing.T) {
	p := newProbeServer(t)
	w := probeDo(p.srv, http.MethodPost, "/api/users/1", nil)
	t.Logf("B-1 POST /api/users/1 -> %d Allow=%q body=%q", w.Code, w.Header().Get("Allow"), w.Body.String())
	// Baseline (Gorilla): 404 without Allow. Target (D1): 405 + Allow: GET, HEAD.
	if w.Code != http.StatusNotFound {
		t.Fatalf("B-1 baseline: want 404, got %d", w.Code)
	}
	if allow := w.Header().Get("Allow"); allow != "" {
		t.Fatalf("B-1 baseline: want no Allow header, got %q", allow)
	}
}

// B-2: exact route requested with trailing slash.
func TestProbeB2_ExactTrailingSlash(t *testing.T) {
	p := newProbeServer(t)
	w := probeDo(p.srv, http.MethodGet, "/api/users/1/", nil)
	t.Logf("B-2 GET /api/users/1/ -> %d Location=%q", w.Code, w.Header().Get("Location"))
	// Baseline (Gorilla StrictSlash): 301 -> /api/users/1. Target (D5): 301 kept.
	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("B-2 baseline: want 301, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/api/users/1" {
		t.Fatalf("B-2 baseline: want Location /api/users/1, got %q", loc)
	}
}

// B-3: prefix (subtree) route requested without trailing slash.
func TestProbeB3_PrefixWithoutSlash(t *testing.T) {
	srv := NewServer()
	srv.HandlePrefix("/sub", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("sub"))
	}))
	w := probeDo(srv, http.MethodGet, "/sub", nil)
	t.Logf("B-3 GET /sub (HandlePrefix /sub) -> %d Location=%q", w.Code, w.Header().Get("Location"))
	// Baseline (Gorilla): 200, PathPrefix matches the bare prefix as well.
	if w.Code != http.StatusOK {
		t.Fatalf("B-3 baseline: want 200, got %d", w.Code)
	}
}

// B-4: duplicate slashes in the path.
func TestProbeB4_DuplicateSlashes(t *testing.T) {
	p := newProbeServer(t)
	w := probeDo(p.srv, http.MethodGet, "/api//users/1", nil)
	t.Logf("B-4 GET /api//users/1 -> %d Location=%q", w.Code, w.Header().Get("Location"))
	// Baseline (Gorilla): 301 -> /api/users/1 (mux cleans the path).
	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("B-4 baseline: want 301, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/api/users/1" {
		t.Fatalf("B-4 baseline: want Location /api/users/1, got %q", loc)
	}
}

// B-5: PathPrefix string-prefix semantics (/api also matches /apiary).
func TestProbeB5_PrefixSegmentBoundary(t *testing.T) {
	srv := NewServer()
	srv.HandlePrefix("/api", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("api"))
	}))
	w := probeDo(srv, http.MethodGet, "/apiary", nil)
	t.Logf("B-5 GET /apiary (HandlePrefix /api) -> %d", w.Code)
	// Baseline (Gorilla): 200, string prefix matches. Target (D3): 404.
	if w.Code != http.StatusOK {
		t.Fatalf("B-5 baseline: want 200, got %d", w.Code)
	}
}

// B-6: HandleHeader matches regardless of path.
func TestProbeB6_HeaderUnrelatedPath(t *testing.T) {
	srv := NewServer()
	srv.HandleHeader("x-probe", "1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("header"))
	})
	w := probeDo(srv, http.MethodGet, "/anywhere/at/all", map[string]string{"X-Probe": "1"})
	t.Logf("B-6 GET /anywhere/at/all with header -> %d body=%q", w.Code, w.Body.String())
	// Baseline (Gorilla): header route hits on an unrelated path.
	if w.Code != http.StatusOK || w.Body.String() != "header" {
		t.Fatalf("B-6 baseline: want 200 header, got %d %q", w.Code, w.Body.String())
	}
}

// B-7: registering the same route twice.
func TestProbeB7_DuplicateRegistration(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("B-7 baseline: Gorilla should not panic on duplicate registration, got %v", r)
		}
	}()
	srv := NewServer()
	srv.HandleFunc("/dup", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("first")) })
	srv.HandleFunc("/dup", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("second")) })
	w := probeDo(srv, http.MethodGet, "/dup", nil)
	t.Logf("B-7 GET /dup (registered twice) -> %d body=%q", w.Code, w.Body.String())
	// Baseline (Gorilla): first registration wins silently. Target (stdlib): panic at registration.
	if w.Body.String() != "first" {
		t.Fatalf("B-7 baseline: want first handler, got %q", w.Body.String())
	}
}

// B-8 / B-14: unmatched requests have no Transport/timeout context.
func TestProbeB8_UnmatchedTransport(t *testing.T) {
	p := newProbeServer(t)
	// Matched request: the route handler observes a Transport whose initial
	// operation is the path template (set by the Use middleware).
	w := probeDo(p.srv, http.MethodGet, "/api/users/1", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("B-8 setup: matched request failed with %d", w.Code)
	}
	if *p.transportInFilter {
		t.Log("B-8: global filter sees a Transport even before matching (unexpected)")
	}
	t.Logf("B-8 matched: transport in filter=%v operation=%q", *p.transportInFilter, *p.operationSeen)

	// Unmatched request through a custom NotFound handler: no Transport in context.
	srv2 := NewServer(NotFoundHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, ok := transport.FromServerContext(r.Context())
		t.Logf("B-8 unmatched: transport in NotFound handler=%v", ok)
		_, _ = w.Write([]byte("custom404"))
	})))
	w = probeDo(srv2, http.MethodGet, "/nowhere", nil)
	// Baseline (Gorilla): NotFound handler runs without a Transport in the
	// request context (Use middleware only runs after a match).
	// Target (D6): Transport exists with Operation "http.unmatched".
}

// B-9: WalkRoute skips no-method routes.
func TestProbeB9_WalkSkipsNoMethod(t *testing.T) {
	srv := NewServer()
	srv.HandleFunc("/plain", func(w http.ResponseWriter, r *http.Request) {})
	srv.HandlePrefix("/pref", http.NotFoundHandler())
	srv.Route("/").Handle("*", "/wild", func(Context) error { return nil })
	var seen []RouteInfo
	if err := srv.WalkRoute(func(r RouteInfo) error {
		seen = append(seen, r)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, r := range seen {
		t.Logf("B-9 WalkRoute: %+v", r)
	}
	// Baseline (Gorilla): only the plain HandleFunc route appears (no method
	// constraint via HandleFunc? HandleFunc registers without Methods so it is
	// skipped too). HandlePrefix and Handle("*") are always skipped.
	for _, r := range seen {
		if r.Method == "" {
			t.Fatalf("B-9 baseline: WalkRoute must skip no-method routes, got %+v", r)
		}
	}
}

// B-10: default 404 delegates to http.DefaultServeMux.
func TestProbeB10_DefaultServeMuxDelegation(t *testing.T) {
	const probePath = "/__spine_probe_defmux__"
	http.DefaultServeMux.HandleFunc(probePath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("defmux"))
	})
	srv := NewServer()
	w := probeDo(srv, http.MethodGet, probePath, nil)
	t.Logf("B-10 GET %s -> %d body=%q", probePath, w.Code, w.Body.String())
	// Baseline: the DefaultServeMux handler is reached through the router's
	// NotFoundHandler delegation. Target: removed (private 404 handling).
	if w.Code != http.StatusOK || w.Body.String() != "defmux" {
		t.Fatalf("B-10 baseline: want 200 defmux, got %d %q", w.Code, w.Body.String())
	}
}

// B-11: HEAD request against a GET route.
func TestProbeB11_HeadOnGetRoute(t *testing.T) {
	p := newProbeServer(t)
	w := probeDo(p.srv, http.MethodHead, "/api/users/1", nil)
	t.Logf("B-11 HEAD /api/users/1 -> %d", w.Code)
	// Baseline (Gorilla): 404, Methods("GET") does not match HEAD.
	// Target (D7): 200 via stdlib GET->HEAD fallback.
	if w.Code != http.StatusNotFound {
		t.Fatalf("B-11 baseline: want 404, got %d", w.Code)
	}
}

// B-13: encoded path parameter values.
func TestProbeB13_EncodedPathValues(t *testing.T) {
	srv := NewServer()
	var got string
	srv.Route("/v1").GET("/files/{id}", func(ctx Context) error {
		got = ctx.Vars().Get("id")
		return ctx.String(http.StatusOK, got)
	})
	for _, c := range []struct {
		path string
		code int
		vars string
	}{
		{"/v1/files/a%2Fb", http.StatusNotFound, ""}, // baseline: decoded "/" breaks the segment
		{"/v1/files/a%25b", http.StatusOK, "a%b"},
		{"/v1/files/a%252Fb", http.StatusOK, "a%2Fb"},
	} {
		got = ""
		w := probeDo(srv, http.MethodGet, c.path, nil)
		t.Logf("B-13 GET %s -> %d vars=%q", c.path, w.Code, got)
		if w.Code != c.code || got != c.vars {
			t.Fatalf("B-13 baseline %s: want %d %q, got %d %q", c.path, c.code, c.vars, w.Code, got)
		}
	}
}

// B-15: effect of no-method fallback patterns on method mismatch (stdlib side).
// The Gorilla baseline returns 404 for method mismatch regardless of fallbacks
// (B-1); the stdlib side of this probe lives in the migration spike and is
// recorded in docs/router-migration-audit.md.
func TestProbeB15_Documentation(t *testing.T) {
	t.Log("B-15: covered by B-1 (Gorilla baseline) and stdlib spike probes in the audit document")
}

// Stdlib redirect probes on the current toolchain (independent of the engine):
// record subtree append-slash and path cleaning status codes so the D5
// normalization decision can be verified per Go version.
func TestProbeStdlibRedirects(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/sub/", func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("/clean/", func(w http.ResponseWriter, r *http.Request) {})

	req := httptest.NewRequest(http.MethodGet, "/sub", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	t.Logf("stdlib GET /sub (subtree /sub/) -> %d Location=%q", w.Code, w.Header().Get("Location"))

	req = httptest.NewRequest(http.MethodGet, "/clean//a", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	t.Logf("stdlib GET /clean//a -> %d Location=%q", w.Code, w.Header().Get("Location"))

	// Exact route with trailing slash request stays 404 on stdlib.
	mux.HandleFunc("/exact", func(w http.ResponseWriter, r *http.Request) {})
	req = httptest.NewRequest(http.MethodGet, "/exact/", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	t.Logf("stdlib GET /exact/ (exact /exact) -> %d", w.Code)

	// Method mismatch on stdlib: 405 + Allow.
	mux.HandleFunc("GET /m", func(w http.ResponseWriter, r *http.Request) {})
	req = httptest.NewRequest(http.MethodPost, "/m", nil)
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	t.Logf("stdlib POST /m (GET /m) -> %d Allow=%q", w.Code, w.Header().Get("Allow"))
	if !strings.HasPrefix(w.Header().Get("Allow"), "GET") {
		t.Fatalf("stdlib 405 must carry Allow, got %q", w.Header().Get("Allow"))
	}
}
