package http

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestCallHeaderDoesNotMutateCallerMap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-App") != "example" {
			t.Error("request header lost")
		}
		w.Header().Set("X-Reply", "yes")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	client, err := NewClient(context.Background(), WithEndpoint(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	original := http.Header{"X-App": []string{"example"}}
	expected := original.Clone()
	output := original
	var reply map[string]any
	if err = client.Invoke(context.Background(), "GET", "/", nil, &reply, Header(&output), Accept("application/json")); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, expected) {
		t.Fatalf("caller header mutated: %v", original)
	}
	if output.Get("X-Reply") != "yes" {
		t.Fatal("response header missing")
	}
	output = original
	stream, err := client.ServerSentEvent(context.Background(), "GET", "/", nil, Header(&output), Accept("text/event-stream"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := stream.CloseSend(); err != nil {
			t.Error(err)
		}
	}()
	if !reflect.DeepEqual(original, expected) {
		t.Fatalf("SSE mutated caller header: %v", original)
	}
}

func TestCallHeaderCapturesIntoNilMap(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Reply", "yes")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()
	client, err := NewClient(context.Background(), WithEndpoint(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var output http.Header
	var reply map[string]any
	if err = client.Invoke(context.Background(), "GET", "/", nil, &reply, Header(&output), Accept("application/json")); err != nil {
		t.Fatal(err)
	}
	if output.Get("X-Reply") != "yes" {
		t.Fatal("missing unary response header")
	}
	output = nil
	stream, err := client.ServerSentEvent(context.Background(), "GET", "/", nil, Header(&output), Accept("text/event-stream"))
	if err != nil {
		t.Fatal(err)
	}
	if err = stream.CloseSend(); err != nil {
		t.Fatal(err)
	}
	if output.Get("X-Reply") != "yes" {
		t.Fatal("missing stream response header")
	}
}
