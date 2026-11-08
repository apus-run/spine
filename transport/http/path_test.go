package http

import (
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/apus-run/spine/v3/internal/testdata/binding"
	"github.com/apus-run/spine/v3/internal/testdata/complex"
)

func TestBuildPath(t *testing.T) {
	tests := []struct {
		name         string
		pathTemplate string
		request      *binding.HelloRequest
		opts         []BuildPathOption
		want         string
	}{
		{
			name:         "path",
			pathTemplate: "/helloworld/{name}",
			request:      &binding.HelloRequest{Name: "spine"},
			want:         "/helloworld/spine",
		},
		{
			name:         "query",
			pathTemplate: "/helloworld/{name}",
			request:      &binding.HelloRequest{Name: "spine", Sub: &binding.Sub{Name: "go"}},
			opts:         []BuildPathOption{WithQueryParams()},
			want:         "/helloworld/spine?sub.naming=go",
		},
		{
			name:         "resource name",
			pathTemplate: "/v1/{name=publishers/*/books/*}",
			request:      &binding.HelloRequest{Name: "publishers/go/books/spine"},
			opts:         []BuildPathOption{WithQueryParams()},
			want:         "/v1/publishers/go/books/spine",
		},
		{
			name:         "omit body field query params",
			pathTemplate: "/helloworld/{name}",
			request:      &binding.HelloRequest{Name: "spine", Sub: &binding.Sub{Name: "go"}},
			opts:         []BuildPathOption{WithQueryParams(), WithOmitFields("sub")},
			want:         "/helloworld/spine",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BuildPath(tt.pathTemplate, tt.request, tt.opts...); got != tt.want {
				t.Errorf("expected %s got %s", tt.want, got)
			}
		})
	}
}

func TestBuildPathFieldNames(t *testing.T) {
	tests := []struct {
		name     string
		template string
		request  any
		opts     []BuildPathOption
		want     string
	}{
		{
			name: "proto name", template: "/items/{opt_string}",
			request: &binding.HelloRequest{OptString: proto.String("item-1")},
			want:    "/items/item-1",
		},
		{
			name: "JSON name", template: "/items/{optString}",
			request: &binding.HelloRequest{OptString: proto.String("item-1")},
			want:    "/items/item-1",
		},
		{
			name: "nested proto name", template: "/items/{sub.name}",
			request: &binding.HelloRequest{Sub: &binding.Sub{Name: "item-1"}},
			want:    "/items/item-1",
		},
		{
			name: "nested JSON name", template: "/items/{sub.naming}",
			request: &binding.HelloRequest{Sub: &binding.Sub{Name: "item-1"}},
			want:    "/items/item-1",
		},
		{
			name: "custom parent JSON name", template: "/items/{simple.component}",
			request: &complex.Complex{Simple: &complex.Simple{Component: "item-1"}},
			want:    "/items/item-1",
		},
		{
			name: "omit proto field", template: "/items",
			request: &binding.HelloRequest{Name: "spine", OptString: proto.String("body")},
			opts:    []BuildPathOption{WithOmitFields("opt_string")},
			want:    "/items?name=spine",
		},
		{
			name: "omit nested proto field", template: "/items",
			request: &binding.HelloRequest{Sub: &binding.Sub{Name: "body"}},
			opts:    []BuildPathOption{WithOmitFields("sub.name")},
			want:    "/items",
		},
		{
			name: "omit custom JSON parent", template: "/items",
			request: &complex.Complex{NoOne: "spine", Simple: &complex.Simple{Component: "body"}},
			opts:    []BuildPathOption{WithOmitFields("simple")},
			want:    "/items?numberOne=spine",
		},
		{
			name: "struct", template: "/items/{resource_id}",
			request: &struct {
				ResourceID string `json:"resource_id"`
			}{ResourceID: "item-1"},
			want: "/items/item-1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := append([]BuildPathOption{WithQueryParams()}, tt.opts...)
			if got := BuildPath(tt.template, tt.request, opts...); got != tt.want {
				t.Fatalf("BuildPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildPathEscapesValues(t *testing.T) {
	tests := []struct {
		name, template, value, want string
	}{
		{"single segment", "/items/{name}", "one/two?#%", "/items/one%2Ftwo%3F%23%25"},
		{"explicit single segment", "/items/{name=*}", "one/two", "/items/one%2Ftwo"},
		{"dot", "/items/{name}", ".", "/items/%2E"},
		{"dot dot", "/items/{name}", "..", "/items/%2E%2E"},
		{"resource name", "/v1/{name=publishers/*/books/*}", "publishers/go/books/one?#%", "/v1/publishers/go/books/one%3F%23%25"},
		{"multiple segments", "/v1/{name=**}", "publishers/go/books/one?#%", "/v1/publishers/go/books/one%3F%23%25"},
		{"resource dot segment", "/v1/{name=publishers/*}", "publishers/..", "/v1/publishers/%2E%2E"},
		// Characters allowed in a segment are kept, matching the unescaped output.
		{"segment chars unchanged", "/items/{name}", "a-b_c.d~e!$&'()*+,;=:@", "/items/a-b_c.d~e!$&'()*+,;=:@"},
		{"dots in value", "/items/{name}", "v1.2...", "/items/v1.2..."},
		{"space and non-ASCII", "/items/{name}", "a b\xe4\xb8\xad", "/items/a%20b%E4%B8%AD"},
		// Values must not be pre-escaped; "%" is always escaped.
		{"pre-escaped value", "/items/{name}", "a%20b", "/items/a%2520b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BuildPath(tt.template, &binding.HelloRequest{Name: tt.value}, WithQueryParams()); got != tt.want {
				t.Fatalf("BuildPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildPathPreservesFieldMask(t *testing.T) {
	request := &binding.HelloRequest{
		Name:       "spine",
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"opt_string", "sub.name"}},
	}
	for _, opts := range [][]BuildPathOption{nil, {WithQueryParams()}, {WithQueryParams(), WithOmitFields("update_mask")}} {
		input := proto.Clone(request)
		BuildPath("/items/{name}", input, opts...)
		if !proto.Equal(input, request) {
			t.Fatalf("BuildPath mutated the request: %v", input)
		}
		if _, err := protojson.Marshal(input); err != nil {
			t.Fatalf("request can no longer be encoded as the body: %v", err)
		}
	}
}

func TestBuildPathCompatibility(t *testing.T) {
	tests := []struct {
		pathTemplate string
		request      *binding.HelloRequest
		opts         []BuildPathOption
		want         string
	}{
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{name}/sub/{sub.naming}",
			request:      &binding.HelloRequest{Name: "test", Sub: &binding.Sub{Name: "2233!!!!"}},
			want:         "http://helloworld.Greeter/helloworld/test/sub/2233!!!!",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{name}/sub/{sub.naming}",
			request:      nil,
			want:         "http://helloworld.Greeter/helloworld/{name}/sub/{sub.naming}",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{}/sub/{sub.naming}",
			request:      &binding.HelloRequest{Name: "test", Sub: &binding.Sub{Name: "hello"}},
			want:         "http://helloworld.Greeter/helloworld/{}/sub/hello",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{}/sub/{sub.name.cc}",
			request:      &binding.HelloRequest{Name: "test", Sub: &binding.Sub{Name: "hello"}},
			want:         "http://helloworld.Greeter/helloworld/{}/sub/",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{}/sub/{test_repeated}",
			request:      &binding.HelloRequest{Name: "test", Sub: &binding.Sub{Name: "hello"}, TestRepeated: []string{"123", "456"}},
			want:         "http://helloworld.Greeter/helloworld/{}/sub/123",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{name}/sub/{sub.naming}",
			request:      &binding.HelloRequest{Name: "test", Sub: &binding.Sub{Name: "5566!!!"}},
			want:         "http://helloworld.Greeter/helloworld/test/sub/5566!!!",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/sub",
			request:      &binding.HelloRequest{Name: "test", Sub: &binding.Sub{Name: "2233!!!"}},
			want:         "http://helloworld.Greeter/helloworld/sub",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{name}/sub/{sub.name}",
			request:      &binding.HelloRequest{Name: "test"},
			want:         "http://helloworld.Greeter/helloworld/test/sub/",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{name}/sub",
			request:      &binding.HelloRequest{Name: "go", Sub: &binding.Sub{Name: "spine"}},
			opts:         []BuildPathOption{WithQueryParams()},
			want:         "http://helloworld.Greeter/helloworld/go/sub?sub.naming=spine",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{name=publishers/*/books/*}/sub",
			request:      &binding.HelloRequest{Name: "publishers/go/books/spine", Sub: &binding.Sub{Name: "spine"}},
			opts:         []BuildPathOption{WithQueryParams()},
			want:         "http://helloworld.Greeter/helloworld/publishers/go/books/spine/sub?sub.naming=spine",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{name=**}/sub",
			request:      &binding.HelloRequest{Name: "publishers/go/books/spine", Sub: &binding.Sub{Name: "spine"}},
			opts:         []BuildPathOption{WithQueryParams()},
			want:         "http://helloworld.Greeter/helloworld/publishers/go/books/spine/sub?sub.naming=spine",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{sub.naming=publishers/*}",
			request:      &binding.HelloRequest{Sub: &binding.Sub{Name: "publishers/spine"}},
			want:         "http://helloworld.Greeter/helloworld/publishers/spine",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/sub/{sub.naming}",
			request:      &binding.HelloRequest{Sub: &binding.Sub{Name: "spine"}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name", "sub.naming"}}},
			want:         "http://helloworld.Greeter/helloworld/sub/spine?updateMask=name,sub.naming",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/sub/[{sub.naming}]",
			request:      &binding.HelloRequest{Sub: &binding.Sub{Name: "spine"}},
			want:         "http://helloworld.Greeter/helloworld/sub/[spine]",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/[{name}]/sub/[{sub.naming}]",
			request:      &binding.HelloRequest{Name: "test", Sub: &binding.Sub{Name: "spine"}},
			want:         "http://helloworld.Greeter/helloworld/[test]/sub/[spine]",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/[{}]/sub/[{sub.naming}]",
			request:      &binding.HelloRequest{Sub: &binding.Sub{Name: "spine"}},
			want:         "http://helloworld.Greeter/helloworld/[{}]/sub/[spine]",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/[{}]/sub/[{sub.naming}]/{[]}",
			request:      &binding.HelloRequest{Sub: &binding.Sub{Name: "spine"}},
			want:         "http://helloworld.Greeter/helloworld/[{}]/sub/[spine]/{[]}",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{[sub]}/[{sub.naming}]",
			request:      &binding.HelloRequest{Sub: &binding.Sub{Name: "spine"}},
			want:         "http://helloworld.Greeter/helloworld/{[sub]}/[spine]",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{[name]}/[{sub.naming}]",
			request:      &binding.HelloRequest{Name: "test", Sub: &binding.Sub{Name: "spine"}},
			want:         "http://helloworld.Greeter/helloworld/{[name]}/[spine]",
		},
		{
			pathTemplate: "http://helloworld.Greeter/helloworld/{}/[]/[{sub.naming}]",
			request:      &binding.HelloRequest{Sub: &binding.Sub{Name: "spine"}},
			want:         "http://helloworld.Greeter/helloworld/{}/[]/[spine]",
		},
	}

	for _, test := range tests {
		if path := BuildPath(test.pathTemplate, test.request, test.opts...); path != test.want {
			t.Fatalf("want: %s, got: %s", test.want, path)
		}
	}
}

func BenchmarkBuildPath(b *testing.B) {
	benchmarks := []struct {
		name         string
		pathTemplate string
		msg          *binding.HelloRequest
		opts         []BuildPathOption
	}{
		{
			name:         "NoParams",
			pathTemplate: "http://helloworld.Greeter/helloworld/sub",
			msg: &binding.HelloRequest{
				Name: "test",
				Sub:  &binding.Sub{Name: "spine"},
			},
		},
		{
			name:         "NoParamsWithQuery",
			pathTemplate: "http://helloworld.Greeter/helloworld/sub",
			msg: &binding.HelloRequest{
				Name: "test",
				Sub:  &binding.Sub{Name: "spine"},
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"name", "sub.naming"},
				},
			},
			opts: []BuildPathOption{WithQueryParams()},
		},
		{
			name:         "WithParams",
			pathTemplate: "http://helloworld.Greeter/helloworld/{name}/sub/{sub.naming}",
			msg: &binding.HelloRequest{
				Name: "test",
				Sub:  &binding.Sub{Name: "spine"},
			},
		},
		{
			name:         "WithParamsAndQuery",
			pathTemplate: "http://helloworld.Greeter/helloworld/{name}/sub/{sub.naming}",
			msg: &binding.HelloRequest{
				Name: "test",
				Sub:  &binding.Sub{Name: "spine"},
				UpdateMask: &fieldmaskpb.FieldMask{
					Paths: []string{"name", "sub.naming"},
				},
			},
			opts: []BuildPathOption{WithQueryParams()},
		},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				BuildPath(bm.pathTemplate, bm.msg, bm.opts...)
			}
		})
	}
}
