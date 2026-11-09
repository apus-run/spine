package main

import (
	"reflect"
	"strings"
	"testing"
)

func mustCompile(t *testing.T, tpl string) *compiledPattern {
	t.Helper()
	cp, err := compilePattern(tpl)
	if err != nil {
		t.Fatalf("compilePattern(%q): %v", tpl, err)
	}
	return cp
}

// No variables: the path passes through unchanged (PRD §12.2).
func TestNoParameters(t *testing.T) {
	cp := mustCompile(t, "/test/noparams")
	if cp.Path != "/test/noparams" || len(cp.Vars) != 0 || cp.Action != "" {
		t.Fatalf("got %+v", cp)
	}
}

// A plain variable becomes a synthetic wildcard bound to the field (§12.3).
func TestSingleParam(t *testing.T) {
	cp := mustCompile(t, "/test/{message.id}")
	if cp.Path != "/test/{p0}" {
		t.Fatalf("path: %s", cp.Path)
	}
	want := []compiledVar{{Field: "message.id", Segments: []string{"*"}, Wildcards: []string{"p0"}}}
	if !reflect.DeepEqual(cp.Vars, want) {
		t.Fatalf("vars: %+v", cp.Vars)
	}
}

// Two variables, one with a constraint (PRD §12.10 TwoParametersReplacement).
func TestTwoParameters(t *testing.T) {
	cp := mustCompile(t, "/test/{message.id}/{message.name=messages/*}")
	if cp.Path != "/test/{p0}/messages/{p1}" {
		t.Fatalf("path: %s", cp.Path)
	}
	want := []compiledVar{
		{Field: "message.id", Segments: []string{"*"}, Wildcards: []string{"p0"}},
		{Field: "message.name", Segments: []string{"messages", "*"}, Wildcards: []string{"p1"}},
	}
	if !reflect.DeepEqual(cp.Vars, want) {
		t.Fatalf("vars: %+v", cp.Vars)
	}
}

// A literal-only constraint contributes no wildcard and a constant value,
// matching the previous regex behavior of {message.id=test} (§12.10
// NoReplacePath).
func TestLiteralConstraint(t *testing.T) {
	cp := mustCompile(t, "/test/{message.id=test}")
	if cp.Path != "/test/test" {
		t.Fatalf("path: %s", cp.Path)
	}
	want := []compiledVar{{Field: "message.id", Segments: []string{"test"}}}
	if !reflect.DeepEqual(cp.Vars, want) {
		t.Fatalf("vars: %+v", cp.Vars)
	}
}

// A constrained variable in the middle of the path (§12.10 IterationMiddle).
func TestConstrainedVarMiddle(t *testing.T) {
	cp := mustCompile(t, "/test/{message.name=messages/*}/books")
	if cp.Path != "/test/messages/{p0}/books" {
		t.Fatalf("path: %s", cp.Path)
	}
	want := []compiledVar{{Field: "message.name", Segments: []string{"messages", "*"}, Wildcards: []string{"p0"}}}
	if !reflect.DeepEqual(cp.Vars, want) {
		t.Fatalf("vars: %+v", cp.Vars)
	}
}

// Boundary constraints without literals (§12.10 ReplaceBoundary).
func TestBoundaryConstraints(t *testing.T) {
	cp := mustCompile(t, "/test/{message.namespace=*}/name/{message.name=*}")
	if cp.Path != "/test/{p0}/name/{p1}" {
		t.Fatalf("path: %s", cp.Path)
	}
	want := []compiledVar{
		{Field: "message.namespace", Segments: []string{"*"}, Wildcards: []string{"p0"}},
		{Field: "message.name", Segments: []string{"*"}, Wildcards: []string{"p1"}},
	}
	if !reflect.DeepEqual(cp.Vars, want) {
		t.Fatalf("vars: %+v", cp.Vars)
	}
}

// Composite resource names compile to literal segments plus wildcards, and
// the binding keeps the literal glue so the full name is reassembled
// (§12.4).
func TestCompositeResourceName(t *testing.T) {
	cp := mustCompile(t, "/v1/{name=projects/*/books/*}")
	if cp.Path != "/v1/projects/{p0}/books/{p1}" {
		t.Fatalf("path: %s", cp.Path)
	}
	want := []compiledVar{
		{Field: "name", Segments: []string{"projects", "*", "books", "*"}, Wildcards: []string{"p0", "p1"}},
	}
	if !reflect.DeepEqual(cp.Vars, want) {
		t.Fatalf("vars: %+v", cp.Vars)
	}
}

// "**" compiles to a multi-segment wildcard with the literal prefix kept in
// the binding so it is re-attached to the captured value (§12.5).
func TestDoubleStar(t *testing.T) {
	cp := mustCompile(t, "/v1/{name=messages/**}")
	if cp.Path != "/v1/messages/{p0...}" {
		t.Fatalf("path: %s", cp.Path)
	}
	want := []compiledVar{
		{Field: "name", Segments: []string{"messages", "**"}, Wildcards: []string{"p0"}},
	}
	if !reflect.DeepEqual(cp.Vars, want) {
		t.Fatalf("vars: %+v", cp.Vars)
	}
	if err := func() error {
		_, err := compilePattern("/v1/{name=messages/**/books}")
		return err
	}(); err == nil {
		t.Fatal(" '**' must be rejected outside the last segment")
	}
}

// Literal segments are no longer regex-escaped: ServeMux patterns treat
// them literally (§12.10 PathTemplateRegex literal case).
func TestLiteralSegmentNotEscaped(t *testing.T) {
	cp := mustCompile(t, "/v1/{name=v1.0/*}")
	if cp.Path != "/v1/v1.0/{p0}" {
		t.Fatalf("path: %s", cp.Path)
	}
	want := []compiledVar{
		{Field: "name", Segments: []string{"v1.0", "*"}, Wildcards: []string{"p0"}},
	}
	if !reflect.DeepEqual(cp.Vars, want) {
		t.Fatalf("vars: %+v", cp.Vars)
	}
}

// Custom verbs compile to a legal pattern plus an Action; the wildcard
// captures "resource:verb" and the dispatcher splits it (§12.6).
func TestCustomAction(t *testing.T) {
	cp := mustCompile(t, "/v1/{name=books/*}:publish")
	if cp.Path != "/v1/books/{p0}" {
		t.Fatalf("path: %s", cp.Path)
	}
	if cp.Action != "publish" {
		t.Fatalf("action: %q", cp.Action)
	}
	want := []compiledVar{
		{Field: "name", Segments: []string{"books", "*"}, Wildcards: []string{"p0"}},
	}
	if !reflect.DeepEqual(cp.Vars, want) {
		t.Fatalf("vars: %+v", cp.Vars)
	}

	// A resource name legitimately containing ':' keeps the last colon as
	// the separator ("a:b:publish" -> resource "a:b", action "publish").
	cp = mustCompile(t, "/v1/{name=books/*}:archive")
	if cp.Action != "archive" {
		t.Fatalf("action: %q", cp.Action)
	}
}

// Invalid templates fail compilation instead of generating patterns the
// ServeMux would panic on.
func TestCompileErrors(t *testing.T) {
	for _, tpl := range []string{
		"/test/{message.id",
		"/test/}x",
		"/test/{=books/*}",
		"/v1/{name=/**/books}",
		"/v1/{name=/x}",
	} {
		if _, err := compilePattern(tpl); err == nil {
			t.Fatalf("compilePattern(%q) should fail", tpl)
		}
	}
}

func TestHTTPTemplateClientUsesBuildPathAndProtoJSONHeaders(t *testing.T) {
	sd := &serviceDesc{
		ServiceType: "Greeter",
		ServiceName: "helloworld.Greeter",
		Methods: []*methodDesc{
			{
				Name:           "SayHello",
				OriginalName:   "SayHello",
				Request:        "HelloRequest",
				Reply:          "HelloReply",
				Path:           "/helloworld/{p0}",
				PathTemplate:   "/helloworld/{name}",
				Method:         "GET",
				RegisterMethod: "GET",
				HasVars:        true,
				BindingsCode:   `[]http.PathBinding{{Field: "name", Segments: []string{"*"}, Wildcards: []string{"p0"}}}`,
			},
			{
				Name:           "CreateHello",
				OriginalName:   "CreateHello",
				Request:        "CreateHelloRequest",
				Reply:          "HelloReply",
				Path:           "/helloworld",
				PathTemplate:   "/helloworld",
				Method:         "POST",
				RegisterMethod: "POST",
				HasBody:        true,
				Body:           "",
			},
		},
	}
	got := sd.execute()
	for _, want := range []string{
		`r.HandleBindings("GET", "/helloworld/{p0}",`,
		`http.RouteBinding{
			Template: "/helloworld/{name}",`,
		`Bindings: []http.PathBinding{{Field: "name", Segments: []string{"*"}, Wildcards: []string{"p0"}}},`,
		`r.HandleBindings("POST", "/helloworld",`,
		`path := http.BuildPath(pattern, in, http.WithQueryParams())`,
		`path := http.BuildPath(pattern, in)`,
		`http.Accept("application/protojson")`,
		`http.ContentType("application/protojson")`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated template missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "binding.") {
		t.Fatalf("generated template should not reference binding package:\n%s", got)
	}
}

// Custom actions sharing a pattern aggregate into a single registration
// with one RouteBinding per RPC, each keeping its own template and handler
// (§12.6).
func TestHTTPTemplateActionAggregation(t *testing.T) {
	sd := &serviceDesc{
		ServiceType: "Library",
		ServiceName: "library.v1.Library",
		Methods: []*methodDesc{
			{
				Name:           "PublishBook",
				OriginalName:   "PublishBook",
				Num:            0,
				Request:        "PublishBookRequest",
				Reply:          "PublishBookResponse",
				Path:           "/v1/books/{p0}",
				PathTemplate:   "/v1/{name=books/*}:publish",
				Method:         "POST",
				RegisterMethod: "POST",
				Action:         "publish",
				HasVars:        true,
				BindingsCode:   `[]http.PathBinding{{Field: "name", Segments: []string{"books", "*"}, Wildcards: []string{"p0"}}}`,
			},
			{
				Name:           "ArchiveBook",
				OriginalName:   "ArchiveBook",
				Num:            1,
				Request:        "ArchiveBookRequest",
				Reply:          "ArchiveBookResponse",
				Path:           "/v1/books/{p0}",
				PathTemplate:   "/v1/{name=books/*}:archive",
				Method:         "POST",
				RegisterMethod: "POST",
				Action:         "archive",
				HasVars:        true,
				BindingsCode:   `[]http.PathBinding{{Field: "name", Segments: []string{"books", "*"}, Wildcards: []string{"p0"}}}`,
			},
		},
	}
	got := sd.execute()
	for _, want := range []string{
		`r.HandleBindings("POST", "/v1/books/{p0}",`,
		`Template: "/v1/{name=books/*}:publish",`,
		`Action: "publish",`,
		`Handler: _Library_PublishBook0_HTTP_Handler(srv),`,
		`Template: "/v1/{name=books/*}:archive",`,
		`Action: "archive",`,
		`Handler: _Library_ArchiveBook1_HTTP_Handler(srv),`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("aggregated action registration missing %q in:\n%s", want, got)
		}
	}
	if strings.Count(got, "r.HandleBindings") != 1 {
		t.Fatalf("actions sharing a pattern must register once:\n%s", got)
	}
}

func TestHTTPTemplateStreamsAndHTTPBody(t *testing.T) {
	sd := &serviceDesc{
		ServiceType: "Greeter",
		ServiceName: "helloworld.Greeter",
		Methods: []*methodDesc{
			{
				Name:            "ListHello",
				OriginalName:    "ListHello",
				Request:         "ListHelloRequest",
				Reply:           "HelloReply",
				Path:            "/helloworld",
				PathTemplate:    "/helloworld",
				Method:          "GET",
				RegisterMethod:  "GET",
				ServerStreaming: true,
			},
			{
				Name:            "ChatHello",
				OriginalName:    "ChatHello",
				Request:         "HelloRequest",
				Reply:           "HelloReply",
				Path:            "/helloworld/chat",
				PathTemplate:    "/helloworld/chat",
				Method:          "POST",
				RegisterMethod:  "GET",
				ClientStreaming: true,
				ServerStreaming: true,
			},
			{
				Name:           "UploadHello",
				OriginalName:   "UploadHello",
				Request:        "UploadHelloRequest",
				Reply:          "UploadHelloReply",
				Path:           "/helloworld/upload",
				PathTemplate:   "/helloworld/upload",
				Method:         "POST",
				RegisterMethod: "POST",
				HasBody:        true,
				Body:           ".Body",
				BodyField:      "body",
				BodyQueryName:  "body",
				BodyHTTPBody:   true,
				ResponseBody:   ".Body",
			},
			{
				Name:            "ChatData",
				OriginalName:    "ChatData",
				Request:         "ChatDataRequest",
				Reply:           "ChatDataReply",
				Path:            "/v1/bitto/chat",
				PathTemplate:    "/v1/bitto/chat",
				Method:          "GET",
				RegisterMethod:  "GET",
				HasBody:         true,
				Body:            ".Data",
				BodyField:       "data",
				BodyQueryName:   "data",
				BodyMessage:     true,
				ClientStreaming: true,
				ServerStreaming: true,
			},
			{
				// Client-streaming RPC whose named body is a scalar field: it is not
				// streamable frame-by-frame, so the whole message is sent/decoded.
				Name:            "ChatText",
				OriginalName:    "ChatText",
				Request:         "ChatTextRequest",
				Reply:           "ChatTextReply",
				Path:            "/v1/bitto/text",
				PathTemplate:    "/v1/bitto/text",
				Method:          "GET",
				RegisterMethod:  "GET",
				HasBody:         true,
				Body:            ".Text",
				BodyField:       "text",
				BodyQueryName:   "text",
				BodyMessage:     false,
				ClientStreaming: true,
				ServerStreaming: true,
			},
		},
	}
	got := sd.execute()
	for _, want := range []string{
		`r.HandleBindings("GET", "/helloworld/chat",`,
		`ListHello(*ListHelloRequest, Greeter_ListHelloServer) error`,
		`stream := http.NewServerSentEventServerStream(ctx)`,
		`stream, err := c.cc.ServerSentEvent(ctx, "GET", path, nil, opts...)`,
		`ChatHello(Greeter_ChatHelloServer) error`,
		`stream, err := http.NewWebSocketServerStream(ctx)`,
		`func (x *Greeter_ChatHelloHTTPClient) open(m *HelloRequest) error`,
		`path := http.BuildPath(x.pattern, m, http.WithQueryParams())`,
		`stream, err := x.cc.WebSocket(x.ctx, path, opts...)`,
		`http.ContentType("application/protojson")`,
		`return &Greeter_ChatHelloHTTPClient{ctx: ctx, cc: c.cc, pattern: pattern, opts: opts}, nil`,
		`http.ContentType(http.BodyContentType(in.Body))`,
		`http.WithOmitFields("body")`,
		`return ctx.Result(200, reply.Body)`,
		// Client-streaming RPC with a streamable (message-kind) named body field.
		`stream, err := http.NewWebSocketServerStream(ctx, http.WithStreamBodyField("data"))`,
		`return x.ClientStream.Send(m.Data)`,
		// Client-streaming RPC with a scalar named body: whole message is streamed.
		`func (x *Greeter_ChatTextHTTPClient) Send(m *ChatTextRequest) error`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated template missing %q in:\n%s", want, got)
		}
	}
	// The whole-message streaming client (ChatHello) and the scalar-body streaming
	// client (ChatText) must both send the whole message, not a sub-field.
	if !strings.Contains(got, "return x.ClientStream.Send(m)\n") {
		t.Fatalf("generated template should send whole message for non-streamable body:\n%s", got)
	}
	// The scalar-body server handler must NOT receive a stream body-field option.
	if strings.Contains(got, `http.WithStreamBodyField("text")`) {
		t.Fatalf("scalar named body should not emit WithStreamBodyField:\n%s", got)
	}
}

func TestExactRootAndTrailingSlash(t *testing.T) {
	for _, tpl := range []string{"/", "/v1/items/"} {
		cp := mustCompile(t, tpl)
		if cp.Path != tpl+"{$}" {
			t.Errorf("%q compiled to %q, want exact path", tpl, cp.Path)
		}
	}
}

func TestLiteralActionPatterns(t *testing.T) {
	for _, tc := range []struct{ template, pattern string }{
		{"/v1/{name=books/special}:publish", "/v1/books/special:publish"},
		{"/v1/{name=projects/*/books/special}:publish", "/v1/projects/{p0}/books/special:publish"},
	} {
		cp := mustCompile(t, tc.template)
		if cp.Path != tc.pattern || cp.Action != "" {
			t.Errorf("%s: path=%q action=%q", tc.template, cp.Path, cp.Action)
		}
	}
}
