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

// PathBinding maps one business field of a request message to the
// wildcards of a compiled route pattern (PRD §12.3-§12.5). The field value
// is the Segments joined with "/": literal segments contribute themselves,
// "*" and "**" contribute the value of the matching pattern wildcard.
// A plain "{id}" variable is Binding{Field: "id", Segments: ["*"],
// Wildcards: ["id"]}; a composite resource name
// "/v1/{name=projects/*/books/*}" compiles to pattern
// "/v1/projects/{p0}/books/{p1}" with Segments
// ["projects", "*", "books", "*"] and Wildcards ["p0", "p1"], so the bound
// field receives the full resource name "projects/.../books/...".
type PathBinding struct {
	Field     string   // business field path, e.g. "name" or "book.name"
	Segments  []string // value segments: literals, "*" and "**"
	Wildcards []string // pattern wildcard name for each "*" / "**" segment
}

// composeValues builds the business-named variable values for a request.
// When a wildcard carries a custom-verb suffix ("42:publish"), the
// dispatcher passes the stripped resource ("42") through overrideName and
// overrideValue; other wildcards are read through Request.PathValue, which
// decodes each segment exactly once (D8).
func composeValues(bindings []PathBinding, r *http.Request, overrideName, overrideValue string) url.Values {
	vars := make(url.Values, len(bindings))
	for _, b := range bindings {
		var sb strings.Builder
		wi := 0
		for i, seg := range b.Segments {
			if i > 0 {
				sb.WriteByte('/')
			}
			if seg == "*" || seg == "**" {
				name := b.Wildcards[wi]
				wi++
				if name == overrideName {
					sb.WriteString(overrideValue)
				} else {
					sb.WriteString(r.PathValue(name))
				}
			} else {
				sb.WriteString(seg)
			}
		}
		vars[b.Field] = []string{sb.String()}
	}
	return vars
}

// routeEntry is one Route Registry record. The registry records
// registration-time metadata only; it never participates in request
// matching and keeps no second priority system (PRD §7.3).
type routeEntry struct {
	method   string        // "" means the route was registered without a method constraint
	path     string        // original path template as registered (before pattern rewrite)
	pattern  string        // ServeMux pattern the route is registered under
	bindings []PathBinding // field extraction descriptors
}

// values extracts this entry's path variables from a matched request.
func (e *routeEntry) values(r *http.Request) url.Values {
	if len(e.bindings) == 0 {
		return url.Values{}
	}
	return composeValues(e.bindings, r, "", "")
}

// headerRoute records one HandleHeader registration (D2): dispatch runs
// before normal routing, in registration order; the first matching
// registration handles the request.
type headerRoute struct {
	key     string
	val     string
	handler http.Handler
}

// routeVarsCtxKey is the request context key carrying the computed path
// variable values (business field names) of the selected route binding.
// Context.Vars and DefaultRequestVars read them; the values are computed
// once after route selection (PRD §5.5).
type routeVarsCtxKey struct{}

// compilePath rewrites a registered path template into a ServeMux pattern
// path and its field bindings.
//
// Gorilla-style single-segment variables with a regex constraint
// ("{id:[0-9]+}") become plain wildcards ("{id}"); the constraint is
// dropped because the standard library has no per-variable regex support.
// Multi-segment wildcards ("{name...}") are preserved. Templates whose
// constraint spans multiple segments ("{name=a/*}" style resource names)
// are rejected here: they are generator territory and must be compiled by
// protoc-gen-go-http (PRD §12), not silently narrowed. Field paths with a
// dot ("{book.name}") are likewise generator territory.
func compilePath(p string) (string, []PathBinding, error) {
	if p == "" {
		return "", nil, fmt.Errorf("invalid route path %q: empty", p)
	}
	var bindings []PathBinding
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
		constraint := ""
		if j := strings.IndexByte(seg, ':'); j >= 0 {
			name, constraint = seg[:j], seg[j+1:]
		}
		// A constraint spanning multiple segments ("/" or "**") is the old
		// generator's Gorilla regex output; narrowing it to a single
		// wildcard would silently change matching. Reject at registration
		// so stale generated code fails loudly instead of mis-routing.
		if strings.Contains(constraint, "/") || strings.Contains(constraint, "**") {
			return "", nil, fmt.Errorf(
				"invalid route path %q: multi-segment constraint %q is not supported; regenerate *_http.pb.go with the current protoc-gen-go-http, or register the compiled pattern via Router.HandleBindings",
				p, constraint)
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
			bindings = append(bindings, PathBinding{Field: name, Segments: []string{"**"}, Wildcards: []string{name}})
		} else {
			b.WriteString("{")
			b.WriteString(name)
			b.WriteString("}")
			bindings = append(bindings, PathBinding{Field: name, Segments: []string{"*"}, Wildcards: []string{name}})
		}
		i += end + 1
	}
	return b.String(), bindings, nil
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
