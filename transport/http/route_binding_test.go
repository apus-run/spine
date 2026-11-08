package http

// End-to-end tests for generated-code-shaped registrations via
// Router.HandleBindings (PRD §12): plain and nested-field variables,
// composite resource names, "**" captures with literal prefixes, and
// custom-verb action dispatch, including the interaction with BuildPath
// client-side template semantics.

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/apus-run/spine/v3/transport"
)

func newBindingServer(t *testing.T) (*Server, *[]string, *[]string) {
	t.Helper()
	hits := &[]string{}
	ops := &[]string{}
	srv := NewServer()
	r := srv.Route("/")

	record := func(h HandlerFunc) HandlerFunc {
		return func(ctx Context) error {
			if tr, ok := transport.FromServerContext(ctx); ok {
				if htr, ok := tr.(*Transport); ok {
					*ops = append(*ops, htr.Operation()+"|"+htr.PathTemplate())
				}
			}
			return h(ctx)
		}
	}

	// GET /v1/users/{id} — plain variable.
	r.HandleBindings("GET", "/v1/users/{p0}",
		RouteBinding{
			Template: "/v1/users/{id}",
			Bindings: []PathBinding{{Field: "id", Segments: []string{"*"}, Wildcards: []string{"p0"}}},
			Handler: record(func(ctx Context) error {
				*hits = append(*hits, "get-user:"+ctx.Vars().Get("id"))
				return nil
			}),
		})

	// GET /v1/books/{book.name} — nested field renamed to p0.
	r.HandleBindings("GET", "/v1/books/{p0}",
		RouteBinding{
			Template: "/v1/books/{book.name}",
			Bindings: []PathBinding{{Field: "book.name", Segments: []string{"*"}, Wildcards: []string{"p0"}}},
			Handler: record(func(ctx Context) error {
				*hits = append(*hits, "get-book:"+ctx.Vars().Get("book.name"))
				return nil
			}),
		})

	// GET /v1/projects/{p0}/books/{p1} — composite resource name.
	r.HandleBindings("GET", "/v1/projects/{p0}/books/{p1}",
		RouteBinding{
			Template: "/v1/{name=projects/*/books/*}",
			Bindings: []PathBinding{{Field: "name", Segments: []string{"projects", "*", "books", "*"}, Wildcards: []string{"p0", "p1"}}},
			Handler: record(func(ctx Context) error {
				*hits = append(*hits, "get-resource:"+ctx.Vars().Get("name"))
				return nil
			}),
		})

	// GET /v1/messages/{p0...} — "**" with the literal prefix re-attached.
	r.HandleBindings("GET", "/v1/messages/{p0...}",
		RouteBinding{
			Template: "/v1/{name=messages/**}",
			Bindings: []PathBinding{{Field: "name", Segments: []string{"messages", "**"}, Wildcards: []string{"p0"}}},
			Handler: record(func(ctx Context) error {
				*hits = append(*hits, "list-messages:"+ctx.Vars().Get("name"))
				return nil
			}),
		})

	// POST /v1/books/{p0} — custom actions plus a plain binding sharing the
	// pattern; dispatch selects by suffix (§12.6).
	r.HandleBindings("POST", "/v1/books/{p0}",
		RouteBinding{
			Template: "/v1/{name=books/*}:publish",
			Action:   "publish",
			Bindings: []PathBinding{{Field: "name", Segments: []string{"books", "*"}, Wildcards: []string{"p0"}}},
			Handler: record(func(ctx Context) error {
				*hits = append(*hits, "publish:"+ctx.Vars().Get("name"))
				return nil
			}),
		},
		RouteBinding{
			Template: "/v1/{name=books/*}:archive",
			Action:   "archive",
			Bindings: []PathBinding{{Field: "name", Segments: []string{"books", "*"}, Wildcards: []string{"p0"}}},
			Handler: record(func(ctx Context) error {
				*hits = append(*hits, "archive:"+ctx.Vars().Get("name"))
				return nil
			}),
		},
		RouteBinding{
			Template: "/v1/{name=books/*}",
			Bindings: []PathBinding{{Field: "name", Segments: []string{"books", "*"}, Wildcards: []string{"p0"}}},
			Handler: record(func(ctx Context) error {
				*hits = append(*hits, "update:"+ctx.Vars().Get("name"))
				return nil
			}),
		})

	return srv, hits, ops
}

func TestRouteBinding_EndToEnd(t *testing.T) {
	srv, hits, ops := newBindingServer(t)

	cases := []struct {
		method, path string
		code         int
		want         string
		opTemplate   string
	}{
		// Plain and nested variables (§12.2, §12.3).
		{http.MethodGet, "/v1/users/42", 200, "get-user:42", "/v1/users/{id}"},
		{http.MethodGet, "/v1/books/hobbit", 200, "get-book:hobbit", "/v1/books/{book.name}"},
		// Composite resource name reassembles the full name (§12.4).
		{http.MethodGet, "/v1/projects/acme/books/42", 200, "get-resource:projects/acme/books/42", "/v1/{name=projects/*/books/*}"},
		// "**" keeps the literal prefix and spans segments (§12.5).
		{http.MethodGet, "/v1/messages/a/b/c", 200, "list-messages:messages/a/b/c", "/v1/{name=messages/**}"},
		{http.MethodGet, "/v1/messages/", 200, "list-messages:messages/", "/v1/{name=messages/**}"},
		// Custom actions dispatch by suffix and strip it from the resource
		// (§12.6); a request without suffix hits the plain binding.
		{http.MethodPost, "/v1/books/42:publish", 200, "publish:books/42", "/v1/{name=books/*}:publish"},
		{http.MethodPost, "/v1/books/42:archive", 200, "archive:books/42", "/v1/{name=books/*}:archive"},
		{http.MethodPost, "/v1/books/42", 200, "update:books/42", "/v1/{name=books/*}"},
		// Encoded action separator decodes to the same dispatch (§12.6).
		{http.MethodPost, "/v1/books/42%3Apublish", 200, "publish:books/42", "/v1/{name=books/*}:publish"},
		// Resource names containing ":" keep everything before the last
		// colon as the resource.
		{http.MethodPost, "/v1/books/a%3Ab:publish", 200, "publish:books/a:b", "/v1/{name=books/*}:publish"},
		// Unknown action: 404, like the previous per-route regex behavior.
		{http.MethodPost, "/v1/books/42:explode", 404, "", ""},
		// Method mismatch keeps native 405 + Allow.
		{http.MethodDelete, "/v1/users/42", 405, "", ""},
	}

	for _, c := range cases {
		*hits = nil
		*ops = nil
		req := httptest.NewRequest(c.method, c.path, nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		if w.Code != c.code {
			t.Fatalf("%s %s: want %d, got %d (body %q)", c.method, c.path, c.code, w.Code, w.Body.String())
		}
		if c.want != "" {
			if len(*hits) != 1 || (*hits)[0] != c.want {
				t.Fatalf("%s %s: handler outcome %v, want [%s]", c.method, c.path, *hits, c.want)
			}
			if len(*ops) != 1 || (*ops)[0] != c.opTemplate+"|"+c.opTemplate {
				t.Fatalf("%s %s: transport metadata %v, want %q", c.method, c.path, *ops, c.opTemplate)
			}
		} else if len(*hits) != 0 {
			t.Fatalf("%s %s: no handler should run, got %v", c.method, c.path, *hits)
		}
	}

	// 405 responses carry Allow.
	req := httptest.NewRequest(http.MethodDelete, "/v1/users/42", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if allow := w.Header().Get("Allow"); allow == "" {
		t.Fatal("405 must carry Allow")
	}
}

// WalkRoute lists every logical binding with its original template, in
// registration order.
func TestRouteBinding_WalkRoute(t *testing.T) {
	srv, _, _ := newBindingServer(t)
	var seen []RouteInfo
	if err := srv.WalkRoute(func(r RouteInfo) error {
		seen = append(seen, r)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := []RouteInfo{
		{Method: "GET", Path: "/v1/users/{id}"},
		{Method: "GET", Path: "/v1/books/{book.name}"},
		{Method: "GET", Path: "/v1/{name=projects/*/books/*}"},
		{Method: "GET", Path: "/v1/{name=messages/**}"},
		{Method: "POST", Path: "/v1/{name=books/*}:publish"},
		{Method: "POST", Path: "/v1/{name=books/*}:archive"},
		{Method: "POST", Path: "/v1/{name=books/*}"},
	}
	if len(seen) != len(want) {
		t.Fatalf("walk = %+v, want %+v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("walk[%d] = %+v, want %+v", i, seen[i], want[i])
		}
	}
}

// BindVars through DefaultRequestVars sees the business field names of the
// selected binding (§5.5), including composite values and no double
// decoding.
func TestRouteBinding_BindVars(t *testing.T) {
	type msg struct {
		Name string `json:"name"`
		Book struct {
			Name string `json:"name"`
		} `json:"book"`
	}
	srv := NewServer()
	r := srv.Route("/")
	var got msg
	r.HandleBindings("GET", "/v1/books/{p0}",
		RouteBinding{
			Template: "/v1/books/{book.name}",
			Bindings: []PathBinding{{Field: "book.name", Segments: []string{"*"}, Wildcards: []string{"p0"}}},
			Handler: func(ctx Context) error {
				if err := ctx.BindVars(&got); err != nil {
					return err
				}
				return nil
			},
		})
	r.HandleBindings("GET", "/v1/messages/{p0...}",
		RouteBinding{
			Template: "/v1/{name=messages/**}",
			Bindings: []PathBinding{{Field: "name", Segments: []string{"messages", "**"}, Wildcards: []string{"p0"}}},
			Handler: func(ctx Context) error {
				got.Name = ctx.Vars().Get("name")
				return nil
			},
		})

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/books/a%25b", nil))
	if w.Code != 200 || got.Book.Name != "a%b" {
		t.Fatalf("BindVars nested field: %d %+v", w.Code, got)
	}

	got = msg{}
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/messages/x%2Fy", nil))
	if w.Code != 200 || got.Name != "messages/x/y" {
		t.Fatalf("BindVars composite: %d %q", w.Code, got.Name)
	}
}

// BuildPath (client) and the server binding semantics agree on the same
// original template (§12.8).
func TestRouteBinding_BuildPathConsistency(t *testing.T) {
	srv, hits, _ := newBindingServer(t)
	type bookID struct {
		Name string `json:"name"`
	}
	for _, tpl := range []string{
		"/v1/{name=messages/**}",
		"/v1/{name=books/*}:publish",
		"/v1/{name=projects/*/books/*}",
	} {
		*hits = nil
		path := BuildPath(tpl, &bookID{Name: "messages/a/b"})
		var want string
		switch tpl {
		case "/v1/{name=messages/**}":
			want = "list-messages:messages/a/b"
		case "/v1/{name=books/*}:publish":
			path = BuildPath(tpl, &bookID{Name: "books/42"})
			want = "publish:books/42"
		case "/v1/{name=projects/*/books/*}":
			path = BuildPath(tpl, &bookID{Name: "projects/acme/books/42"})
			want = "get-resource:projects/acme/books/42"
		}
		method := http.MethodGet
		if want == "publish:books/42" {
			method = http.MethodPost
		}
		req := httptest.NewRequest(method, path, nil)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		if w.Code != 200 || len(*hits) != 1 || (*hits)[0] != want {
			t.Fatalf("BuildPath %s -> %s: %d %v, want %s", tpl, path, w.Code, *hits, want)
		}
	}
}
