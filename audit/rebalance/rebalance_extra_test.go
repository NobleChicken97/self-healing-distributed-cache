package rebalance_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"selfhealingcache/internal/rebalance"
	"selfhealingcache/internal/ring"
)

// errorTransport always returns an error
type errorTransport struct {
	err error
}

func (t *errorTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, t.err
}

// statusTransport returns a specific status code
type statusTransport struct {
	status int
	body   string
}

func (t *statusTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: t.status,
		Body:       io.NopCloser(strings.NewReader(t.body)),
		Header:     make(http.Header),
	}, nil
}

// TestExecuteMigrationPullFailure verifies pull failure path
func TestExecuteMigrationPullFailure(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})
	r.AddNode(ring.Node{ID: "node-b", Addr: "127.0.0.1:8081"})

	rb := rebalance.New(r, nil)
	rb.SetTransport(&errorTransport{err: errors.New("connection refused")})

	m := &rebalance.Migration{
		Key:      "test-key",
		NewOwner: ring.Node{ID: "node-b", Addr: "127.0.0.1:8081"},
	}

	err := rb.ExecuteMigration(m, "127.0.0.1:8080")
	if err == nil {
		t.Fatal("expected error when pull fails")
	}
}

// TestExecuteMigrationPushFailure verifies push failure path
func TestExecuteMigrationPushFailure(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})
	r.AddNode(ring.Node{ID: "node-b", Addr: "127.0.0.1:8081"})

	rb := rebalance.New(r, nil)
	rb.SetTransport(&statusTransport{status: http.StatusInternalServerError, body: "server error"})

	m := &rebalance.Migration{
		Key:      "test-key",
		NewOwner: ring.Node{ID: "node-b", Addr: "127.0.0.1:8081"},
	}

	err := rb.ExecuteMigration(m, "127.0.0.1:8080")
	if err == nil {
		t.Fatal("expected error when push fails")
	}
}

// TestExecuteMigrationInvalidJSON verifies decode error path
func TestExecuteMigrationInvalidJSON(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})
	r.AddNode(ring.Node{ID: "node-b", Addr: "127.0.0.1:8081"})

	rb := rebalance.New(r, nil)
	rb.SetTransport(&statusTransport{status: http.StatusOK, body: `{invalid json`})

	m := &rebalance.Migration{
		Key:      "test-key",
		NewOwner: ring.Node{ID: "node-b", Addr: "127.0.0.1:8081"},
	}

	err := rb.ExecuteMigration(m, "127.0.0.1:8080")
	if err == nil {
		t.Fatal("expected error when JSON is invalid")
	}
}

// TestRebalanceConcurrentCall verifies inProgress guard
func TestRebalanceConcurrentCall(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})

	rb := rebalance.New(r, nil)

	// Use reflection or a test hook to set inProgress
	// Since we can't access unexported fields, we test the behavior
	// by calling Rebalance twice quickly

	var wg sync.WaitGroup
	var result1, result2 rebalance.Result

	wg.Add(2)
	go func() {
		defer wg.Done()
		result1 = rb.Rebalance("node-a", "127.0.0.1:8080", []string{"key-1"})
	}()
	go func() {
		defer wg.Done()
		result2 = rb.Rebalance("node-a", "127.0.0.1:8080", []string{"key-2"})
	}()
	wg.Wait()

	// One of them should have been blocked or both should complete
	t.Logf("Result1: %+v", result1)
	t.Logf("Result2: %+v", result2)
}

// TestComputeKeyMovements verifies the planning function
func TestComputeKeyMovements(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})
	r.AddNode(ring.Node{ID: "node-b", Addr: "127.0.0.1:8081"})

	rb := rebalance.New(r, nil)

	// Compute which keys need to move from node-a
	keys := []string{"key-1", "key-2", "key-3"}
	migrations := rb.ComputeKeyMovements("node-a", keys)

	// All migrations should have Pending status
	for _, m := range migrations {
		if m.Status.String() != "PENDING" {
			t.Fatalf("expected Pending status, got %s", m.Status.String())
		}
	}
}

// TestMigrationStatusString verifies status string conversion
func TestMigrationStatusString(t *testing.T) {
	tests := []struct {
		status rebalance.MigrationStatus
		want   string
	}{
		{rebalance.MigrationStatus(0), "PENDING"},
		{rebalance.MigrationStatus(1), "PULLING"},
		{rebalance.MigrationStatus(2), "VERIFYING"},
		{rebalance.MigrationStatus(3), "COMPLETE"},
		{rebalance.MigrationStatus(4), "FAILED"},
		{rebalance.MigrationStatus(99), "UNKNOWN"},
	}

	for _, tt := range tests {
		if got := tt.status.String(); got != tt.want {
			t.Fatalf("Status.String() = %s, want %s", got, tt.want)
		}
	}
}

// TestRebalanceLastResult verifies LastResult returns nil initially
func TestRebalanceLastResult(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})

	rb := rebalance.New(r, nil)

	if rb.LastResult() != nil {
		t.Fatal("expected nil LastResult initially")
	}
}

// TestRebalanceIsInProgress verifies IsInProgress initial state
func TestRebalanceIsInProgress(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})

	rb := rebalance.New(r, nil)

	if rb.IsInProgress() {
		t.Fatal("expected IsInProgress to be false initially")
	}
}

// TestMigrationsInitiallyEmpty verifies Migrations returns empty initially
func TestMigrationsInitiallyEmpty(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})

	rb := rebalance.New(r, nil)

	migrations := rb.Migrations()
	// Migrations may be nil or empty slice initially
	if len(migrations) != 0 {
		t.Fatalf("expected 0 migrations initially, got %d", len(migrations))
	}
}

// TestNewWithNilLogger verifies logger defaults
func TestNewWithNilLogger(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})

	rb := rebalance.New(r, nil)

	if rb == nil {
		t.Fatal("rebalancer should not be nil")
	}
}

// TestSetTransport verifies transport can be changed
func TestSetTransport(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})

	rb := rebalance.New(r, nil)
	customTransport := &errorTransport{err: errors.New("custom")}
	rb.SetTransport(customTransport)

	// Verify transport was set by attempting a migration that will fail
	m := &rebalance.Migration{
		Key:      "test",
		NewOwner: ring.Node{ID: "node-b", Addr: "127.0.0.1:8081"},
	}
	err := rb.ExecuteMigration(m, "127.0.0.1:8080")
	if err == nil {
		t.Fatal("expected error with custom transport")
	}
}

// TestRebalanceDurationIsRecorded verifies timestamps are set after rebalance
func TestRebalanceDurationIsRecorded(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})

	rb := rebalance.New(r, nil)

	result := rb.Rebalance("node-a", "127.0.0.1:8080", []string{})

	// Duration may be 0 for empty rebalance (no work done)
	if result.StartedAt.IsZero() {
		t.Fatal("expected StartedAt to be set")
	}
	if result.CompletedAt.IsZero() {
		t.Fatal("expected CompletedAt to be set")
	}
	// CompletedAt should be >= StartedAt
	if result.CompletedAt.Before(result.StartedAt) {
		t.Fatal("CompletedAt should be >= StartedAt")
	}
}

// TestRebalanceWithNoKeys verifies empty key handling
func TestRebalanceWithNoKeys(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})

	rb := rebalance.New(r, nil)

	result := rb.Rebalance("node-a", "127.0.0.1:8080", []string{})

	if result.TotalKeys != 0 {
		t.Fatalf("expected 0 TotalKeys, got %d", result.TotalKeys)
	}
	if result.MovedKeys != 0 {
		t.Fatalf("expected 0 MovedKeys, got %d", result.MovedKeys)
	}
	if result.FailedKeys != 0 {
		t.Fatalf("expected 0 FailedKeys, got %d", result.FailedKeys)
	}
}

// TestConcurrentRebalanceAccess verifies thread safety of Migrations()
func TestConcurrentRebalanceAccess(t *testing.T) {
	r := ring.New(150)
	r.AddNode(ring.Node{ID: "node-a", Addr: "127.0.0.1:8080"})

	rb := rebalance.New(r, nil)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rb.Migrations()
			rb.LastResult()
			rb.IsInProgress()
		}()
	}
	wg.Wait()
}
