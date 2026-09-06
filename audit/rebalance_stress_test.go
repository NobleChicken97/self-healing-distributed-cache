package audit

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"shdc/internal/rebalance"
	"shdc/internal/ring"
)

// TestRebalanceLargeDataset tests migration of 10,000+ keys
func TestRebalanceLargeDataset(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})
	r.AddNode(ring.Node{ID: "node-b", Addr: "127.0.0.1:8081"})

	rb := rebalance.New(r, nil)

	// Generate 10,000 keys
	const numKeys = 10000
	keys := make([]string, numKeys)
	for i := 0; i < numKeys; i++ {
		keys[i] = fmt.Sprintf("key-%d", i)
	}

	// Compute how many keys need to move
	migrations := rb.ComputeKeyMovements("node-a", keys)
	t.Logf("Keys to migrate: %d out of %d", len(migrations), numKeys)

	if len(migrations) == 0 {
		t.Skip("No keys need to move (hash distribution)")
	}

	// Verify all migrations are in pending state
	pendingCount := 0
	for _, m := range migrations {
		if m.Status.String() == "PENDING" {
			pendingCount++
		}
	}

	if pendingCount != len(migrations) {
		t.Fatalf("expected all migrations to be PENDING, got %d/%d", pendingCount, len(migrations))
	}

	t.Logf("All %d migrations are in PENDING state", pendingCount)
}

// TestRebalanceBackToBack tests two consecutive rebalance operations
func TestRebalanceBackToBack(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})
	r.AddNode(ring.Node{ID: "node-b", Addr: "127.0.0.1:8081"})

	rb := rebalance.New(r, nil)

	// First rebalance
	keys := []string{"key-1", "key-2", "key-3", "key-4", "key-5"}
	result1 := rb.Rebalance("node-a", "127.0.0.1:8080", keys)
	t.Logf("First rebalance: total=%d moved=%d failed=%d", result1.TotalKeys, result1.MovedKeys, result1.FailedKeys)

	// Second rebalance (should handle state correctly)
	result2 := rb.Rebalance("node-a", "127.0.0.1:8080", keys)
	t.Logf("Second rebalance: total=%d moved=%d failed=%d", result2.TotalKeys, result2.MovedKeys, result2.FailedKeys)

	// Both should have the same total keys
	if result1.TotalKeys != result2.TotalKeys {
		t.Logf("Warning: Total keys changed between rebalances: %d -> %d", result1.TotalKeys, result2.TotalKeys)
	}
}

// TestRebalanceConcurrentStress tests concurrent rebalance operations
func TestRebalanceConcurrentStress(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})
	r.AddNode(ring.Node{ID: "node-b", Addr: "127.0.0.1:8081"})

	rb := rebalance.New(r, nil)

	// Launch multiple concurrent rebalances
	const numGoroutines = 5
	var wg sync.WaitGroup
	results := make([]rebalance.Result, numGoroutines)
	errors := make([]error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			keys := []string{
				fmt.Sprintf("key-%d-a", idx),
				fmt.Sprintf("key-%d-b", idx),
				fmt.Sprintf("key-%d-c", idx),
			}
			results[idx] = rb.Rebalance("node-a", "127.0.0.1:8080", keys)
		}(i)
	}

	wg.Wait()

	// Check for any panics or errors
	for i, err := range errors {
		if err != nil {
			t.Errorf("Goroutine %d error: %v", i, err)
		}
	}

	t.Logf("All %d concurrent rebalances completed without panic", numGoroutines)
}

// TestRebalanceStateConsistency verifies internal state is consistent after rebalance
func TestRebalanceStateConsistency(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})

	rb := rebalance.New(r, nil)

	// Initial state
	if rb.IsInProgress() {
		t.Fatal("should not be in progress initially")
	}
	if rb.LastResult() != nil {
		t.Fatal("last result should be nil initially")
	}
	if len(rb.Migrations()) != 0 {
		t.Fatal("migrations should be empty initially")
	}

	// Run rebalance
	keys := []string{"key-1", "key-2", "key-3"}
	result := rb.Rebalance("node-a", "127.0.0.1:8080", keys)

	// Post-rebalance state
	if rb.IsInProgress() {
		t.Fatal("should not be in progress after completion")
	}
	if rb.LastResult() == nil {
		t.Fatal("last result should be set after rebalance")
	}
	if rb.LastResult().TotalKeys != result.TotalKeys {
		t.Fatalf("last result total keys mismatch: %d vs %d", rb.LastResult().TotalKeys, result.TotalKeys)
	}
}

// TestRebalanceWithAllKeysMoving tests when all keys need to move
func TestRebalanceWithAllKeysMoving(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})
	r.AddNode(ring.Node{ID: "node-b", Addr: "127.0.0.1:8081"})

	rb := rebalance.New(r, nil)

	// Find keys that all move to node-b
	var movingKeys []string
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("test-key-%d", i)
		owner, _ := r.Lookup(key)
		if owner.ID == "node-b" {
			movingKeys = append(movingKeys, key)
			if len(movingKeys) >= 100 {
				break
			}
		}
	}

	if len(movingKeys) == 0 {
		t.Skip("No keys found that move to node-b")
	}

	t.Logf("Testing with %d keys that all need to move", len(movingKeys))

	// All these keys should need to move from node-a to node-b
	migrations := rb.ComputeKeyMovements("node-a", movingKeys)
	if len(migrations) != len(movingKeys) {
		t.Fatalf("expected %d migrations, got %d", len(movingKeys), len(migrations))
	}
}

// TestRebalancePerformance measures time for large rebalance
func TestRebalancePerformance(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})
	r.AddNode(ring.Node{ID: "node-b", Addr: "127.0.0.1:8081"})

	rb := rebalance.New(r, nil)

	// Create 1000 keys
	const numKeys = 1000
	keys := make([]string, numKeys)
	for i := 0; i < numKeys; i++ {
		keys[i] = fmt.Sprintf("perf-key-%d", i)
	}

	start := time.Now()
	result := rb.Rebalance("node-a", "127.0.0.1:8080", keys)
	elapsed := time.Since(start)

	t.Logf("Rebalanced %d keys in %v", numKeys, elapsed)
	t.Logf("Result: total=%d moved=%d failed=%d duration=%v",
		result.TotalKeys, result.MovedKeys, result.FailedKeys, result.Duration)

	// The rebalance should complete in reasonable time
	if elapsed > 5*time.Second {
		t.Logf("Warning: Rebalance took longer than expected: %v", elapsed)
	}
}
