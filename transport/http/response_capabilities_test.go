package http

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Logging filters can expose optional capabilities through Unwrap instead
// of implementing every ResponseWriter extension themselves.
type unwrapOnlyWriter struct{ http.ResponseWriter }

func (w unwrapOnlyWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func unwrapOnlyFilter(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(unwrapOnlyWriter{w}, r) })
}

func TestResponseControllerFlushThroughFilter(t *testing.T) {
	s := NewServer(Filter(unwrapOnlyFilter))
	s.HandleFunc("/events", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("event"))
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
		}
	})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/events", nil))
	if !w.Flushed {
		t.Fatal("flush did not reach the underlying writer")
	}
}

func TestResponseControllerHijackThroughFilter(t *testing.T) {
	s := NewServer(Filter(unwrapOnlyFilter))
	s.HandleFunc("/upgrade", func(w http.ResponseWriter, _ *http.Request) {
		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer conn.Close()
		if _, err = fmt.Fprint(rw, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"); err != nil {
			t.Error(err)
			return
		}
		if err = rw.Flush(); err != nil {
			t.Error(err)
		}
	})
	ts := httptest.NewServer(s)
	defer ts.Close()
	resp, err := ts.Client().Get(ts.URL + "/upgrade")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || string(body) != "ok" {
		t.Fatalf("%d %q", resp.StatusCode, body)
	}
}

func TestResponseControllerUnsupportedFlush(t *testing.T) {
	s := NewServer(Filter(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(struct{ http.ResponseWriter }{w}, r)
		})
	}))
	s.HandleFunc("/flush", func(w http.ResponseWriter, _ *http.Request) {
		if err := http.NewResponseController(w).Flush(); !errors.Is(err, http.ErrNotSupported) {
			t.Errorf("flush error = %v", err)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/flush", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("unsupported flush committed status %d", w.Code)
	}
}
