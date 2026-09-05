// Package cluster provides failure detection using hashicorp/memberlist,
// which implements the SWIM (Scalable Weakly-consistent Infection-style
// Process Group Membership) protocol.
package cluster

import (
	"fmt"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/hashicorp/memberlist"
)

// Event represents a cluster membership event.
type Event struct {
	Timestamp  time.Time
	Node       string
	Type       EventType
	DetectedBy string
}

type EventType int

const (
	NodeJoin EventType = iota
	NodeLeave
	NodeFailed
)

func (e EventType) String() string {
	switch e {
	case NodeJoin:
		return "JOIN"
	case NodeLeave:
		return "LEAVE"
	case NodeFailed:
		return "FAILED"
	default:
		return "UNKNOWN"
	}
}

// Delegate implements memberlist.Delegate to receive local state on join.
type Delegate struct {
	meta []byte
}

func (d *Delegate) NodeMeta(limit int) []byte                  { return d.meta }
func (d *Delegate) NotifyMsg([]byte)                           {}
func (d *Delegate) GetBroadcasts(overhead, limit int) [][]byte { return nil }
func (d *Delegate) LocalState(join bool) []byte                { return nil }
func (d *Delegate) MergeRemoteState(buf []byte, join bool)     {}

// EventDelegate implements memberlist.EventDelegate to receive join/leave events.
type EventDelegate struct {
	mu       sync.Mutex
	events   []Event
	logger   *log.Logger
	onChange func(nodeID string, alive bool)
}

func (d *EventDelegate) NotifyJoin(node *memberlist.Node) {
	d.mu.Lock()
	event := Event{
		Timestamp:  time.Now(),
		Node:       node.Name,
		Type:       NodeJoin,
		DetectedBy: "gossip",
	}
	d.events = append(d.events, event)
	d.mu.Unlock()
	d.logger.Printf("[CLUSTER] %s node=%s detected_by=%s time=%s",
		event.Type, event.Node, event.DetectedBy, event.Timestamp.Format(time.RFC3339Nano))
	if d.onChange != nil {
		d.onChange(node.Name, true)
	}
}

func (d *EventDelegate) NotifyLeave(node *memberlist.Node) {
	d.mu.Lock()
	event := Event{
		Timestamp:  time.Now(),
		Node:       node.Name,
		Type:       NodeFailed,
		DetectedBy: "gossip",
	}
	d.events = append(d.events, event)
	d.mu.Unlock()
	d.logger.Printf("[CLUSTER] %s node=%s detected_by=%s time=%s",
		event.Type, event.Node, event.DetectedBy, event.Timestamp.Format(time.RFC3339Nano))
	if d.onChange != nil {
		d.onChange(node.Name, false)
	}
}

func (d *EventDelegate) NotifyUpdate(node *memberlist.Node) {
	// Node metadata updated — not used for failure detection.
}

// Events returns a copy of all recorded events.
func (d *EventDelegate) Events() []Event {
	d.mu.Lock()
	defer d.mu.Unlock()
	result := make([]Event, len(d.events))
	copy(result, d.events)
	return result
}

// Cluster wraps a memberlist instance and provides failure detection.
type Cluster struct {
	list          *memberlist.Memberlist
	nodeID        string
	eventDelegate *EventDelegate
	logger        *log.Logger
	mu            sync.RWMutex
	aliveNodes    map[string]bool
}

// Config holds cluster configuration.
type Config struct {
	NodeID   string
	BindAddr string
	BindPort int
	// AdvertiseAddr is the host peers should dial to reach this node. It
	// must be reachable from other hosts (e.g. a public IP behind Docker
	// port mapping). Empty means memberlist's default (the bind address),
	// which is wrong whenever BindAddr is 0.0.0.0 or localhost-only while
	// peers are remote.
	AdvertiseAddr string
	SeedPeers     []string
	Logger        *log.Logger
	// OnTopologyChange is called when a node joins or leaves the cluster.
	// Use this to trigger rebalancing or other topology-dependent operations.
	OnTopologyChange func(nodeID string, alive bool)
}

// New creates and joins a cluster.
func New(cfg Config) (*Cluster, error) {
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}

	eventDelegate := &EventDelegate{
		logger: cfg.Logger,
	}

	conf := memberlist.DefaultLANConfig()
	conf.Name = cfg.NodeID
	conf.BindAddr = cfg.BindAddr
	conf.BindPort = cfg.BindPort
	conf.AdvertisePort = cfg.BindPort
	if cfg.AdvertiseAddr != "" {
		conf.AdvertiseAddr = cfg.AdvertiseAddr
	}
	conf.Events = eventDelegate
	conf.Delegate = &Delegate{}
	conf.LogOutput = &logWriter{logger: cfg.Logger, prefix: "[MEMBERLIST] "}

	list, err := memberlist.Create(conf)
	if err != nil {
		return nil, fmt.Errorf("memberlist create: %w", err)
	}

	c := &Cluster{
		list:          list,
		nodeID:        cfg.NodeID,
		eventDelegate: eventDelegate,
		logger:        cfg.Logger,
		aliveNodes:    make(map[string]bool),
	}

	// Initialize alive nodes with self.
	c.aliveNodes[cfg.NodeID] = true

	// Wire events to update alive tracking and notify topology changes.
	eventDelegate.onChange = func(nodeID string, alive bool) {
		c.SetNodeAlive(nodeID, alive)
		c.logger.Printf("[CLUSTER] alive_update node=%s alive=%v", nodeID, alive)
		if cfg.OnTopologyChange != nil {
			cfg.OnTopologyChange(nodeID, alive)
		}
	}

	if len(cfg.SeedPeers) > 0 {
		_, err = list.Join(cfg.SeedPeers)
		if err != nil {
			cfg.Logger.Printf("[CLUSTER] failed to join seeds: %v", err)
		}
	}

	return c, nil
}

// Members returns the current list of alive cluster members.
func (c *Cluster) Members() []string {
	var members []string
	for _, m := range c.list.Members() {
		if m.State == memberlist.StateAlive {
			members = append(members, m.Name)
		}
	}
	return members
}

// NodeCount returns the number of alive members.
func (c *Cluster) NodeCount() int {
	return c.list.NumMembers()
}

// Events returns all recorded cluster events.
func (c *Cluster) Events() []Event {
	return c.eventDelegate.Events()
}

// IsAlive reports whether a node is currently considered alive by the cluster.
func (c *Cluster) IsAlive(nodeID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.aliveNodes[nodeID]
}

// SetNodeAlive updates the liveness status of a node.
func (c *Cluster) SetNodeAlive(nodeID string, alive bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.aliveNodes[nodeID] = alive
}

// AliveCount returns the number of nodes considered alive.
func (c *Cluster) AliveCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	count := 0
	for _, alive := range c.aliveNodes {
		if alive {
			count++
		}
	}
	return count
}

// Shutdown leaves the cluster gracefully.
func (c *Cluster) Shutdown() error {
	if err := c.list.Leave(5 * time.Second); err != nil {
		c.logger.Printf("[CLUSTER] leave failed: %v", err)
	}
	return c.list.Shutdown()
}

// logWriter adapts a log.Logger to io.Writer for memberlist's internal logging.
type logWriter struct {
	logger *log.Logger
	prefix string
}

func (w *logWriter) Write(p []byte) (n int, err error) {
	w.logger.Printf("%s%s", w.prefix, string(p))
	return len(p), nil
}

// ExtractPeerAddr extracts host:port from a memberlist node name or address.
// Used to map cluster members to ring nodes.
func ExtractPeerAddr(node *memberlist.Node, defaultPort int) string {
	host := node.Addr
	port := int(node.Port)
	if port == 0 {
		port = defaultPort
	}
	return net.JoinHostPort(host.String(), strconv.Itoa(port))
}
