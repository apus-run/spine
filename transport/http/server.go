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
	"slices"
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

	mux   *http.ServeMux // main router: all business routes register here
	scope *http.ServeMux // optional prefix-scope entry gating header dispatch (nil when no PathPrefix)

	headerRoutes     []headerRoute          // HandleHeader registrations, in registration order (D2)
	routes           []*routeEntry          // Route Registry, in registration order
	sets             map[string]*bindingSet // registered patterns, keyed structurally
	notFound         http.Handler           // custom 404 body (status preserved)
	methodNotAllowed http.Handler           // custom 405 body (status + Allow preserved)
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
		sets:        make(map[string]*bindingSet),
	}
	for _, o := range opts {
		o(srv)
	}
	if srv.prefix != "" && srv.prefix != "/" {
		scope := http.NewServeMux()
		dispatch := http.HandlerFunc(srv.dispatch)
		// Segment-boundary scope (D3): the bare prefix and everything below it.
		scope.Handle(strings.TrimSuffix(srv.prefix, "/"), dispatch)
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
// Subtree semantics are preserved (no "{$}" exactness mapping).
func (s *Server) HandlePrefix(prefix string, h http.Handler) {
	full := s.fullPath(prefix)
	if full == "/" || strings.HasSuffix(full, "/") {
		// A trailing-slash prefix is a single subtree pattern.
		s.registerPattern(full, h)
		return
	}
	s.registerPattern(full, h)
	s.registerPattern(full+"/", h)
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

// register compiles a route template (any accepted grammar, see
// compileRoute) and registers it. method "" registers without a method
// constraint (matches all methods). Invalid templates panic at
// registration time; duplicate plain registrations keep the previous
// engine's first-registration-wins behavior instead of panicking (B-7).
func (s *Server) register(method, p string, h http.Handler) *routeEntry {
	cr, err := compileRoute(p)
	if err != nil {
		panic(err)
	}
	return s.addBinding(method, cr.pattern, &routeBinding{
		template:   p,
		action:     cr.action,
		bindings:   cr.bindings,
		validators: cr.validators,
		handler:    h,
	})
}

// registerPattern registers a raw ServeMux pattern (no template
// compilation, no "{$}" exactness mapping) - used by HandlePrefix, whose
// subtree semantics must survive. Duplicate patterns keep the first
// registration.
func (s *Server) registerPattern(pattern string, h http.Handler) {
	if _, ok := s.sets[pattern]; ok {
		return // first registration wins (B-7 previous behavior)
	}
	set := &bindingSet{pattern: pattern, wildcardNames: patternWildcards(pattern)}
	set.bindings = append(set.bindings, &routeBinding{template: pattern, handler: h})
	s.sets[pattern] = set
	s.mux.Handle(pattern, &bindingDispatcher{set: set})
	s.routes = append(s.routes, &routeEntry{path: pattern, pattern: pattern})
}

// addBinding attaches one logical binding to the pattern it compiles to.
// Patterns are keyed structurally (wildcard names normalized): bindings
// that compile to the same structure share one ServeMux registration and
// one dispatcher - custom actions join it, and duplicate plain
// registrations keep the first (previous engine behavior, B-7).
func (s *Server) addBinding(method, patternPath string, b *routeBinding) *routeEntry {
	if method != "" {
		for i := 0; i < len(method); i++ {
			if c := method[i]; c == ' ' || c == '\t' || c == '/' {
				panic(fmt.Sprintf("invalid method %q for route %q", method, patternPath))
			}
		}
	}
	pattern := patternPath
	if method != "" {
		pattern = method + " " + patternPath
	}
	key := normalizePattern(pattern)
	e := &routeEntry{method: method, path: b.template, pattern: pattern, bindings: b.bindings}
	s.routes = append(s.routes, e)

	set, ok := s.sets[key]
	if ok {
		if b.action == "" && !set.hasAction {
			// Duplicate plain registration: recorded for WalkRoute but the
			// first registration keeps serving (B-7).
			return e
		}
		renameWildcards(b, set.wildcardNames)
	} else {
		set = &bindingSet{pattern: pattern, wildcardNames: patternWildcards(patternPath)}
		s.sets[key] = set
		s.mux.Handle(pattern, &bindingDispatcher{set: set})
	}
	set.bindings = append(set.bindings, b)
	if b.action != "" {
		set.hasAction = true
	}
	return e
}

// routeBinding is one logical binding served under a pattern: the handler
// chain (filters already applied), the original template used as Transport
// metadata, the variable bindings, an optional custom-verb action and any
// residual value validators.
type routeBinding struct {
	template   string
	action     string
	bindings   []PathBinding
	validators []valueValidator
	handler    http.Handler
}

// bindingSet owns one ServeMux registration: every logical binding that
// compiles to the same structural pattern dispatches through it.
type bindingSet struct {
	pattern       string // registered ServeMux pattern (first registration's names)
	wildcardNames []string
	bindings      []*routeBinding
	hasAction     bool
}

// selectBinding picks the binding for a request: with actions, the last
// wildcard capture is split on the last ":" and the suffix selects the
// binding; the resource (suffix stripped) is returned for variable
// composition. Without actions the single (first) binding serves
// everything.
func (s *bindingSet) selectBinding(r *http.Request) (*routeBinding, *string) {
	if !s.hasAction {
		return s.bindings[0], nil
	}
	raw := r.PathValue(lastName(s.wildcardNames))
	resource, action := raw, ""
	if i := strings.LastIndex(raw, ":"); i >= 0 {
		resource, action = raw[:i], raw[i+1:]
	}
	for _, b := range s.bindings {
		if b.action == action {
			return b, &resource
		}
	}
	return nil, nil
}

// bindingDispatcher is the single handler registered on the ServeMux for
// a pattern. It selects the binding (custom action), fills the Transport
// metadata with the selected binding's template (S9.4: before filters),
// validates residual regex constraints, exposes the composed variables and
// runs the binding's handler chain. A request whose action matches no
// binding is a 404, mirroring the previous per-route regex behavior.
type bindingDispatcher struct {
	set *bindingSet
}

func (d *bindingDispatcher) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if sw, ok := w.(*statusWriter); ok {
		sw.matched = true
	}
	b, resource := d.set.selectBinding(r)
	if b == nil {
		http.NotFound(w, r)
		return
	}
	if tr, ok := transport.FromServerContext(r.Context()); ok {
		if htr, ok := tr.(*Transport); ok {
			htr.operation = b.template
			htr.pathTemplate = b.template
		}
	}
	overrideName, overrideValue := "", ""
	if resource != nil && len(d.set.wildcardNames) > 0 {
		overrideName, overrideValue = lastName(d.set.wildcardNames), *resource
	}
	for _, v := range b.validators {
		value := overrideValue
		if v.wildcard != overrideName {
			value = r.PathValue(v.wildcard)
		}
		if !v.re.MatchString(value) {
			http.NotFound(w, r)
			return
		}
	}
	if len(b.bindings) > 0 {
		vars := composeValues(b.bindings, r, overrideName, overrideValue)
		r = r.WithContext(context.WithValue(r.Context(), routeVarsCtxKey{}, vars))
	}
	b.handler.ServeHTTP(w, r)
}

// patternWildcards returns the wildcard names of a pattern path in order.
func patternWildcards(pattern string) []string {
	var names []string
	for i := 0; i < len(pattern); {
		if pattern[i] != '{' {
			i++
			continue
		}
		j := strings.IndexByte(pattern[i:], '}')
		if j < 0 {
			break
		}
		names = append(names, strings.TrimSuffix(pattern[i+1:i+j], "..."))
		i += j + 1
	}
	return names
}

// normalizePattern replaces wildcard names with placeholders so templates
// binding different field names at the same positions share one key.
func normalizePattern(pattern string) string {
	var b strings.Builder
	for i := 0; i < len(pattern); {
		c := pattern[i]
		if c != '{' {
			b.WriteByte(c)
			i++
			continue
		}
		j := strings.IndexByte(pattern[i:], '}')
		if j < 0 {
			b.WriteString(pattern[i:])
			break
		}
		if strings.HasSuffix(pattern[i+1:i+j], "...") {
			b.WriteString("{...}")
		} else {
			b.WriteString("{}")
		}
		i += j + 1
	}
	return b.String()
}

// renameWildcards rewrites a joining binding's wildcard names to the
// registered pattern's names positionally, so variable composition reads
// the wildcards the ServeMux actually set.
func renameWildcards(b *routeBinding, names []string) {
	if len(names) == 0 {
		return
	}
	pos := 0
	for bi := range b.bindings {
		for wi, w := range b.bindings[bi].Wildcards {
			if pos < len(names) && w != names[pos] {
				b.bindings[bi].Wildcards[wi] = names[pos]
			}
			pos++
		}
	}
}

func lastName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return names[len(names)-1]
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
	sw := &statusWriter{ResponseWriter: w, req: r, customNotFound: s.notFound, customMethodNotAllowed: s.methodNotAllowed}
	if s.strictSlash {
		sw.alias = s.strictSlashRedirect
	}
	if s.scope != nil {
		s.scope.ServeHTTP(sw, r)
	} else {
		s.dispatch(sw, r)
	}
	sw.finish()
}

// dispatch performs header dispatch (D2) before a single normal routing
// pass: the first matching header registration handles the request; on a
// miss the ServeMux decides 404/405/Allow and sets path values and Pattern.
func (s *Server) dispatch(w http.ResponseWriter, r *http.Request) {
	r.Pattern = "" // The private scope is not a selected business route.
	for _, hr := range s.headerRoutes {
		values := r.Header.Values(hr.key)
		if values != nil && (hr.val == "" || slices.Contains(values, hr.val)) {
			if tr, ok := transport.FromServerContext(r.Context()); ok {
				if tr, ok := tr.(*Transport); ok {
					tr.operation = headerOperation
					tr.pathTemplate = headerOperation
				}
			}
			if sw, ok := w.(*statusWriter); ok {
				sw.matched = true
			}
			hr.handler.ServeHTTP(w, r)
			return
		}
	}
	s.serveMux(w, r)
}

// serveMux performs exactly one normal routing pass.
func (s *Server) serveMux(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// strictSlashRedirect reports the redirect target when the request path
// differs from a registered route only by a trailing slash (D5). It looks
// the alternate path up through the ServeMux itself (never executing a
// handler) and only runs for unmatched requests.
func (s *Server) strictSlashRedirect(r *http.Request) (string, bool) {
	p := r.URL.EscapedPath()
	if p == "" || p == "/" || p == "*" {
		return "", false
	}
	alt := p + "/"
	if strings.HasSuffix(p, "/") {
		alt = strings.TrimSuffix(p, "/")
	}
	probe := r.Clone(r.Context())
	probe.URL.Path, _ = url.PathUnescape(alt)
	probe.URL.RawPath = alt
	h, pattern := s.mux.Handler(probe)
	d, exact := h.(*bindingDispatcher)
	if pattern == "" || !exact || strings.HasSuffix(d.set.pattern, "/") || strings.Contains(d.set.pattern, "...}") {
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
// a request that has not entered a registered handler are
// intercepted; anything a matched handler writes passes through untouched,
// and only router-produced redirects are normalized to 301.
type statusWriter struct {
	http.ResponseWriter
	req                    *http.Request
	customNotFound         http.Handler
	customMethodNotAllowed http.Handler
	alias                  func(*http.Request) (string, bool)

	matched     bool // a registered business/header handler has been entered
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
	// Informational responses do not commit the final response. Switching
	// protocols is terminal and retains the usual single-header behavior.
	if code >= 100 && code < 200 && code != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(code)
		return
	}
	w.wroteHeader = true
	w.status = code
	// The StrictSlash alias also needs the 404 intercepted, even when no
	// custom 404 handler is configured.
	suppress := code == http.StatusNotFound && (w.customNotFound != nil || w.alias != nil)
	suppress = suppress || (code == http.StatusMethodNotAllowed && w.customMethodNotAllowed != nil)
	if suppress && !w.matched {
		if code == http.StatusMethodNotAllowed {
			w.allow = w.Header().Get("Allow")
		}
		w.suppressed = true
		return
	}
	if !w.matched && (code == http.StatusTemporaryRedirect || code == http.StatusPermanentRedirect) {
		code = http.StatusMovedPermanently
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
	dw := &deferredWriter{ResponseWriter: w.ResponseWriter, code: code, allow: w.allow}
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

// deferredWriter preserves the routing status and Allow while the custom
// handler controls the response body and other headers.
type deferredWriter struct {
	http.ResponseWriter
	code  int
	allow string
	wrote bool
}

func (w *deferredWriter) WriteHeader(_ int) {
	if w.wrote {
		return
	}
	w.wrote = true
	if w.code == http.StatusMethodNotAllowed {
		w.Header().Set("Allow", w.allow)
	}
	w.ResponseWriter.WriteHeader(w.code)
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
