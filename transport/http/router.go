package http

import (
	"fmt"
	"net/http"
)

// WalkRouteFunc is the type of the function called for each route visited by Walk.
type WalkRouteFunc func(RouteInfo) error

// RouteInfo is an HTTP route info.
type RouteInfo struct {
	Path   string
	Method string
}

// HandlerFunc defines a function to serve HTTP requests.
type HandlerFunc func(Context) error

// Router is an HTTP router.
type Router struct {
	prefix  string
	srv     *Server
	filters []FilterFunc
}

func newRouter(prefix string, srv *Server, filters ...FilterFunc) *Router {
	r := &Router{
		prefix:  prefix,
		srv:     srv,
		filters: filters,
	}
	return r
}

// Group returns a new router group.
func (r *Router) Group(prefix string, filters ...FilterFunc) *Router {
	newFilters := make([]FilterFunc, 0, len(r.filters)+len(filters))
	newFilters = append(newFilters, r.filters...)
	newFilters = append(newFilters, filters...)
	return newRouter(joinPath(r.prefix, prefix), r.srv, newFilters...)
}

// Handle registers a new route with a matcher for the URL path and method.
// A method of "*" registers without a method constraint (matches every
// method), preserving the previous Router.Handle("*", ...) behavior; an
// empty method is rejected at registration time.
func (r *Router) Handle(method, relativePath string, h HandlerFunc, filters ...FilterFunc) {
	if method == "" {
		panic(fmt.Sprintf("invalid method %q for route %q: use \"*\" to match all methods", method, relativePath))
	}
	next := http.Handler(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		ctx := &wrapper{router: r}
		ctx.Reset(res, req)
		if err := h(ctx); err != nil {
			r.srv.ene(res, req, err)
		}
	}))
	next = FilterChain(filters...)(next)
	next = FilterChain(r.filters...)(next)
	if method == "*" {
		method = ""
	}
	r.srv.register(method, r.srv.fullPath(joinPath(r.prefix, relativePath)), next)
}

// RouteBinding is one logical route binding under a compiled ServeMux
// pattern, produced by protoc-gen-go-http at generation time (PRD §12.9).
// Template is the original google.api.http path template and becomes the
// Transport path template and initial Operation when the binding is
// selected. Action is the custom-verb suffix (":publish" templates compile
// to a wildcard that captures "resource:action"); a binding with an empty
// Action serves requests without a suffix.
type RouteBinding struct {
	Template string
	Action   string
	Bindings []PathBinding
	Handler  HandlerFunc
}

// HandleBindings registers one or more logical bindings that share a
// pre-compiled ServeMux pattern (generator output, PRD §12.9). When the
// pattern matches, the dispatcher selects the binding whose action suffix
// equals the request's, fills the Transport metadata with its template and
// exposes its variable values — all before the group filters and handler
// run (PRD §9.4, §12.6). A request whose suffix matches no binding gets a
// 404, mirroring the previous per-route regex behavior. The last wildcard
// carries the action suffix for custom-verb routes.
func (r *Router) HandleBindings(method, pattern string, bindings ...RouteBinding) {
	if method == "" || method == "*" {
		panic(fmt.Sprintf("invalid method %q for route %q: HandleBindings requires an explicit method", method, pattern))
	}
	full := r.srv.fullPath(joinPath(r.prefix, pattern))
	for _, rb := range bindings {
		h := r.handlerOf(rb.Handler)
		h = FilterChain(r.filters...)(h)
		r.srv.addBinding(method, full, &routeBinding{
			template: r.srv.fullPath(joinPath(r.prefix, rb.Template)),
			action:   rb.Action,
			bindings: rb.Bindings,
			handler:  h,
		})
	}
}

// handlerOf wraps a spine handler with the request context and the error
// encoder, the same wrapping Router.Handle applies.
func (r *Router) handlerOf(h HandlerFunc) http.Handler {
	return http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		ctx := &wrapper{router: r}
		ctx.Reset(res, req)
		if err := h(ctx); err != nil {
			r.srv.ene(res, req, err)
		}
	})
}

// GET registers a new GET route for a path with matching handler in the router.
func (r *Router) GET(path string, h HandlerFunc, m ...FilterFunc) {
	r.Handle(http.MethodGet, path, h, m...)
}

// HEAD registers a new HEAD route for a path with matching handler in the router.
func (r *Router) HEAD(path string, h HandlerFunc, m ...FilterFunc) {
	r.Handle(http.MethodHead, path, h, m...)
}

// POST registers a new POST route for a path with matching handler in the router.
func (r *Router) POST(path string, h HandlerFunc, m ...FilterFunc) {
	r.Handle(http.MethodPost, path, h, m...)
}

// PUT registers a new PUT route for a path with matching handler in the router.
func (r *Router) PUT(path string, h HandlerFunc, m ...FilterFunc) {
	r.Handle(http.MethodPut, path, h, m...)
}

// PATCH registers a new PATCH route for a path with matching handler in the router.
func (r *Router) PATCH(path string, h HandlerFunc, m ...FilterFunc) {
	r.Handle(http.MethodPatch, path, h, m...)
}

// DELETE registers a new DELETE route for a path with matching handler in the router.
func (r *Router) DELETE(path string, h HandlerFunc, m ...FilterFunc) {
	r.Handle(http.MethodDelete, path, h, m...)
}

// CONNECT registers a new CONNECT route for a path with matching handler in the router.
func (r *Router) CONNECT(path string, h HandlerFunc, m ...FilterFunc) {
	r.Handle(http.MethodConnect, path, h, m...)
}

// OPTIONS registers a new OPTIONS route for a path with matching handler in the router.
func (r *Router) OPTIONS(path string, h HandlerFunc, m ...FilterFunc) {
	r.Handle(http.MethodOptions, path, h, m...)
}

// TRACE registers a new TRACE route for a path with matching handler in the router.
func (r *Router) TRACE(path string, h HandlerFunc, m ...FilterFunc) {
	r.Handle(http.MethodTrace, path, h, m...)
}
