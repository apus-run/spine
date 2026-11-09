package http

// Definition-of-Done acceptance tests (PRD §19): multi-Server isolation,
// WalkHandle, and full verb coverage through the ServeMux engine.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Multiple Server instances are fully isolated: registrations, 404
// handling and header dispatch on one server never affect another.
func TestDoD_MultiServerIsolation(t *testing.T) {
	a := NewServer()
	a.Route("/shared").GET("/thing", func(ctx Context) error {
		return ctx.String(http.StatusOK, "server-a")
	})
	a.HandleFunc("/only-a", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("only-a"))
	})

	b := NewServer(NotFoundHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("b-404"))
	})))
	b.HandleFunc("/shared/other", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("server-b"))
	})

	// Server A serves its own route and its default 404.
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/shared/thing", nil))
	if w.Code != http.StatusOK || w.Body.String() != "server-a" {
		t.Fatalf("server A route: %d %q", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/shared/other", nil))
	if w.Code != http.StatusNotFound || w.Body.String() == "b-404" {
		t.Fatalf("server A must not see server B routes/custom 404: %d %q", w.Code, w.Body.String())
	}

	// Server B serves its own route and its custom 404; A's routes 404.
	w = httptest.NewRecorder()
	b.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/shared/other", nil))
	if w.Code != http.StatusOK || w.Body.String() != "server-b" {
		t.Fatalf("server B route: %d %q", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	b.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/shared/thing", nil))
	if w.Code != http.StatusNotFound || w.Body.String() != "b-404" {
		t.Fatalf("server B must use its own 404: %d %q", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	b.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/only-a", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("server B must not see server A routes: %d", w.Code)
	}
}

// WalkHandle yields the registered method/path and a handler that serves
// the route through the server pipeline.
func TestDoD_WalkHandle(t *testing.T) {
	srv := NewServer()
	srv.Route("/v1").GET("/things/{id}", func(ctx Context) error {
		return ctx.String(http.StatusOK, ctx.Vars().Get("id"))
	})
	var calls int
	if err := srv.WalkHandle(func(method, path string, handler http.HandlerFunc) {
		calls++
		if method != http.MethodGet || path != "/v1/things/{id}" {
			t.Fatalf("walk = %s %s", method, path)
		}
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest(http.MethodGet, "/v1/things/42", nil))
		if w.Code != http.StatusOK || w.Body.String() != "42" {
			t.Fatalf("walk handler: %d %q", w.Code, w.Body.String())
		}
	}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d", calls)
	}
}

// Every Router verb helper registers a route matched by its method.
func TestDoD_AllVerbs(t *testing.T) {
	srv := NewServer()
	r := srv.Route("/verbs")
	var hit string
	h := func(Context) error { return nil }
	record := func(fn HandlerFunc) HandlerFunc {
		return func(ctx Context) error {
			hit = ctx.Request().Method
			return fn(ctx)
		}
	}
	r.GET("/get", record(h))
	r.HEAD("/head", record(h))
	r.POST("/post", record(h))
	r.PUT("/put", record(h))
	r.PATCH("/patch", record(h))
	r.DELETE("/delete", record(h))
	r.CONNECT("/connect", record(h))
	r.OPTIONS("/options", record(h))
	r.TRACE("/trace", record(h))

	verbs := []string{
		http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodConnect,
		http.MethodOptions, http.MethodTrace,
	}
	for _, v := range verbs {
		hit = ""
		req := httptest.NewRequest(v, "/verbs/"+strings.ToLower(v), nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: want 200, got %d", v, w.Code)
		}
		if hit != v {
			t.Fatalf("%s: handler saw %q", v, hit)
		}
	}

	// Custom (non-standard) methods work through Handle.
	srv.Route("/custom").Handle("PURGE", "/x", record(h))
	req := httptest.NewRequest("PURGE", "/custom/x", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusOK || hit != "PURGE" {
		t.Fatalf("custom method: %d hit=%q", w.Code, hit)
	}
}
