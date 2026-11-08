package http

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/apus-run/spine/v3/internal/endpoint"
	"github.com/apus-run/spine/v3/internal/host"
	"github.com/apus-run/spine/v3/internal/matcher"
	"github.com/apus-run/spine/v3/log"
	"github.com/apus-run/spine/v3/middleware"
	"github.com/apus-run/spine/v3/transport"
)

var (
	_ transport.Server     = (*Server)(nil)
	_ transport.Endpointer = (*Server)(nil)
	_ http.Handler         = (*Server)(nil)
)

// ServerOption is an HTTP server option.
type ServerOption func(*Server)

// Network with server network.
func Network(network string) ServerOption {
	return func(s *Server) {
		s.network = network
	}
}

// Address with server address.
func Address(addr string) ServerOption {
	return func(s *Server) {
		s.address = addr
	}
}

// Endpoint with server address.
func Endpoint(endpoint *url.URL) ServerOption {
	return func(s *Server) {
		s.endpoint = endpoint
	}
}

// Timeout with server timeout.
func Timeout(timeout time.Duration) ServerOption {
	return func(s *Server) {
		s.timeout = timeout
	}
}

// Middleware with service middleware option.
func Middleware(m ...middleware.Middleware) ServerOption {
	return func(o *Server) {
		o.middleware.Use(m...)
	}
}

// Filter with HTTP middleware option.
func Filter(filters ...FilterFunc) ServerOption {
	return func(o *Server) {
		o.filters = filters
	}
}

// RequestVarsDecoder with request decoder.
func RequestVarsDecoder(dec DecodeRequestFunc) ServerOption {
	return func(o *Server) {
		o.decVars = dec
	}
}

// RequestQueryDecoder with request decoder.
func RequestQueryDecoder(dec DecodeRequestFunc) ServerOption {
	return func(o *Server) {
		o.decQuery = dec
	}
}

// RequestDecoder with request decoder.
func RequestDecoder(dec DecodeRequestFunc) ServerOption {
	return func(o *Server) {
		o.decBody = dec
	}
}

// ResponseEncoder with response encoder.
func ResponseEncoder(en EncodeResponseFunc) ServerOption {
	return func(o *Server) {
		o.enc = en
	}
}

// ErrorEncoder with error encoder.
func ErrorEncoder(en EncodeErrorFunc) ServerOption {
	return func(o *Server) {
		o.ene = en
	}
}

// TLSConfig with TLS config.
func TLSConfig(c *tls.Config) ServerOption {
	return func(o *Server) {
		o.tlsConf = c
	}
}

// StrictSlash keeps the exact-route trailing-slash alias: when enabled and a
// request path is not matched, a path that differs from a registered exact
// route only by a trailing slash is redirected to it (301, decision D5).
// It does not affect the ServeMux-native subtree append-slash and path
// cleaning redirects. When disabled, exact routes match their path exactly.
func StrictSlash(strictSlash bool) ServerOption {
	return func(o *Server) {
		o.strictSlash = strictSlash
	}
}

// Listener with server lis
func Listener(lis net.Listener) ServerOption {
	return func(s *Server) {
		s.lis = lis
	}
}

// PathPrefix scopes every subsequent registration under prefix. The prefix
// matches on path-segment boundaries: "/api" covers "/api" and "/api/..."
// but not "/apiary" (decision D3). When combined with HandleHeader, header
// dispatch only applies within the scope (decision D2).
func PathPrefix(prefix string) ServerOption {
	return func(s *Server) {
		s.prefix = joinPath(s.prefix, prefix)
	}
}

// NotFoundHandler customizes the response body of unmatched requests. The
// 404 status is preserved; the handler must not run business logic.
func NotFoundHandler(handler http.Handler) ServerOption {
	return func(s *Server) {
		s.notFound = handler
	}
}

// MethodNotAllowedHandler customizes the response body of method-mismatch
// responses. The 405 status and the Allow header are preserved (decision D1).
func MethodNotAllowedHandler(handler http.Handler) ServerOption {
	return func(s *Server) {
		s.methodNotAllowed = handler
	}
}

// Server is an HTTP server wrapper.
type Server struct {
	*http.Server
	lis      net.Listener
	tlsConf  *tls.Config
	endpoint *url.URL
	err      error
	network  string
	address  string
	timeout  time.Duration
	filters  []FilterFunc

	middleware matcher.Matcher
	decVars    DecodeRequestFunc
	decQuery   DecodeRequestFunc
	decBody    DecodeRequestFunc
	enc        EncodeResponseFunc
	ene        EncodeErrorFunc

	strictSlash bool
	prefix      string // PathPrefix option scope, composed into registrations

	mux  *http.ServeMux // main router: all business routes register here
	scope *http.ServeMux // optional prefix-scope entry gating header dispatch (nil when no PathPrefix)

	headerRoutes     []headerRoute  // HandleHeader registrations, in registration order (D2)
	routes           []*routeEntry  // Route Registry, in registration order
	notFound         http.Handler   // custom 404 body (status preserved)
	methodNotAllowed http.Handler   // custom 405 body (status + Allow preserved)
}

// NewServer creates an HTTP server by options.
func NewServer(opts ...ServerOption) *Server {
	srv := &Server{
		network:     "tcp",
		address:     ":0",
		timeout:     1 * time.Second,
		middleware:  matcher.New(),
		decVars:     DefaultRequestVars,
		decQuery:    DefaultRequestQuery,
		decBody:     DefaultRequestDecoder,
		enc:         DefaultResponseEncoder,
		ene:         DefaultErrorEncoder,
		strictSlash: true,
		mux:         http.NewServeMux(),
	}
	for _, o := range opts {
		o(srv)
	}
	if srv.prefix != "" && srv.prefix != "/" {
		scope := http.NewServeMux()
		dispatch := http.HandlerFunc(srv.dispatch)
		// Segment-boundary scope (D3): the bare prefix and everything below it.
		scope.Handle(srv.prefix, dispatch)
		scope.Handle(strings.TrimSuffix(srv.prefix, "/")+"/", dispatch)
		srv.scope = scope
	}
	handler := http.Handler(http.HandlerFunc(srv.route))
	if len(srv.filters) > 0 {
		handler = FilterChain(srv.filters...)(handler)
	}
	srv.Server = &http.Server{
		Handler:   srv.transportHandler(handler),
		TLSConfig: srv.tlsConf,
	}
	return srv
}

// Use uses a service middleware with selector.
// selector:
//   - '/*'
//   - '/helloworld.v1.Greeter/*'
//   - '/helloworld.v1.Greeter/SayHello'
func (s *Server) Use(selector string, m ...middleware.Middleware) {
	s.middleware.Add(selector, m...)
}

// WalkRoute calls fn for each registered route with a method constraint,
// in registration order. Registrations without a method constraint
// (Handle with "*", HandleFunc, HandlePrefix) are skipped, matching the
// previous WalkRoute behavior.
func (s *Server) WalkRoute(fn WalkRouteFunc) error {
	for _, e := range s.routes {
		if e.method == "" {
			continue
		}
		if err := fn(RouteInfo{Method: e.method, Path: e.path}); err != nil {
			return err
		}
	}
	return nil
}

// WalkHandle walks the registered routes and calls handle for each.
func (s *Server) WalkHandle(handle func(method, path string, handler http.HandlerFunc)) error {
	return s.WalkRoute(func(r RouteInfo) error {
		handle(r.Method, r.Path, s.ServeHTTP)
		return nil
	})
}

// Route registers an HTTP router.
func (s *Server) Route(prefix string, filters ...FilterFunc) *Router {
	return newRouter(prefix, s, filters...)
}

// Handle registers a new route with a matcher for the URL path.
func (s *Server) Handle(path string, h http.Handler) {
	s.register("", s.fullPath(path), h)
}

// HandlePrefix registers a new route with a matcher for the URL path prefix.
// The prefix matches on path-segment boundaries (decision D3): a prefix
// "/api" serves "/api" itself and every "/api/..." path, but not "/apiary".
func (s *Server) HandlePrefix(prefix string, h http.Handler) {
	full := s.fullPath(prefix)
	if full == "/" || strings.HasSuffix(full, "/") {
		// A trailing-slash prefix is a single subtree pattern.
		s.register("", full, h)
		return
	}
	s.register("", full, h)
	s.register("", full+"/", h)
}

// HandleFunc registers a new route with a matcher for the URL path.
func (s *Server) HandleFunc(path string, h http.HandlerFunc) {
	s.register("", s.fullPath(path), h)
}

// HandleHeader registers a handler dispatched on an HTTP header value
// before normal routing (decision D2): the first registration whose key and
// value match handles the request regardless of path and method; when no
// header registration matches, the request goes through normal routing
// exactly once. When a PathPrefix scope is configured, header dispatch only
// applies within the scope.
func (s *Server) HandleHeader(key, val string, h http.HandlerFunc) {
	s.headerRoutes = append(s.headerRoutes, headerRoute{key: key, val: val, handler: h})
}

// ServeHTTP should write reply headers and data to the ResponseWriter and then return.
func (s *Server) ServeHTTP(res http.ResponseWriter, req *http.Request) {
	s.Handler.ServeHTTP(res, req)
}

// fullPath composes a registration path with the PathPrefix scope.
func (s *Server) fullPath(p string) string {
	if s.prefix == "" || s.prefix == "/" {
		return p
	}
	return joinPath(s.prefix, p)
}

// register compiles a route, registers it on the main ServeMux and records
// it in the Route Registry. Pattern conflicts and invalid patterns panic at
// registration time (initialization) and are not silently ignored (§14.4).
// method "" registers without a method constraint (matches all methods).
func (s *Server) register(method, p string, h http.Handler) *routeEntry {
	patternPath, vars, err := compilePath(p)
	if err != nil {
		panic(err)
	}
	pattern := patternPath
	if method != "" {
		for i := 0; i < len(method); i++ {
			if c := method[i]; c == ' ' || c == '\t' || c == '/' {
				panic(fmt.Sprintf("invalid method %q for route %q", method, p))
			}
		}
		pattern = method + " " + patternPath
	}
	e := &routeEntry{method: method, path: p, pattern: pattern, vars: vars}
	s.mux.Handle(pattern, s.routeHandler(e, h))
	s.routes = append(s.routes, e)
	return e
}

// routeHandler wraps a registered handler with the route metadata layer: it
// fills the request Transport with the selected route's template (replacing
// the initial "http.unmatched" values, D6) and exposes the route's variable
// descriptors on the request context (PRD §5.5) before group/route filters
// and the handler run.
func (s *Server) routeHandler(e *routeEntry, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tr, ok := transport.FromServerContext(r.Context()); ok {
			if tr, ok := tr.(*Transport); ok {
				tr.operation = e.path
				tr.pathTemplate = e.path
			}
		}
		if len(e.vars) > 0 {
			r = r.WithContext(context.WithValue(r.Context(), routeVarsCtxKey{}, e))
		}
		next.ServeHTTP(w, r)
	})
}

// transportHandler is the outermost request layer (PRD §4.2): it creates
// the timeout context and the request-scoped Transport for every request —
// including unmatched (404/405) and redirected ones — with the fixed
// initial metadata (D6), then enters the global filters.
func (s *Server) transportHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var (
			ctx    context.Context
			cancel context.CancelFunc
		)
		if s.timeout > 0 {
			ctx, cancel = context.WithTimeout(req.Context(), s.timeout)
		} else {
			ctx, cancel = context.WithCancel(req.Context())
		}
		defer cancel()

		tr := &Transport{
			operation:    unmatchedOperation,
			pathTemplate: unmatchedOperation,
			reqHeader:    headerCarrier(req.Header),
			replyHeader:  headerCarrier(w.Header()),
			request:      req,
			response:     w,
		}
		if s.endpoint != nil {
			tr.endpoint = s.endpoint.String()
		}
		tr.request = req.WithContext(transport.NewServerContext(ctx, tr))
		next.ServeHTTP(w, tr.request)
	})
}

// route is the entry behind the global filters: it enters the prefix scope
// when a PathPrefix option is configured (header dispatch then applies only
// within the scope, D2), otherwise it dispatches directly.
func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	if s.scope != nil {
		s.scope.ServeHTTP(w, r)
		return
	}
	s.dispatch(w, r)
}

// dispatch performs header dispatch (D2) before a single normal routing
// pass: the first matching header registration handles the request; on a
// miss the ServeMux decides 404/405/Allow and sets path values and Pattern.
func (s *Server) dispatch(w http.ResponseWriter, r *http.Request) {
	for _, hr := range s.headerRoutes {
		if r.Header.Get(hr.key) == hr.val {
			if tr, ok := transport.FromServerContext(r.Context()); ok {
				if tr, ok := tr.(*Transport); ok {
					tr.operation = headerOperation
					tr.pathTemplate = headerOperation
				}
			}
			hr.handler.ServeHTTP(w, r)
			return
		}
	}
	s.serveMux(w, r)
}

// serveMux routes through the main ServeMux. A response writer wrapper is
// installed when a custom 404/405 handler or the StrictSlash alias may need
// to replace the mux's default responses; otherwise requests pass through
// unwrapped.
func (s *Server) serveMux(w http.ResponseWriter, r *http.Request) {
	if s.notFound == nil && s.methodNotAllowed == nil && !s.strictSlash {
		s.mux.ServeHTTP(w, r)
		return
	}
	sw := &statusWriter{
		ResponseWriter:         w,
		req:                    r,
		customNotFound:         s.notFound,
		customMethodNotAllowed: s.methodNotAllowed,
		alias:                  s.strictSlashRedirect,
	}
	s.mux.ServeHTTP(sw, r)
	sw.finish()
}

// strictSlashRedirect reports the redirect target when the request path
// differs from a registered route only by a trailing slash (D5). It looks
// the alternate path up through the ServeMux itself (never executing a
// handler) and only runs for unmatched requests.
func (s *Server) strictSlashRedirect(r *http.Request) (string, bool) {
	p := r.URL.Path
	if p == "" || p == "/" || p == "*" {
		return "", false
	}
	alt := p + "/"
	if strings.HasSuffix(p, "/") {
		alt = strings.TrimSuffix(p, "/")
	}
	probe := r.Clone(r.Context())
	probe.URL.Path = alt
	if _, pattern := s.mux.Handler(probe); pattern == "" {
		return "", false
	}
	loc := alt
	if r.URL.RawQuery != "" {
		loc += "?" + r.URL.RawQuery
	}
	return loc, true
}

// statusWriter wraps the ResponseWriter handed to the ServeMux so the mux's
// own 404/405 responses can be replaced. Only responses the mux writes for
// a request it did not match (r.Pattern == "", nothing written yet) are
// intercepted; anything a matched handler writes passes through untouched,
// and redirect responses are never modified.
type statusWriter struct {
	http.ResponseWriter
	req                    *http.Request
	customNotFound         http.Handler
	customMethodNotAllowed http.Handler
	alias                  func(*http.Request) (string, bool)

	wroteHeader bool
	status      int
	suppressed  bool
	allow       string
}

func (w *statusWriter) customFor(code int) http.Handler {
	switch code {
	case http.StatusNotFound:
		return w.customNotFound
	case http.StatusMethodNotAllowed:
		return w.customMethodNotAllowed
	}
	return nil
}

func (w *statusWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = code
	// The StrictSlash alias also needs the 404 intercepted, even when no
	// custom 404 handler is configured.
	suppress := code == http.StatusNotFound && (w.customNotFound != nil || w.alias != nil)
	suppress = suppress || (code == http.StatusMethodNotAllowed && w.customMethodNotAllowed != nil)
	if suppress && w.req.Pattern == "" {
		if code == http.StatusMethodNotAllowed {
			w.allow = w.Header().Get("Allow")
		}
		w.suppressed = true
		return
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.suppressed {
		return len(p), nil // swallow the default status body
	}
	return w.ResponseWriter.Write(p)
}

// finish completes a suppressed default response, in order: the
// StrictSlash alias redirect (D5), the custom handler body (404/405, with
// status and Allow preserved), or a replay of the default response.
func (w *statusWriter) finish() {
	if !w.suppressed {
		return
	}
	if w.status == http.StatusNotFound && w.alias != nil {
		if loc, ok := w.alias(w.req); ok {
			w.Header().Del("Content-Type")
			w.Header().Del("Content-Length")
			w.Header().Del("X-Content-Type-Options")
			http.Redirect(w.ResponseWriter, w.req, loc, http.StatusMovedPermanently)
			return
		}
	}
	custom := w.customFor(w.status)
	if custom == nil {
		w.replayDefault()
		return
	}
	code := w.status
	// Drop the default error body headers; the custom handler sets its own.
	w.Header().Del("Content-Type")
	w.Header().Del("Content-Length")
	w.Header().Del("X-Content-Type-Options")
	if w.status == http.StatusMethodNotAllowed {
		w.Header().Set("Allow", w.allow)
	}
	dw := &deferredWriter{ResponseWriter: w.ResponseWriter, code: code}
	custom.ServeHTTP(dw, w.req)
	dw.finish()
}

// replayDefault reproduces the ServeMux's default response for the
// suppressed status, including the Allow header for 405.
func (w *statusWriter) replayDefault() {
	if w.status == http.StatusMethodNotAllowed && w.allow != "" {
		w.Header().Set("Allow", w.allow)
	}
	text := "404 page not found"
	if w.status != http.StatusNotFound {
		text = http.StatusText(w.status)
	}
	http.Error(w.ResponseWriter, text, w.status)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("http: response writer does not implement http.Hijacker")
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// deferredWriter writes code when the handler finishes without writing a
// status itself.
type deferredWriter struct {
	http.ResponseWriter
	code  int
	wrote bool
}

func (w *deferredWriter) WriteHeader(code int) {
	if w.wrote {
		return
	}
	w.wrote = true
	w.ResponseWriter.WriteHeader(code)
}

func (w *deferredWriter) Write(p []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(w.code)
	}
	return w.ResponseWriter.Write(p)
}

func (w *deferredWriter) finish() {
	if !w.wrote {
		w.WriteHeader(w.code)
	}
}

// Endpoint return a real address to registry endpoint.
// examples:
//
//	https://127.0.0.1:8000
//	Legacy: http://127.0.0.1:8000?isSecure=false
func (s *Server) Endpoint() (*url.URL, error) {
	if err := s.listenAndEndpoint(); err != nil {
		return nil, err
	}
	return s.endpoint, nil
}

// Start start the HTTP server.
func (s *Server) Start(ctx context.Context) error {
	if err := s.listenAndEndpoint(); err != nil {
		return err
	}
	s.BaseContext = func(net.Listener) context.Context {
		return ctx
	}
	log.Info("[HTTP] server listening", "addr", s.lis.Addr().String())
	var err error
	if s.tlsConf != nil {
		err = s.ServeTLS(s.lis, "", "")
	} else {
		err = s.Serve(s.lis)
	}
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Stop stop the HTTP server.
func (s *Server) Stop(ctx context.Context) error {
	log.Info("[HTTP] server stopping")
	err := s.Shutdown(ctx)
	if err != nil {
		if ctx.Err() != nil {
			log.Warn("[HTTP] server couldn't stop gracefully in time, doing force stop")
			err = s.Close()
		}
	}
	return err
}

func (s *Server) listenAndEndpoint() error {
	if s.lis == nil {
		lis, err := net.Listen(s.network, s.address)
		if err != nil {
			s.err = err
			return err
		}
		s.lis = lis
	}
	if s.endpoint == nil {
		addr, err := host.Extract(s.address, s.lis)
		if err != nil {
			s.err = err
			return err
		}
		s.endpoint = endpoint.NewEndpoint(endpoint.Scheme("http", s.tlsConf != nil), addr)
	}
	return s.err
}
