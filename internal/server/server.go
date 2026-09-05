package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"selfhealingcache/internal/cluster"
	"selfhealingcache/internal/rebalance"
	"selfhealingcache/internal/ring"
	"selfhealingcache/internal/store"
)

type Server struct {
	store             *store.Store
	nodeID            string
	listenAddr        string
	ring              *ring.Ring
	transport         http.RoundTripper
	replicationFactor int
	cluster           *cluster.Cluster
	logger            *log.Logger
	rebalancer        *rebalance.Rebalancer
	rebalancerMu      sync.Mutex // protects rebalancer field
}

// New creates a server with ring-based routing and replication.
func New(s *store.Store, nodeID string, r *ring.Ring) *Server {
	return &Server{
		store:             s,
		nodeID:            nodeID,
		ring:              r,
		transport:         http.DefaultTransport,
		replicationFactor: 2,
		logger:            log.Default(),
	}
}

// WithCluster sets the cluster for failure detection and returns the server
// for chaining.
func (s *Server) WithCluster(c *cluster.Cluster) *Server {
	s.cluster = c
	return s
}

// WithLogger sets a custom logger and returns the server for chaining.
func (s *Server) WithLogger(l *log.Logger) *Server {
	s.logger = l
	return s
}

// WithListenAddr sets the listen address for this server (used for rebalance).
func (s *Server) WithListenAddr(addr string) *Server {
	s.listenAddr = addr
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/set", s.handleSet)
	mux.HandleFunc("/get", s.handleGet)
	mux.HandleFunc("/delete", s.handleDelete)
	mux.HandleFunc("/replica/set", s.handleReplicaSet)
	mux.HandleFunc("/replica/delete", s.handleReplicaDelete)
	mux.HandleFunc("/ring/info", s.handleRingInfo)
	mux.HandleFunc("/cluster/info", s.handleClusterInfo)
	mux.HandleFunc("/rebalance", s.handleRebalance)
	mux.HandleFunc("/rebalance/accept", s.handleRebalanceAccept)
	mux.HandleFunc("/rebalance/pull", s.handleRebalancePull)
	mux.HandleFunc("/rebalance/complete", s.handleRebalanceComplete)
	mux.HandleFunc("/rebalance/status", s.handleRebalanceStatus)
	// Quorum consistency endpoints (opt-in stronger consistency)
	mux.HandleFunc("/quorum/set", s.handleQuorumSet)
	mux.HandleFunc("/quorum/get", s.handleQuorumGet)
	mux.HandleFunc("/replica/get", s.handleReplicaGet)
	mux.HandleFunc("/replica/quorum/set", s.handleReplicaQuorumSet)
	return mux
}

// getOrCreateRebalancer returns the existing rebalancer or creates one thread-safely.
func (s *Server) getOrCreateRebalancer() *rebalance.Rebalancer {
	s.rebalancerMu.Lock()
	defer s.rebalancerMu.Unlock()
	if s.rebalancer == nil {
		s.rebalancer = rebalance.New(s.ring, s.logger)
	}
	return s.rebalancer
}

// TriggerRebalance initiates a rebalance operation asynchronously.
// It computes which keys need to move and migrates them safely.
// This should be called when the ring topology changes (node join/leave).
func (s *Server) TriggerRebalance() {
	rb := s.getOrCreateRebalancer()

	// Get local keys that need to be checked for migration.
	localKeys := s.store.Keys()

	go func() {
		defer func() {
			if r := recover(); r != nil {
				s.logger.Printf("[REBALANCE] panic: %v", r)
			}
		}()
		result := rb.Rebalance(s.nodeID, s.listenAddr, localKeys)
		s.logger.Printf("[REBALANCE] completed: total=%d moved=%d failed=%d duration=%v",
			result.TotalKeys, result.MovedKeys, result.FailedKeys, result.Duration)
	}()
}

// handleRebalance triggers a rebalance operation.
func (s *Server) handleRebalance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method must be POST")
		return
	}

	rb := s.getOrCreateRebalancer()

	// Get local keys from the store.
	localKeys := s.store.Keys()

	// Run rebalance in background.
	go func() {
		defer func() {
			if r := recover(); r != nil {
				s.logger.Printf("[REBALANCE] panic: %v", r)
			}
		}()
		result := rb.Rebalance(s.nodeID, s.listenAddr, localKeys)
		s.logger.Printf("[REBALANCE] completed: total=%d moved=%d failed=%d duration=%v",
			result.TotalKeys, result.MovedKeys, result.FailedKeys, result.Duration)
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{
		"status": "rebalance started",
	})
}

// handleRebalanceAccept accepts a key being pushed by another node during rebalance.
// This is the endpoint that the new owner calls to receive the key.
// The request body contains the key's value and absolute expiry timestamp.
func (s *Server) handleRebalanceAccept(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method must be POST")
		return
	}
	key := r.URL.Query().Get("key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "key query parameter is required")
		return
	}

	// Parse the request body to get the value and expiry.
	var req struct {
		Key       string `json:"key"`
		Value     string `json:"value"`
		ExpiresAt int64  `json:"expires_at_ms"` // Absolute expiry; 0 = no expiry
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// Store the key locally with the original absolute expiry for TTL consistency.
	var expiresAt time.Time
	if req.ExpiresAt > 0 {
		expiresAt = time.UnixMilli(req.ExpiresAt)
	}
	s.store.SetWithExpiry(req.Key, req.Value, expiresAt)

	s.logger.Printf("[REBALANCE] accepted key=%s on node=%s (expires=%v)", req.Key, s.nodeID, expiresAt)
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "accepted",
		"key":    req.Key,
	})
}

// handleRebalancePull handles a pull request from a new owner during rebalance.
// The new owner is requesting the key's value so it can store it locally.
// This is the "pull before drop" protocol: the new owner gets the key first,
// then the old owner deletes it.
// Returns the value and absolute expiry timestamp for TTL preservation.
func (s *Server) handleRebalancePull(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method must be POST")
		return
	}
	key := r.URL.Query().Get("key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "key query parameter is required")
		return
	}

	// Get the value locally.
	value, err := s.store.Get(key)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "key not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get failed")
		return
	}

	// Get the absolute expiry timestamp for TTL consistency.
	expiry := s.store.GetExpiry(key)

	// Return the value and expiry to the new owner.
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"key":           key,
		"value":         value,
		"expires_at_ms": expiry.UnixMilli(),
	})
}

// handleRebalanceComplete handles a completion signal from the rebalancer.
// This is called after a key has been successfully migrated to the new owner.
// The old owner can now safely delete the key.
func (s *Server) handleRebalanceComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method must be POST")
		return
	}
	key := r.URL.Query().Get("key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "key query parameter is required")
		return
	}

	s.store.Delete(key)
	s.logger.Printf("[REBALANCE] deleted migrated key=%s", key)
	writeJSON(w, http.StatusOK, map[string]string{
		"status": "deleted",
		"key":    key,
	})
}

// handleRebalanceStatus returns the status of the last rebalance operation.
func (s *Server) handleRebalanceStatus(w http.ResponseWriter, r *http.Request) {
	s.rebalancerMu.Lock()
	rb := s.rebalancer
	s.rebalancerMu.Unlock()

	if rb == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "no rebalance performed yet",
		})
		return
	}

	result := rb.LastResult()
	writeJSON(w, http.StatusOK, map[string]any{
		"in_progress": rb.IsInProgress(),
		"last_result": result,
		"migrations":  rb.Migrations(),
	})
}

// isNodeAlive reports whether a node is alive, using cluster health info if
// available. If no cluster is configured, it assumes all nodes are alive.
func (s *Server) isNodeAlive(nodeID string) bool {
	if s.cluster == nil {
		return true
	}
	return s.cluster.IsAlive(nodeID)
}

// handleClusterInfo returns cluster membership and health status.
func (s *Server) handleClusterInfo(w http.ResponseWriter, r *http.Request) {
	if s.cluster == nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"cluster_enabled": false,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"cluster_enabled": true,
		"node_id":         s.nodeID,
		"alive_count":     s.cluster.AliveCount(),
		"members":         s.cluster.Members(),
	})
}

// ownsLocal checks whether this node is the ring's owner for the given key.
func (s *Server) ownsLocal(key string) bool {
	owner, ok := s.ring.Lookup(key)
	return ok && owner.ID == s.nodeID
}

// forward proxies the request to the node that owns the key.
// If the owner is known dead, it tries replicas instead.
func (s *Server) forward(w http.ResponseWriter, r *http.Request, key string) {
	owner, ok := s.ring.Lookup(key)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "no nodes available")
		return
	}

	// If primary is known dead, route to a replica instead.
	if !s.isNodeAlive(owner.ID) {
		s.forwardToReplica(w, r, key)
		return
	}

	// Read body once so it can be retried if forwarding fails.
	bodyBytes, err := readRequestBody(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read request body")
		return
	}

	targetURL := fmt.Sprintf("http://%s%s?key=%s", owner.Addr, r.URL.Path, url.QueryEscape(key))
	req, err := http.NewRequest(r.Method, targetURL, bytes.NewReader(bodyBytes))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "forwarding failed")
		return
	}
	if len(bodyBytes) > 0 {
		req.Header.Set("Content-Type", r.Header.Get("Content-Type"))
	}

	s.doForward(w, req, owner.Addr)
}

// readRequestBody reads and returns the request body, restoring it for future reads.
func readRequestBody(r *http.Request) ([]byte, error) {
	if r.Body == nil || r.Body == http.NoBody {
		return nil, nil
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	r.Body.Close()
	// Restore the body so it can be read again if needed.
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

// forwardWithBody proxies a request with a reconstructed body (used after the
// original body has already been consumed for JSON decoding).
func (s *Server) forwardWithBody(w http.ResponseWriter, r *http.Request, key string, body []byte) {
	owner, ok := s.ring.Lookup(key)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "no nodes available")
		return
	}

	// If primary is known dead, route to a replica instead.
	if !s.isNodeAlive(owner.ID) {
		s.forwardToReplica(w, r, key)
		return
	}

	targetURL := fmt.Sprintf("http://%s%s?key=%s", owner.Addr, r.URL.Path, url.QueryEscape(key))

	req, err := http.NewRequest(r.Method, targetURL, bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "forwarding failed")
		return
	}
	req.Header.Set("Content-Type", "application/json")

	s.doForward(w, req, owner.Addr)
}

// forwardToReplica routes a write/delete to a replica when the primary is dead.
// The replica will accept the write and become the new primary for that key.
func (s *Server) forwardToReplica(w http.ResponseWriter, r *http.Request, key string) {
	replicas := s.ring.Replicas(key, s.replicationFactor)
	aliveReplicas, deadReplicas := splitByAlive(s, replicas)
	tryOrder := append(aliveReplicas, deadReplicas...)

	// Read body once so it can be retried across multiple replicas.
	bodyBytes, err := readRequestBody(r)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read request body")
		return
	}

	for _, replica := range tryOrder {
		if replica.ID == s.nodeID {
			// This node is the replica; handle locally.
			s.handleReplicaWriteWithBody(w, r, key, bodyBytes)
			return
		}
		targetURL := fmt.Sprintf("http://%s%s?key=%s", replica.Addr, r.URL.Path, url.QueryEscape(key))
		req, err := http.NewRequest(r.Method, targetURL, bytes.NewReader(bodyBytes))
		if err != nil {
			continue
		}
		if len(bodyBytes) > 0 {
			req.Header.Set("Content-Type", r.Header.Get("Content-Type"))
		}
		resp, err := s.transport.RoundTrip(req)
		if err != nil {
			continue
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(respBody)
		return
	}

	writeError(w, http.StatusBadGateway, "primary and all replicas unavailable")
}

// handleReplicaWriteWithBody handles a write operation when this node is acting as
// a replica for a dead primary. It parses the body and writes locally.
func (s *Server) handleReplicaWriteWithBody(w http.ResponseWriter, r *http.Request, key string, bodyBytes []byte) {
	switch r.URL.Path {
	case "/set":
		// Parse the set request from the body.
		var req setRequest
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		ttl := time.Duration(req.TTLMS) * time.Millisecond
		s.store.Set(req.Key, req.Value, ttl)
		s.logger.Printf("[FAILOVER] accepted SET for key=%s on replica node=%s", req.Key, s.nodeID)
	case "/delete":
		s.store.Delete(key)
		s.logger.Printf("[FAILOVER] accepted DELETE for key=%s on replica node=%s", key, s.nodeID)
	default:
		writeError(w, http.StatusBadRequest, "unsupported operation")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "note": "accepted_by_replica"})
}

// doForward executes the forwarded request and copies the response back.
func (s *Server) doForward(w http.ResponseWriter, req *http.Request, addr string) {
	resp, err := s.transport.RoundTrip(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "node unavailable: "+addr)
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
}

type setRequest struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	TTLMS int64  `json:"ttl_ms"`
}

func (s *Server) handleSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method must be POST")
		return
	}
	var req setRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Key == "" {
		writeError(w, http.StatusBadRequest, "key and valid JSON body are required")
		return
	}
	if req.TTLMS < 0 {
		writeError(w, http.StatusBadRequest, "ttl_ms must not be negative")
		return
	}

	if !s.ownsLocal(req.Key) {
		body, _ := json.Marshal(req)
		s.forwardWithBody(w, r, req.Key, body)
		return
	}

	// This node is the primary: write locally, then replicate asynchronously.
	ttl := time.Duration(req.TTLMS) * time.Millisecond
	s.store.Set(req.Key, req.Value, ttl)

	// Compute absolute expiry timestamp for consistent replication.
	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}
	go s.replicateSet(req.Key, req.Value, expiresAt)

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// replicateSet propagates a write to all replica nodes asynchronously.
// It sends the absolute expiry timestamp so all replicas expire simultaneously.
func (s *Server) replicateSet(key, value string, expiresAt time.Time) {
	replicas := s.ring.Replicas(key, s.replicationFactor)
	for _, replica := range replicas {
		if replica.ID == s.nodeID {
			continue
		}
		// Send absolute expiry timestamp for consistency.
		body, _ := json.Marshal(replicateRequest{
			Key:       key,
			Value:     value,
			ExpiresAt: expiresAt.UnixMilli(),
		})
		url := fmt.Sprintf("http://%s/replica/set?key=%s", replica.Addr, url.QueryEscape(key))
		req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := s.transport.RoundTrip(req)
		if err != nil {
			continue
		}
		resp.Body.Close()
	}
}

// replicateRequest is the payload sent to replicas during replication.
// It includes the absolute expiry timestamp for TTL consistency.
type replicateRequest struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	ExpiresAt int64  `json:"expires_at_ms"` // Absolute expiry in milliseconds since epoch; 0 = no expiry
}

// handleReplicaSet receives a replicated write from the primary node.
// It preserves the absolute expiry timestamp from the primary.
func (s *Server) handleReplicaSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method must be POST")
		return
	}
	var req replicateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Key == "" {
		writeError(w, http.StatusBadRequest, "key and valid JSON body are required")
		return
	}

	// Use the absolute expiry timestamp from the primary for consistency.
	var expiresAt time.Time
	if req.ExpiresAt > 0 {
		expiresAt = time.UnixMilli(req.ExpiresAt)
	}
	s.store.SetWithExpiry(req.Key, req.Value, expiresAt)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method must be GET")
		return
	}
	key := r.URL.Query().Get("key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "key query parameter is required")
		return
	}

	if !s.ownsLocal(key) {
		s.forwardWithFallback(w, r, key)
		return
	}

	value, err := s.store.Get(key)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "key not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"key": key, "value": value})
}

// forwardWithFallback tries the primary node first; if unreachable, falls back
// to replica nodes. Uses cluster health info to skip known-dead primaries
// immediately instead of waiting for a timeout.
func (s *Server) forwardWithFallback(w http.ResponseWriter, r *http.Request, key string) {
	primary, ok := s.ring.Lookup(key)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "no nodes available")
		return
	}

	// Try primary first — but only if it's known to be alive.
	if s.isNodeAlive(primary.ID) {
		targetURL := fmt.Sprintf("http://%s/get?key=%s", primary.Addr, url.QueryEscape(key))
		req, err := http.NewRequest(http.MethodGet, targetURL, nil)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "request failed")
			return
		}

		resp, err := s.transport.RoundTrip(req)
		if err == nil {
			defer resp.Body.Close()
			respBody, _ := io.ReadAll(resp.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(resp.StatusCode)
			w.Write(respBody)
			return
		}
		s.logger.Printf("[FAILOVER] primary %s unreachable for key=%s, falling back to replicas", primary.ID, key)
	} else if s.cluster != nil {
		s.logger.Printf("[FAILOVER] primary %s known dead for key=%s, skipping to replicas", primary.ID, key)
	}

	// Primary unreachable or known dead — try replicas.
	replicas := s.ring.Replicas(key, s.replicationFactor)
	// Prefer alive replicas: sort so we try alive ones first.
	aliveReplicas, deadReplicas := splitByAlive(s, replicas)
	tryOrder := append(aliveReplicas, deadReplicas...)

	for _, replica := range tryOrder {
		if replica.ID == s.nodeID {
			// This node is the replica; read locally.
			value, err := s.store.Get(key)
			if err == nil {
				writeJSON(w, http.StatusOK, map[string]string{"key": key, "value": value})
				return
			}
			continue
		}
		targetURL := fmt.Sprintf("http://%s/get?key=%s", replica.Addr, url.QueryEscape(key))
		req, err := http.NewRequest(http.MethodGet, targetURL, nil)
		if err != nil {
			continue
		}
		resp, err := s.transport.RoundTrip(req)
		if err != nil {
			continue
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		w.Write(respBody)
		return
	}

	writeError(w, http.StatusBadGateway, "primary and all replicas unavailable")
}

// splitByAlive partitions nodes into alive and dead lists based on cluster health.
func splitByAlive(s *Server, nodes []ring.Node) (alive, dead []ring.Node) {
	for _, n := range nodes {
		if s.isNodeAlive(n.ID) {
			alive = append(alive, n)
		} else {
			dead = append(dead, n)
		}
	}
	return
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method must be DELETE")
		return
	}
	key := r.URL.Query().Get("key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "key query parameter is required")
		return
	}

	if !s.ownsLocal(key) {
		s.forward(w, r, key)
		return
	}

	s.store.Delete(key)
	go s.replicateDelete(key)

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// replicateDelete propagates a delete to all replica nodes asynchronously.
func (s *Server) replicateDelete(key string) {
	replicas := s.ring.Replicas(key, s.replicationFactor)
	for _, replica := range replicas {
		if replica.ID == s.nodeID {
			continue
		}
		url := fmt.Sprintf("http://%s/replica/delete?key=%s", replica.Addr, url.QueryEscape(key))
		req, err := http.NewRequest(http.MethodDelete, url, nil)
		if err != nil {
			continue
		}
		resp, err := s.transport.RoundTrip(req)
		if err != nil {
			continue
		}
		resp.Body.Close()
	}
}

// handleReplicaDelete receives a replicated delete from the primary node.
func (s *Server) handleReplicaDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method must be DELETE")
		return
	}
	key := r.URL.Query().Get("key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "key query parameter is required")
		return
	}
	s.store.Delete(key)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReplicaQuorumSet handles a quorum write from the primary node.
// Stores the key with the specified version and returns acknowledgment.
func (s *Server) handleReplicaQuorumSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method must be POST")
		return
	}
	key := r.URL.Query().Get("key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "key query parameter is required")
		return
	}

	var req struct {
		Key       string `json:"key"`
		Value     string `json:"value"`
		ExpiresAt int64  `json:"expires_at_ms"`
		Version   int64  `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	var expiresAt time.Time
	if req.ExpiresAt > 0 {
		expiresAt = time.UnixMilli(req.ExpiresAt)
	}

	// Store with version for consistency.
	s.store.SetVersion(req.Key, req.Value, expiresAt, req.Version)

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"node_id": s.nodeID,
	})
}

func (s *Server) handleRingInfo(w http.ResponseWriter, r *http.Request) {
	nodes := s.ring.Nodes()
	writeJSON(w, http.StatusOK, map[string]any{
		"node_id":    s.nodeID,
		"ring_nodes": nodes,
	})
}

// quorumSetRequest is the payload for quorum writes.
type quorumSetRequest struct {
	Key     string `json:"key"`
	Value   string `json:"value"`
	TTLMS   int64  `json:"ttl_ms"`
	Version int64  `json:"version"` // Optional: for conditional writes
}

// quorumGetResponse is the response from a quorum read.
type quorumGetResponse struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	Version   int64  `json:"version"`
	ExpiresAt int64  `json:"expires_at_ms"`
	FromNode  string `json:"from_node"`
}

// handleQuorumSet performs a quorum write: writes to primary and waits for
// acknowledgment from a majority of replicas before returning success.
// This provides stronger consistency at the cost of higher write latency.
func (s *Server) handleQuorumSet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method must be POST")
		return
	}

	var req quorumSetRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Key == "" {
		writeError(w, http.StatusBadRequest, "key and valid JSON body are required")
		return
	}

	if !s.ownsLocal(req.Key) {
		// Forward to the primary node.
		bodyBytes, _ := readRequestBody(r)
		s.forwardQuorumSet(w, req.Key, bodyBytes)
		return
	}

	// This node is the primary.
	ttl := time.Duration(req.TTLMS) * time.Millisecond
	expiresAt := time.Now().Add(ttl)
	if req.TTLMS <= 0 {
		expiresAt = time.Time{} // No expiry
	}

	// Get the next version.
	_, currentVersion, _, _ := s.store.GetWithVersion(req.Key)
	version := currentVersion + 1

	// Write locally first.
	s.store.SetVersion(req.Key, req.Value, expiresAt, version)

	// Replicate to replicas and wait for quorum.
	// Quorum = majority of all nodes (primary + replicas)
	replicas := s.ring.Replicas(req.Key, s.replicationFactor)
	totalNodes := len(replicas) + 1      // +1 for primary
	quorumNeeded := (totalNodes / 2) + 1 // Majority

	replicaAcks := 0
	var lastErr error

	for _, replica := range replicas {
		if replica.ID == s.nodeID {
			continue
		}
		err := s.replicateQuorumSet(replica, req.Key, req.Value, expiresAt, version)
		if err != nil {
			lastErr = err
			continue
		}
		replicaAcks++
	}

	// Total acks = replica acks + 1 (primary)
	totalAcks := replicaAcks + 1

	if totalAcks < quorumNeeded {
		s.logger.Printf("[QUORUM] write failed: got %d acks (%d replicas + primary), needed %d: %v",
			totalAcks, replicaAcks, quorumNeeded, lastErr)
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status":       "quorum_failed",
			"acks":         totalAcks,
			"replica_acks": replicaAcks,
			"needed":       quorumNeeded,
			"error":        lastErr,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":       "ok",
		"acks":         totalAcks,
		"replica_acks": replicaAcks,
		"version":      version,
	})
}

// replicateQuorumSet sends a write to a replica and waits for acknowledgment.
func (s *Server) replicateQuorumSet(replica ring.Node, key, value string, expiresAt time.Time, version int64) error {
	body, _ := json.Marshal(map[string]interface{}{
		"key":           key,
		"value":         value,
		"expires_at_ms": expiresAt.UnixMilli(),
		"version":       version,
	})
	url := fmt.Sprintf("http://%s/replica/quorum/set?key=%s", replica.Addr, url.QueryEscape(key))
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.transport.RoundTrip(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("replica %s returned %d: %s", replica.ID, resp.StatusCode, string(body))
	}
	return nil
}

// handleQuorumGet performs a quorum read: queries a majority of replicas and
// returns the most recent value (highest version). This provides read-after-write
// consistency at the cost of higher read latency.
func (s *Server) handleQuorumGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method must be GET")
		return
	}
	key := r.URL.Query().Get("key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "key query parameter is required")
		return
	}

	// Collect responses from this node and all replicas.
	type nodeResponse struct {
		value     string
		version   int64
		expiresAt time.Time
		nodeID    string
	}

	var responses []nodeResponse

	// Query local store.
	if value, version, expiresAt, err := s.store.GetWithVersion(key); err == nil {
		responses = append(responses, nodeResponse{value, version, expiresAt, s.nodeID})
	}

	// Query replicas.
	replicas := s.ring.Replicas(key, s.replicationFactor)
	for _, replica := range replicas {
		if replica.ID == s.nodeID {
			continue
		}
		resp, err := http.Get(fmt.Sprintf("http://%s/replica/get?key=%s", replica.Addr, url.QueryEscape(key)))
		if err != nil {
			continue
		}
		var result quorumGetResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err == nil && result.Value != "" {
			expiresAt := time.Time{}
			if result.ExpiresAt > 0 {
				expiresAt = time.UnixMilli(result.ExpiresAt)
			}
			responses = append(responses, nodeResponse{result.Value, result.Version, expiresAt, result.FromNode})
		}
		resp.Body.Close()
	}

	if len(responses) == 0 {
		writeError(w, http.StatusNotFound, "key not found")
		return
	}

	// Find the response with the highest version (most recent write).
	var best *nodeResponse
	for i := range responses {
		if best == nil || responses[i].version > best.version {
			best = &responses[i]
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"key":     key,
		"value":   best.value,
		"version": best.version,
		"from":    best.nodeID,
	})
}

// handleReplicaGet serves a key with version info for quorum reads.
func (s *Server) handleReplicaGet(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "key query parameter is required")
		return
	}

	value, version, expiresAt, err := s.store.GetWithVersion(key)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "key not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "get failed")
		return
	}

	writeJSON(w, http.StatusOK, quorumGetResponse{
		Key:       key,
		Value:     value,
		Version:   version,
		ExpiresAt: expiresAt.UnixMilli(),
		FromNode:  s.nodeID,
	})
}

// forwardQuorumSet forwards a quorum set request to the primary node.
func (s *Server) forwardQuorumSet(w http.ResponseWriter, key string, bodyBytes []byte) {
	owner, ok := s.ring.Lookup(key)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "no nodes available")
		return
	}

	targetURL := fmt.Sprintf("http://%s/quorum/set?key=%s", owner.Addr, url.QueryEscape(key))
	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(bodyBytes))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "forwarding failed")
		return
	}
	req.Header.Set("Content-Type", "application/json")

	s.doForward(w, req, owner.Addr)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
