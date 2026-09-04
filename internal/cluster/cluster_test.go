package cluster

import (
	"testing"
	"time"
)

// TestClusterJoinLeave verifies that nodes can join a cluster and that
// leaving a node generates a failure event.
func TestClusterJoinLeave(t *testing.T) {
	// Create the first node (seed).
	c1, err := New(Config{
		NodeID:   "node-1",
		BindAddr: "127.0.0.1",
		BindPort: 7946,
	})
	if err != nil {
		t.Fatalf("failed to create node-1: %v", err)
	}
	defer c1.Shutdown()

	// Create the second node, joining via the first.
	c2, err := New(Config{
		NodeID:    "node-2",
		BindAddr:  "127.0.0.1",
		BindPort:  7947,
		SeedPeers: []string{"127.0.0.1:7946"},
	})
	if err != nil {
		t.Fatalf("failed to create node-2: %v", err)
	}
	defer c2.Shutdown()

	// Create the third node.
	c3, err := New(Config{
		NodeID:    "node-3",
		BindAddr:  "127.0.0.1",
		BindPort:  7948,
		SeedPeers: []string{"127.0.0.1:7946"},
	})
	if err != nil {
		t.Fatalf("failed to create node-3: %v", err)
	}
	// Note: we don't defer c3.Shutdown() because we'll kill it manually.

	// Wait for cluster to stabilize.
	time.Sleep(2 * time.Second)

	// Verify all 3 nodes are alive.
	if c1.AliveCount() < 2 {
		t.Fatalf("node-1 sees %d alive nodes, expected at least 2", c1.AliveCount())
	}
	if c2.AliveCount() < 2 {
		t.Fatalf("node-2 sees %d alive nodes, expected at least 2", c2.AliveCount())
	}
	if c3.AliveCount() < 2 {
		t.Fatalf("node-3 sees %d alive nodes, expected at least 2", c3.AliveCount())
	}

	t.Logf("cluster stable: node-1=%d alive, node-2=%d alive, node-3=%d alive",
		c1.AliveCount(), c2.AliveCount(), c3.AliveCount())

	// "Kill" node-3 by shutting it down abruptly.
	c3.Shutdown()

	// Wait for failure detection (SWIM should detect within a few seconds).
	deadline := time.Now().Add(10 * time.Second)
	var detected bool
	for time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		events := c1.Events()
		for _, e := range events {
			if e.Node == "node-3" && e.Type == NodeFailed {
				detected = true
				t.Logf("node-1 detected node-3 failure at %s (detected_by=%s)",
					e.Timestamp.Format(time.RFC3339Nano), e.DetectedBy)
				break
			}
		}
		if detected {
			break
		}
	}

	if !detected {
		t.Fatal("node-1 did not detect node-3 failure within 10 seconds")
	}

	// Verify node-3 is marked as not alive.
	if c1.IsAlive("node-3") {
		t.Fatal("node-3 should not be alive after shutdown")
	}
	if c2.IsAlive("node-3") {
		t.Fatal("node-3 should not be alive after shutdown (from node-2's perspective)")
	}
}

// TestClusterSingleNode verifies that a single-node cluster initializes correctly.
func TestClusterSingleNode(t *testing.T) {
	c, err := New(Config{
		NodeID:   "solo",
		BindAddr: "127.0.0.1",
		BindPort: 7949,
	})
	if err != nil {
		t.Fatalf("failed to create single node: %v", err)
	}
	defer c.Shutdown()

	if c.AliveCount() != 1 {
		t.Fatalf("expected 1 alive node, got %d", c.AliveCount())
	}
	if !c.IsAlive("solo") {
		t.Fatal("solo node should be alive")
	}
}
