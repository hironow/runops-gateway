// Package cache holds the in-process token cache that sits between
// the broker use case (internal/usecase/broker_token.go) and the
// upstream GitHub App token mint (Phase 2b adapter). It implements
// the §5.5-aligned read-through cache from plan v8 §6 step 15:
// short-TTL in-memory store + singleflight dedup of concurrent
// mints for the same key.
//
// Phase 2a (this PR) ships the store + singleflight pair only. The
// usecase layer wires the cache into the orchestration in Phase 2b
// once the GitHub App adapter is in place.
package cache

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/hironow/runops-gateway/internal/core/domain"
)

// safetyMargin is the time before token.ExpiresAt at which the cache
// stops serving the cached value. The 5-minute window gives downstream
// callers (paintress / sightjack / amadeus / dominator) enough wall
// clock time to use the token before GitHub itself rejects it.
const safetyMargin = 5 * time.Minute

// FetchFunc mints a fresh InstallationToken for a cache miss. It is
// supplied per-call by the use case so the cache stays free of
// broker / GitHub-App imports (= cache layer is leaf-only).
type FetchFunc func(ctx context.Context) (domain.InstallationToken, error)

// InstallationTokenCache is a goroutine-safe in-memory cache for
// short-lived GitHub installation tokens. It dedups concurrent
// mints for the same key via singleflight so a thundering herd
// of callers triggers exactly one upstream mint call.
type InstallationTokenCache struct {
	mu    sync.Mutex
	items map[string]domain.InstallationToken
	sf    singleflight.Group
}

// NewInstallationTokenCache returns a fresh, empty cache.
func NewInstallationTokenCache() *InstallationTokenCache {
	return &InstallationTokenCache{items: make(map[string]domain.InstallationToken)}
}

// GetOrFetch returns the cached token for key when it is still fresh
// (= ExpiresAt is more than safetyMargin away from now), otherwise
// invokes fetch under a singleflight guard and caches the result.
// Errors from fetch propagate to every concurrent caller and are
// NOT cached, so the next GetOrFetch retries from scratch.
func (c *InstallationTokenCache) GetOrFetch(ctx context.Context, key string, fetch FetchFunc) (domain.InstallationToken, error) {
	if tok, ok := c.lookup(key); ok {
		return tok, nil
	}

	v, err, _ := c.sf.Do(key, func() (any, error) {
		// Re-check after acquiring the singleflight slot — another
		// goroutine may have populated the cache while we were
		// queued.
		if tok, ok := c.lookup(key); ok {
			return tok, nil
		}
		tok, err := fetch(ctx)
		if err != nil {
			return domain.InstallationToken{}, err
		}
		c.store(key, tok)
		return tok, nil
	})
	if err != nil {
		return domain.InstallationToken{}, err
	}
	return v.(domain.InstallationToken), nil
}

func (c *InstallationTokenCache) lookup(key string) (domain.InstallationToken, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tok, ok := c.items[key]
	if !ok {
		return domain.InstallationToken{}, false
	}
	if time.Until(tok.ExpiresAt) <= safetyMargin {
		delete(c.items, key)
		return domain.InstallationToken{}, false
	}
	return tok, true
}

func (c *InstallationTokenCache) store(key string, tok domain.InstallationToken) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = tok
}
