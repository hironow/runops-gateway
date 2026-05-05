package state

import (
	"sync"
	"time"
)

// MemoryConsumedStore is an in-memory implementation of port.ConsumedTokenStore.
// Tokens stay marked as consumed for ttl after their first sighting; expired
// entries are cleaned lazily on the next MarkConsumed call so there is no
// background goroutine to manage.
//
// Concurrency: a single mutex protects all fields. Phase 1 traffic volume
// (handful of /agent invocations per day) makes this trivially adequate; the
// lazy GC ensures the map size stays bounded by ttl × concurrent operators.
type MemoryConsumedStore struct {
	mu        sync.Mutex
	tokens    map[string]time.Time // token -> earliest expiry
	ttl       time.Duration
	clock     func() time.Time
	gcEvery   int
	gcCounter int
}

// NewMemoryConsumedStore returns a store that remembers tokens for ttl.
// Pass time.Now as the clock for production; tests inject a deterministic clock.
func NewMemoryConsumedStore(ttl time.Duration) *MemoryConsumedStore {
	return newMemoryConsumedStoreWithClock(ttl, time.Now)
}

func newMemoryConsumedStoreWithClock(ttl time.Duration, clock func() time.Time) *MemoryConsumedStore {
	return &MemoryConsumedStore{
		tokens:  make(map[string]time.Time),
		ttl:     ttl,
		clock:   clock,
		gcEvery: 64, // GC scan every 64 calls — cheap, bounded
	}
}

// MarkConsumed returns true if token is being recorded for the first time
// within the ttl window, false if it was already seen and is still inside the
// window (i.e. a replay).
func (s *MemoryConsumedStore) MarkConsumed(token string) bool {
	now := s.clock()
	s.mu.Lock()
	defer s.mu.Unlock()

	s.gcCounter++
	if s.gcCounter >= s.gcEvery {
		s.gcCounter = 0
		s.gcLocked(now)
	}

	if expiry, exists := s.tokens[token]; exists && now.Before(expiry) {
		return false
	}
	s.tokens[token] = now.Add(s.ttl)
	return true
}

func (s *MemoryConsumedStore) gcLocked(now time.Time) {
	for k, expiry := range s.tokens {
		if !now.Before(expiry) {
			delete(s.tokens, k)
		}
	}
}
