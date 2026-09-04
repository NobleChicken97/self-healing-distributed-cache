// Package rebalance implements safe key migration when nodes join or leave.
//
// Migration protocol (ensures zero downtime):
//  1. Compute which keys changed ownership (old_owner -> new_owner)
//  2. For each moved key:
//     a. new_owner pulls key from old_owner (or any replica)
//     b. new_owner confirms it has the key locally
//     c. old_owner deletes the key only after confirmation
//  4. During step 2, both nodes have the key, so reads always succeed.
package rebalance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"selfhealingcache/internal/ring"
)

// Migration represents a single key being moved from one node to another.
type Migration struct {
	Key       string
	OldOwner  ring.Node
	NewOwner  ring.Node
	Status    MigrationStatus
	Error     string
	Timestamp time.Time
}

// RebalanceRequest is used to trigger a rebalance from outside.
type RebalanceRequest struct {
	LocalNodeID  string
	OldOwnerAddr string
	LocalKeys    []string
}

type MigrationStatus int

const (
	MigrationPending MigrationStatus = iota
	MigrationPulling
	MigrationVerifying
	MigrationComplete
	MigrationFailed
)

func (s MigrationStatus) String() string {
	switch s {
	case MigrationPending:
		return "PENDING"
	case MigrationPulling:
		return "PULLING"
	case MigrationVerifying:
		return "VERIFYING"
	case MigrationComplete:
		return "COMPLETE"
	case MigrationFailed:
		return "FAILED"
	default:
		return "UNKNOWN"
	}
}

// Result summarizes a rebalance operation.
type Result struct {
	TotalKeys   int
	MovedKeys   int
	FailedKeys  int
	Duration    time.Duration
	Migrations  []Migration
	StartedAt   time.Time
	CompletedAt time.Time
}

// Rebalancer orchestrates key migration when the ring topology changes.
type Rebalancer struct {
	ring       *ring.Ring
	transport  http.RoundTripper
	logger     *log.Logger
	mu         sync.Mutex
	inProgress bool
	lastResult *Result
	migrations []Migration
}

// New creates a Rebalancer for the given ring.
func New(r *ring.Ring, logger *log.Logger) *Rebalancer {
	if logger == nil {
		logger = log.Default()
	}
	return &Rebalancer{
		ring:      r,
		transport: http.DefaultTransport,
		logger:    logger,
	}
}

// SetTransport sets the HTTP transport (used for testing).
func (rb *Rebalancer) SetTransport(t http.RoundTripper) {
	rb.transport = t
}

// Rebalance computes which keys need to move and migrates them safely.
// localNodeID is the ID of the node performing the rebalance.
// oldOwnerAddr is the address of this node (where to pull keys from).
// localKeys are the keys currently stored on this node.
func (rb *Rebalancer) Rebalance(localNodeID string, oldOwnerAddr string, localKeys []string) Result {
	rb.mu.Lock()
	if rb.inProgress {
		rb.mu.Unlock()
		return Result{StartedAt: time.Now(), CompletedAt: time.Now()}
	}
	rb.inProgress = true
	rb.mu.Unlock()

	defer func() {
		rb.mu.Lock()
		rb.inProgress = false
		rb.mu.Unlock()
	}()

	startedAt := time.Now()
	result := Result{
		StartedAt: startedAt,
	}

	// Compute which local keys need to move to a new owner.
	var migrations []Migration
	for _, key := range localKeys {
		owner, ok := rb.ring.Lookup(key)
		if !ok {
			continue
		}
		// If the new owner is not this node, the key needs to move.
		if owner.ID != localNodeID {
			migrations = append(migrations, Migration{
				Key:       key,
				NewOwner:  owner,
				Status:    MigrationPending,
				Timestamp: time.Now(),
			})
		}
	}

	result.TotalKeys = len(localKeys)
	result.MovedKeys = 0
	result.FailedKeys = 0

	rb.mu.Lock()
	rb.migrations = migrations
	rb.mu.Unlock()

	// Execute migrations: pull to new owner before dropping locally.
	for i := range migrations {
		m := &migrations[i]

		// Execute the migration via HTTP.
		err := rb.ExecuteMigration(m, oldOwnerAddr)
		if err != nil {
			m.Status = MigrationFailed
			m.Error = err.Error()
			result.FailedKeys++
			rb.logger.Printf("[REBALANCE] failed to migrate key=%s to %s: %v", m.Key, m.NewOwner.ID, err)
			continue
		}

		m.Timestamp = time.Now()
		result.MovedKeys++
		rb.logger.Printf("[REBALANCE] migrated key=%s to %s", m.Key, m.NewOwner.ID)
	}

	result.Migrations = migrations
	result.Duration = time.Since(startedAt)
	result.CompletedAt = time.Now()

	rb.mu.Lock()
	rb.lastResult = &result
	rb.mu.Unlock()

	return result
}

// ExecuteMigration performs the actual key migration via HTTP.
// This is called by the old owner to push the key to the new owner.
// Protocol:
//  1. Old owner GETs the key's value from its local store (via the server's /rebalance/pull endpoint)
//  2. Old owner POSTs the value to the new owner's /rebalance/accept endpoint
//  3. New owner stores the key locally
//  4. Old owner deletes the key locally
func (rb *Rebalancer) ExecuteMigration(m *Migration, oldOwnerAddr string) error {
	m.Status = MigrationPulling

	// Step 1: Get the value from the old owner.
	pullURL := fmt.Sprintf("http://%s/rebalance/pull?key=%s", oldOwnerAddr, url.QueryEscape(m.Key))
	req, err := http.NewRequest(http.MethodPost, pullURL, nil)
	if err != nil {
		return fmt.Errorf("create pull request: %w", err)
	}

	resp, err := rb.transport.RoundTrip(req)
	if err != nil {
		return fmt.Errorf("pull key from old owner: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("old owner returned status=%d body=%s", resp.StatusCode, string(body))
	}

	// Parse the response to get the value and expiry.
	var result struct {
		Key       string `json:"key"`
		Value     string `json:"value"`
		ExpiresAt int64  `json:"expires_at_ms"` // Absolute expiry; 0 = no expiry
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode pull response: %w", err)
	}

	m.Status = MigrationVerifying

	// Step 2: Push the value and expiry to the new owner.
	acceptURL := fmt.Sprintf("http://%s/rebalance/accept?key=%s", m.NewOwner.Addr, url.QueryEscape(m.Key))
	pushBody, _ := json.Marshal(map[string]interface{}{
		"key":           result.Key,
		"value":         result.Value,
		"expires_at_ms": result.ExpiresAt,
	})

	req, err = http.NewRequest(http.MethodPost, acceptURL, bytes.NewReader(pushBody))
	if err != nil {
		return fmt.Errorf("create accept request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err = rb.transport.RoundTrip(req)
	if err != nil {
		return fmt.Errorf("push key to new owner: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("new owner returned status=%d body=%s", resp.StatusCode, string(body))
	}

	// Step 3: Signal completion to delete key from old owner.
	// This ensures the key exists on both nodes during migration (no window where it's absent).
	if err := rb.signalMigrationComplete(m, oldOwnerAddr); err != nil {
		// Log but don't fail - the key will be cleaned up on next rebalance or TTL expiry
		rb.logger.Printf("[REBALANCE] failed to signal completion for key=%s: %v", m.Key, err)
	}

	m.Status = MigrationComplete
	return nil
}

// signalMigrationComplete tells the old owner to delete the migrated key.
func (rb *Rebalancer) signalMigrationComplete(m *Migration, oldOwnerAddr string) error {
	completeURL := fmt.Sprintf("http://%s/rebalance/complete?key=%s", oldOwnerAddr, url.QueryEscape(m.Key))
	req, err := http.NewRequest(http.MethodPost, completeURL, nil)
	if err != nil {
		return fmt.Errorf("create complete request: %w", err)
	}

	resp, err := rb.transport.RoundTrip(req)
	if err != nil {
		return fmt.Errorf("signal complete: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("old owner returned status=%d body=%s", resp.StatusCode, string(body))
	}
	return nil
}

// Migrations returns the current/recent migrations.
func (rb *Rebalancer) Migrations() []Migration {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	result := make([]Migration, len(rb.migrations))
	copy(result, rb.migrations)
	return result
}

// LastResult returns the result of the last rebalance operation.
func (rb *Rebalancer) LastResult() *Result {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.lastResult
}

// IsInProgress returns true if a rebalance is currently running.
func (rb *Rebalancer) IsInProgress() bool {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.inProgress
}

// ComputeKeyMovements returns the keys that need to move given a set of local keys.
// This is useful for planning before executing the rebalance.
func (rb *Rebalancer) ComputeKeyMovements(localNodeID string, localKeys []string) []Migration {
	var migrations []Migration
	for _, key := range localKeys {
		owner, ok := rb.ring.Lookup(key)
		if !ok {
			continue
		}
		if owner.ID != localNodeID {
			migrations = append(migrations, Migration{
				Key:      key,
				NewOwner: owner,
				Status:   MigrationPending,
			})
		}
	}
	return migrations
}
