package http

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestRegistrationExactAndWildcard(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		s := NewServer()
		exact := func() { s.Route("/").GET("/items/", func(c Context) error { return c.String(200, "exact") }) }
		wildcard := func() {
			s.Route("/").GET("/items/{id}", func(c Context) error { return c.String(200, c.Vars().Get("id")) })
		}
		if reverse {
			wildcard()
			exact()
		} else {
			exact()
			wildcard()
		}
		for _, tc := range []struct{ path, body string }{{"/items/", "exact"}, {"/items/42", "42"}} {
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
			if w.Code != 200 || w.Body.String() != tc.body {
				t.Errorf("reverse=%v %s: %d %q", reverse, tc.path, w.Code, w.Body.String())
			}
		}
	}
}

func TestRegistrationActionValidatorNames(t *testing.T) {
	s := NewServer()
	s.Route("/").POST("/books/{name}:publish", func(c Context) error { return c.String(200, c.Vars().Get("name")) })
	s.Route("/").POST("/books/{id:[0-9]+}:archive", func(c Context) error { return c.String(200, c.Vars().Get("id")) })
	for _, tc := range []struct {
		path string
		code int
	}{{"/books/42:archive", 200}, {"/books/x:archive", 404}, {"/books/x:publish", 200}} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("POST", tc.path, nil))
		if w.Code != tc.code {
			t.Errorf("%s: %d want %d", tc.path, w.Code, tc.code)
		}
	}
}

func TestRegistrationBindingOwnership(t *testing.T) {
	bindings := []PathBinding{{Field: "id", Segments: []string{"*"}, Wildcards: []string{"p0"}}}
	rb := RouteBinding{
		Template: "/items/{id}:archive", Action: "archive", Bindings: bindings,
		Handler: func(c Context) error { return c.String(200, c.Vars().Get("id")) },
	}
	first := NewServer()
	first.Route("/").POST("/items/{name}:publish", func(Context) error { return nil })
	first.Route("/").HandleBindings("POST", "/items/{p0}", rb)
	if !reflect.DeepEqual(bindings[0].Wildcards, []string{"p0"}) {
		t.Errorf("registration mutated caller bindings: %v", bindings)
	}
	second := NewServer()
	second.Route("/").HandleBindings("POST", "/items/{p0}", rb)
	bindings[0].Segments[0] = "changed"
	bindings[0].Wildcards[0] = "changed"
	for i, s := range []*Server{first, second} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("POST", "/items/42:archive", nil))
		if w.Code != 200 || w.Body.String() != "42" {
			t.Errorf("server %d: %d %q", i, w.Code, w.Body.String())
		}
	}
}

func TestRegistrationPrefixAndExact(t *testing.T) {
	for _, prefixFirst := range []bool{false, true} {
		t.Run(map[bool]string{true: "prefix-first", false: "exact-first"}[prefixFirst], func(t *testing.T) {
			s := NewServer()
			prefix := func() {
				s.HandlePrefix("/items/{id}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var v struct {
						ID string `json:"id"`
					}
					if err := DefaultRequestVars(r, &v); err != nil {
						t.Error(err)
					}
					_, _ = w.Write([]byte(v.ID))
				}))
			}
			exact := func() {
				s.HandleFunc("/items/{name}", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("exact")) })
			}
			if prefixFirst {
				prefix()
				exact()
			} else {
				exact()
				prefix()
			}
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest("GET", "/items/42/child", nil))
			if w.Code != 200 || w.Body.String() != "42" {
				t.Fatalf("%d %q", w.Code, w.Body.String())
			}
		})
	}
}

func TestRegistrationTransportRequestVars(t *testing.T) {
	s := NewServer()
	s.Route("/").POST("/books/{id}:publish", func(c Context) error {
		req, ok := RequestFromServerContext(c)
		if !ok {
			t.Fatal("missing transport request")
		}
		var v struct {
			ID string `json:"id"`
		}
		if err := DefaultRequestVars(req, &v); err != nil {
			t.Fatal(err)
		}
		if v.ID != "42" {
			t.Errorf("transport request vars: %q", v.ID)
		}
		return nil
	})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", "/books/42:publish", nil))
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
}

func TestRegistrationBindingsWithGroupWildcard(t *testing.T) {
	s := NewServer()
	s.Route("/").POST("/tenants/{tenant}/items/{name}:publish", func(Context) error { return nil })
	s.Route("/tenants/{tenant}").HandleBindings("POST", "/items/{p0}", RouteBinding{
		Template: "/items/{id}:archive", Action: "archive",
		Bindings: []PathBinding{{Field: "id", Segments: []string{"*"}, Wildcards: []string{"p0"}}},
		Handler:  func(c Context) error { return c.String(200, c.Vars().Get("id")) },
	})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("POST", "/tenants/acme/items/42:archive", nil))
	if w.Code != 200 || w.Body.String() != "42" {
		t.Fatalf("%d %q", w.Code, w.Body.String())
	}
}
