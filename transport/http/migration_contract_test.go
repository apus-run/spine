package http

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMigrationRedirectContract(t *testing.T) {
	for _, strict := range []bool{false, true} {
		s := NewServer(StrictSlash(strict))
		s.Route("/").GET("/users/{id}", func(Context) error { return nil })
		s.HandlePrefix("/tree/", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		for _, tc := range []struct {
			path, location string
			code           int
		}{
			{"/users/a/?q=1", "/users/a?q=1", 301},
			{"/users/a%2Fb/?q=1", "/users/a%2Fb?q=1", 301},
			{"/users/a%252Fb/?q=1", "/users/a%252Fb?q=1", 301},
			{"/users//a?q=1", "/users/a?q=1", 301},
			{"/tree?q=1", "/tree/?q=1", 301},
			{"/tree/a/", "", 200},
		} {
			want, loc := tc.code, tc.location
			if !strict && (tc.path == "/users/a/?q=1" || tc.path == "/users/a%2Fb/?q=1" || tc.path == "/users/a%252Fb/?q=1") {
				want, loc = 404, ""
			}
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
			if w.Code != want || w.Header().Get("Location") != loc {
				t.Errorf("strict=%v %s: got %d %q want %d %q", strict, tc.path, w.Code, w.Header().Get("Location"), want, loc)
			}
		}
	}
}

func TestMigrationCustomStatusContract(t *testing.T) {
	custom := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Allow", "WRONG")
		w.WriteHeader(202)
		_, _ = w.Write([]byte("custom"))
	})
	s := NewServer(StrictSlash(false), NotFoundHandler(custom), MethodNotAllowedHandler(custom))
	s.Route("/").GET("/ok", func(Context) error { return nil })
	for _, tc := range []struct {
		method, path string
		code         int
		allow        string
	}{{"POST", "/ok", 405, "GET, HEAD"}, {"GET", "/missing", 404, "WRONG"}, {"GET", "/ok/", 404, "WRONG"}} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.code || w.Header().Get("Allow") != tc.allow || w.Body.String() != "custom" {
			t.Errorf("%s %s: %d %q %q", tc.method, tc.path, w.Code, w.Header().Get("Allow"), w.Body.String())
		}
	}
}

func TestMigrationPrefixTrailingSlash(t *testing.T) {
	defer func() {
		if p := recover(); p != nil {
			t.Errorf("prefix registration panicked: %v", p)
		}
	}()
	s := NewServer(PathPrefix("/api/"), NotFoundHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("custom")) })))
	s.HandleHeader("X-Test", "yes", func(w http.ResponseWriter, r *http.Request) {
		if r.Pattern != "" {
			t.Errorf("header inherited scope pattern %q", r.Pattern)
		}
		w.WriteHeader(204)
	})
	for _, path := range []string{"/api", "/api/x", "/outside"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("X-Test", "yes")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if path == "/outside" {
			if w.Code != 404 || w.Body.String() != "custom" {
				t.Errorf("outside: %d %q", w.Code, w.Body.String())
			}
		} else if w.Code != 204 {
			t.Errorf("%s: %d", path, w.Code)
		}
	}
}

func TestMigrationNativeWildcards(t *testing.T) {
	s := NewServer()
	s.Route("/").GET("/files/{rest...}", func(c Context) error { return c.String(200, c.Vars().Get("rest")) })
	for _, tc := range []struct{ path, want string }{{"/files/a/b", "a/b"}, {"/files/", ""}, {"/files/a%252Fb", "a%2Fb"}} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != 200 || w.Body.String() != tc.want {
			t.Errorf("%s: %d %q", tc.path, w.Code, w.Body.String())
		}
	}
}

func TestMigrationBusinessRedirectUnchanged(t *testing.T) {
	for _, code := range []int{301, 302, 307, 308} {
		s := NewServer(PathPrefix("/api"))
		s.HandleFunc("/business", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/target", code) })
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", "/api/business", nil))
		if w.Code != code {
			t.Errorf("business %d changed to %d", code, w.Code)
		}
	}
}

func TestMigrationRedirectFollowMethod(t *testing.T) {
	s := NewServer()
	s.Route("/").Handle("*", "/target", func(c Context) error { return c.String(200, c.Request().Method) })
	ts := httptest.NewServer(s)
	defer ts.Close()
	resp, err := ts.Client().Post(ts.URL+"/target/?q=1", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || string(body) != "GET" || resp.Request.URL.RawQuery != "q=1" {
		t.Fatalf("redirect follow: %d %q %s", resp.StatusCode, body, resp.Request.URL)
	}
}

func TestMigrationExplicitSlashAndHead(t *testing.T) {
	s := NewServer()
	s.Route("/").GET("/item", func(c Context) error { return c.String(201, "plain") })
	s.Route("/").GET("/item/", func(c Context) error { return c.String(202, "slash") })
	s.Route("/").HEAD("/item", func(c Context) error { return c.String(203, "head") })
	ts := httptest.NewServer(s)
	defer ts.Close()
	for _, tc := range []struct {
		method, path string
		code         int
		body         string
	}{{"GET", "/item", 201, "plain"}, {"GET", "/item/", 202, "slash"}, {"HEAD", "/item", 203, ""}, {"HEAD", "/item/", 202, ""}} {
		req, _ := http.NewRequest(tc.method, ts.URL+tc.path, nil)
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != tc.code || string(b) != tc.body {
			t.Errorf("%s %s: %d %q", tc.method, tc.path, resp.StatusCode, b)
		}
	}
}

func TestMigrationRegexWholeSegment(t *testing.T) {
	s := NewServer()
	s.Route("/").GET("/items/{id:[0-9]+}", func(c Context) error { return c.String(200, c.Vars().Get("id")) })
	for _, tc := range []struct {
		path string
		code int
	}{{"/items/123", 200}, {"/items/x123", 404}, {"/items/123x", 404}} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != tc.code {
			t.Errorf("%s: got %d want %d", tc.path, w.Code, tc.code)
		}
	}
}
