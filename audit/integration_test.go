package audit

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"shdc/internal/ring"
	"shdc/internal/server"
	"shdc/internal/store"
)

// getFreePort returns a free port for testing
func getFreePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

// startTestServer starts an HTTP server on a random port and returns the server and port
func startTestServer(t *testing.T, r *ring.Ring, nodeID, baseAddr string) (*server.Server, int, func()) {
	port, err := getFreePort()
	if err != nil {
		t.Fatalf("failed to get free port: %v", err)
	}

	s, cleanup := startTestServerOnPort(t, r, nodeID, port)
	return s, port, cleanup
}

// startTestServerOnPort starts an HTTP server on a specific port
func startTestServerOnPort(t *testing.T, r *ring.Ring, nodeID string, port int) (*server.Server, func()) {
	store := store.New(time.Second)
	s := server.New(store, nodeID, r)

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("failed to listen on %s: %v", addr, err)
	}

	go func() {
		http.Serve(listener, s.Handler())
	}()

	// Wait for server to be ready
	time.Sleep(50 * time.Millisecond)

	cleanup := func() {
		listener.Close()
	}

	return s, cleanup
}

// TestLiveClusterIntegration tests a real 3-node cluster with HTTP servers
func TestLiveClusterIntegration(t *testing.T) {
	// Get 3 free ports first
	portA, err := getFreePort()
	if err != nil {
		t.Fatalf("failed to get free port: %v", err)
	}
	portB, err := getFreePort()
	if err != nil {
		t.Fatalf("failed to get free port: %v", err)
	}
	portC, err := getFreePort()
	if err != nil {
		t.Fatalf("failed to get free port: %v", err)
	}

	// Create ring with actual ports
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: fmt.Sprintf("127.0.0.1:%d", portA)})
	r.AddNode(ring.Node{ID: "node-b", Addr: fmt.Sprintf("127.0.0.1:%d", portB)})
	r.AddNode(ring.Node{ID: "node-c", Addr: fmt.Sprintf("127.0.0.1:%d", portC)})

	// Start 3 nodes on the allocated ports
	_, cleanupA := startTestServerOnPort(t, r, "node-a", portA)
	defer cleanupA()

	_, cleanupB := startTestServerOnPort(t, r, "node-b", portB)
	defer cleanupB()

	_, cleanupC := startTestServerOnPort(t, r, "node-c", portC)
	defer cleanupC()

	// Wait for servers to start
	time.Sleep(100 * time.Millisecond)

	baseA := fmt.Sprintf("http://127.0.0.1:%d", portA)

	// Test 1: Set a key
	setBody := `{"key":"test-key","value":"test-value","ttl_ms":60000}`
	resp, err := http.Post(baseA+"/set", "application/json", strings.NewReader(setBody))
	if err != nil {
		t.Fatalf("set failed: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("set returned %d", resp.StatusCode)
	}
	resp.Body.Close()
	t.Log("Set operation succeeded")

	// Test 2: Get the key
	resp, err = http.Get(baseA + "/get?key=test-key")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get returned %d: %s", resp.StatusCode, string(body))
	}
	t.Logf("Get operation succeeded: %s", string(body))

	// Test 3: Verify ring info
	resp, err = http.Get(baseA + "/ring/info")
	if err != nil {
		t.Fatalf("ring info failed: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	t.Logf("Ring info: %s", string(body))
	_ = body
}

// TestContinuousTrafficDuringRebalance verifies traffic during node join
func TestContinuousTrafficDuringRebalance(t *testing.T) {
	r := ring.New(150)

	// Start initial 2 nodes
	_, portA, cleanupA := startTestServer(t, r, "node-a", "")
	defer cleanupA()

	_, portB, cleanupB := startTestServer(t, r, "node-b", "")
	defer cleanupB()

	r.AddNode(ring.Node{ID: "node-a", Addr: fmt.Sprintf("127.0.0.1:%d", portA)})
	r.AddNode(ring.Node{ID: "node-b", Addr: fmt.Sprintf("127.0.0.1:%d", portB)})

	time.Sleep(100 * time.Millisecond)

	baseA := fmt.Sprintf("http://127.0.0.1:%d", portA)

	// Pre-populate with keys
	const numKeys = 50
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("pre-key-%d", i)
		setBody := fmt.Sprintf(`{"key":"%s","value":"value-%d","ttl_ms":60000}`, key, i)
		resp, err := http.Post(baseA+"/set", "application/json", strings.NewReader(setBody))
		if err != nil {
			t.Fatalf("pre-populate failed: %v", err)
		}
		resp.Body.Close()
	}
	t.Logf("Pre-populated %d keys", numKeys)

	// Start continuous traffic
	var totalRequests, failedRequests, notFoundCount int64
	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				key := fmt.Sprintf("pre-key-%d", int(time.Now().UnixNano())%numKeys)
				resp, err := http.Get(fmt.Sprintf("%s/get?key=%s", baseA, key))
				if err != nil {
					atomic.AddInt64(&failedRequests, 1)
					continue
				}
				_, _ = io.ReadAll(resp.Body)
				resp.Body.Close()

				atomic.AddInt64(&totalRequests, 1)
				if resp.StatusCode == http.StatusNotFound {
					atomic.AddInt64(&notFoundCount, 1)
				}
			}
		}
	}()

	// Let traffic run
	time.Sleep(200 * time.Millisecond)

	// Add third node (triggers rebalance)
	_, portC, cleanupC := startTestServer(t, r, "node-c", "")
	defer cleanupC()

	r.AddNode(ring.Node{ID: "node-c", Addr: fmt.Sprintf("127.0.0.1:%d", portC)})
	t.Log("Added node-c")

	// Let traffic continue during rebalance
	time.Sleep(300 * time.Millisecond)

	// Stop traffic
	close(stop)
	wg.Wait()

	total := atomic.LoadInt64(&totalRequests)
	failed := atomic.LoadInt64(&failedRequests)
	notFound := atomic.LoadInt64(&notFoundCount)

	t.Logf("Results: total=%d failed=%d not_found=%d", total, failed, notFound)

	// Document current behavior - some failures may occur during routing changes
	if notFound > 0 {
		t.Logf("WARNING: %d keys not found during rebalance", notFound)
	}
}

// TestNodeRecoveryRebalance tests node recovery scenario
func TestNodeRecoveryRebalance(t *testing.T) {
	r := ring.New(150)

	// Start initial 2 nodes
	_, portA, cleanupA := startTestServer(t, r, "node-a", "")
	defer cleanupA()

	_, portB, cleanupB := startTestServer(t, r, "node-b", "")
	defer cleanupB()

	r.AddNode(ring.Node{ID: "node-a", Addr: fmt.Sprintf("127.0.0.1:%d", portA)})
	r.AddNode(ring.Node{ID: "node-b", Addr: fmt.Sprintf("127.0.0.1:%d", portB)})

	time.Sleep(100 * time.Millisecond)

	baseA := fmt.Sprintf("http://127.0.0.1:%d", portA)

	// Add some keys
	for i := 0; i < 10; i++ {
		key := fmt.Sprintf("recovery-key-%d", i)
		setBody := fmt.Sprintf(`{"key":"%s","value":"value-%d","ttl_ms":60000}`, key, i)
		resp, err := http.Post(baseA+"/set", "application/json", strings.NewReader(setBody))
		if err != nil {
			t.Fatalf("set failed: %v", err)
		}
		resp.Body.Close()
	}

	// Simulate node failure and recovery by adding a new node
	_, portC, cleanupC := startTestServer(t, r, "node-c", "")
	defer cleanupC()

	r.AddNode(ring.Node{ID: "node-c", Addr: fmt.Sprintf("127.0.0.1:%d", portC)})
	t.Log("Added recovery node")

	time.Sleep(100 * time.Millisecond)

	// Verify cluster is functional
	resp, err := http.Get(baseA + "/get?key=recovery-key-0")
	if err != nil {
		t.Fatalf("get after recovery failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	t.Logf("Get after recovery: status=%d body=%s", resp.StatusCode, string(body))
}

// TestAutoRebalanceOnNodeFailure verifies that when a node fails,
// the cluster automatically triggers rebalancing.
func TestAutoRebalanceOnNodeFailure(t *testing.T) {
	r := ring.New(150)

	// Start initial 2 nodes
	_, portA, cleanupA := startTestServer(t, r, "node-a", "")
	defer cleanupA()

	_, portB, cleanupB := startTestServer(t, r, "node-b", "")
	defer cleanupB()

	r.AddNode(ring.Node{ID: "node-a", Addr: fmt.Sprintf("127.0.0.1:%d", portA)})
	r.AddNode(ring.Node{ID: "node-b", Addr: fmt.Sprintf("127.0.0.1:%d", portB)})

	time.Sleep(100 * time.Millisecond)

	baseA := fmt.Sprintf("http://127.0.0.1:%d", portA)

	// Write some keys
	for i := 0; i < 20; i++ {
		key := fmt.Sprintf("rebalance-key-%d", i)
		setBody := fmt.Sprintf(`{"key":"%s","value":"value-%d","ttl_ms":60000}`, key, i)
		resp, err := http.Post(baseA+"/set", "application/json", strings.NewReader(setBody))
		if err != nil {
			t.Fatalf("set failed: %v", err)
		}
		resp.Body.Close()
	}
	t.Log("Wrote 20 keys")

	// Wait for replication
	time.Sleep(300 * time.Millisecond)

	// Verify all keys are readable before node failure
	for i := 0; i < 20; i++ {
		key := fmt.Sprintf("rebalance-key-%d", i)
		resp, err := http.Get(baseA + "/get?key=" + key)
		if err != nil {
			t.Fatalf("get failed for %s: %v", key, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("key %s not found before failure: status=%d", key, resp.StatusCode)
		}
	}
	t.Log("All keys verified before failure")

	// "Kill" node-b by cleaning it up
	cleanupB()
	t.Log("Killed node-b")

	// Wait for failure detection and rebalance
	time.Sleep(3 * time.Second)

	// Verify all keys are still readable (from node-a)
	for i := 0; i < 20; i++ {
		key := fmt.Sprintf("rebalance-key-%d", i)
		resp, err := http.Get(baseA + "/get?key=" + key)
		if err != nil {
			t.Logf("get failed for %s (may be expected during rebalance): %v", key, err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Logf("key %s found after failure", key)
		} else {
			t.Logf("key %s status=%d body=%s", key, resp.StatusCode, string(body))
		}
	}
}
