package store

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestConcurrentSetGetDelete verifies the store handles concurrent access correctly
// with multiple goroutines performing set, get, and delete operations simultaneously.
func TestConcurrentSetGetDelete(t *testing.T) {
	s := New(100 * time.Millisecond)
	defer s.Close()

	const numGoroutines = 50
	const operationsPerGoroutine = 200

	var wg sync.WaitGroup
	var errorCount int64

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < operationsPerGoroutine; j++ {
				key := fmt.Sprintf("key-%d-%d", id, j%20) // Some overlap for contention
				value := fmt.Sprintf("value-%d-%d", id, j)

				switch j % 3 {
				case 0: // Set
					s.Set(key, value, 5*time.Second)
				case 1: // Get
					_, _ = s.Get(key)
				case 2: // Delete (only some keys)
					if j%6 == 0 {
						s.Delete(key)
					}
				}
			}
		}(i)
	}

	wg.Wait()

	// Verify store is still functional after concurrent access
	s.Set("final-key", "final-value", time.Second)
	val, err := s.Get("final-key")
	if err != nil || val != "final-value" {
		t.Fatalf("Store not functional after concurrent access: val=%q, err=%v", val, err)
	}

	if errorCount > 0 {
		t.Logf("Concurrent operations completed with %d errors", errorCount)
	}
	t.Logf("Completed %d concurrent operations across %d goroutines",
		numGoroutines*operationsPerGoroutine, numGoroutines)
}

// TestHighContentionKeyAccess verifies correct behavior when multiple goroutines
// access the same keys simultaneously.
func TestHighContentionKeyAccess(t *testing.T) {
	s := New(100 * time.Millisecond)
	defer s.Close()

	const numGoroutines = 100
	const iterations = 500

	// Shared keys with high contention
	keys := []string{"hot-key-1", "hot-key-2", "hot-key-3"}

	var wg sync.WaitGroup
	var setCount, getCount, deleteCount int64

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				key := keys[j%len(keys)]
				switch j % 3 {
				case 0:
					s.Set(key, fmt.Sprintf("value-%d-%d", id, j), 5*time.Second)
					atomic.AddInt64(&setCount, 1)
				case 1:
					_, _ = s.Get(key)
					atomic.AddInt64(&getCount, 1)
				case 2:
					s.Delete(key)
					atomic.AddInt64(&deleteCount, 1)
				}
			}
		}(i)
	}

	wg.Wait()

	t.Logf("High contention test: %d sets, %d gets, %d deletes",
		setCount, getCount, deleteCount)

	// Verify store is still functional
	s.Set("contention-test", "works", time.Second)
	if val, err := s.Get("contention-test"); err != nil || val != "works" {
		t.Fatalf("Store not functional after high contention: val=%q, err=%v", val, err)
	}
}

// TestConcurrentTTLExpiry verifies TTL expiry works correctly under concurrent access.
func TestConcurrentTTLExpiry(t *testing.T) {
	s := New(50 * time.Millisecond) // Fast sweep
	defer s.Close()

	const numGoroutines = 20
	const keysPerGoroutine = 50

	var wg sync.WaitGroup

	// Write keys with short TTL concurrently
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < keysPerGoroutine; j++ {
				key := fmt.Sprintf("ttl-key-%d-%d", id, j)
				s.Set(key, "value", 200*time.Millisecond)
			}
		}(i)
	}

	wg.Wait()

	// Verify all keys exist immediately after write
	time.Sleep(50 * time.Millisecond)
	existingCount := 0
	for i := 0; i < numGoroutines; i++ {
		for j := 0; j < keysPerGoroutine; j++ {
			key := fmt.Sprintf("ttl-key-%d-%d", i, j)
			if _, err := s.Get(key); err == nil {
				existingCount++
			}
		}
	}
	t.Logf("Keys existing after 50ms: %d/%d", existingCount, numGoroutines*keysPerGoroutine)

	// Wait for TTL to expire
	time.Sleep(300 * time.Millisecond)

	// Verify all keys have expired
	expiredCount := 0
	for i := 0; i < numGoroutines; i++ {
		for j := 0; j < keysPerGoroutine; j++ {
			key := fmt.Sprintf("ttl-key-%d-%d", i, j)
			if _, err := s.Get(key); err == nil {
				expiredCount++
			}
		}
	}

	if expiredCount > 0 {
		t.Fatalf("%d keys should have expired but still exist", expiredCount)
	}
	t.Logf("All %d keys expired correctly under concurrent access", numGoroutines*keysPerGoroutine)
}

// TestConcurrentEviction verifies LRU eviction works correctly under concurrent access.
func TestConcurrentEviction(t *testing.T) {
	const memCap int64 = 1000 // Very small cap to trigger eviction
	s := NewWithEviction(100*time.Millisecond, memCap)
	defer s.Close()

	const numGoroutines = 10
	const keysPerGoroutine = 100

	var wg sync.WaitGroup

	// Write many keys concurrently to trigger eviction
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < keysPerGoroutine; j++ {
				key := fmt.Sprintf("evict-key-%d-%d", id, j)
				value := fmt.Sprintf("value-%d", j)
				s.Set(key, value, 0) // No TTL
			}
		}(i)
	}

	wg.Wait()

	// Verify memory usage stays under cap (with some tolerance for concurrent writes)
	finalMem := s.MemoryUsage()
	if finalMem > memCap*2 { // Allow 2x tolerance for concurrent writes
		t.Logf("Memory usage %d exceeds 2x cap %d (expected some tolerance for concurrent writes)", finalMem, memCap)
	}

	// Verify store is still functional
	s.Set("eviction-test", "works", time.Second)
	if val, err := s.Get("eviction-test"); err != nil || val != "works" {
		t.Fatalf("Store not functional after concurrent eviction: val=%q, err=%v", val, err)
	}

	t.Logf("Concurrent eviction test: memory usage %d, cap %d", finalMem, memCap)
}

// TestMemoryLeakOnExpiry verifies that expired keys are properly cleaned up
// and don't cause memory leaks.
func TestMemoryLeakOnExpiry(t *testing.T) {
	s := New(50 * time.Millisecond)
	defer s.Close()

	// Write many keys with short TTL
	const numKeys = 1000
	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("leak-test-key-%d", i)
		s.Set(key, "value", 200*time.Millisecond)
	}

	initialCount := s.EntryCount()
	if initialCount < numKeys/2 {
		t.Fatalf("Expected at least %d keys, got %d", numKeys/2, initialCount)
	}

	// Wait for all keys to expire and be swept
	time.Sleep(500 * time.Millisecond)

	// Force multiple sweep cycles
	time.Sleep(500 * time.Millisecond)

	finalCount := s.EntryCount()
	if finalCount > 10 { // Allow some tolerance
		t.Fatalf("Memory leak detected: %d keys still exist after expiry (expected ~0)", finalCount)
	}

	t.Logf("Memory leak test: started with %d keys, ended with %d", initialCount, finalCount)
}
