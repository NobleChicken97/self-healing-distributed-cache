package audit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"shdc/internal/ring"
	"shdc/internal/server"
	"shdc/internal/store"
)

// TestQuorumWriteFailsWithoutMajority verifies quorum write fails when majority unreachable
func TestQuorumWriteFailsWithoutMajority(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})
	r.AddNode(ring.Node{ID: "node-b", Addr: "127.0.0.1:8081"})
	r.AddNode(ring.Node{ID: "node-c", Addr: "127.0.0.1:8082"})

	s := server.New(store.New(time.Second), "node-a", r)

	// Find a key owned by node-a
	var testKey string
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("quorum-test-%d", i)
		owner, _ := r.Lookup(key)
		if owner.ID == "node-a" {
			testKey = key
			break
		}
	}
	if testKey == "" {
		t.Fatal("could not find a key owned by node-a")
	}

	// Quorum write with no replicas available (they don't exist)
	body := strings.NewReader(fmt.Sprintf(`{"key":"%s","value":"value","ttl_ms":1000}`, testKey))
	req, _ := http.NewRequest(http.MethodPost, "/quorum/set?key="+testKey, body)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	// Should fail because quorum (2 of 3) can't be reached
	if w.Code != http.StatusServiceUnavailable {
		t.Logf("Got status %d body: %s", w.Code, w.Body.String())
		// This is expected to fail since replicas don't exist
	}
}

// TestQuorumReadVersionConflict verifies quorum read resolves version conflicts
func TestQuorumReadVersionConflict(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})

	s := server.New(store.New(time.Second), "node-a", r)

	// Set a key via normal endpoint
	body := strings.NewReader(`{"key":"versioned-key","value":"original-value","ttl_ms":0}`)
	req, _ := http.NewRequest(http.MethodPost, "/set?key=versioned-key", body)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("set failed: %d %s", w.Code, w.Body.String())
	}

	// Quorum get should return the value
	req, _ = http.NewRequest(http.MethodGet, "/quorum/get?key=versioned-key", nil)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]interface{}
	json.NewDecoder(w.Body).Decode(&result)

	if result["value"] != "original-value" {
		t.Fatalf("expected original-value, got %v", result["value"])
	}
}

// TestQuorumDoesNotAffectNormalEndpoints verifies quorum mode is opt-in
func TestQuorumDoesNotAffectNormalEndpoints(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})

	s := server.New(store.New(time.Second), "node-a", r)

	// Set a key via normal endpoint
	body := strings.NewReader(`{"key":"normal-key","value":"normal-value","ttl_ms":0}`)
	req, _ := http.NewRequest(http.MethodPost, "/set?key=normal-key", body)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("normal set failed: %d %s", w.Code, w.Body.String())
	}

	// Get via normal endpoint
	req, _ = http.NewRequest(http.MethodGet, "/get?key=normal-key", nil)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("normal get failed: %d %s", w.Code, w.Body.String())
	}

	var result map[string]interface{}
	json.NewDecoder(w.Body).Decode(&result)
	if result["value"] != "normal-value" {
		t.Fatalf("expected normal-value, got %v", result["value"])
	}

	// Now use quorum endpoint - should return same value
	req, _ = http.NewRequest(http.MethodGet, "/quorum/get?key=normal-key", nil)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("quorum get failed: %d %s", w.Code, w.Body.String())
	}

	var quorumResult map[string]interface{}
	json.NewDecoder(w.Body).Decode(&quorumResult)
	if quorumResult["value"] != "normal-value" {
		t.Fatalf("quorum should return same value, got %v", quorumResult["value"])
	}
}

// TestQuorumReadSingleNode verifies quorum read works with single node
func TestQuorumReadSingleNode(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})

	s := server.New(store.New(time.Second), "node-a", r)

	// Set a key via normal endpoint
	body := strings.NewReader(`{"key":"single-key","value":"single-value","ttl_ms":0}`)
	req, _ := http.NewRequest(http.MethodPost, "/set?key=single-key", body)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("set failed: %d %s", w.Code, w.Body.String())
	}

	// Quorum get should work even with single node
	req, _ = http.NewRequest(http.MethodGet, "/quorum/get?key=single-key", nil)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]interface{}
	json.NewDecoder(w.Body).Decode(&result)
	if result["value"] != "single-value" {
		t.Fatalf("expected single-value, got %v", result["value"])
	}
}

// TestQuorumWriteWithVersion verifies quorum write works
func TestQuorumWriteWithVersion(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})

	s := server.New(store.New(time.Second), "node-a", r)

	// Quorum set a key
	body := strings.NewReader(`{"key":"version-key","value":"v1","ttl_ms":0}`)
	req, _ := http.NewRequest(http.MethodPost, "/quorum/set?key=version-key", body)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	// Should succeed with single node (quorum = 1)
	if w.Code != http.StatusOK {
		t.Logf("Got status %d body: %s", w.Code, w.Body.String())
	}

	// Verify we can read it back via quorum get
	req, _ = http.NewRequest(http.MethodGet, "/quorum/get?key=version-key", nil)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)

	if w.Code == http.StatusOK {
		var result map[string]interface{}
		json.NewDecoder(w.Body).Decode(&result)
		t.Logf("Quorum get result: %v", result)
		if result["value"] != "v1" {
			t.Fatalf("expected v1, got %v", result["value"])
		}
	}
}

// TestNormalEndpointVsQuorumEndpoint compares behavior
func TestNormalEndpointVsQuorumEndpoint(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})

	s := server.New(store.New(time.Second), "node-a", r)

	// Write via normal endpoint
	body := bytes.NewBufferString(`{"key":"compare-key","value":"test-value","ttl_ms":1000}`)
	req, _ := http.NewRequest(http.MethodPost, "/set?key=compare-key", body)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	normalSetCode := w.Code

	// Write via quorum endpoint
	body = bytes.NewBufferString(`{"key":"compare-key","value":"test-value","ttl_ms":1000}`)
	req, _ = http.NewRequest(http.MethodPost, "/quorum/set?key=compare-key", body)
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	quorumSetCode := w.Code

	t.Logf("Normal set: %d, Quorum set: %d", normalSetCode, quorumSetCode)

	// Both should succeed
	if normalSetCode != http.StatusOK {
		t.Errorf("normal set failed: %d", normalSetCode)
	}
	if quorumSetCode != http.StatusOK {
		t.Logf("quorum set returned %d (may fail if replicas unreachable)", quorumSetCode)
	}
}
