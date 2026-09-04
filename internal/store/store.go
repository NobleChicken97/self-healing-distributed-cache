package store

import (
	"container/list"
	"errors"
	"sync"
	"time"
)

var ErrNotFound = errors.New("key not found")

// entry stores a key-value pair with metadata for TTL, LRU eviction, and versioning.
type entry struct {
	value     string
	expiresAt time.Time
	version   int64     // Monotonically increasing version for quorum reads
	updatedAt time.Time // Wall-clock time of last update for conflict resolution
	// lruElement is a pointer to the element in the LRU list for O(1) access tracking.
	// nil if LRU eviction is disabled.
	lruElement *list.Element
	// size is the estimated memory size of this entry in bytes.
	size int64
}

// Store is an in-memory key-value store with TTL and optional LRU eviction.
type Store struct {
	mu       sync.RWMutex
	entries  map[string]entry
	interval time.Duration
	stop     chan struct{}
	stopped  chan struct{}
	// LRU fields (nil if eviction disabled)
	memCap     int64      // Maximum memory in bytes; 0 = unlimited
	currentMem int64      // Current estimated memory usage
	lruList    *list.List // Front = most recent, Back = least recent
}

// New creates a store with TTL sweep but no eviction (unlimited memory).
func New(sweepInterval time.Duration) *Store {
	if sweepInterval <= 0 {
		sweepInterval = time.Second
	}

	s := &Store{
		entries:  make(map[string]entry),
		interval: sweepInterval,
		stop:     make(chan struct{}),
		stopped:  make(chan struct{}),
	}
	go s.sweepLoop()
	return s
}

// NewWithEviction creates a store with LRU eviction when memory cap is exceeded.
// memCap is the maximum memory in bytes.
func NewWithEviction(sweepInterval time.Duration, memCap int64) *Store {
	if sweepInterval <= 0 {
		sweepInterval = time.Second
	}

	s := &Store{
		entries:  make(map[string]entry),
		interval: sweepInterval,
		stop:     make(chan struct{}),
		stopped:  make(chan struct{}),
		memCap:   memCap,
		lruList:  list.New(),
	}
	go s.sweepLoop()
	return s
}

// HasEviction returns true if LRU eviction is enabled.
func (s *Store) HasEviction() bool {
	return s.memCap > 0
}

func (s *Store) Set(key, value string, ttl time.Duration) {
	var expiresAt time.Time
	if ttl > 0 {
		expiresAt = time.Now().Add(ttl)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Calculate size of this entry (key + value bytes).
	entrySize := int64(len(key) + len(value))

	// Increment version for existing keys.
	var newVersion int64 = 1
	if existing, ok := s.entries[key]; ok {
		s.currentMem -= existing.size
		newVersion = existing.version + 1
		// Remove from LRU list if present.
		if existing.lruElement != nil {
			s.lruList.Remove(existing.lruElement)
		}
	}

	s.entries[key] = entry{
		value:      value,
		expiresAt:  expiresAt,
		version:    newVersion,
		updatedAt:  time.Now(),
		size:       entrySize,
		lruElement: nil,
	}
	s.currentMem += entrySize

	// Add to front of LRU list (most recently used).
	if s.lruList != nil {
		elem := s.lruList.PushFront(key)
		s.entries[key] = entry{
			value:      value,
			expiresAt:  expiresAt,
			version:    newVersion,
			updatedAt:  time.Now(),
			size:       entrySize,
			lruElement: elem,
		}
	}

	// Evict if over memory cap.
	s.evictIfNeeded()
}

// SetWithExpiry sets a key with an absolute expiry timestamp.
// This is used during replication to preserve the original expiry time
// across replicas, ensuring all replicas expire the key simultaneously.
func (s *Store) SetWithExpiry(key, value string, expiresAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entrySize := int64(len(key) + len(value))

	var newVersion int64 = 1
	if existing, ok := s.entries[key]; ok {
		s.currentMem -= existing.size
		newVersion = existing.version + 1
		if existing.lruElement != nil {
			s.lruList.Remove(existing.lruElement)
		}
	}

	s.entries[key] = entry{
		value:      value,
		expiresAt:  expiresAt,
		version:    newVersion,
		updatedAt:  time.Now(),
		size:       entrySize,
		lruElement: nil,
	}
	s.currentMem += entrySize

	if s.lruList != nil {
		elem := s.lruList.PushFront(key)
		s.entries[key] = entry{
			value:      value,
			expiresAt:  expiresAt,
			version:    newVersion,
			updatedAt:  time.Now(),
			size:       entrySize,
			lruElement: elem,
		}
	}

	s.evictIfNeeded()
}

// GetExpiry returns the absolute expiry time for a key.
// Returns zero time if the key doesn't exist or has no expiry.
func (s *Store) GetExpiry(key string) time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.entries[key].expiresAt
}

// GetWithVersion returns the value, version, and expiry for a key.
// Used by quorum reads to resolve the most recent value across replicas.
func (s *Store) GetWithVersion(key string) (value string, version int64, expiresAt time.Time, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	item, ok := s.entries[key]
	if !ok {
		return "", 0, time.Time{}, ErrNotFound
	}
	if !item.expiresAt.IsZero() && !time.Now().Before(item.expiresAt) {
		s.deleteLocked(key)
		return "", 0, time.Time{}, ErrNotFound
	}

	// Update LRU: move to front (most recently used).
	if item.lruElement != nil {
		s.lruList.MoveToFront(item.lruElement)
	}

	return item.value, item.version, item.expiresAt, nil
}

// SetVersion sets a key with a specific version (used by quorum replication).
// Only updates if the new version is greater than the existing version.
func (s *Store) SetVersion(key, value string, expiresAt time.Time, version int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if we already have a newer version.
	if existing, ok := s.entries[key]; ok && existing.version >= version {
		return false // Don't overwrite with older version
	}

	entrySize := int64(len(key) + len(value))

	if existing, ok := s.entries[key]; ok {
		s.currentMem -= existing.size
		if existing.lruElement != nil {
			s.lruList.Remove(existing.lruElement)
		}
	}

	s.entries[key] = entry{
		value:      value,
		expiresAt:  expiresAt,
		version:    version,
		updatedAt:  time.Now(),
		size:       entrySize,
		lruElement: nil,
	}
	s.currentMem += entrySize

	if s.lruList != nil {
		elem := s.lruList.PushFront(key)
		s.entries[key] = entry{
			value:      value,
			expiresAt:  expiresAt,
			version:    version,
			updatedAt:  time.Now(),
			size:       entrySize,
			lruElement: elem,
		}
	}

	s.evictIfNeeded()
	return true
}

func (s *Store) Get(key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	item, ok := s.entries[key]
	if !ok {
		return "", ErrNotFound
	}
	if !item.expiresAt.IsZero() && !time.Now().Before(item.expiresAt) {
		s.deleteLocked(key)
		return "", ErrNotFound
	}

	// Update LRU: move to front (most recently used).
	if item.lruElement != nil {
		s.lruList.MoveToFront(item.lruElement)
	}

	return item.value, nil
}

func (s *Store) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteLocked(key)
}

// deleteLocked removes a key. Caller must hold the write lock.
func (s *Store) deleteLocked(key string) {
	if item, ok := s.entries[key]; ok {
		s.currentMem -= item.size
		if item.lruElement != nil {
			s.lruList.Remove(item.lruElement)
		}
		delete(s.entries, key)
	}
}

// evictIfNeeded evicts least-recently-used entries until under memory cap.
// Caller must hold the write lock.
func (s *Store) evictIfNeeded() {
	if s.memCap <= 0 || s.lruList == nil {
		return
	}
	// Evict from the back (least recently used) until under cap.
	for s.currentMem > s.memCap {
		back := s.lruList.Back()
		if back == nil {
			break
		}
		key := back.Value.(string)
		s.lruList.Remove(back)
		if item, ok := s.entries[key]; ok {
			s.currentMem -= item.size
			delete(s.entries, key)
		}
	}
}

func (s *Store) Close() {
	select {
	case <-s.stop:
		return
	default:
		close(s.stop)
	}
	<-s.stopped
}

// Keys returns all non-expired keys currently in the store.
func (s *Store) Keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	keys := make([]string, 0, len(s.entries))
	now := time.Now()
	for key, item := range s.entries {
		// Skip expired entries.
		if !item.expiresAt.IsZero() && !now.Before(item.expiresAt) {
			continue
		}
		keys = append(keys, key)
	}
	return keys
}

// MemoryUsage returns the current estimated memory usage in bytes.
func (s *Store) MemoryUsage() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentMem
}

// MemoryCap returns the memory cap (0 if unlimited).
func (s *Store) MemoryCap() int64 {
	return s.memCap
}

// EntryCount returns the number of entries in the store.
func (s *Store) EntryCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

func (s *Store) sweepLoop() {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	defer close(s.stopped)

	for {
		select {
		case now := <-ticker.C:
			s.mu.Lock()
			for key, item := range s.entries {
				if !item.expiresAt.IsZero() && !now.Before(item.expiresAt) {
					s.deleteLocked(key)
				}
			}
			s.mu.Unlock()
		case <-s.stop:
			return
		}
	}
}
