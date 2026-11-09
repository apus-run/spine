package http

// Phase 3 spike: HandleHeader dispatch matrix (decision D2, PRD §8.3).
//
// The matrix is accepted on status, Allow header, Transport metadata, which
// handler actually ran and how many times — not just the status code.
// Conclusions are recorded in docs/router-migration-audit.md.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/apus-run/spine/v3/transport"
)

type headerMatrix struct {
	srv      *Server
	hits     *[]string // names of handlers that ran, in order
	headerOp *string
}

func newHeaderMatrix(t *testing.T) *headerMatrix {
	t.Helper()
	hits := &[]string{}
	headerOp := new(string)
	hm := &headerMatrix{hits: hits, headerOp: headerOp}
	record := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			*hits = append(*hits, name)
			if tr, ok := transport.FromServerContext(r.Context()); ok {
				if htr, ok := tr.(*Transport); ok {
					*headerOp = htr.Operation()
				}
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(name))
		}
	}
	srv := NewServer()
	// Normal routes: GET-only and POST-only on overlapping paths.
	srv.Route("/api").GET("/users/{id}", func(ctx Context) error {
		*hits = append(*hits, "normal-get")
		return ctx.String(http.StatusOK, "normal-get")
	})
	srv.Route("/api").POST("/users", func(ctx Context) error {
		*hits = append(*hits, "normal-post")
		return ctx.String(http.StatusCreated, "normal-post")
	})
	// Header routes, mixed registration order relative to normal routes.
	srv.HandleHeader("x-dispatch", "one", record("header-one"))
	srv.HandleHeader("x-dispatch", "two", record("header-two"))
	hm.srv = srv
	return hm
}

func (hm *headerMatrix) run(method, path string, header map[string]string) *httptest.ResponseRecorder {
	*hm.hits = nil
	req := httptest.NewRequest(method, path, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	hm.srv.ServeHTTP(w, req)
	return w
}

func wantHits(t *testing.T, hm *headerMatrix, want ...string) {
	t.Helper()
	if len(*hm.hits) != len(want) {
		t.Fatalf("handlers run = %v, want exactly %v", *hm.hits, want)
	}
	for i := range want {
		if (*hm.hits)[i] != want[i] {
			t.Fatalf("handlers run = %v, want %v", *hm.hits, want)
		}
	}
}

// Hit: header match wins over an existing normal route regardless of method
// and registration order; Transport metadata is "http.header".
func TestHeaderMatrix_Hit(t *testing.T) {
	hm := newHeaderMatrix(t)

	w := hm.run(http.MethodGet, "/api/users/1", map[string]string{"X-Dispatch": "one"})
	if w.Code != http.StatusOK || w.Body.String() != "header-one" {
		t.Fatalf("want 200 header-one, got %d %q", w.Code, w.Body.String())
	}
	wantHits(t, hm, "header-one")
	if *hm.headerOp != headerOperation {
		t.Fatalf("want operation %q, got %q", headerOperation, *hm.headerOp)
	}

	// Path does not exist: header route still handles it (path-independent).
	w = hm.run(http.MethodGet, "/nowhere", map[string]string{"X-Dispatch": "one"})
	if w.Code != http.StatusOK || w.Body.String() != "header-one" {
		t.Fatalf("want 200 header-one on missing path, got %d %q", w.Code, w.Body.String())
	}
	wantHits(t, hm, "header-one")

	// Method normally disallowed (DELETE on a GET/POST-only path): the
	// header dispatch allows any method (D2).
	w = hm.run(http.MethodDelete, "/api/users/1", map[string]string{"X-Dispatch": "one"})
	if w.Code != http.StatusOK || w.Body.String() != "header-one" {
		t.Fatalf("want 200 header-one for disallowed method, got %d %q", w.Code, w.Body.String())
	}
	wantHits(t, hm, "header-one")

	// First registration wins among multiple header routes.
	w = hm.run(http.MethodGet, "/api/users/1", map[string]string{"X-Dispatch": "two", "x-other": "x"})
	if w.Code != http.StatusOK || w.Body.String() != "header-two" {
		t.Fatalf("want 200 header-two, got %d %q", w.Code, w.Body.String())
	}
	wantHits(t, hm, "header-two")
}

// Miss: exactly one normal ServeMux dispatch decides 404/405/Allow (D1/D2).
func TestHeaderMatrix_Miss(t *testing.T) {
	hm := newHeaderMatrix(t)

	// Path exists, method allowed -> normal route.
	w := hm.run(http.MethodGet, "/api/users/1", map[string]string{"X-Dispatch": "other"})
	if w.Code != http.StatusOK || w.Body.String() != "normal-get" {
		t.Fatalf("want 200 normal-get, got %d %q", w.Code, w.Body.String())
	}
	wantHits(t, hm, "normal-get")

	// Path exists, method not allowed -> native 405 + Allow, no Allow from
	// header dispatch, exactly one handler-less response.
	w = hm.run(http.MethodPut, "/api/users/1", map[string]string{"X-Dispatch": "other"})
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", w.Code)
	}
	if allow := w.Header().Get("Allow"); allow == "" {
		t.Fatal("want Allow header on 405")
	}
	wantHits(t, hm) // no handler ran

	// Path does not exist -> 404.
	w = hm.run(http.MethodGet, "/nowhere", map[string]string{"X-Dispatch": "other"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
	wantHits(t, hm)

	// No header at all -> normal routing.
	w = hm.run(http.MethodPost, "/api/users", nil)
	if w.Code != http.StatusCreated || w.Body.String() != "normal-post" {
		t.Fatalf("want 201 normal-post, got %d %q", w.Code, w.Body.String())
	}
	wantHits(t, hm, "normal-post")
}

// Cross order: header routes registered after normal routes still take
// priority (header dispatch precedes normal routing by design, D2).
func TestHeaderMatrix_CrossOrder(t *testing.T) {
	srv := NewServer()
	srv.HandleFunc("/x", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("normal"))
	})
	srv.HandleHeader("x-late", "1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("header-late"))
	})
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Late", "1")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Body.String() != "header-late" {
		t.Fatalf("header route registered after normal route must win, got %q", w.Body.String())
	}
}

// PathPrefix scope: header dispatch applies only within the scope.
func TestHeaderMatrix_PrefixScope(t *testing.T) {
	srv := NewServer(PathPrefix("/api"))
	srv.HandleHeader("x-scope", "1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("header-scoped"))
	})
	srv.Route("/").GET("/ping", func(ctx Context) error {
		return ctx.String(http.StatusOK, "pong")
	})

	// Inside the scope: header hit and normal routing both work.
	req := httptest.NewRequest(http.MethodGet, "/api/anything", nil)
	req.Header.Set("X-Scope", "1")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "header-scoped" {
		t.Fatalf("in-scope header hit: want 200 header-scoped, got %d %q", w.Code, w.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/api/ping", nil)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "pong" {
		t.Fatalf("in-scope normal route: want 200 pong, got %d %q", w.Code, w.Body.String())
	}

	// Outside the scope: header dispatch does not apply; scope 404.
	req = httptest.NewRequest(http.MethodGet, "/outside", nil)
	req.Header.Set("X-Scope", "1")
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("out-of-scope header request: want 404, got %d", w.Code)
	}
	if w.Body.String() == "header-scoped" {
		t.Fatal("out-of-scope request must not reach the header handler")
	}
}

// An empty condition requires the header to exist; any value then matches.
func TestHeaderMatrix_EmptyValue(t *testing.T) {
	for _, tc := range []struct {
		values []string
		code   int
	}{
		{nil, 404}, {[]string{}, 200}, {[]string{""}, 200}, {[]string{"present"}, 200},
	} {
		srv := NewServer()
		srv.HandleHeader("x-missing", "", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
		req := httptest.NewRequest("GET", "/anywhere", nil)
		if tc.values != nil {
			req.Header["X-Missing"] = tc.values
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		if w.Code != tc.code {
			t.Errorf("values=%v got %d want %d", tc.values, w.Code, tc.code)
		}
	}
}

func TestHeaderMatrix_MultipleValues(t *testing.T) {
	srv := NewServer()
	srv.HandleHeader("x-match", "second", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	req := httptest.NewRequest("POST", "/anywhere", nil)
	req.Header.Add("X-Match", "first")
	req.Header.Add("X-Match", "second")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != 204 {
		t.Fatalf("got %d", w.Code)
	}
}

// Global filters wrap header dispatch too; the initial Transport is visible
// with "http.unmatched" before any selection (§4.2, D6).
func TestHeaderMatrix_GlobalFilterTransport(t *testing.T) {
	var filterOp string
	srv := NewServer(Filter(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if tr, ok := transport.FromServerContext(r.Context()); ok {
				if htr, ok := tr.(*Transport); ok {
					filterOp = htr.Operation()
				}
			}
			next.ServeHTTP(w, r)
		})
	}))
	srv.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) })
	req := httptest.NewRequest(http.MethodGet, "/ok", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if filterOp != unmatchedOperation {
		t.Fatalf("global filter must see initial operation %q, got %q", unmatchedOperation, filterOp)
	}
}

// PathPrefix composes with Route/Handle registrations (registration paths
// are relative to the scope, like the previous subrouter behavior).
func TestHeaderMatrix_PathPrefixComposition(t *testing.T) {
	srv := NewServer(PathPrefix("/api"))
	srv.Route("/v1").GET("/things/{id}", func(ctx Context) error {
		return ctx.String(http.StatusOK, ctx.Vars().Get("id"))
	})
	srv.HandleFunc("/plain", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("plain"))
	})
	srv.HandlePrefix("/assets", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("asset"))
	}))

	for _, c := range []struct{ method, path, want string }{
		{http.MethodGet, "/api/v1/things/42", "42"},
		{http.MethodGet, "/api/plain", "plain"},
		{http.MethodGet, "/api/assets/x.css", "asset"},
		{http.MethodGet, "/api/assets", "asset"},
	} {
		req := httptest.NewRequest(c.method, c.path, nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		if w.Code != http.StatusOK || w.Body.String() != c.want {
			t.Fatalf("%s: want 200 %q, got %d %q", c.path, c.want, w.Code, w.Body.String())
		}
	}
	// Outside the scope nothing is served, including the prefix-free twin.
	for _, path := range []string{"/v1/things/42", "/plain", "/apifoo"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s: want 404 outside scope, got %d", path, w.Code)
		}
	}
}
