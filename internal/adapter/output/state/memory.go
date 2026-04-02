package state

import "sync"

// MemoryStore is a thread-safe in-memory implementation of port.StateStore.
type MemoryStore struct {
	mu   sync.Mutex
	keys map[string]struct{}
}

// NewMemoryStore creates a new MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{keys: make(map[string]struct{})}
}

// TryLock claims key. Returns true if claimed, false if already held.
func (s *MemoryStore) TryLock(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.keys[key]; exists {
		return false
	}
	s.keys[key] = struct{}{}
	return true
}

// Release removes the lock for key.
func (s *MemoryStore) Release(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.keys, key)
}
