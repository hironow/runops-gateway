package cache_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hironow/runops-gateway/internal/adapter/output/cache"
	"github.com/hironow/runops-gateway/internal/core/domain"
)

// freshToken returns a synthetic InstallationToken whose ExpiresAt
// is far enough in the future that the cache's safety-margin check
// always treats it as fresh.
func freshToken(s string) domain.InstallationToken {
	return domain.InstallationToken{
		Token:     s,
		ExpiresAt: time.Now().Add(55 * time.Minute),
		ProjectID: "proj",
		Tool:      domain.ToolPaintress,
	}
}

// staleToken returns a token whose ExpiresAt is already inside the
// cache's safety margin (5 minutes), so the cache must treat it as
// expired and re-fetch.
func staleToken(s string) domain.InstallationToken {
	return domain.InstallationToken{
		Token:     s,
		ExpiresAt: time.Now().Add(2 * time.Minute), // inside 5-min safety margin
		ProjectID: "proj",
		Tool:      domain.ToolPaintress,
	}
}

// First call fetches; second call within TTL is a hit and the fetch
// function is NOT invoked again.
func TestInstallationTokenCache_GetOrFetch_HitAfterPut(t *testing.T) {
	c := cache.NewInstallationTokenCache()
	var calls int32
	fetch := func(_ context.Context) (domain.InstallationToken, error) {
		atomic.AddInt32(&calls, 1)
		return freshToken("ghs_first"), nil
	}

	tok1, err := c.GetOrFetch(context.Background(), "k1", fetch)
	if err != nil {
		t.Fatalf("first GetOrFetch error: %v", err)
	}
	tok2, err := c.GetOrFetch(context.Background(), "k1", fetch)
	if err != nil {
		t.Fatalf("second GetOrFetch error: %v", err)
	}
	if tok1.Token != tok2.Token {
		t.Errorf("cache returned different tokens for same key: %q vs %q", tok1.Token, tok2.Token)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("fetch must be called exactly once on cache hit; got %d", got)
	}
}

// A token whose ExpiresAt is inside the 5-minute safety margin is
// expired from the cache's perspective and triggers a re-fetch.
func TestInstallationTokenCache_GetOrFetch_ExpiredTriggersRefetch(t *testing.T) {
	c := cache.NewInstallationTokenCache()
	var calls int32
	first := staleToken("ghs_stale")
	second := freshToken("ghs_fresh")
	fetch := func(_ context.Context) (domain.InstallationToken, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return first, nil
		}
		return second, nil
	}

	_, err := c.GetOrFetch(context.Background(), "k2", fetch)
	if err != nil {
		t.Fatalf("first GetOrFetch error: %v", err)
	}
	got, err := c.GetOrFetch(context.Background(), "k2", fetch)
	if err != nil {
		t.Fatalf("second GetOrFetch error: %v", err)
	}
	if got.Token != "ghs_fresh" {
		t.Errorf("expired token must be re-fetched; got %q want %q", got.Token, "ghs_fresh")
	}
	if n := atomic.LoadInt32(&calls); n != 2 {
		t.Errorf("expired token re-fetch must call fetch twice; got %d", n)
	}
}

// Singleflight: N goroutines racing on the same key produce exactly
// ONE fetch call (the others share the same result). Without
// singleflight a cold cache would mint N tokens in parallel.
func TestInstallationTokenCache_GetOrFetch_SingleflightDeduplicatesConcurrentMints(t *testing.T) {
	c := cache.NewInstallationTokenCache()
	var calls int32
	gate := make(chan struct{})
	fetch := func(_ context.Context) (domain.InstallationToken, error) {
		atomic.AddInt32(&calls, 1)
		<-gate // hold inside the singleflight window so all goroutines arrive
		return freshToken("ghs_shared"), nil
	}

	const n = 50
	var wg sync.WaitGroup
	results := make([]string, n)
	wg.Add(n)
	for i := range n {
		go func(i int) {
			defer wg.Done()
			tok, err := c.GetOrFetch(context.Background(), "k3", fetch)
			if err != nil {
				return
			}
			results[i] = tok.Token
		}(i)
	}
	// Give every goroutine time to arrive at GetOrFetch and queue
	// behind the singleflight window, then release the fetch.
	time.Sleep(20 * time.Millisecond)
	close(gate)
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("singleflight must collapse %d concurrent mints into 1; got %d fetch calls", n, got)
	}
	for i, r := range results {
		if r != "ghs_shared" {
			t.Errorf("goroutine %d got unexpected token %q", i, r)
		}
	}
}

// Fetch errors propagate to every waiting caller AND must NOT be
// cached — the next GetOrFetch should re-attempt rather than serving
// the failed result.
func TestInstallationTokenCache_GetOrFetch_FetchErrorPropagatesAndIsNotCached(t *testing.T) {
	c := cache.NewInstallationTokenCache()
	wantErr := errors.New("synthetic upstream failure")
	var calls int32
	fetch := func(_ context.Context) (domain.InstallationToken, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			return domain.InstallationToken{}, wantErr
		}
		return freshToken("ghs_recovered"), nil
	}

	if _, err := c.GetOrFetch(context.Background(), "k4", fetch); !errors.Is(err, wantErr) {
		t.Errorf("first GetOrFetch want %v, got %v", wantErr, err)
	}
	got, err := c.GetOrFetch(context.Background(), "k4", fetch)
	if err != nil {
		t.Fatalf("second GetOrFetch after error: %v", err)
	}
	if got.Token != "ghs_recovered" {
		t.Errorf("error must not be cached; got %q want %q", got.Token, "ghs_recovered")
	}
}
