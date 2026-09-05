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

	"selfhealingcache/internal/cluster"
	"selfhealingcache/internal/ring"
	"selfhealingcache/internal/server"
	"selfhealingcache/internal/store"
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	advertiseAddr := flag.String("advertise-addr", "", "address advertised in ring (defaults to addr)")
	nodeID := flag.String("id", "", "unique node ID (defaults to addr)")
	peers := flag.String("peers", "", "comma-separated list of peer addresses (e.g. :8081,:8082)")
	clusterPort := flag.Int("cluster-port", 0, "port for cluster gossip (0 = auto)")
	flag.Parse()

	if *nodeID == "" {
		*nodeID = *addr
	}
	if *advertiseAddr == "" {
		*advertiseAddr = *addr
	}

	// Parse host from addr for cluster binding.
	host, _, err := net.SplitHostPort(*addr)
	if err != nil {
		log.Fatalf("invalid addr %q: %v", *addr, err)
	}
	if host == "" {
		host = "127.0.0.1"
	}

	// Derive cluster bind port from HTTP port if not specified.
	clusterBindPort := *clusterPort
	if clusterBindPort == 0 {
		_, portStr, _ := net.SplitHostPort(*addr)
		port, _ := strconv.Atoi(portStr)
		clusterBindPort = port + 1000 // e.g., :8080 -> :9080 for gossip
	}

	cache := store.New(time.Second)
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

	// Start cluster membership (SWIM/gossip) for failure detection.
	c, err := cluster.New(cluster.Config{
		NodeID:    *nodeID,
		BindAddr:  host,
		BindPort:  clusterBindPort,
		SeedPeers: peersToHostPort(*peers),
	})
	if err != nil {
		log.Fatalf("cluster init failed: %v", err)
	}
	defer c.Shutdown()

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           server.New(cache, *nodeID, r).WithCluster(c).Handler(),
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
	}()

	log.Printf("cache node %s listening on %s (cluster port %d, peers: %s)",
		*nodeID, *addr, clusterBindPort, *peers)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// peersToHostPort converts ":port" addresses to "host:port" for memberlist.
func peersToHostPort(peers string) []string {
	if peers == "" {
		return nil
	}
	var result []string
	for _, peer := range strings.Split(peers, ",") {
		peer = strings.TrimSpace(peer)
		if peer == "" {
			continue
		}
		if strings.HasPrefix(peer, ":") {
			result = append(result, "127.0.0.1"+peer)
		} else {
			result = append(result, peer)
		}
	}
	return result
}
