package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"shdc/internal/cluster"
	"shdc/internal/ring"
	"shdc/internal/server"
	"shdc/internal/store"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	advertiseAddr := flag.String("advertise-addr", "", "address advertised in ring (defaults to addr)")
	nodeID := flag.String("id", "", "unique node ID (defaults to addr)")
	peers := flag.String("peers", "", "comma-separated HTTP peer addresses for ring/proxying (e.g. 10.0.0.2:8080); gossip seeds are derived as host:<cluster-port>")
	gossipAdv := flag.String("gossip-advertise-addr", "", "host IP peers dial for gossip (e.g. the node's public IP behind Docker port mapping); empty = bind address (local dev only)")
	clusterPort := flag.Int("cluster-port", 0, "port for cluster gossip (0 = auto)")
	memCap := flag.Int64("mem-cap", 0, "per-node memory cap in bytes for LRU eviction (0 = unlimited)")
	flag.Parse()

	if *nodeID == "" {
		*nodeID = *addr
	}
	if *advertiseAddr == "" {
		*advertiseAddr = *addr
	}

	// Parse host from addr for cluster binding. An empty host (":8080") must
	// bind all interfaces: binding localhost would make gossip unreachable
	// from other hosts (connection refused through Docker port mapping).
	host := gossipBindAddr(*addr)

	// Derive cluster bind port from HTTP port if not specified.
	// Explicit uniform ports (e.g. -cluster-port 7946 on every node, as in
	// Docker/Lightsail deploys) vs default per-node ports (HTTP+1000, as in
	// local multi-process dev clusters) also decide how gossip seeds below
	// are derived, since peers only carry HTTP addresses.
	clusterBindPort := *clusterPort
	useDefaultPorts := false
	if clusterBindPort == 0 {
		useDefaultPorts = true
		_, portStr, _ := net.SplitHostPort(*addr)
		port, _ := strconv.Atoi(portStr)
		clusterBindPort = port + 1000 // e.g., :8080 -> :9080 for gossip
	}

	cache := store.New(time.Second)
	if *memCap > 0 {
		cache = store.NewWithEviction(time.Second, *memCap)
		log.Printf("LRU eviction enabled with memory cap %d bytes", *memCap)
	}
	defer cache.Close()

	r := ring.New(150)
	r.AddNode(ring.Node{ID: *nodeID, Addr: *advertiseAddr})
	if *peers != "" {
		for _, peer := range strings.Split(*peers, ",") {
			peer = strings.TrimSpace(peer)
			if peer != "" {
				r.AddNode(ring.Node{ID: peer, Addr: peer})
			}
		}
	}

	// Create the server first so we can wire cluster events to rebalancing.
	srv := server.New(cache, *nodeID, r).WithListenAddr(*advertiseAddr)

	// Start cluster membership (SWIM/gossip) for failure detection.
	// Wire topology changes to trigger automatic rebalancing.
	c, err := cluster.New(cluster.Config{
		NodeID:        *nodeID,
		BindAddr:      host,
		BindPort:      clusterBindPort,
		AdvertiseAddr: *gossipAdv,
		SeedPeers:     peersToGossipPeers(*peers, clusterBindPort, useDefaultPorts),
		OnTopologyChange: func(nodeID string, alive bool) {
			if !alive {
				log.Printf("[CLUSTER] node %s left/failed, triggering rebalance", nodeID)
				srv.TriggerRebalance()
			}
		},
	})
	if err != nil {
		log.Fatalf("cluster init failed: %v", err)
	}
	defer c.Shutdown()

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           srv.WithCluster(c).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP shutdown failed: %v", err)
		}
		// Wait for in-flight replications to complete.
		srv.ShutdownReplication()
	}()

	log.Printf("cache node %s listening on %s (cluster port %d, peers: %s)",
		*nodeID, *addr, clusterBindPort, *peers)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// gossipBindAddr extracts the host for the SWIM/gossip listener from the
// HTTP listen address. Empty host (":8080") or unparseable addr binds all
// interfaces so peers on other hosts can reach it; otherwise the addr's host
// is used (e.g. "127.0.0.1:8081" stays local for multi-process dev clusters).
func gossipBindAddr(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return "0.0.0.0"
	}
	return host
}

// peersToGossipPeers derives memberlist seed addresses from the HTTP peer
// list. -peers carries HTTP addresses (host:HTTPPort) used for the ring and
// request proxying, but gossip must dial a gossip port, which peers don't
// advertise. Two conventions cover every documented setup:
//   - explicit uniform gossip port (e.g. -cluster-port 7946 on every node, as
//     in Docker/Lightsail deploys): seeds are host(peer):gossipPort.
//   - default per-node ports (gossip = HTTP+1000, as in local multi-process
//     dev clusters): seeds are host(peer):peerHTTPPort+1000.
//
// useDefaultPorts selects the second convention. ":port" shorthand means
// localhost; peers without a usable port fall back to gossipPort.
func peersToGossipPeers(peers string, gossipPort int, useDefaultPorts bool) []string {
	if peers == "" {
		return nil
	}
	var result []string
	for _, peer := range strings.Split(peers, ",") {
		peer = strings.TrimSpace(peer)
		if peer == "" {
			continue
		}
		host, portStr, err := net.SplitHostPort(peer)
		if err != nil || host == "" {
			// ":port" form or bare hostname — treat as localhost or as-is.
			if strings.HasPrefix(peer, ":") {
				host = "127.0.0.1"
			} else {
				host = peer
			}
		}
		seedPort := gossipPort
		if useDefaultPorts {
			if p, perr := strconv.Atoi(portStr); perr == nil && p > 0 && p < 65535-1000 {
				seedPort = p + 1000
			}
		}
		result = append(result, net.JoinHostPort(host, strconv.Itoa(seedPort)))
	}
	return result
}
