package main

// compilePattern compiles a google.api.http path template into a ServeMux
// pattern plus field bindings, at generation time (PRD §12.9). The compiled
// pattern uses synthetic wildcard names ("p0", "p1", ...); the bindings map
// business field paths back to those wildcards, including the literal
// segments of composite resource names.
//
//	/v1/users/{id}                    -> /v1/users/{p0}            id <- p0
//	/v1/books/{book.name}             -> /v1/books/{p0}            book.name <- p0
//	/v1/{name=projects/*/books/*}     -> /v1/projects/{p0}/books/{p1}
//	                                                                  name = projects/p0/books/p1
//	/v1/{name=messages/**}            -> /v1/messages/{p0...}      name = messages/+p0
//	/v1/{name=books/*}:publish        -> /v1/{p0}                  action "publish",
//	                                                                  p0 captures "42:publish"

import (
	"fmt"
	"os"
	"strings"
)

type compiledVar struct {
	Field     string   // business field path ("name", "book.name")
	Segments  []string // value segments: literals, "*" and "**"
	Wildcards []string // ServeMux wildcard names for the "*" / "**" segments
}

type compiledPattern struct {
	Path     string        // ServeMux pattern path
	Template string        // original template (kept for metadata)
	Vars     []compiledVar // one per template variable
	Action   string        // trailing custom-verb suffix ("" when absent)
}

func compilePattern(tpl string) (*compiledPattern, error) {
	if strings.HasSuffix(tpl, "/") {
		fmt.Fprintf(os.Stderr, "\u001B[33mWARN\u001B[m: Path %s should not end with \"/\" \n", tpl)
	}
	cp := &compiledPattern{Template: tpl}
	wc := 0
	nextWildcard := func() string {
		name := fmt.Sprintf("p%d", wc)
		wc++
		return name
	}

	// A trailing ":verb" after the last '}' is a custom action. Templates
	// without variables keep any suffix literal (nothing can carry an
	// action), matching the previous pass-through behavior.
	rest := tpl
	if i := strings.LastIndex(rest, "}"); i >= 0 {
		tail := rest[i+1:]
		if strings.HasPrefix(tail, ":") && isVerb(tail[1:]) {
			cp.Action = tail[1:]
			rest = rest[:i+1]
		}
	}

	var path strings.Builder
	for i := 0; i < len(rest); {
		c := rest[i]
		if c != '{' {
			if c == '}' {
				return nil, fmt.Errorf("invalid path template %q: unexpected '}'", tpl)
			}
			path.WriteByte(c)
			i++
			continue
		}
		end := strings.IndexByte(rest[i:], '}')
		if end < 0 {
			return nil, fmt.Errorf("invalid path template %q: unclosed '{'", tpl)
		}
		body := rest[i+1 : i+end]
		field, constraint := body, ""
		if j := strings.IndexByte(body, '='); j >= 0 {
			field, constraint = body[:j], body[j+1:]
		}
		if field == "" {
			return nil, fmt.Errorf("invalid path template %q: empty variable name", tpl)
		}
		v := compiledVar{Field: field}
		if constraint == "" {
			name := nextWildcard()
			path.WriteString("{" + name + "}")
			v.Segments = []string{"*"}
			v.Wildcards = []string{name}
		} else {
			segs := strings.Split(constraint, "/")
			for si, seg := range segs {
				if si > 0 {
					path.WriteByte('/')
				}
				switch seg {
				case "*":
					name := nextWildcard()
					path.WriteString("{" + name + "}")
					v.Segments = append(v.Segments, "*")
					v.Wildcards = append(v.Wildcards, name)
				case "**":
					if si != len(segs)-1 {
						return nil, fmt.Errorf("invalid path template %q: '**' must be the last segment", tpl)
					}
					name := nextWildcard()
					path.WriteString("{" + name + "...}")
					v.Segments = append(v.Segments, "**")
					v.Wildcards = append(v.Wildcards, name)
				default:
					if seg == "" {
						return nil, fmt.Errorf("invalid path template %q: empty segment in %q", tpl, constraint)
					}
					if strings.ContainsAny(seg, "{}") {
						return nil, fmt.Errorf("invalid path template %q: literal segment %q must not contain '{{' or '}}'", tpl, seg)
					}
					path.WriteString(seg)
					v.Segments = append(v.Segments, seg)
				}
			}
		}
		cp.Vars = append(cp.Vars, v)
		i += end + 1
	}
	cp.Path = path.String()
	if strings.HasSuffix(cp.Path, "/") {
		cp.Path += "{$}"
	}
	return cp, nil
}

func isVerb(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '_', 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		default:
			return false
		}
	}
	return true
}
