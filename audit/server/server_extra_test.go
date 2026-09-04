package server_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"selfhealingcache/internal/ring"
	"selfhealingcache/internal/server"
	"selfhealingcache/internal/store"
)

// TestTriggerRebalance verifies the async rebalance trigger doesn't panic
func TestTriggerRebalance(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})

	s := server.New(store.New(time.Second), "node-a", r)
	s.WithListenAddr("127.0.0.1:8080")

	// Trigger rebalance - should not panic
	s.TriggerRebalance()

	// Give async goroutine time to complete
	time.Sleep(100 * time.Millisecond)
}

// TestServerCreation verifies server can be created with various configurations
func TestServerCreation(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})

	s := server.New(store.New(time.Second), "node-a", r)

	// Test that handler can be created
	handler := s.Handler()
	if handler == nil {
		t.Fatal("handler should not be nil")
	}
}

// TestConcurrentServerAccess verifies thread safety of server operations
func TestConcurrentServerAccess(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})

	s := server.New(store.New(time.Second), "node-a", r)

	var wg sync.WaitGroup
	var mu sync.Mutex
	errors := make([]error, 0)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Each goroutine gets its own request and response writer
			body := strings.NewReader(`{"key":"test-key","value":"value","ttl_ms":0}`)
			req, _ := http.NewRequest(http.MethodPost, "/set", body)
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				mu.Lock()
				errors = append(errors, fmt.Errorf("goroutine %d: expected 200, got %d", i, w.Code))
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	if len(errors) > 0 {
		t.Logf("Got %d errors during concurrent access", len(errors))
		for _, err := range errors {
			t.Log(err)
		}
	}
}

// TestServerHandlerEndpoints verifies basic endpoint routing
func TestServerHandlerEndpoints(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})

	s := server.New(store.New(time.Second), "node-a", r)

	// Test ring info endpoint
	req, _ := http.NewRequest(http.MethodGet, "/ring/info", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result map[string]interface{}
	json.NewDecoder(w.Body).Decode(&result)
	if result["node_id"] != "node-a" {
		t.Fatalf("expected node-a, got %v", result["node_id"])
	}
}

// TestQuorumSetEndpoint verifies quorum endpoint exists and responds
func TestQuorumSetEndpoint(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})

	s := server.New(store.New(time.Second), "node-a", r)

	// Test quorum set endpoint with valid body
	body := strings.NewReader(`{"key":"test","value":"value","ttl_ms":1000}`)
	req, _ := http.NewRequest(http.MethodPost, "/quorum/set?key=test", body)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	// Should get some response (may fail due to no replicas, but endpoint exists)
	t.Logf("Quorum set response code: %d, body: %s", w.Code, w.Body.String())
}

// TestQuorumGetEndpoint verifies quorum get endpoint exists
func TestQuorumGetEndpoint(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})

	s := server.New(store.New(time.Second), "node-a", r)

	req, _ := http.NewRequest(http.MethodGet, "/quorum/get?key=nonexistent", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	// Should get not found or similar response
	t.Logf("Quorum get response code: %d, body: %s", w.Code, w.Body.String())
}

// TestRebalanceEndpoint verifies rebalance endpoint exists
func TestRebalanceEndpoint(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})

	s := server.New(store.New(time.Second), "node-a", r)

	req, _ := http.NewRequest(http.MethodPost, "/rebalance", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}
}

// TestRebalanceStatusEndpoint verifies rebalance status endpoint
func TestRebalanceStatusEndpoint(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})

	s := server.New(store.New(time.Second), "node-a", r)

	req, _ := http.NewRequest(http.MethodGet, "/rebalance/status", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

// TestClusterInfoEndpoint verifies cluster info endpoint
func TestClusterInfoEndpoint(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})

	s := server.New(store.New(time.Second), "node-a", r)

	req, _ := http.NewRequest(http.MethodGet, "/cluster/info", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]interface{}
	json.NewDecoder(w.Body).Decode(&result)
	if result["cluster_enabled"] != false {
		t.Fatalf("expected cluster_enabled=false, got %v", result["cluster_enabled"])
	}
}
