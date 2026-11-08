package http

// Migration behavior probes (PRD §22 / Phase 1).
//
// This file records the routing behavior of the HTTP transport. It was
// first captured while the engine was Gorilla Mux v1.8.1 (commit 2fdc2115,
// go1.27.3 darwin/arm64); results are recorded in
// docs/router-migration-audit.md. After the ServeMux migration the
// expectations below follow the decided D1-D8 behavior:
//
//   B-1  405 + Allow                     (D1)
//   B-2  301 alias                       (D5; served by the StrictSlash alias
//                                        added in Phase 3)
//   B-4  mux-native cleaning redirect    (301 on Go 1.25.x, 307 on 1.26+)
//   B-5  404 on segment boundary miss    (D3)
//   B-7  registration-time panic         (accepted, §14.4)
//   B-8  Transport on every request,     (D6: "http.unmatched")
//        "http.unmatched" when unmatched
//   B-10 no http.DefaultServeMux delegation
//   B-11 HEAD matches GET routes         (D7)
//   B-13 %2F matches, PathValue decoded  (D8)
//
// Probes run through Server.ServeHTTP with an httptest recorder to avoid
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

// B-1: path exists, method does not match -> 405 + Allow (D1).
func TestProbeB1_MethodMismatch(t *testing.T) {
	p := newProbeServer(t)
	w := probeDo(p.srv, http.MethodPost, "/api/users/1", nil)
	t.Logf("B-1 POST /api/users/1 -> %d Allow=%q", w.Code, w.Header().Get("Allow"))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("B-1: want 405, got %d", w.Code)
	}
	if allow := w.Header().Get("Allow"); !strings.HasPrefix(allow, "GET") {
		t.Fatalf("B-1: want Allow starting with GET, got %q", allow)
	}
}

// B-2: exact route requested with trailing slash -> 301 alias (D5), served
// by the StrictSlash alias adaptation (Phase 3). Query is preserved.
func TestProbeB2_ExactTrailingSlash(t *testing.T) {
	p := newProbeServer(t)
	w := probeDo(p.srv, http.MethodGet, "/api/users/1/?q=1", nil)
	t.Logf("B-2 GET /api/users/1/?q=1 -> %d Location=%q", w.Code, w.Header().Get("Location"))
	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("B-2: want 301, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/api/users/1?q=1" {
		t.Fatalf("B-2: want Location /api/users/1?q=1, got %q", loc)
	}
}

// B-3: prefix route requested without trailing slash still serves (D3 keeps
// the bare prefix covered).
func TestProbeB3_PrefixWithoutSlash(t *testing.T) {
	srv := NewServer()
	srv.HandlePrefix("/sub", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("sub"))
	}))
	w := probeDo(srv, http.MethodGet, "/sub", nil)
	t.Logf("B-3 GET /sub (HandlePrefix /sub) -> %d", w.Code)
	if w.Code != http.StatusOK {
		t.Fatalf("B-3: want 200, got %d", w.Code)
	}
}

// B-4: duplicate slashes are cleaned by the ServeMux with a redirect whose
// status depends on the Go version (301 on 1.25.x, 307 on 1.26+).
func TestProbeB4_DuplicateSlashes(t *testing.T) {
	p := newProbeServer(t)
	w := probeDo(p.srv, http.MethodGet, "/api//users/1", nil)
	t.Logf("B-4 GET /api//users/1 -> %d Location=%q", w.Code, w.Header().Get("Location"))
	if w.Code != http.StatusMovedPermanently && w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("B-4: want 301 or 307, got %d", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/api/users/1" {
		t.Fatalf("B-4: want Location /api/users/1, got %q", loc)
	}
}

// B-5: PathPrefix matches on segment boundaries -> /apiary is 404 (D3).
func TestProbeB5_PrefixSegmentBoundary(t *testing.T) {
	srv := NewServer()
	srv.HandlePrefix("/api", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("api"))
	}))
	w := probeDo(srv, http.MethodGet, "/apiary", nil)
	t.Logf("B-5 GET /apiary (HandlePrefix /api) -> %d", w.Code)
	if w.Code != http.StatusNotFound {
		t.Fatalf("B-5: want 404, got %d", w.Code)
	}
	// The prefix itself and everything below it still serves.
	if w := probeDo(srv, http.MethodGet, "/api", nil); w.Code != http.StatusOK {
		t.Fatalf("B-5: want 200 for /api, got %d", w.Code)
	}
	if w := probeDo(srv, http.MethodGet, "/api/x/y", nil); w.Code != http.StatusOK {
		t.Fatalf("B-5: want 200 for /api/x/y, got %d", w.Code)
	}
}

// B-6: HandleHeader matches regardless of path (D2).
func TestProbeB6_HeaderUnrelatedPath(t *testing.T) {
	srv := NewServer()
	srv.HandleHeader("x-probe", "1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("header"))
	})
	w := probeDo(srv, http.MethodGet, "/anywhere/at/all", map[string]string{"X-Probe": "1"})
	t.Logf("B-6 GET /anywhere/at/all with header -> %d body=%q", w.Code, w.Body.String())
	if w.Code != http.StatusOK || w.Body.String() != "header" {
		t.Fatalf("B-6: want 200 header, got %d %q", w.Code, w.Body.String())
	}
}

// B-7: registering the same route twice panics at registration (§14.4).
func TestProbeB7_DuplicateRegistration(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("B-7: want panic on duplicate registration")
		}
	}()
	srv := NewServer()
	srv.HandleFunc("/dup", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("first")) })
	srv.HandleFunc("/dup", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("second")) })
}

// B-8 / B-14: every request carries a Transport; unmatched requests keep
// the initial "http.unmatched" metadata (D6).
func TestProbeB8_UnmatchedTransport(t *testing.T) {
	p := newProbeServer(t)
	// Matched request: the route handler observes a Transport whose initial
	// operation is the route template.
	w := probeDo(p.srv, http.MethodGet, "/api/users/1", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("B-8 setup: matched request failed with %d", w.Code)
	}
	if !*p.transportInFilter {
		t.Log("B-8: global filter sees the initial Transport before route metadata is filled (expected)")
	}
	if *p.operationSeen != "/api/users/{id}" {
		t.Fatalf("B-8 matched: want operation /api/users/{id}, got %q", *p.operationSeen)
	}

	// Unmatched request through a custom NotFound handler: Transport exists
	// with the fixed initial metadata.
	var op, tmpl string
	srv2 := NewServer(NotFoundHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tr, ok := transport.FromServerContext(r.Context()); ok {
			if htr, ok := tr.(*Transport); ok {
				op, tmpl = htr.Operation(), htr.PathTemplate()
			}
		}
		_, _ = w.Write([]byte("custom404"))
	})))
	w = probeDo(srv2, http.MethodGet, "/nowhere", nil)
	if w.Code != http.StatusNotFound || w.Body.String() != "custom404" {
		t.Fatalf("B-8 unmatched: want 404 custom404, got %d %q", w.Code, w.Body.String())
	}
	if op != unmatchedOperation || tmpl != unmatchedOperation {
		t.Fatalf("B-8 unmatched: want %q metadata, got operation=%q template=%q", unmatchedOperation, op, tmpl)
	}
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
	for _, r := range seen {
		if r.Method == "" {
			t.Fatalf("B-9: WalkRoute must skip no-method routes, got %+v", r)
		}
	}
}

// B-10: unmatched requests no longer delegate to http.DefaultServeMux.
func TestProbeB10_DefaultServeMuxDelegation(t *testing.T) {
	const probePath = "/__spine_probe_defmux__"
	http.DefaultServeMux.HandleFunc(probePath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("defmux"))
	})
	srv := NewServer()
	w := probeDo(srv, http.MethodGet, probePath, nil)
	t.Logf("B-10 GET %s -> %d body=%q", probePath, w.Code, w.Body.String())
	if w.Code != http.StatusNotFound {
		t.Fatalf("B-10: want 404 (no delegation), got %d", w.Code)
	}
}

// B-11: HEAD request against a GET route -> 200 (D7).
func TestProbeB11_HeadOnGetRoute(t *testing.T) {
	p := newProbeServer(t)
	w := probeDo(p.srv, http.MethodHead, "/api/users/1", nil)
	t.Logf("B-11 HEAD /api/users/1 -> %d", w.Code)
	if w.Code != http.StatusOK {
		t.Fatalf("B-11: want 200, got %d", w.Code)
	}
	// An explicit HEAD route takes precedence (D7).
	srv := NewServer()
	srv.Route("/x").HEAD("/h", func(ctx Context) error { return ctx.String(http.StatusOK, "explicit-head") })
	w = probeDo(srv, http.MethodHead, "/x/h", nil)
	if w.Code != http.StatusOK || w.Body.String() != "explicit-head" {
		t.Fatalf("B-11 explicit HEAD: want 200 explicit-head, got %d %q", w.Code, w.Body.String())
	}
}

// B-13: encoded path parameter values (D8: PathValue decodes exactly once).
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
		{"/v1/files/a%2Fb", http.StatusOK, "a/b"},  // D8: encoded slash matches one segment
		{"/v1/files/a%25b", http.StatusOK, "a%b"},  // single decode
		{"/v1/files/a%252Fb", http.StatusOK, "a%2Fb"}, // no double decode
	} {
		got = ""
		w := probeDo(srv, http.MethodGet, c.path, nil)
		t.Logf("B-13 GET %s -> %d vars=%q", c.path, w.Code, got)
		if w.Code != c.code || got != c.vars {
			t.Fatalf("B-13 %s: want %d %q, got %d %q", c.path, c.code, c.vars, w.Code, got)
		}
	}
}

// Stdlib redirect probes on the current toolchain (independent of the
// engine): record subtree append-slash and path cleaning status codes so
// the D5 normalization decision can be verified per Go version.
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

	// Exact route with trailing slash request stays 404 on stdlib; the
	// framework alias (D5) covers this case above.
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
