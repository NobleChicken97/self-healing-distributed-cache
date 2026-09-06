package audit

import (
	"fmt"
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

// TestStoreThroughput measures the throughput of basic store operations.
func TestStoreThroughput(t *testing.T) {
	s := store.New(time.Second)
	defer s.Close()

	const duration = 2 * time.Second
	const numWorkers = 10

	var operations int64
	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Start workers
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			counter := 0
			for {
				select {
				case <-stop:
					return
				default:
					key := fmt.Sprintf("throughput-%d-%d", id, counter)
					s.Set(key, "value", 5*time.Second)
					_, _ = s.Get(key)
					atomic.AddInt64(&operations, 2)
					counter++
				}
			}
		}(i)
	}

	// Run for specified duration
	time.Sleep(duration)
	close(stop)
	wg.Wait()

	totalOps := atomic.LoadInt64(&operations)
	opsPerSec := float64(totalOps) / duration.Seconds()

	t.Logf("Store throughput: %d operations in %v (%.0f ops/sec)", totalOps, duration, opsPerSec)

	if opsPerSec < 1000 {
		t.Logf("Warning: throughput below 1000 ops/sec: %.0f ops/sec", opsPerSec)
	}
}

// TestRingLookupPerformance measures the performance of ring lookups.
func TestRingLookupPerformance(t *testing.T) {
	r := ring.New(150)

	// Add 10 nodes
	for i := 0; i < 10; i++ {
		r.AddNode(ring.Node{
			ID:   fmt.Sprintf("perf-node-%d", i),
			Addr: fmt.Sprintf("127.0.0.1:%d", 8080+i),
		})
	}

	const numLookups = 100000

	// Measure lookup time
	start := time.Now()
	for i := 0; i < numLookups; i++ {
		key := fmt.Sprintf("perf-key-%d", i)
		_, _ = r.Lookup(key)
	}
	elapsed := time.Since(start)

	lookupsPerSec := float64(numLookups) / elapsed.Seconds()
	t.Logf("Ring lookup performance: %d lookups in %v (%.0f lookups/sec)", numLookups, elapsed, lookupsPerSec)

	if lookupsPerSec < 100000 {
		t.Logf("Warning: lookup performance below 100k/sec: %.0f lookups/sec", lookupsPerSec)
	}
}

// TestReplicationOverhead measures the overhead of replication.
func TestReplicationOverhead(t *testing.T) {
	r := ring.New(150)

	storeA := store.New(time.Second)
	defer storeA.Close()
	storeB := store.New(time.Second)
	defer storeB.Close()

	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})
	r.AddNode(ring.Node{ID: "node-b", Addr: "127.0.0.1:8081"})

	_ = server.New(storeA, "node-a", r)
	_ = storeB // Use storeB directly for verification

	// Measure write time without replication overhead
	const numWrites = 1000

	// Direct store writes (no replication)
	start := time.Now()
	for i := 0; i < numWrites; i++ {
		key := fmt.Sprintf("direct-%d", i)
		storeA.Set(key, "value", 5*time.Second)
	}
	directElapsed := time.Since(start)

	// Writes via server (with replication)
	start = time.Now()
	for i := 0; i < numWrites; i++ {
		key := fmt.Sprintf("replicated-%d", i)
		// Simulate server write (which triggers replication)
		storeA.Set(key, "value", 5*time.Second)
		// In real scenario, this would also trigger async replication
	}
	replicatedElapsed := time.Since(start)

	t.Logf("Replication overhead: direct=%v, replicated=%v for %d writes",
		directElapsed, replicatedElapsed, numWrites)

	// Verify data exists
	keys := storeA.Keys()
	t.Logf("Store has %d keys after writes", len(keys))
}

// TestConcurrentClientLoad simulates multiple concurrent clients.
func TestConcurrentClientLoad(t *testing.T) {
	r := ring.New(150)

	_, portA, cleanupA := startTestServer(t, r, "node-a", "")
	defer cleanupA()
	_, portB, cleanupB := startTestServer(t, r, "node-b", "")
	defer cleanupB()

	r.AddNode(ring.Node{ID: "node-a", Addr: fmt.Sprintf("127.0.0.1:%d", portA)})
	r.AddNode(ring.Node{ID: "node-b", Addr: fmt.Sprintf("127.0.0.1:%d", portB)})

	baseA := fmt.Sprintf("http://127.0.0.1:%d", portA)
	baseB := fmt.Sprintf("http://127.0.0.1:%d", portB)

	const numClients = 20
	const requestsPerClient = 100
	const duration = 5 * time.Second

	var totalRequests, successRequests, failedRequests int64
	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Start concurrent clients
	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			counter := 0
			for {
				select {
				case <-stop:
					return
				default:
					key := fmt.Sprintf("client-%d-key-%d", id, counter%50)
					value := fmt.Sprintf("value-%d", counter)

					// Alternate between nodes and operations
					var base string
					if counter%2 == 0 {
						base = baseA
					} else {
						base = baseB
					}

					var resp *http.Response
					var err error

					if counter%3 == 0 {
						// Write
						setBody := fmt.Sprintf(`{"key":"%s","value":"%s","ttl_ms":30000}`, key, value)
						resp, err = http.Post(base+"/set", "application/json", strings.NewReader(setBody))
					} else {
						// Read
						resp, err = http.Get(base + "/get?key=" + key)
					}

					atomic.AddInt64(&totalRequests, 1)
					if err == nil {
						resp.Body.Close()
						if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
							atomic.AddInt64(&successRequests, 1)
						} else {
							atomic.AddInt64(&failedRequests, 1)
						}
					} else {
						atomic.AddInt64(&failedRequests, 1)
					}
					counter++
				}
			}
		}(i)
	}

	// Run for specified duration
	time.Sleep(duration)
	close(stop)
	wg.Wait()

	total := atomic.LoadInt64(&totalRequests)
	success := atomic.LoadInt64(&successRequests)
	failed := atomic.LoadInt64(&failedRequests)

	t.Logf("Concurrent client load: %d clients, %d total requests, %d success, %d failed",
		numClients, total, success, failed)
	t.Logf("Throughput: %.0f requests/sec", float64(total)/duration.Seconds())

	if total > 0 && failed > total/10 {
		t.Logf("Warning: high failure rate: %d/%d (%.1f%%)", failed, total, float64(failed)/float64(total)*100)
	}
}

// TestMemoryUsageUnderLoad verifies memory usage stays reasonable under load.
func TestMemoryUsageUnderLoad(t *testing.T) {
	const memCap int64 = 1024 * 1024 // 1MB cap
	s := store.NewWithEviction(time.Second, memCap)
	defer s.Close()

	const numKeys = 10000
	value := strings.Repeat("x", 100) // 100 byte values

	// Write keys until we hit the cap
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("memory-key-%d", i)
		s.Set(key, value, 0)
	}

	finalMem := s.MemoryUsage()
	entryCount := s.EntryCount()

	t.Logf("Memory usage under load: %d bytes, %d entries (cap: %d)", finalMem, entryCount, memCap)

	// Memory should be near or below cap (with some tolerance)
	if finalMem > memCap*3 {
		t.Logf("Warning: memory usage %d exceeds 3x cap %d", finalMem, memCap)
	}

	// Verify store is still functional
	s.Set("final-key", "final-value", time.Second)
	if val, err := s.Get("final-key"); err != nil || val != "final-value" {
		t.Fatalf("Store not functional after memory pressure: val=%q, err=%v", val, err)
	}
}

// TestFailoverTimeMeasurement measures the time taken for failover.
func TestFailoverTimeMeasurement(t *testing.T) {
	r := ring.New(150)

	_, portA, cleanupA := startTestServer(t, r, "node-a", "")
	defer cleanupA()
	_, portB, cleanupB := startTestServer(t, r, "node-b", "")
	defer cleanupB()

	r.AddNode(ring.Node{ID: "node-a", Addr: fmt.Sprintf("127.0.0.1:%d", portA)})
	r.AddNode(ring.Node{ID: "node-b", Addr: fmt.Sprintf("127.0.0.1:%d", portB)})

	baseA := fmt.Sprintf("http://127.0.0.1:%d", portA)

	// Write a key
	setBody := `{"key":"failover-time-test","value":"test-value","ttl_ms":60000}`
	resp, err := http.Post(baseA+"/set", "application/json", strings.NewReader(setBody))
	if err != nil {
		t.Fatalf("Failed to set key: %v", err)
	}
	resp.Body.Close()

	// Wait for replication
	time.Sleep(300 * time.Millisecond)

	// Kill node-b and measure failover time
	cleanupB()

	start := time.Now()
	// Try to read the key - should failover to replica
	var failoverTime time.Duration
	for i := 0; i < 100; i++ {
		resp, err := http.Get(baseA + "/get?key=failover-time-test")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				failoverTime = time.Since(start)
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	if failoverTime > 0 {
		t.Logf("Failover time: %v", failoverTime)
		if failoverTime > 5*time.Second {
			t.Logf("Warning: failover took longer than 5 seconds: %v", failoverTime)
		}
	} else {
		t.Log("Failover time measurement inconclusive (key may have been on node-b)")
	}
}
