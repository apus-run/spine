package main

import (
	"bytes"
	_ "embed"
	"strings"
	"text/template"
)

//go:embed httpTemplate.tpl
var httpTemplate string

type serviceDesc struct {
	ServiceType string // Greeter
	ServiceName string // helloworld.Greeter
	Metadata    string // api/helloworld/helloworld.proto
	Methods     []*methodDesc
	MethodSets  map[string]*methodDesc
	Groups      []*bindingGroup // registrations, grouped by (method, pattern)
}

// bindingGroup aggregates the logical bindings that share one ServeMux
// pattern so they register together (custom actions, PRD §12.6).
type bindingGroup struct {
	Method   string
	Pattern  string
	Bindings []*methodDesc
}

type methodDesc struct {
	// method
	Name         string
	OriginalName string // The parsed original name
	Num          int
	Request      string
	Reply        string
	Comment      string
	// http_rule
	Path            string // compiled ServeMux pattern path
	PathTemplate    string // original google.api.http path template
	Method          string
	RegisterMethod  string // method used for server registration (GET for WebSocket)
	Action          string // custom-verb suffix ("" when absent)
	BindingsCode    string // rendered []transport/http.PathBinding literal
	HasVars         bool
	HasBody         bool
	Body            string
	BodyField       string
	BodyQueryName   string
	BodyHTTPBody    bool
	BodyMessage     bool
	ResponseBody    string
	ResponseBodyHTTPBody bool
	ReplyHTTPBody   bool
	ClientStreaming bool
	ServerStreaming bool
}

func (s *serviceDesc) execute() string {
	s.MethodSets = make(map[string]*methodDesc)
	for _, m := range s.Methods {
		s.MethodSets[m.Name] = m
	}
	s.Groups = groupBindings(s.Methods)
	buf := new(bytes.Buffer)
	tmpl, err := template.New("http").Parse(strings.TrimSpace(httpTemplate))
	if err != nil {
		panic(err)
	}
	if err := tmpl.Execute(buf, s); err != nil {
		panic(err)
	}
	return strings.Trim(buf.String(), "\r\n")
}

// groupBindings groups the bindings by registration method and compiled
// pattern; every group becomes a single HandleBindings registration.
func groupBindings(methods []*methodDesc) []*bindingGroup {
	var groups []*bindingGroup
	index := make(map[string]*bindingGroup, len(methods))
	for _, m := range methods {
		key := m.RegisterMethod + " " + m.Path
		g, ok := index[key]
		if !ok {
			g = &bindingGroup{Method: m.RegisterMethod, Pattern: m.Path}
			index[key] = g
			groups = append(groups, g)
		}
		g.Bindings = append(g.Bindings, m)
	}
	return groups
}
