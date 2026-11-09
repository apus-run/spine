package http

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"reflect"
	"testing"
)

func TestProductionWildcardNames(t *testing.T) {
	s := NewServer()
	s.Route("/").GET("/{book.name}/{p0}/{p01}", func(c Context) error {
		want := map[string]string{"book.name": "book", "p0": "zero", "p01": "one"}
		for k, v := range want {
			if c.Vars().Get(k) != v {
				t.Errorf("%s: got %q want %q", k, c.Vars().Get(k), v)
			}
		}
		return c.String(200, "ok")
	})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, httptest.NewRequest("GET", "/book/zero/one", nil))
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
}

func TestProductionRegexQuantifiers(t *testing.T) {
	for _, pattern := range []string{"/items/{id:[0-9]{2}}", "/items/{id:[0-9]{2,3}}", "/items/{id:[0-9]{2}}:publish"} {
		t.Run(pattern, func(t *testing.T) {
			s := NewServer()
			s.Route("/").POST(pattern, func(c Context) error { return c.String(200, c.Vars().Get("id")) })
			suffix := ""
			if pattern[len(pattern)-1] != '}' {
				suffix = ":publish"
			}
			for _, tc := range []struct {
				value string
				code  int
			}{{"12", 200}, {"1", 404}, {"a12", 404}} {
				w := httptest.NewRecorder()
				s.ServeHTTP(w, httptest.NewRequest("POST", "/items/"+tc.value+suffix, nil))
				if w.Code != tc.code {
					t.Errorf("%s: got %d want %d", tc.value, w.Code, tc.code)
				}
			}
		})
	}
}

func TestProductionLiteralActions(t *testing.T) {
	s := NewServer()
	for _, action := range []string{"publish", "archive"} {
		s.Route("/").POST("/v1/{name=books/special}:"+action, func(c Context) error { return c.String(200, action+":"+c.Vars().Get("name")) })
	}
	for _, tc := range []struct {
		path, body string
		code       int
	}{
		{"/v1/books/special:publish", "publish:books/special", 200},
		{"/v1/books/special:archive", "archive:books/special", 200},
		{"/v1/books/special:unknown", "", 404},
		{"/v1/books/special", "", 404},
	} {
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("POST", tc.path, nil))
		if w.Code != tc.code || (tc.code == 200 && w.Body.String() != tc.body) {
			t.Errorf("%s: %d %q", tc.path, w.Code, w.Body.String())
		}
	}
}

func TestProductionInformationalResponse(t *testing.T) {
	s := NewServer()
	s.HandleFunc("/hints", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(103)
		w.WriteHeader(103)
		w.WriteHeader(201)
		_, _ = w.Write([]byte("created"))
	})
	ts := httptest.NewServer(s)
	defer ts.Close()
	var hints []int
	req, _ := http.NewRequest("GET", ts.URL+"/hints", nil)
	trace := &httptrace.ClientTrace{Got1xxResponse: func(code int, _ textproto.MIMEHeader) error {
		hints = append(hints, code)
		return nil
	}}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 201 || string(body) != "created" || !reflect.DeepEqual(hints, []int{103, 103}) {
		t.Fatalf("final=%d body=%q hints=%v", resp.StatusCode, body, hints)
	}
}

func TestProductionRegexBraceCharacters(t *testing.T) {
	for _, pattern := range []string{`/items/{id:\{[0-9]{2}\}}`, `/items/{id:[{][0-9]{2}[}]}`} {
		t.Run(pattern, func(t *testing.T) {
			s := NewServer()
			s.Route("/").GET(pattern, func(c Context) error { return c.String(200, c.Vars().Get("id")) })
			w := httptest.NewRecorder()
			s.ServeHTTP(w, httptest.NewRequest("GET", "/items/%7B12%7D", nil))
			if w.Code != 200 || w.Body.String() != "{12}" {
				t.Fatalf("%d %q", w.Code, w.Body.String())
			}
		})
	}
}
