package http

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDefaultClientReusesConcurrentConnections(t *testing.T) {
	const workers = 8
	const rounds = 10
	gates := make([]chan struct{}, rounds)
	for i := range gates {
		gates[i] = make(chan struct{})
	}
	arrived := make(chan struct{}, workers)
	var connections atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		round, _ := strconv.Atoi(r.URL.Query().Get("round"))
		arrived <- struct{}{}
		select {
		case <-gates[round]:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	client, err := NewClient(context.Background(), WithEndpoint(server.URL), WithTimeout(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for round := 0; round < rounds; round++ {
		var wait sync.WaitGroup
		for i := 0; i < workers; i++ {
			wait.Add(1)
			go func() {
				defer wait.Done()
				var reply map[string]bool
				if err := client.Invoke(context.Background(), "GET", "/?round="+strconv.Itoa(round), nil, &reply); err != nil {
					t.Error(err)
				}
			}()
		}
		for i := 0; i < workers; i++ {
			select {
			case <-arrived:
			case <-time.After(6 * time.Second):
				t.Fatal("requests did not arrive")
			}
		}
		close(gates[round])
		wait.Wait()
	}
	if count := connections.Load(); count > workers {
		t.Fatalf("opened %d connections for %d concurrent callers", count, workers)
	}
}

func TestDefaultClientOwnsItsPool(t *testing.T) {
	base := http.DefaultTransport.(*http.Transport)
	original := base.MaxIdleConnsPerHost
	first, err := NewClient(context.Background(), WithEndpoint("localhost:80"))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := NewClient(context.Background(), WithEndpoint("localhost:80"))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if first.cc.Transport == base || first.cc.Transport == second.cc.Transport {
		t.Fatal("clients share the global connection pool")
	}
	if base.MaxIdleConnsPerHost != original {
		t.Fatal("global default transport was modified")
	}
	custom := base.Clone()
	custom.MaxIdleConnsPerHost = 3
	explicit, err := NewClient(context.Background(), WithEndpoint("localhost:80"), WithTransport(custom))
	if err != nil {
		t.Fatal(err)
	}
	defer explicit.Close()
	if explicit.cc.Transport != custom || custom.MaxIdleConnsPerHost != 3 {
		t.Fatal("explicit transport was changed")
	}
}
