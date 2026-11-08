package http

import (
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// unmatchedOperation is the fixed initial Operation and path template of the
// request-scoped Transport for requests that no route has been selected for
// yet (including 404/405 and framework redirects). Selected routes replace
// both with the registered template (decision D6).
const unmatchedOperation = "http.unmatched"

// headerOperation is the fixed Operation and path template used when a
// HandleHeader dispatch handles a request (decision D2). The raw request
// path is never used as a template for header dispatch.
const headerOperation = "http.header"

// routeVar describes one path variable of a registered route. name is the
// wildcard name used in the ServeMux pattern; field is the business field
// name the value binds to (empty means same as name). Generated routes map
// internal names ("p0") back to their original field paths ("book.name").
type routeVar struct {
	name  string
	field string
}

// routeEntry is one Route Registry record. The registry records
// registration-time metadata only; it never participates in request
// matching and keeps no second priority system (PRD §7.3).
type routeEntry struct {
	method  string     // "" means the route was registered without a method constraint
	path    string     // original path template as registered (before pattern rewrite)
	pattern string     // ServeMux pattern the route is registered under
	vars    []routeVar // path variable descriptors
}

// values extracts this entry's path variables from a matched request.
// Values are read through Request.PathValue which decodes each segment
// exactly once; no additional unescaping is applied (D8).
func (e *routeEntry) values(r *http.Request) url.Values {
	vars := make(url.Values, len(e.vars))
	for _, v := range e.vars {
		name := v.field
		if name == "" {
			name = v.name
		}
		vars[name] = []string{r.PathValue(v.name)}
	}
	return vars
}

// headerRoute records one HandleHeader registration (D2): dispatch runs
// before normal routing, in registration order; the first matching
// registration handles the request.
type headerRoute struct {
	key     string
	val     string
	handler http.Handler
}

// routeVarsCtxKey is the request context key carrying the selected
// routeEntry whose variable descriptors Context.Vars and DefaultRequestVars
// read. The entry is placed on the request after route selection (PRD §5.5).
type routeVarsCtxKey struct{}

// compilePath rewrites a registered path template into a ServeMux pattern
// path and its variable descriptors.
//
// Gorilla-style single-segment variables with a regex constraint
// ("{id:[0-9]+}") become plain wildcards ("{id}"); the constraint is
// dropped because the standard library has no per-variable regex support.
// Multi-segment wildcards ("{name...}") are preserved. Templates whose
// constraint spans multiple segments ("{name=a/*}" style resource names)
// are rejected here: they are generator territory and must be compiled by
// protoc-gen-go-http (PRD §12), not silently narrowed.
func compilePath(p string) (string, []routeVar, error) {
	if p == "" {
		return "", nil, fmt.Errorf("invalid route path %q: empty", p)
	}
	var vars []routeVar
	var b strings.Builder
	for i := 0; i < len(p); {
		if p[i] != '{' {
			b.WriteByte(p[i])
			i++
			continue
		}
		end := strings.IndexByte(p[i:], '}')
		if end < 0 {
			return "", nil, fmt.Errorf("invalid route path %q: unclosed '{'", p)
		}
		seg := p[i+1 : i+end]
		name := seg
		if j := strings.IndexByte(seg, ':'); j >= 0 {
			name = seg[:j]
		}
		multi := strings.HasSuffix(name, "...")
		name = strings.TrimSuffix(name, "...")
		if name == "" {
			return "", nil, fmt.Errorf("invalid route path %q: empty wildcard name", p)
		}
		for k := 0; k < len(name); k++ {
			c := name[k]
			if !(c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9') {
				return "", nil, fmt.Errorf("invalid route path %q: wildcard name %q must match [A-Za-z0-9_]", p, name)
			}
		}
		if multi {
			b.WriteString("{")
			b.WriteString(name)
			b.WriteString("...}")
		} else {
			b.WriteString("{")
			b.WriteString(name)
			b.WriteString("}")
		}
		vars = append(vars, routeVar{name: name})
		i += end + 1
	}
	return b.String(), vars, nil
}

// buildPattern composes the full ServeMux pattern for a registration.
// method "*" and "" semantics:
//   - "*" registers without a method constraint (matches every method),
//     matching the previous Router.Handle("*", ...) behavior.
//   - "" is rejected: the previous engine could never match it and the
//     standard library has no equivalent dead pattern, so it is exposed at
//     registration time instead of being silently remapped (PRD §5.4).
func buildPattern(method, fullPath string) (string, error) {
	switch method {
	case "*":
		return fullPath, nil
	case "":
		return "", fmt.Errorf("invalid method %q for route %q: use \"*\" to match all methods", method, fullPath)
	}
	for i := 0; i < len(method); i++ {
		c := method[i]
		if c == ' ' || c == '\t' || c == '/' {
			return "", fmt.Errorf("invalid method %q for route %q", method, fullPath)
		}
	}
	return method + " " + fullPath, nil
}

// joinPath combines a route prefix and a relative path without path.Join's
// trailing-slash loss: a relative path ending in "/" keeps its trailing
// slash so subtree registrations survive composition (PRD §5.6). The result
// is always cleaned of duplicate slashes and "." / ".." segments.
func joinPath(prefix, rel string) string {
	p := path.Join(prefix, rel)
	if p == "/" {
		return "/"
	}
	if strings.HasSuffix(rel, "/") && !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}
