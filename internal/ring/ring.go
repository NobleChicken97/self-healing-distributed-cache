// Package ring implements a consistent hashing ring for key distribution.
//
// Why modulo hashing reshuffles most keys when N changes:
// With modulo hashing (key_hash % N), adding or removing a node changes the
// divisor N. Since most key hashes are not evenly distributed across all possible
// remainders, roughly (N-1)/N of all keys get reassigned to a different node.
// For example, going from 3 to 4 nodes moves ~75% of keys. Consistent hashing
// avoids this by mapping keys to a fixed ring of hash values; adding or removing
// a node only affects the keys that were owned by that node's arc on the ring,
// which is approximately 1/N of all keys.
package ring

import (
	"crypto/sha256"
	"encoding/binary"
	"sort"
	"strconv"
	"sync"
)

// Node identifies a cache node by its HTTP address.
type Node struct {
	ID   string
	Addr string
}

// Ring maps keys to nodes using consistent hashing with virtual nodes.
type Ring struct {
	mu           sync.RWMutex
	nodes        map[string]Node   // nodeID -> Node
	points       []uint32          // sorted hash values
	pointToNode  map[uint32]string // hash -> nodeID
	virtualNodes int               // virtual nodes per physical node
}

// New creates a ring with the specified number of virtual nodes per physical node.
func New(virtualNodes int) *Ring {
	if virtualNodes <= 0 {
		virtualNodes = 150
	}
	return &Ring{
		nodes:        make(map[string]Node),
		pointToNode:  make(map[uint32]string),
		virtualNodes: virtualNodes,
	}
}

// hash computes a uint32 hash for a key using SHA-256 (first 4 bytes).
func hash(key string) uint32 {
	sum := sha256.Sum256([]byte(key))
	return binary.BigEndian.Uint32(sum[:4])
}

// nodePointKey generates a unique key for a virtual node point.
func nodePointKey(nodeID string, replica int) string {
	return nodeID + "#" + strconv.Itoa(replica)
}

// AddNode registers a node and its virtual points on the ring.
func (r *Ring) AddNode(node Node) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.nodes[node.ID]; exists {
		return
	}
	r.nodes[node.ID] = node

	for i := 0; i < r.virtualNodes; i++ {
		h := hash(nodePointKey(node.ID, i))
		r.pointToNode[h] = node.ID
	}
	r.rebuildPoints()
}

// RemoveNode removes a node and its virtual points from the ring.
func (r *Ring) RemoveNode(nodeID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.nodes[nodeID]; !exists {
		return
	}
	delete(r.nodes, nodeID)

	for i := 0; i < r.virtualNodes; i++ {
		h := hash(nodePointKey(nodeID, i))
		delete(r.pointToNode, h)
	}
	r.rebuildPoints()
}

// rebuildPoints sorts the hash points after add/remove.
func (r *Ring) rebuildPoints() {
	points := make([]uint32, 0, len(r.pointToNode))
	for h := range r.pointToNode {
		points = append(points, h)
	}
	sort.Slice(points, func(i, j int) bool { return points[i] < points[j] })
	r.points = points
}

// Lookup returns the node responsible for the given key.
func (r *Ring) Lookup(key string) (Node, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.points) == 0 {
		return Node{}, false
	}

	h := hash(key)
	// Binary search for first point >= h (clockwise on the ring).
	idx := sort.Search(len(r.points), func(i int) bool {
		return r.points[i] >= h
	})
	// Wrap around to the first point if we passed the end.
	if idx == len(r.points) {
		idx = 0
	}
	nodeID := r.pointToNode[r.points[idx]]
	return r.nodes[nodeID], true
}

// Replicas returns the next distinct physical node clockwise from the primary,
// used as the replica for the given key. Returns false if no replica exists
// (i.e., there's only one physical node).
func (r *Ring) Replicas(key string, replicationFactor int) []Node {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.nodes) <= 1 || replicationFactor <= 1 {
		return nil
	}

	h := hash(key)
	idx := sort.Search(len(r.points), func(i int) bool {
		return r.points[i] >= h
	})
	if idx == len(r.points) {
		idx = 0
	}

	// Walk clockwise from the primary's point, collecting distinct physical
	// nodes until we have replicationFactor-1 replicas or we've gone all the way
	// around the ring.
	seen := make(map[string]bool)
	var replicas []Node
	primaryID := r.pointToNode[r.points[idx]]
	seen[primaryID] = true

	for i := 1; i < len(r.points); i++ {
		wrappedIdx := (idx + i) % len(r.points)
		nodeID := r.pointToNode[r.points[wrappedIdx]]
		if seen[nodeID] {
			continue
		}
		seen[nodeID] = true
		replicas = append(replicas, r.nodes[nodeID])
		if len(replicas) >= replicationFactor-1 {
			break
		}
	}
	return replicas
}

// Nodes returns all registered physical nodes.
func (r *Ring) Nodes() []Node {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Node, 0, len(r.nodes))
	for _, n := range r.nodes {
		result = append(result, n)
	}
	return result
}
