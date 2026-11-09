package http

// Phase 4 regression: transport adaptation over the ServeMux engine.
// Covers the Operation lifecycle (D6 initial value, route metadata,
// SetOperation override), service middleware selection by Operation,
// request decoding paths, and the timeout context for matched and
// unmatched requests (§4.2).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	spineerrors "github.com/apus-run/spine/v3/errors"
	"github.com/apus-run/spine/v3/middleware"
	"github.com/apus-run/spine/v3/transport"
)

// The Operation lifecycle: unmatched requests keep "http.unmatched"
// (D6); a selected route fills the registered template; SetOperation
// overrides it before service middleware runs.
func TestTransportRegression_OperationLifecycle(t *testing.T) {
	var seen []string
	srv := NewServer()
	srv.Use("helloworld.v1.Greeter/*", func(h middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (any, error) {
			if tr, ok := transport.FromServerContext(ctx); ok {
				seen = append(seen, tr.Operation())
			}
			return h(ctx, req)
		}
	})
	srv.Route("/helloworld").GET("/{name}", func(ctx Context) error {
		// Before SetOperation the Transport carries the route template.
		if tr, ok := transport.FromServerContext(ctx); ok {
			if htr, ok := tr.(*Transport); ok {
				if htr.Operation() != "/helloworld/{name}" || htr.PathTemplate() != "/helloworld/{name}" {
					t.Fatalf("route metadata: got operation=%q template=%q", htr.Operation(), htr.PathTemplate())
				}
			}
		}
		SetOperation(ctx, "helloworld.v1.Greeter/SayHello")
		h := ctx.Middleware(func(_ context.Context, _ any) (any, error) {
			return "ok", nil
		})
		_, err := h(ctx, nil)
		if err != nil {
			return err
		}
		return ctx.String(http.StatusOK, "done")
	})

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/helloworld/foo", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if len(seen) != 1 || seen[0] != "helloworld.v1.Greeter/SayHello" {
		t.Fatalf("middleware must match the overridden operation, got %v", seen)
	}

	// Unmatched request: the custom 404 handler observes the D6 metadata.
	srv2 := NewServer(NotFoundHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tr, ok := transport.FromServerContext(r.Context()); ok {
			if htr, ok := tr.(*Transport); ok {
				seen = append(seen, htr.Operation())
			}
		}
		w.WriteHeader(http.StatusNotFound)
	})))
	w = httptest.NewRecorder()
	srv2.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
	if len(seen) != 2 || seen[1] != unmatchedOperation {
		t.Fatalf("unmatched operation: want %q, got %v", unmatchedOperation, seen)
	}
}

// Timeout context covers matched handlers and unmatched requests alike.
func TestTransportRegression_TimeoutContext(t *testing.T) {
	var handlerDeadline, missDeadline bool
	srv := NewServer(Timeout(150*time.Millisecond),
		NotFoundHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, missDeadline = r.Context().Deadline()
			w.WriteHeader(http.StatusNotFound)
		})))
	srv.Route("/slow").GET("", func(ctx Context) error {
		_, handlerDeadline = ctx.Deadline()
		select {
		case <-ctx.Done():
			return ctx.String(http.StatusRequestTimeout, "timeout")
		case <-time.After(time.Second):
			return ctx.String(http.StatusOK, "done")
		}
	})

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/slow", nil))
	if !handlerDeadline {
		t.Fatal("matched handler must see the timeout deadline")
	}
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
	if !missDeadline {
		t.Fatal("unmatched request must also carry the timeout context (B-8/D6)")
	}
}

// Decoding paths over the new variable descriptors: BindVars with a
// template variable, BindQuery and Bind with a JSON body.
func TestTransportRegression_Decoders(t *testing.T) {
	type req struct {
		Name  string `json:"name"`
		Limit int    `json:"limit"`
	}
	srv := NewServer()
	var gotName string
	var gotQuery req
	var gotBody req
	srv.Route("/v1").GET("/files/{name}", func(ctx Context) error {
		var v struct {
			Name string `json:"name"`
		}
		if err := ctx.BindVars(&v); err != nil {
			return err
		}
		gotName = v.Name
		if err := ctx.BindQuery(&gotQuery); err != nil {
			return err
		}
		return nil
	})
	srv.Route("/v1").POST("/files", func(ctx Context) error {
		if err := ctx.Bind(&gotBody); err != nil {
			return err
		}
		return nil
	})

	r := httptest.NewRequest(http.MethodGet, "/v1/files/a%25b?limit=7", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d %s", w.Code, w.Body.String())
	}
	if gotName != "a%b" {
		t.Fatalf("BindVars: want a%%b, got %q", gotName)
	}
	if gotQuery.Limit != 7 {
		t.Fatalf("BindQuery: want limit 7, got %d", gotQuery.Limit)
	}

	r = httptest.NewRequest(http.MethodPost, "/v1/files", strings.NewReader(`{"name":"x"}`))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d %s", w.Code, w.Body.String())
	}
	if gotBody.Name != "x" {
		t.Fatalf("Bind: want x, got %q", gotBody.Name)
	}
}

// ErrorEncoder still receives handler errors through the route layer and
// redirects from Redirector pass through untouched by the router wrappers.
func TestTransportRegression_ErrorEncoderAndRedirector(t *testing.T) {
	srv := NewServer()
	srv.Route("/err").GET("", func(_ Context) error {
		return spineerrors.BadRequest("TEST", "bad request")
	})
	srv.Route("/redir").GET("", func(_ Context) error {
		return NewRedirect("/target", http.StatusFound)
	})
	srv.HandleFunc("/target", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("target"))
	})

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/err", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "bad") {
		t.Fatalf("error body: %q", w.Body.String())
	}

	w = httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/redir", nil))
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/target" {
		t.Fatalf("business redirect must pass through: got %d %q", w.Code, w.Header().Get("Location"))
	}
}
