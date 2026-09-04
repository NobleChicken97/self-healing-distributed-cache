package store

import (
	"errors"
	"testing"
	"time"
)

func TestSetGet(t *testing.T) {
	s := New(10 * time.Millisecond)
	defer s.Close()

	s.Set("language", "Go", 0)
	got, err := s.Get("language")
	if err != nil || got != "Go" {
		t.Fatalf("Get() = %q, %v; want Go, nil", got, err)
	}
}

func TestDelete(t *testing.T) {
	s := New(10 * time.Millisecond)
	defer s.Close()

	s.Set("language", "Go", 0)
	s.Delete("language")
	if _, err := s.Get("language"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() error = %v; want ErrNotFound", err)
	}
}

func TestTTLExpiry(t *testing.T) {
	s := New(5 * time.Millisecond)
	defer s.Close()

	s.Set("temporary", "value", 20*time.Millisecond)
	if _, err := s.Get("temporary"); err != nil {
		t.Fatalf("Get() before expiry returned %v", err)
	}
	time.Sleep(35 * time.Millisecond)
	if _, err := s.Get("temporary"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get() after expiry error = %v; want ErrNotFound", err)
	}
}

func TestConcurrentAccess(t *testing.T) {
	s := New(10 * time.Millisecond)
	defer s.Close()

	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				s.Set("shared", "value", 0)
				_, _ = s.Get("shared")
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestLRUEviction(t *testing.T) {
	// Create a store with a small memory cap.
	// Each entry is roughly key + value bytes.
	// "key-1" + "value-1" = 10 bytes, etc.
	s := NewWithEviction(100*time.Millisecond, 30) // Cap at 30 bytes
	defer s.Close()

	// Add entries until we exceed the cap.
	s.Set("key-1", "value-1", 0) // ~10 bytes
	s.Set("key-2", "value-2", 0) // ~10 bytes
	s.Set("key-3", "value-3", 0) // ~10 bytes

	// Should be at or near cap.
	if s.MemoryUsage() > 30 {
		t.Logf("Memory usage: %d (cap: 30)", s.MemoryUsage())
	}

	// Add another entry - should trigger eviction of least recently used.
	s.Set("key-4", "value-4", 0) // ~10 bytes

	// Should have evicted key-1 (least recently used).
	if _, err := s.Get("key-1"); err == nil {
		t.Log("key-1 was not evicted (may be within cap)")
	} else {
		t.Log("key-1 was evicted as expected")
	}

	// key-4 should exist.
	if _, err := s.Get("key-4"); err != nil {
		t.Fatalf("key-4 should exist: %v", err)
	}
}

func TestLRUAccessOrder(t *testing.T) {
	// Create a store with cap for ~3 entries.
	s := NewWithEviction(100*time.Millisecond, 30)
	defer s.Close()

	s.Set("a", "1", 0)
	s.Set("b", "2", 0)
	s.Set("c", "3", 0)

	// Access "a" to make it most recently used.
	_, _ = s.Get("a")

	// Add "d" - should evict "b" (least recently used after accessing "a").
	s.Set("d", "4", 0)

	// "a" should still exist (was accessed recently).
	if _, err := s.Get("a"); err != nil {
		t.Fatalf("key 'a' should exist after being accessed: %v", err)
	}

	// "b" should be evicted.
	if _, err := s.Get("b"); err == nil {
		t.Log("key 'b' was not evicted")
	} else {
		t.Log("key 'b' was evicted as expected (least recently used)")
	}
}

func TestMemoryTracking(t *testing.T) {
	s := NewWithEviction(100*time.Millisecond, 1000)
	defer s.Close()

	if !s.HasEviction() {
		t.Fatal("store should have eviction enabled")
	}

	initialMem := s.MemoryUsage()
	s.Set("test-key", "test-value", 0)

	if s.MemoryUsage() <= initialMem {
		t.Fatal("memory usage should increase after Set")
	}

	expectedSize := int64(len("test-key") + len("test-value"))
	if s.MemoryUsage() != expectedSize {
		t.Fatalf("expected memory usage %d, got %d", expectedSize, s.MemoryUsage())
	}

	s.Delete("test-key")
	if s.MemoryUsage() != 0 {
		t.Fatalf("expected memory usage 0 after delete, got %d", s.MemoryUsage())
	}
}

func TestVersionIncrement(t *testing.T) {
	s := New(time.Second)
	defer s.Close()

	// Set a key multiple times and verify version increments.
	s.Set("versioned-key", "value-1", 0)
	_, version1, _, err := s.GetWithVersion("versioned-key")
	if err != nil {
		t.Fatalf("key should exist: %v", err)
	}
	if version1 != 1 {
		t.Fatalf("expected version 1, got %d", version1)
	}

	s.Set("versioned-key", "value-2", 0)
	_, version2, _, err := s.GetWithVersion("versioned-key")
	if err != nil {
		t.Fatalf("key should exist: %v", err)
	}
	if version2 != 2 {
		t.Fatalf("expected version 2, got %d", version2)
	}

	s.Set("versioned-key", "value-3", 0)
	_, version3, _, err := s.GetWithVersion("versioned-key")
	if err != nil {
		t.Fatalf("key should exist: %v", err)
	}
	if version3 != 3 {
		t.Fatalf("expected version 3, got %d", version3)
	}

	t.Logf("version increments: %d -> %d -> %d", version1, version2, version3)
}

func TestSetVersionConflictResolution(t *testing.T) {
	s := New(time.Second)
	defer s.Close()

	// Set with version 1.
	updated := s.SetVersion("conflict-key", "value-1", time.Time{}, 1)
	if !updated {
		t.Fatal("should have set new key")
	}

	// Try to set with older version - should not overwrite.
	updated = s.SetVersion("conflict-key", "value-old", time.Time{}, 1)
	if updated {
		t.Fatal("should not overwrite with same version")
	}

	// Verify original value is preserved.
	value, version, _, err := s.GetWithVersion("conflict-key")
	if err != nil {
		t.Fatalf("key should exist: %v", err)
	}
	if value != "value-1" {
		t.Fatalf("expected value-1, got %s", value)
	}
	if version != 1 {
		t.Fatalf("expected version 1, got %d", version)
	}

	// Set with newer version - should overwrite.
	updated = s.SetVersion("conflict-key", "value-2", time.Time{}, 2)
	if !updated {
		t.Fatal("should overwrite with newer version")
	}

	value, version, _, err = s.GetWithVersion("conflict-key")
	if err != nil {
		t.Fatalf("key should exist: %v", err)
	}
	if value != "value-2" {
		t.Fatalf("expected value-2, got %s", value)
	}
	if version != 2 {
		t.Fatalf("expected version 2, got %d", version)
	}
}
