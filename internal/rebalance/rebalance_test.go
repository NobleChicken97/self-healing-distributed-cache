package rebalance

import (
	"testing"
	"time"

	"shdc/internal/ring"
)

func TestComputeKeyMovements(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})
	r.AddNode(ring.Node{ID: "node-b", Addr: "127.0.0.1:8081"})

	rb := New(r, nil)

	// Simulate node-a's local keys.
	localKeys := []string{"key-1", "key-2", "key-3", "key-4", "key-5"}

	migrations := rb.ComputeKeyMovements("node-a", localKeys)

	// Some keys should move to node-b.
	if len(migrations) == 0 {
		t.Fatal("expected some keys to move, got none")
	}

	t.Logf("keys to move: %d", len(migrations))
	for _, m := range migrations {
		t.Logf("  key=%s -> new_owner=%s", m.Key, m.NewOwner.ID)
	}

	// Verify all migrations have node-b as the new owner.
	for _, m := range migrations {
		if m.NewOwner.ID != "node-b" {
			t.Errorf("expected new owner to be node-b, got %s", m.NewOwner.ID)
		}
	}
}

func TestRebalanceWithNoChanges(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})

	rb := New(r, nil)

	// All keys should stay on node-a (it's the only node).
	localKeys := []string{"key-1", "key-2", "key-3"}

	result := rb.Rebalance("node-a", "127.0.0.1:8080", localKeys)

	if result.MovedKeys != 0 {
		t.Fatalf("expected 0 keys to move, got %d", result.MovedKeys)
	}
	if result.FailedKeys != 0 {
		t.Fatalf("expected 0 failures, got %d", result.FailedKeys)
	}
	t.Logf("rebalance result: total=%d moved=%d failed=%d",
		result.TotalKeys, result.MovedKeys, result.FailedKeys)
}

func TestRebalanceMeasuresDuration(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})
	r.AddNode(ring.Node{ID: "node-b", Addr: "127.0.0.1:8081"})

	rb := New(r, nil)

	// Use keys that will actually move.
	localKeys := []string{"key-1", "key-2", "key-3"}

	start := time.Now()
	result := rb.Rebalance("node-a", "127.0.0.1:8080", localKeys)
	elapsed := time.Since(start)

	// Duration should be non-negative and reasonable.
	if result.Duration < 0 {
		t.Fatal("expected non-negative duration")
	}
	if elapsed < result.Duration {
		t.Fatal("measured duration should be at least the result duration")
	}

	t.Logf("rebalance took %v (reported %v), moved=%d", elapsed, result.Duration, result.MovedKeys)
}

func TestRebalanceIsInProgress(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})

	rb := New(r, nil)

	if rb.IsInProgress() {
		t.Fatal("should not be in progress initially")
	}

	// Rebalance with no keys should complete immediately.
	result := rb.Rebalance("node-a", "127.0.0.1:8080", []string{})
	if result.TotalKeys != 0 {
		t.Fatalf("expected 0 total keys, got %d", result.TotalKeys)
	}

	if rb.IsInProgress() {
		t.Fatal("should not be in progress after completion")
	}
}
