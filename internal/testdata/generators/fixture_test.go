package fixture

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	codesfixture "example.com/spine-generator-test/codes"

	kerrors "github.com/apus-run/spine/v3/errors"
	"github.com/apus-run/spine/v3/middleware"
	"github.com/apus-run/spine/v3/transport"
	spinehttp "github.com/apus-run/spine/v3/transport/http"
	"github.com/gorilla/websocket"
	"google.golang.org/genproto/googleapis/api/httpbody"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type service struct{ UnimplementedFixtureServer }

func (service) Reserved(_ context.Context, r *ReservedRequest) (*ReservedReply, error) {
	return &ReservedReply{String_: r.Reset_}, nil
}
func (service) Any(_ context.Context, r *GetRequest) (*Reply, error) {
	return &Reply{Message: r.Id}, nil
}
func (service) Total(context.Context, *GetRequest) (*Reply, error) {
	return &Reply{Total: 9007199254740993}, nil
}
func (service) Reason(context.Context, *GetRequest) (*Reply, error) {
	return &Reply{Reason: ErrorReason_BUSY}, nil
}
func (service) Scalar(_ context.Context, r *GetRequest) (*Reply, error) {
	return &Reply{Message: r.Id + "|" + r.GetNote()}, nil
}
func (service) Get(_ context.Context, r *GetRequest) (*Reply, error) {
	return &Reply{Message: r.Id + "|" + r.GetNote()}, nil
}
func (service) Publish(_ context.Context, r *UpdateRequest) (*Reply, error) {
	return &Reply{Book: &Book{Name: r.Book.Name, Title: "publish:" + r.Book.Title + ":" + r.Tag}}, nil
}
func (service) Archive(_ context.Context, r *UpdateRequest) (*Reply, error) {
	return &Reply{Book: &Book{Name: r.Book.Name, Title: "archive:" + r.Book.Title + ":" + r.Tag}}, nil
}
func (service) Literal(_ context.Context, r *NameRequest) (*Reply, error) {
	return &Reply{Message: r.Name}, nil
}
func (service) Resource(_ context.Context, r *NameRequest) (*Reply, error) {
	return &Reply{Message: r.Name}, nil
}
func (service) Fail(_ context.Context, r *GetRequest) (*Reply, error) {
	return nil, ErrorNotFound("missing %s", r.Id).WithMetadata(map[string]string{"id": r.Id})
}
func (service) Custom(_ context.Context, r *GetRequest) (*Reply, error) {
	return &Reply{Message: r.Id}, nil
}
func (service) Download(_ context.Context, r *GetRequest) (*httpbody.HttpBody, error) {
	return &httpbody.HttpBody{ContentType: "application/octet-stream", Data: []byte(r.Id)}, nil
}
func (service) Upload(_ context.Context, r *httpbody.HttpBody) (*httpbody.HttpBody, error) {
	return r, nil
}
func (service) WatchBook(r *GetRequest, s Fixture_WatchBookServer) error {
	return s.SendMsg(&Reply{Book: &Book{Name: r.Id}})
}
func (service) WatchScalar(r *GetRequest, s Fixture_WatchScalarServer) error {
	return s.Send(&Reply{Message: r.Id})
}
func (service) CollectBooks(s Fixture_CollectBooksServer) error {
	var names []string
	for {
		r, err := s.Recv()
		if errors.Is(err, io.EOF) {
			return s.SendAndClose(&Reply{Book: &Book{Name: strings.Join(names, ",")}})
		}
		if err != nil {
			return err
		}
		names = append(names, r.Book.Name+":"+r.Tag)
	}
}

func (service) Watch(r *GetRequest, s Fixture_WatchServer) error {
	if err := s.Send(&Reply{Message: r.Id + ":1"}); err != nil {
		return err
	}
	if r.Id == "fail" {
		return ErrorBusy("retry later")
	}
	return s.Send(&Reply{Message: r.Id + ":2"})
}
func (service) Collect(s Fixture_CollectServer) error {
	var ids []string
	for {
		r, err := s.Recv()
		if errors.Is(err, io.EOF) {
			return s.SendAndClose(&Reply{Message: strings.Join(ids, ",")})
		}
		if err != nil {
			return err
		}
		ids = append(ids, r.Id)
	}
}
func (service) EchoID(s Fixture_EchoIDServer) error { return (service{}).Echo(s) }
func (service) Echo(s Fixture_EchoServer) error {
	for {
		r := new(GetRequest)
		err := s.RecvMsg(r)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if r.Id == "fail" {
			return ErrorBusy("retry later").WithMetadata(map[string]string{"retry": "yes"}).WithCause(errors.New("private cause"))
		}
		if err = s.Send(&Reply{Message: r.Id}); err != nil {
			return err
		}
	}
}

func setup(t *testing.T) (context.Context, FixtureHTTPClient, *httptest.Server) {
	t.Helper()
	return setupServer(t, false)
}

func setupServer(t *testing.T, tls bool) (context.Context, FixtureHTTPClient, *httptest.Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	s := spinehttp.NewServer()
	s.Use("/*", func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, r any) (any, error) {
			tr, ok := transport.FromServerContext(ctx)
			if !ok || !strings.HasPrefix(tr.Operation(), "/acceptance.v1.") {
				t.Errorf("missing RPC operation: %v", tr)
			}
			return next(ctx, r)
		}
	})
	RegisterFixtureHTTPServer(s, service{})
	RegisterMirrorHTTPServer(s, mirrorService{})
	ts := httptest.NewUnstartedServer(s)
	if tls {
		ts.StartTLS()
	} else {
		ts.Start()
	}
	t.Cleanup(ts.Close)
	opts := []spinehttp.ClientOption{spinehttp.WithEndpoint(ts.URL), spinehttp.WithTimeout(5 * time.Second)}
	if tls {
		opts = append(opts, spinehttp.WithTLSConfig(ts.Client().Transport.(*http.Transport).TLSClientConfig.Clone()))
	}
	cc, err := spinehttp.NewClient(ctx, opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := cc.Close(); err != nil {
			t.Error(err)
		}
	})
	return ctx, NewFixtureHTTPClient(cc), ts
}

func TestGeneratedErrors(t *testing.T) {
	if !codesfixture.IsPkgError(codesfixture.ErrorPkgError("package alias")) {
		t.Fatal("errors-named package failed")
	}

	if !IsNestedOnly(ErrorNestedOnly("nested")) {
		t.Fatal("nested-only file missing helpers")
	}
	e := ErrorNotFound("missing %s", "book")
	if e.Code != 404 || e.Reason != "NOT_FOUND" || e.Message != "missing book" {
		t.Fatalf("%+v", e)
	}
	if !IsNotFound(fmt.Errorf("wrapped: %w", e)) || IsNotFound(nil) || IsNotFound(kerrors.New(400, "NOT_FOUND", "different")) {
		t.Fatal("incorrect error predicate")
	}
	if status.Code(e) != codes.NotFound {
		t.Fatalf("gRPC code %v", status.Code(e))
	}
	if !IsNestedConflict(ErrorNestedConflict("conflict")) || ErrorNestedConflict("conflict").Code != 409 {
		t.Fatal("nested error/default code")
	}
	if ErrorUnknown("default").Code != 500 {
		t.Fatal("default code")
	}
}

func TestGeneratedUnaryRoundTrips(t *testing.T) {
	ctx, c, ts := setup(t)
	scalar, scalarErr := c.Scalar(ctx, &GetRequest{Id: "42", Note: proto.String("scalar body")}, spinehttp.ContentType("application/protojson"))
	if scalarErr != nil || scalar.GetMessage() != "42|scalar body" {
		t.Fatalf("scalar fields: %v %v", scalar, scalarErr)
	}
	for _, id := range []string{"hello", "中文 空格", "a/b", "a%b", "a%2Fb"} {
		out, err := c.Get(ctx, &GetRequest{Id: id, Note: proto.String("note + value")})
		if err != nil {
			t.Fatal(err)
		}
		if out.Message != id+"|note + value" {
			t.Fatalf("roundtrip %q: %q", id, out.Message)
		}
	}
	for _, call := range []struct {
		name string
		fn   func(context.Context, *UpdateRequest, ...spinehttp.CallOption) (*Reply, error)
	}{{"publish", c.Publish}, {"archive", c.Archive}} {
		out, err := call.fn(ctx, &UpdateRequest{Book: &Book{Name: "projects/acme/books/a b", Title: "title"}, Tag: "query"})
		if err != nil {
			t.Fatal(err)
		}
		if out.Book.Name != "projects/acme/books/a b" || out.Book.Title != call.name+":title:query" {
			t.Fatalf("body/response_body: %+v", out)
		}
	}
	for _, name := range []string{"files/", "files/a/b", "files/a%2Fb/中文"} {
		out, err := c.Resource(ctx, &NameRequest{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		if out.Message != name {
			t.Fatalf("resource %q", out.Message)
		}
	}
	out, err := c.Literal(ctx, &NameRequest{Name: "books/special"})
	if err != nil || out.GetMessage() != "books/special" {
		t.Fatalf("literal: %v %v", out, err)
	}
	out, err = c.Custom(ctx, &GetRequest{Id: "42"})
	if err != nil || out.GetMessage() != "42" {
		t.Fatalf("custom: %v %v", out, err)
	}
	resp, err := ts.Client().Get(ts.URL + "/v1/aliases/42?note=alternate")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || !bytes.Contains(body, []byte("42|alternate")) {
		t.Fatalf("additional binding: %d %s", resp.StatusCode, body)
	}
	_, err = c.Fail(ctx, &GetRequest{Id: "42"})
	if !IsNotFound(err) || kerrors.FromError(err).Metadata["id"] != "42" {
		t.Fatalf("error roundtrip: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = c.Get(canceled, &GetRequest{Id: "42"}); err == nil {
		t.Fatal("cancellation ignored")
	}
}

func TestGeneratedHTTPBody(t *testing.T) {
	ctx, c, _ := setup(t)
	input := &httpbody.HttpBody{ContentType: "application/octet-stream", Data: []byte{0, 1, 2, 255}}
	out, err := c.Upload(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if out.ContentType != input.ContentType || !bytes.Equal(out.Data, input.Data) {
		t.Fatalf("raw body %+v", out)
	}
	out, err = c.Download(ctx, &GetRequest{Id: "bytes"})
	if err != nil {
		t.Fatal(err)
	}
	if out.ContentType != "application/octet-stream" || string(out.Data) != "bytes" {
		t.Fatalf("download %+v", out)
	}
}

func TestGeneratedStreams(t *testing.T) {
	ctx, c, _ := setup(t)
	watch, err := c.Watch(ctx, &GetRequest{Id: "watch"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"watch:1", "watch:2"} {
		out, err := watch.Recv()
		if err != nil || out.GetMessage() != want {
			t.Fatalf("SSE %v %v", out, err)
		}
	}
	if _, err = watch.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("SSE EOF: %v", err)
	}
	failed, err := c.Watch(ctx, &GetRequest{Id: "fail"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = failed.Recv(); err != nil {
		t.Fatal(err)
	}
	if _, err = failed.Recv(); !IsBusy(err) {
		t.Fatalf("SSE error: %v", err)
	}
	collect, err := c.Collect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two"} {
		if err = collect.Send(&GetRequest{Id: id}); err != nil {
			t.Fatal(err)
		}
	}
	out, err := collect.CloseAndRecv()
	if err != nil || out.GetMessage() != "one,two" {
		t.Fatalf("client stream: %v %v", out, err)
	}
	echo, err := c.Echo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two"} {
		if err = echo.Send(&GetRequest{Id: id}); err != nil {
			t.Fatal(err)
		}
		out, err = echo.Recv()
		if err != nil || out.GetMessage() != id {
			t.Fatalf("bidi: %v %v", out, err)
		}
	}
	if err = echo.CloseSend(); err != nil {
		t.Fatal(err)
	}
	if _, err = echo.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("bidi EOF: %v", err)
	}
}

func TestGeneratedConcurrentClients(t *testing.T) {
	ctx, c, _ := setup(t)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				id := fmt.Sprintf("%d-%d", i, j)
				if j%3 == 0 {
					out, err := c.Total(ctx, &GetRequest{Id: id})
					if err != nil || out.GetTotal() != 9007199254740993 {
						t.Errorf("concurrent projection: %v %v", out, err)
						return
					}
					continue
				}
				out, err := c.Get(ctx, &GetRequest{Id: id})
				if err != nil || out.GetMessage() != id+"|" {
					t.Errorf("concurrent: %v %v", out, err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestGeneratedMethodMismatch(t *testing.T) {
	_, _, ts := setup(t)
	req, err := http.NewRequest("DELETE", ts.URL+"/v1/items/42", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 405 || resp.Header.Get("Allow") != "GET, HEAD" {
		t.Fatalf("%d %q", resp.StatusCode, resp.Header.Get("Allow"))
	}
}

func TestGeneratedStreamFieldProjection(t *testing.T) {
	ctx, c, _ := setup(t)
	books, err := c.WatchBook(ctx, &GetRequest{Id: "book"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := books.Recv()
	if err != nil || out.GetBook().GetName() != "book" {
		t.Fatalf("SSE message projection: %v %v", out, err)
	}
	if _, err = books.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("EOF: %v", err)
	}
	scalar, err := c.WatchScalar(ctx, &GetRequest{Id: "scalar"})
	if err != nil {
		t.Fatal(err)
	}
	out, err = scalar.Recv()
	if err != nil || out.GetMessage() != "scalar" {
		t.Fatalf("SSE scalar projection: %v %v", out, err)
	}
	if _, err = scalar.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("EOF: %v", err)
	}
	collect, err := c.CollectBooks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		if err = collect.Send(&UpdateRequest{Book: &Book{Name: name}, Tag: "query"}); err != nil {
			t.Fatal(err)
		}
	}
	out, err = collect.CloseAndRecv()
	if err != nil || out.GetBook().GetName() != "a:query,b:query" {
		t.Fatalf("stream message projection: %v %v", out, err)
	}
}

func TestGeneratedGenericStreamAPI(t *testing.T) {
	ctx, c, _ := setup(t)
	stream, err := c.EchoID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stream.Context() != ctx {
		t.Fatal("lazy stream lost its context")
	}
	if stream.Trailer() != nil {
		t.Fatal("unopened stream has trailers")
	}
	if _, err = stream.Header(); err == nil {
		t.Fatal("unopened stream must report unavailable headers")
	}
	if _, err = stream.Recv(); err == nil {
		t.Fatal("parameterized stream opened without request")
	}
	if err = stream.SendMsg(&Reply{}); err == nil {
		t.Fatal("wrong message type accepted")
	}
	if err = stream.SendMsg(&GetRequest{Id: "a/b"}); err != nil {
		t.Fatal(err)
	}
	var out Reply
	if err = stream.RecvMsg(&out); err != nil || out.Message != "a/b" {
		t.Fatalf("generic receive: %v %v", &out, err)
	}
	if _, err = stream.Header(); err != nil {
		t.Fatal(err)
	}
	if err = stream.SendMsg(&GetRequest{Id: "other"}); err != nil {
		t.Fatal(err)
	}
	if err = stream.RecvMsg(&out); err != nil || out.Message != "a/b" {
		t.Fatalf("stream path binding changed: %v %v", &out, err)
	}
	if err = stream.CloseSend(); err != nil {
		t.Fatal(err)
	}
	if err = stream.RecvMsg(&out); !errors.Is(err, io.EOF) {
		t.Fatalf("EOF: %v", err)
	}
}

func TestGeneratedConcurrentStreamSendRecv(t *testing.T) {
	ctx, c, _ := setup(t)
	for attempt := 0; attempt < 8; attempt++ {
		stream, err := c.Echo(ctx)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			for i := 0; i < 20; i++ {
				if err := stream.Send(&GetRequest{Id: fmt.Sprint(i)}); err != nil {
					done <- err
					return
				}
			}
			done <- stream.CloseSend()
		}()
		for i := 0; i < 20; i++ {
			out, err := stream.Recv()
			if err != nil || out.GetMessage() != fmt.Sprint(i) {
				t.Fatalf("concurrent stream: %v %v", out, err)
			}
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
			t.Fatalf("EOF: %v", err)
		}
	}
}

func TestGeneratedStreamCancellation(t *testing.T) {
	ctx, c, _ := setup(t)
	callCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := c.Echo(callCtx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.CloseSend() })
	if err = stream.Send(&GetRequest{Id: "open"}); err != nil {
		t.Fatal(err)
	}
	if _, err = stream.Recv(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := stream.Recv(); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not unblock receive")
	}
}

type mirrorService struct{}

func (mirrorService) Publish(ctx context.Context, r *AlternateUpdate) (*Reply, error) {
	tr, ok := transport.FromServerContext(ctx)
	if !ok || tr.Operation() != OperationMirrorPublish {
		return nil, fmt.Errorf("incorrect mirror operation")
	}
	return &Reply{Book: &Book{Name: r.Resource.Name, Title: r.Resource.Title + ":" + r.Tag}}, nil
}
func TestGeneratedCrossServiceActions(t *testing.T) {
	ctx, _, ts := setup(t)
	cc, err := spinehttp.NewClient(ctx, spinehttp.WithEndpoint(ts.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	out, err := NewMirrorHTTPClient(cc).Publish(ctx, &AlternateUpdate{Resource: &Book{Name: "projects/acme/books/42", Title: "mirror"}, Tag: "query"})
	if err != nil || out.GetBook().GetName() != "projects/acme/books/42" || out.GetBook().GetTitle() != "mirror:query" {
		t.Fatalf("cross-service action: %v %v", out, err)
	}
}

func TestGeneratedTLS(t *testing.T) {
	ctx, c, _ := setupServer(t, true)
	out, err := c.Get(ctx, &GetRequest{Id: "secure"})
	if err != nil || out.GetMessage() != "secure|" {
		t.Fatalf("HTTPS: %v %v", out, err)
	}
	stream, err := c.Echo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = stream.Send(&GetRequest{Id: "secure stream"}); err != nil {
		t.Fatal(err)
	}
	out, err = stream.Recv()
	if err != nil || out.GetMessage() != "secure stream" {
		t.Fatalf("WSS: %v %v", out, err)
	}
	if err = stream.CloseSend(); err != nil {
		t.Fatal(err)
	}
	if _, err = stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("WSS EOF: %v", err)
	}
}

func TestGeneratedProjectionProtoJSONFormat(t *testing.T) {
	ctx, c, ts := setup(t)
	for _, tc := range []struct{ path, want string }{{"/v1/total/42", `"9007199254740993"`}, {"/v1/reason/42", `"BUSY"`}} {
		resp, err := ts.Client().Get(ts.URL + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 200 || string(data) != tc.want {
			t.Errorf("%s: %d %s", tc.path, resp.StatusCode, data)
		}
	}
	out, err := c.Total(ctx, &GetRequest{Id: "42"}, spinehttp.Accept("application/protojson"))
	if err != nil || out.GetTotal() != 9007199254740993 {
		t.Fatalf("int64 projection: %v %v", out, err)
	}
	out, err = c.Reason(ctx, &GetRequest{Id: "42"})
	if err != nil || out.GetReason() != ErrorReason_BUSY {
		t.Fatalf("enum projection: %v %v", out, err)
	}
}

func TestGeneratedErrorsAcrossGRPC(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	RegisterFixtureServer(server, service{})
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if err := <-done; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Error(err)
		}
	})
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = NewFixtureClient(conn).Fail(ctx, &GetRequest{Id: "42"})
	if !IsNotFound(err) || status.Code(err) != codes.NotFound || kerrors.FromError(err).Metadata["id"] != "42" {
		t.Fatalf("gRPC error roundtrip: %v", err)
	}
}

func (service) RawEcho(s Fixture_RawEchoServer) error {
	for {
		r, err := s.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if err = s.Send(r); err != nil {
			return err
		}
	}
}

func TestGeneratedWebSocketErrors(t *testing.T) {
	ctx, c, ts := setup(t)
	for _, opts := range [][]spinehttp.CallOption{nil, {spinehttp.ContentType("application/proto"), spinehttp.Accept("application/proto")}} {
		stream, err := c.Echo(ctx, opts...)
		if err != nil {
			t.Fatal(err)
		}
		if err = stream.Send(&GetRequest{Id: "fail"}); err != nil {
			t.Fatal(err)
		}
		_, err = stream.Recv()
		if !IsBusy(err) || kerrors.FromError(err).Metadata["retry"] != "yes" {
			t.Errorf("WebSocket status: %v", err)
		}
	}
	// A peer without capability negotiation continues receiving legacy errors.
	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/v1/echo", nil)
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err = conn.WriteJSON(&GetRequest{Id: "fail"}); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("private cause")) {
		t.Fatal("legacy stream leaked private cause")
	}
	if !bytes.HasPrefix(data, []byte("\x1eerror:")) {
		t.Fatalf("legacy error format: %q", data)
	}
}

func TestGeneratedWebSocketHandshakeError(t *testing.T) {
	server := spinehttp.NewServer(spinehttp.Filter(func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			spinehttp.DefaultErrorEncoder(w, r, ErrorBusy("unavailable"))
		})
	}))
	ts := httptest.NewServer(server)
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cc, err := spinehttp.NewClient(ctx, spinehttp.WithEndpoint(ts.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer cc.Close()
	stream, err := NewFixtureHTTPClient(cc).Echo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = stream.Send(&GetRequest{Id: "first"}); !IsBusy(err) {
		t.Fatalf("handshake status: %v", err)
	}
}

func TestGeneratedBinaryStreamPayloads(t *testing.T) {
	ctx, c, _ := setup(t)
	stream, err := c.RawEcho(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{{0, 255, 1}, []byte("\x1eend"), []byte("\x1eerror:payload"), []byte("\x1estatus:payload")} {
		if err = stream.Send(&httpbody.HttpBody{ContentType: "application/octet-stream", Data: data}); err != nil {
			t.Fatal(err)
		}
		out, err := stream.Recv()
		if err != nil || !bytes.Equal(out.GetData(), data) {
			t.Fatalf("binary data: %v %v", out, err)
		}
	}
	if err = stream.CloseSend(); err != nil {
		t.Fatal(err)
	}
	if _, err = stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("binary EOF: %v", err)
	}
}

func TestGeneratedWildcardMethod(t *testing.T) {
	ctx, c, ts := setup(t)
	out, err := c.Any(ctx, &GetRequest{Id: "generated"})
	if err != nil || out.GetMessage() != "generated" {
		t.Fatalf("wildcard client: %v %v", out, err)
	}
	for _, method := range []string{"GET", "POST", "PATCH"} {
		req, err := http.NewRequestWithContext(ctx, method, ts.URL+"/v1/any/external", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 200 || !bytes.Contains(data, []byte("external")) {
			t.Fatalf("%s: %d %s", method, resp.StatusCode, data)
		}
	}
}

func TestGeneratedReservedGoFields(t *testing.T) {
	ctx, c, _ := setup(t)
	out, err := c.Reserved(ctx, &ReservedRequest{Reset_: &Book{Name: "reserved"}})
	if err != nil || out == nil || out.String_ == nil || out.String_.Name != "reserved" {
		t.Fatalf("reserved fields: %v %v", out, err)
	}
}
