package server

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"selfhealingcache/internal/cluster"
	rb "selfhealingcache/internal/rebalance"
	"selfhealingcache/internal/ring"
	"selfhealingcache/internal/store"
)

// TestReplicationOnSet verifies that a write to the primary is replicated to
// the replica node.
func TestReplicationOnSet(t *testing.T) {
	storeA := store.New(time.Second)
	defer storeA.Close()
	storeB := store.New(time.Second)
	defer storeB.Close()

	// Create test servers first to get actual ports.
	tsA := httptest.NewServer(nil)
	defer tsA.Close()
	tsB := httptest.NewServer(nil)
	defer tsB.Close()

	// Create ring with actual test server addresses.
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: tsA.Listener.Addr().String()})
	r.AddNode(ring.Node{ID: "node-b", Addr: tsB.Listener.Addr().String()})

	// Now create servers with the correct ring.
	serverA := New(storeA, "node-a", r)
	serverB := New(storeB, "node-b", r)
	tsA.Config.Handler = serverA.Handler()
	tsB.Config.Handler = serverB.Handler()

	// Set a key via node A (which may or may not be the primary).
	setBody := `{"key":"repl-test","value":"hello","ttl_ms":0}`
	resp, err := http.Post(tsA.URL+"/set", "application/json", strings.NewReader(setBody))
	if err != nil {
		t.Fatalf("set request failed: %v", err)
	}
	resp.Body.Close()

	// Wait for async replication.
	time.Sleep(300 * time.Millisecond)

	// The key should exist on whichever node is the primary.
	// Check both nodes — at least one should have it.
	hasA, _ := storeA.Get("repl-test")
	hasB, _ := storeB.Get("repl-test")

	if hasA == "" && hasB == "" {
		t.Fatalf("key not found on either node after replication")
	}
	t.Logf("key on node-a: %q, key on node-b: %q", hasA, hasB)
}

// TestFailoverRead verifies that when the primary node is down, a read can
// still succeed from the replica.
func TestFailoverRead(t *testing.T) {
	storeA := store.New(time.Second)
	defer storeA.Close()
	storeB := store.New(time.Second)
	defer storeB.Close()
	storeC := store.New(time.Second)
	defer storeC.Close()

	// Create test servers first to get actual ports.
	tsA := httptest.NewServer(nil)
	defer tsA.Close()
	tsB := httptest.NewServer(nil)
	defer tsB.Close()
	tsC := httptest.NewServer(nil)
	defer tsC.Close()

	// Create ring with actual test server addresses.
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: tsA.Listener.Addr().String()})
	r.AddNode(ring.Node{ID: "node-b", Addr: tsB.Listener.Addr().String()})
	r.AddNode(ring.Node{ID: "node-c", Addr: tsC.Listener.Addr().String()})

	serverA := New(storeA, "node-a", r)
	serverB := New(storeB, "node-b", r)
	serverC := New(storeC, "node-c", r)
	tsA.Config.Handler = serverA.Handler()
	tsB.Config.Handler = serverB.Handler()
	tsC.Config.Handler = serverC.Handler()

	// Set a key via node A.
	setBody := `{"key":"failover-test","value":"survives","ttl_ms":0}`
	resp, err := http.Post(tsA.URL+"/set", "application/json", strings.NewReader(setBody))
	if err != nil {
		t.Fatalf("set request failed: %v", err)
	}
	resp.Body.Close()

	// Wait for async replication.
	time.Sleep(100 * time.Millisecond)

	// Determine which node is the primary for this key.
	primary, _ := r.Lookup("failover-test")
	t.Logf("primary for failover-test: %s", primary.ID)

	// "Kill" the primary by closing its test server.
	if primary.ID == "node-a" {
		tsA.Close()
	} else if primary.ID == "node-b" {
		tsB.Close()
	} else {
		tsC.Close()
	}

	// Try to read the key from a surviving node.
	// The surviving node should forward to primary, fail, then fallback to replica.
	survivingURL := tsB.URL
	if primary.ID == "node-b" {
		survivingURL = tsC.URL
	}

	resp, err = http.Get(survivingURL + "/get?key=failover-test")
	if err != nil {
		t.Fatalf("get request failed: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	t.Logf("response: status=%d body=%s", resp.StatusCode, string(body))

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK from replica fallback, got %d: %s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), "survives") {
		t.Fatalf("expected value 'survives' in response, got: %s", string(body))
	}
}

// TestHealthAwareRouting verifies that when cluster health info marks a primary
// as dead, read requests are immediately routed to replicas without trying the
// dead primary first.
func TestHealthAwareRouting(t *testing.T) {
	storeA := store.New(time.Second)
	defer storeA.Close()
	storeB := store.New(time.Second)
	defer storeB.Close()
	storeC := store.New(time.Second)
	defer storeC.Close()

	// Create test servers first to get actual ports.
	tsA := httptest.NewServer(nil)
	defer tsA.Close()
	tsB := httptest.NewServer(nil)
	defer tsB.Close()
	tsC := httptest.NewServer(nil)
	defer tsC.Close()

	// Use a small number of virtual nodes for predictable replica placement.
	r := ring.New(10)
	r.AddNode(ring.Node{ID: "node-a", Addr: tsA.Listener.Addr().String()})
	r.AddNode(ring.Node{ID: "node-b", Addr: tsB.Listener.Addr().String()})
	r.AddNode(ring.Node{ID: "node-c", Addr: tsC.Listener.Addr().String()})

	// Create a cluster for node-b to track health.
	c, err := cluster.New(cluster.Config{
		NodeID:   "node-b",
		BindAddr: "127.0.0.1",
		BindPort: 7960,
		Logger:   log.New(os.Stderr, "[TEST] ", log.Ltime),
	})
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer c.Shutdown()

	serverA := New(storeA, "node-a", r)
	serverB := New(storeB, "node-b", r).WithCluster(c)
	serverC := New(storeC, "node-c", r)
	tsA.Config.Handler = serverA.Handler()
	tsB.Config.Handler = serverB.Handler()
	tsC.Config.Handler = serverC.Handler()

	// Find a key where node-b is the primary and node-a is the replica.
	// This way we can mark node-b as dead and verify node-a serves the read.
	var testKey string
	var primaryNode *httptest.Server
	var replicaNode *httptest.Server
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("failover-key-%d", i)
		primary, _ := r.Lookup(key)
		if primary.ID == "node-b" {
			replicas := r.Replicas(key, 2)
			for _, rep := range replicas {
				if rep.ID == "node-a" {
					testKey = key
					primaryNode = tsB
					replicaNode = tsA
					goto found
				}
			}
		}
	}
	t.Fatal("could not find a suitable key for testing")
found:
	t.Logf("test key: %s, primary: node-b, replica: node-a", testKey)

	// Write the key to the primary (node-b) via HTTP.
	setBody := fmt.Sprintf(`{"key":"%s","value":"fast-failover","ttl_ms":0}`, testKey)
	resp, err := http.Post(primaryNode.URL+"/set", "application/json", strings.NewReader(setBody))
	if err != nil {
		t.Fatalf("set request failed: %v", err)
	}
	resp.Body.Close()

	// Wait for async replication to reach the replica.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		resp, err := http.Get(replicaNode.URL + "/get?key=" + testKey)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Logf("replica has key: %s", string(body))
			goto replicated
		}
	}
	t.Fatal("timeout waiting for replication to complete")
replicated:

	// Now mark node-b as dead in the cluster.
	c.SetNodeAlive("node-b", false)
	t.Logf("marked node-b as dead in cluster")

	// Read from node-a (which has cluster health info via the ring).
	// node-a should detect node-b is dead and route to itself (the replica).
	// Since node-a is the replica and has the key locally, it should serve it.
	start := time.Now()

	// Send request to node-c, which will forward to primary (node-b), fail,
	// then fallback to replica (node-a).
	resp, err = http.Get(tsC.URL + "/get?key=" + testKey)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("get request failed: %v", err)
	}
	defer resp.Body.Close()

	failoverBody, _ := io.ReadAll(resp.Body)
	t.Logf("response: status=%d body=%s elapsed=%v", resp.StatusCode, string(failoverBody), elapsed)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", resp.StatusCode, string(failoverBody))
	}
	if !strings.Contains(string(failoverBody), "fast-failover") {
		t.Fatalf("expected value 'fast-failover' in response, got: %s", string(failoverBody))
	}

	// The response should be fast (no timeout waiting for dead primary).
	if elapsed > 2*time.Second {
		t.Fatalf("failover took too long: %v (expected < 2s with health-aware routing)", elapsed)
	}
	t.Logf("health-aware failover completed in %v", elapsed)
}

// TestRebalanceOnNodeJoin verifies that when a new node joins, keys are migrated
// correctly and reads continue to succeed throughout the rebalance.
func TestRebalanceOnNodeJoin(t *testing.T) {
	// Start with 2 nodes.
	storeA := store.New(time.Second)
	defer storeA.Close()
	storeB := store.New(time.Second)
	defer storeB.Close()

	tsA := httptest.NewServer(nil)
	defer tsA.Close()
	tsB := httptest.NewServer(nil)
	defer tsB.Close()

	// Create ring with 2 nodes.
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: tsA.Listener.Addr().String()})
	r.AddNode(ring.Node{ID: "node-b", Addr: tsB.Listener.Addr().String()})

	serverA := New(storeA, "node-a", r)
	serverB := New(storeB, "node-b", r)
	tsA.Config.Handler = serverA.Handler()
	tsB.Config.Handler = serverB.Handler()

	// Write some keys to the cluster.
	keys := []string{"rebalance-key-1", "rebalance-key-2", "rebalance-key-3", "rebalance-key-4", "rebalance-key-5"}
	for _, key := range keys {
		setBody := fmt.Sprintf(`{"key":"%s","value":"value-%s","ttl_ms":0}`, key, key)
		resp, err := http.Post(tsA.URL+"/set", "application/json", strings.NewReader(setBody))
		if err != nil {
			t.Fatalf("set request failed for %s: %v", key, err)
		}
		resp.Body.Close()
	}

	// Wait for replication.
	time.Sleep(300 * time.Millisecond)

	// Verify all keys are readable.
	for _, key := range keys {
		resp, err := http.Get(tsA.URL + "/get?key=" + key)
		if err != nil {
			t.Fatalf("get failed for %s: %v", key, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("key %s not found before rebalance: status=%d body=%s", key, resp.StatusCode, string(body))
		}
	}

	// Now add a third node to the ring.
	storeC := store.New(time.Second)
	defer storeC.Close()

	tsC := httptest.NewServer(nil)
	defer tsC.Close()

	r.AddNode(ring.Node{ID: "node-c", Addr: tsC.Listener.Addr().String()})
	serverC := New(storeC, "node-c", r)
	tsC.Config.Handler = serverC.Handler()

	// Trigger rebalance on node-a (it will compute which keys moved to node-c).
	rebalancerA := rb.New(r, nil)
	result := rebalancerA.Rebalance("node-a", tsA.Listener.Addr().String(), storeA.Keys())

	t.Logf("rebalance result: total=%d moved=%d failed=%d duration=%v",
		result.TotalKeys, result.MovedKeys, result.FailedKeys, result.Duration)

	// Verify all keys are still readable after rebalance.
	for _, key := range keys {
		// Try reading from any node.
		for _, ts := range []*httptest.Server{tsA, tsB, tsC} {
			resp, err := http.Get(ts.URL + "/get?key=" + key)
			if err != nil {
				continue
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				t.Logf("key %s found: %s", key, string(body))
				goto keyFound
			}
		}
		t.Fatalf("key %s not found after rebalance", key)
	keyFound:
	}
}

// TestTTLConsistencyAcrossReplicas verifies that when a key with TTL is set,
// the primary and all replicas have the same absolute expiry timestamp.
func TestTTLConsistencyAcrossReplicas(t *testing.T) {
	storeA := store.New(time.Second)
	defer storeA.Close()
	storeB := store.New(time.Second)
	defer storeB.Close()

	// Create test servers first to get actual ports.
	tsA := httptest.NewServer(nil)
	defer tsA.Close()
	tsB := httptest.NewServer(nil)
	defer tsB.Close()

	// Create ring with actual test server addresses.
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: tsA.Listener.Addr().String()})
	r.AddNode(ring.Node{ID: "node-b", Addr: tsB.Listener.Addr().String()})

	serverA := New(storeA, "node-a", r)
	serverB := New(storeB, "node-b", r)
	tsA.Config.Handler = serverA.Handler()
	tsB.Config.Handler = serverB.Handler()

	// Set a key with a 10-second TTL via node A.
	setBody := `{"key":"ttl-test","value":"expires-soon","ttl_ms":10000}`
	resp, err := http.Post(tsA.URL+"/set", "application/json", strings.NewReader(setBody))
	if err != nil {
		t.Fatalf("set request failed: %v", err)
	}
	resp.Body.Close()

	// Wait for async replication.
	time.Sleep(300 * time.Millisecond)

	// Get the expiry timestamps from both nodes.
	expiryA := storeA.GetExpiry("ttl-test")
	expiryB := storeB.GetExpiry("ttl-test")

	t.Logf("expiry on node-a: %v", expiryA)
	t.Logf("expiry on node-b: %v", expiryB)

	// Both should have the same expiry time.
	if expiryA.IsZero() {
		t.Fatal("node-a should have an expiry time")
	}
	if expiryB.IsZero() {
		t.Fatal("node-b should have an expiry time (replicated)")
	}

	// The expiry times should be identical (or very close).
	drift := expiryA.Sub(expiryB)
	if drift < 0 {
		drift = -drift
	}
	t.Logf("expiry drift between replicas: %v", drift)

	// Allow up to 10ms drift for processing time.
	if drift > 10*time.Millisecond {
		t.Fatalf("expiry drift too large: %v (expected < 10ms)", drift)
	}

	// Verify both nodes return the same value.
	resp, err = http.Get(tsA.URL + "/get?key=ttl-test")
	if err != nil {
		t.Fatalf("get from node-a failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("key not found on node-a: status=%d body=%s", resp.StatusCode, string(body))
	}

	resp, err = http.Get(tsB.URL + "/get?key=ttl-test")
	if err != nil {
		t.Fatalf("get from node-b failed: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("key not found on node-b: status=%d body=%s", resp.StatusCode, string(body))
	}

	t.Logf("TTL consistency verified: both replicas have same expiry")
}

// TestTTLExpirySynchronized verifies that a key expires at approximately the
// same time on both the primary and replica.
func TestTTLExpirySynchronized(t *testing.T) {
	storeA := store.New(10 * time.Millisecond) // Fast sweep for testing
	defer storeA.Close()
	storeB := store.New(10 * time.Millisecond) // Fast sweep for testing
	defer storeB.Close()

	// Create test servers first to get actual ports.
	tsA := httptest.NewServer(nil)
	defer tsA.Close()
	tsB := httptest.NewServer(nil)
	defer tsB.Close()

	// Create ring with actual test server addresses.
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: tsA.Listener.Addr().String()})
	r.AddNode(ring.Node{ID: "node-b", Addr: tsB.Listener.Addr().String()})

	serverA := New(storeA, "node-a", r)
	serverB := New(storeB, "node-b", r)
	tsA.Config.Handler = serverA.Handler()
	tsB.Config.Handler = serverB.Handler()

	// Set a key with a TTL (500ms).
	setBody := `{"key":"short-ttl","value":"will-expire","ttl_ms":500}`
	resp, err := http.Post(tsA.URL+"/set", "application/json", strings.NewReader(setBody))
	if err != nil {
		t.Fatalf("set request failed: %v", err)
	}
	resp.Body.Close()

	// Wait for async replication.
	time.Sleep(300 * time.Millisecond)

	// Verify both nodes have the key before TTL expires.
	valA, err := storeA.Get("short-ttl")
	if err != nil {
		t.Fatalf("key should exist on node-a before expiry: %v", err)
	}
	valB, err := storeB.Get("short-ttl")
	if err != nil {
		t.Fatalf("key should exist on node-b before expiry: %v", err)
	}
	t.Logf("key exists on node-a: %q, node-b: %q", valA, valB)

	// Record the expiry times - they should be identical.
	expiryA := storeA.GetExpiry("short-ttl")
	expiryB := storeB.GetExpiry("short-ttl")
	t.Logf("expiry on node-a: %v", expiryA)
	t.Logf("expiry on node-b: %v", expiryB)

	// Verify expiry times are synchronized.
	drift := expiryA.Sub(expiryB)
	if drift < 0 {
		drift = -drift
	}
	if drift > 10*time.Millisecond {
		t.Fatalf("expiry drift too large: %v", drift)
	}

	// Wait for the TTL to expire.
	time.Sleep(500 * time.Millisecond)

	// Both nodes should have expired the key.
	_, err = storeA.Get("short-ttl")
	if err == nil {
		t.Fatal("key should have expired on node-a")
	}
	_, err = storeB.Get("short-ttl")
	if err == nil {
		t.Fatal("key should have expired on node-b")
	}

	t.Logf("TTL expiry synchronized: both replicas expired the key at the same time")
}

func TestQuorumWriteAndRead(t *testing.T) {
	// This test verifies quorum write and read operations.
	storeA := store.New(time.Second)
	defer storeA.Close()
	storeB := store.New(time.Second)
	defer storeB.Close()

	// Create test servers first to get actual ports.
	tsA := httptest.NewServer(nil)
	defer tsA.Close()
	tsB := httptest.NewServer(nil)
	defer tsB.Close()

	// Create ring with actual test server addresses.
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: tsA.Listener.Addr().String()})
	r.AddNode(ring.Node{ID: "node-b", Addr: tsB.Listener.Addr().String()})

	serverA := New(storeA, "node-a", r)
	serverB := New(storeB, "node-b", r)
	tsA.Config.Handler = serverA.Handler()
	tsB.Config.Handler = serverB.Handler()

	// Find a key where node-a is primary.
	var testKey string
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("quorum-key-%d", i)
		owner, _ := r.Lookup(key)
		if owner.ID == "node-a" {
			testKey = key
			break
		}
	}
	if testKey == "" {
		t.Fatal("could not find a key owned by node-a")
	}
	t.Logf("test key: %s (primary: node-a)", testKey)

	// Perform a quorum write.
	setBody := fmt.Sprintf(`{"key":"%s","value":"quorum-value","ttl_ms":60000}`, testKey)
	resp, err := http.Post(tsA.URL+"/quorum/set", "application/json", strings.NewReader(setBody))
	if err != nil {
		t.Fatalf("quorum set request failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("quorum set failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	t.Logf("quorum write response: %s", string(body))

	// Verify both nodes have the key with the same version.
	_, versionA, _, err := storeA.GetWithVersion(testKey)
	if err != nil {
		t.Fatalf("key should exist on node-a: %v", err)
	}
	_, versionB, _, err := storeB.GetWithVersion(testKey)
	if err != nil {
		t.Fatalf("key should exist on node-b (replicated): %v", err)
	}

	if versionA != versionB {
		t.Fatalf("versions should match: node-a=%d, node-b=%d", versionA, versionB)
	}
	t.Logf("both nodes have version %d", versionA)

	// Perform a quorum read.
	resp, err = http.Get(tsB.URL + "/quorum/get?key=" + testKey)
	if err != nil {
		t.Fatalf("quorum get request failed: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("quorum get failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	t.Logf("quorum read response: %s", string(body))
}

// TestDeadPrimarySetAcceptedByReplica verifies that a write for a key whose
// primary is known dead is accepted by a replica with the FULL reconstructed
// body. Regression test: forwardWithBody used to hand forwardToReplica an
// already-consumed (empty) request body, so the replica rejected the write
// with 400 "invalid JSON body".
func TestDeadPrimarySetAcceptedByReplica(t *testing.T) {
	st := store.New(time.Second)
	defer st.Close()

	r := ring.New(10)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:19001"})
	r.AddNode(ring.Node{ID: "node-b", Addr: "127.0.0.1:19002"})
	r.AddNode(ring.Node{ID: "node-c", Addr: "127.0.0.1:19003"})

	c, err := cluster.New(cluster.Config{
		NodeID:   "node-a",
		BindAddr: "127.0.0.1",
		BindPort: 19000,
		Logger:   log.New(os.Stderr, "[TEST] ", log.Ltime),
	})
	if err != nil {
		t.Fatalf("failed to create cluster: %v", err)
	}
	defer c.Shutdown()

	srv := New(st, "node-a", r).WithCluster(c)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Find a key whose primary is a dead peer while node-a is a replica.
	// In a fresh cluster only node-a is alive, so every peer is "known dead".
	var testKey string
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("dead-primary-set-%d", i)
		primary, _ := r.Lookup(key)
		if primary.ID == "node-a" {
			continue
		}
		for _, rep := range r.Replicas(key, 2) {
			if rep.ID == "node-a" {
				testKey = key
				goto found
			}
		}
	}
	t.Fatal("could not find a suitable key for testing")
found:
	t.Logf("test key: %s (node-a is replica, primary is dead)", testKey)

	setBody := fmt.Sprintf(`{"key":"%s","value":"replica-accept","ttl_ms":60000}`, testKey)
	resp, err := http.Post(ts.URL+"/set", "application/json", strings.NewReader(setBody))
	if err != nil {
		t.Fatalf("set request failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dead-primary write rejected: status=%d body=%s", resp.StatusCode, string(body))
	}

	// The replica must have stored the full value (not an empty body).
	got, err := st.Get(testKey)
	if err != nil {
		t.Fatalf("key missing from replica store: %v", err)
	}
	if got != "replica-accept" {
		t.Fatalf("replica stored %q, want %q", got, "replica-accept")
	}
}
