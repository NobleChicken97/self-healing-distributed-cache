package ring

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"testing"
)

func TestRingLookup(t *testing.T) {
	r := New(150)
	r.AddNode(Node{ID: "node-a", Addr: ":8080"})
	r.AddNode(Node{ID: "node-b", Addr: ":8081"})
	r.AddNode(Node{ID: "node-c", Addr: ":8082"})

	// Every key should map to some node.
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("key-%d", i)
		node, ok := r.Lookup(key)
		if !ok {
			t.Fatalf("Lookup(%q) returned no node", key)
		}
		if node.ID == "" {
			t.Fatalf("Lookup(%q) returned empty node ID", key)
		}
	}
}

func TestRingAddNode(t *testing.T) {
	r := New(150)
	r.AddNode(Node{ID: "node-a", Addr: ":8080"})
	r.AddNode(Node{ID: "node-b", Addr: ":8081"})

	// Record where each key lands before adding a third node.
	before := make(map[string]string)
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("key-%d", i)
		node, _ := r.Lookup(key)
		before[key] = node.ID
	}

	r.AddNode(Node{ID: "node-c", Addr: ":8082"})

	// Count how many keys moved.
	moved := 0
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("key-%d", i)
		node, _ := r.Lookup(key)
		if node.ID != before[key] {
			moved++
		}
	}

	fraction := float64(moved) / 10000.0
	t.Logf("After adding node-c: %d/%d keys moved (%.2f%%)", moved, 10000, fraction*100)

	// With consistent hashing, roughly 1/N (33%) of keys should move.
	// Allow a generous band: 20-50%.
	if fraction < 0.20 || fraction > 0.50 {
		t.Fatalf("key movement fraction %.2f is outside expected range [0.20, 0.50]", fraction)
	}
}

func TestRingRemoveNode(t *testing.T) {
	r := New(150)
	r.AddNode(Node{ID: "node-a", Addr: ":8080"})
	r.AddNode(Node{ID: "node-b", Addr: ":8081"})
	r.AddNode(Node{ID: "node-c", Addr: ":8082"})

	// Record where each key lands before removing a node.
	before := make(map[string]string)
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("key-%d", i)
		node, _ := r.Lookup(key)
		before[key] = node.ID
	}

	r.RemoveNode("node-b")

	// Count how many keys moved. Keys that were on node-b must move;
	// keys on other nodes should stay put.
	moved := 0
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("key-%d", i)
		node, _ := r.Lookup(key)
		if node.ID != before[key] {
			moved++
		}
	}

	fraction := float64(moved) / 10000.0
	t.Logf("After removing node-b: %d/%d keys moved (%.2f%%)", moved, 10000, fraction*100)

	// Roughly 1/N (33%) should move. Allow 20-50%.
	if fraction < 0.20 || fraction > 0.50 {
		t.Fatalf("key movement fraction %.2f is outside expected range [0.20, 0.50]", fraction)
	}
}

// TestModuloHashingFailureMode demonstrates why modulo hashing is unsuitable:
// adding a node reshuffles nearly all keys, not just 1/N.
func TestModuloHashingFailureMode(t *testing.T) {
	hash := func(key string) uint32 {
		sum := sha256.Sum256([]byte(key))
		return binary.BigEndian.Uint32(sum[:4])
	}

	moduloLookup := func(key string, n int) int {
		return int(hash(key) % uint32(n))
	}

	// With 3 nodes, record assignments.
	before := make(map[string]int)
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("key-%d", i)
		before[key] = moduloLookup(key, 3)
	}

	// Add a 4th node.
	moved := 0
	for i := 0; i < 10000; i++ {
		key := fmt.Sprintf("key-%d", i)
		after := moduloLookup(key, 4)
		if after != before[key] {
			moved++
		}
	}

	fraction := float64(moved) / 10000.0
	t.Logf("Modulo hashing: adding 4th node moved %d/%d keys (%.2f%%)", moved, 10000, fraction*100)

	// Modulo hashing moves ~75% of keys (N-1)/N = 3/4.
	if fraction < 0.60 {
		t.Fatalf("modulo hashing moved %.2f%%, expected >60%%", fraction*100)
	}
}

// TestVirtualNodeDistribution confirms that virtual nodes produce a more even
// distribution of keys across physical nodes than a single point per node.
func TestVirtualNodeDistribution(t *testing.T) {
	// Without virtual nodes (1 point per node).
	r1 := New(1)
	r1.AddNode(Node{ID: "node-a", Addr: ":8080"})
	r1.AddNode(Node{ID: "node-b", Addr: ":8081"})
	r1.AddNode(Node{ID: "node-c", Addr: ":8082"})

	counts1 := countKeysPerNode(r1, 10000)
	stddev1 := stddev(counts1, 10000/3)
	t.Logf("Without virtual nodes: distribution %v, stddev %.1f", counts1, stddev1)

	// With virtual nodes (150 points per node).
	r2 := New(150)
	r2.AddNode(Node{ID: "node-a", Addr: ":8080"})
	r2.AddNode(Node{ID: "node-b", Addr: ":8081"})
	r2.AddNode(Node{ID: "node-c", Addr: ":8082"})

	counts2 := countKeysPerNode(r2, 10000)
	stddev2 := stddev(counts2, 10000/3)
	t.Logf("With virtual nodes: distribution %v, stddev %.1f", counts2, stddev2)

	// Virtual nodes should produce a tighter distribution.
	if stddev2 >= stddev1 {
		t.Fatalf("virtual nodes stddev %.1f >= no-virtual stddev %.1f", stddev2, stddev1)
	}
}

func countKeysPerNode(r *Ring, n int) map[string]int {
	counts := make(map[string]int)
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("key-%d", i)
		node, _ := r.Lookup(key)
		counts[node.ID]++
	}
	return counts
}

func stddev(counts map[string]int, expected int) float64 {
	var sumSq float64
	for _, c := range counts {
		diff := float64(c - expected)
		sumSq += diff * diff
	}
	meanSq := sumSq / float64(len(counts))
	return sqrt(meanSq)
}

// sqrt is a simple integer-avoiding square root (Newton's method).
func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	z := x
	for i := 0; i < 20; i++ {
		z = (z + x/z) / 2
	}
	return z
}

func TestRingReplicas(t *testing.T) {
	r := New(150)
	r.AddNode(Node{ID: "node-a", Addr: ":8080"})
	r.AddNode(Node{ID: "node-b", Addr: ":8081"})
	r.AddNode(Node{ID: "node-c", Addr: ":8082"})

	// With replication factor 2, each key should have exactly 1 replica.
	replicaCount := make(map[string]int)
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("key-%d", i)
		replicas := r.Replicas(key, 2)
		if len(replicas) != 1 {
			t.Fatalf("key %s: expected 1 replica, got %d", key, len(replicas))
		}
		replicaCount[replicas[0].ID]++
	}

	t.Logf("Replica distribution: %v", replicaCount)

	// Replicas should be distributed across nodes (not all on one node).
	if len(replicaCount) < 2 {
		t.Fatalf("replicas only on %d node(s), expected at least 2", len(replicaCount))
	}

	// The replica for a key should never be the same as the primary.
	for i := 0; i < 100; i++ {
		key := fmt.Sprintf("key-%d", i)
		primary, _ := r.Lookup(key)
		replicas := r.Replicas(key, 2)
		for _, rep := range replicas {
			if rep.ID == primary.ID {
				t.Fatalf("key %s: replica %s is the same as primary %s", key, rep.ID, primary.ID)
			}
		}
	}
}

func TestRingReplicasSingleNode(t *testing.T) {
	r := New(150)
	r.AddNode(Node{ID: "node-a", Addr: ":8080"})

	replicas := r.Replicas("any-key", 2)
	if len(replicas) != 0 {
		t.Fatalf("expected 0 replicas with single node, got %d", len(replicas))
	}
}
