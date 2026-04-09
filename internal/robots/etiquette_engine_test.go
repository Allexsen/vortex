package robots

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type mockCache struct {
	data   map[string][]byte
	getErr error
	setErr error
}

func newMockCache() *mockCache {
	return &mockCache{data: make(map[string][]byte)}
}

func (m *mockCache) Get(ctx context.Context, domain string) ([]byte, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	data, ok := m.data[domain]
	if !ok {
		return nil, nil
	}
	return data, nil
}

func (m *mockCache) Set(ctx context.Context, domain string, data []byte, ttl time.Duration) error {
	if m.setErr != nil {
		return m.setErr
	}
	m.data[domain] = data
	return nil
}

type mockFetcher struct {
	data map[string][]byte
	err  error
}

func (m *mockFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	if m.err != nil {
		return nil, m.err
	}
	data, ok := m.data[url]
	if !ok {
		return nil, nil // no robots.txt
	}
	return data, nil
}

func TestCanCrawl(t *testing.T) {
	allowAll := []byte("User-agent: *\nAllow: /")
	disallowPrivate := []byte("User-agent: TestBot\nDisallow: /private")
	withDelay := []byte("User-agent: TestBot\nCrawl-delay: 2\nAllow: /")

	tests := []struct {
		name       string
		url        string
		cacheData  map[string][]byte
		fetchData  map[string][]byte
		fetchErr   error
		cacheErr   error
		wantAllow  bool
		wantDelay  time.Duration
		wantErr    bool
	}{
		{
			name:      "cache hit - allowed",
			url:       "https://example.com/page",
			cacheData: map[string][]byte{"example.com": allowAll},
			wantAllow: true,
		},
		{
			name:      "cache hit - denied marker",
			url:       "https://example.com/page",
			cacheData: map[string][]byte{"example.com": []byte("ROBOTS_DENIED")},
			wantAllow: false,
		},
		{
			name:      "cache hit - path disallowed",
			url:       "https://example.com/private/secret",
			cacheData: map[string][]byte{"example.com": disallowPrivate},
			wantAllow: false,
		},
		{
			name:      "cache hit - path allowed",
			url:       "https://example.com/public",
			cacheData: map[string][]byte{"example.com": disallowPrivate},
			wantAllow: true,
		},
		{
			name:      "cache miss - fetch success allows",
			url:       "https://example.com/page",
			fetchData: map[string][]byte{"https://example.com/robots.txt": allowAll},
			wantAllow: true,
		},
		{
			name:      "cache miss - fetch returns nil (no robots.txt)",
			url:       "https://example.com/page",
			fetchData: map[string][]byte{},
			wantAllow: true,
		},
		{
			name:     "cache miss - fetch returns ErrAccessDenied",
			url:      "https://example.com/page",
			fetchErr: fmt.Errorf("%w: 403", ErrAccessDenied),
			wantAllow: false,
		},
		{
			name:     "cache miss - fetch returns ErrServerError",
			url:      "https://example.com/page",
			fetchErr: fmt.Errorf("%w: 500", ErrServerError),
			wantErr:  true,
		},
		{
			name:     "cache miss - fetch returns generic error",
			url:      "https://example.com/page",
			fetchErr: errors.New("network timeout"),
			wantErr:  true,
		},
		{
			name:      "crawl delay returned",
			url:       "https://example.com/page",
			cacheData: map[string][]byte{"example.com": withDelay},
			wantAllow: true,
			wantDelay: 2 * time.Second,
		},
		{
			name:    "invalid URL",
			url:     "://",
			wantErr: true,
		},
		{
			name:      "cache read error falls through to fetch",
			url:       "https://example.com/page",
			cacheErr:  errors.New("redis down"),
			fetchData: map[string][]byte{"https://example.com/robots.txt": allowAll},
			wantAllow: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := newMockCache()
			if tt.cacheData != nil {
				cache.data = tt.cacheData
			}
			cache.getErr = tt.cacheErr

			fetcher := &mockFetcher{
				data: tt.fetchData,
				err:  tt.fetchErr,
			}

			engine := NewEtiquetteEngine(cache, fetcher, "TestBot", 24*time.Hour, 2*time.Hour)
			allowed, delay, err := engine.CanCrawl(context.Background(), tt.url)

			if (err != nil) != tt.wantErr {
				t.Fatalf("CanCrawl() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}

			if allowed != tt.wantAllow {
				t.Errorf("CanCrawl() allowed = %v, want %v", allowed, tt.wantAllow)
			}

			if delay != tt.wantDelay {
				t.Errorf("CanCrawl() delay = %v, want %v", delay, tt.wantDelay)
			}
		})
	}
}

func TestCanCrawlCachesDeniedMarker(t *testing.T) {
	cache := newMockCache()
	fetcher := &mockFetcher{
		err: fmt.Errorf("%w: 403", ErrAccessDenied),
	}

	engine := NewEtiquetteEngine(cache, fetcher, "TestBot", 24*time.Hour, 2*time.Hour)
	allowed, _, err := engine.CanCrawl(context.Background(), "https://example.com/page")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allowed {
		t.Error("expected allowed=false for access denied")
	}

	// Verify denied marker was cached
	cached, ok := cache.data["example.com"]
	if !ok {
		t.Fatal("expected cache entry for example.com")
	}
	if string(cached) != "ROBOTS_DENIED" {
		t.Errorf("cached value = %q, want %q", string(cached), "ROBOTS_DENIED")
	}
}

func TestCanCrawlCachesRobotsData(t *testing.T) {
	robotsTxt := []byte("User-agent: *\nAllow: /")
	cache := newMockCache()
	fetcher := &mockFetcher{
		data: map[string][]byte{"https://example.com/robots.txt": robotsTxt},
	}

	engine := NewEtiquetteEngine(cache, fetcher, "TestBot", 24*time.Hour, 2*time.Hour)
	engine.CanCrawl(context.Background(), "https://example.com/page")

	cached, ok := cache.data["example.com"]
	if !ok {
		t.Fatal("expected cache entry for example.com")
	}
	if string(cached) != string(robotsTxt) {
		t.Errorf("cached value = %q, want %q", string(cached), string(robotsTxt))
	}
}

func TestCanCrawlCachesEmptyOnNoRobotsTxt(t *testing.T) {
	cache := newMockCache()
	fetcher := &mockFetcher{data: map[string][]byte{}}

	engine := NewEtiquetteEngine(cache, fetcher, "TestBot", 24*time.Hour, 2*time.Hour)
	allowed, _, err := engine.CanCrawl(context.Background(), "https://example.com/page")

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !allowed {
		t.Error("expected allowed=true when no robots.txt")
	}

	cached, ok := cache.data["example.com"]
	if !ok {
		t.Fatal("expected cache entry for example.com")
	}
	if len(cached) != 0 {
		t.Errorf("expected empty cached value, got %q", string(cached))
	}
}

func TestCanCrawlConcurrentFetchDedup(t *testing.T) {
	robotsTxt := []byte("User-agent: *\nAllow: /")
	var fetchCount atomic.Int32

	// blockingFetcher blocks until gate is opened, then returns robots.txt.
	// Tracks how many times Fetch is called.
	gate := make(chan struct{})
	fetcher := &mockFetcher{data: map[string][]byte{"https://example.com/robots.txt": robotsTxt}}

	cache := newMockCache()
	engine := NewEtiquetteEngine(cache, &countingFetcher{
		inner:      fetcher,
		count:      &fetchCount,
		gate:       gate,
	}, "TestBot", 24*time.Hour, 2*time.Hour)

	const goroutines = 3
	var wg sync.WaitGroup
	results := make([]bool, goroutines)
	errs := make([]error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			allowed, _, err := engine.CanCrawl(context.Background(), "https://example.com/page")
			results[idx] = allowed
			errs[idx] = err
		}(i)
	}

	// Give goroutines time to hit the inflight map
	time.Sleep(50 * time.Millisecond)

	// Release the fetch
	close(gate)
	wg.Wait()

	for i := 0; i < goroutines; i++ {
		if errs[i] != nil {
			t.Errorf("goroutine %d: unexpected error: %v", i, errs[i])
		}
		if !results[i] {
			t.Errorf("goroutine %d: expected allowed=true", i)
		}
	}

	if n := fetchCount.Load(); n != 1 {
		t.Errorf("expected 1 fetch call, got %d", n)
	}
}

// countingFetcher wraps a Fetcher, blocks on a gate channel, and counts calls.
type countingFetcher struct {
	inner Fetcher
	count *atomic.Int32
	gate  chan struct{}
}

func (f *countingFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	f.count.Add(1)
	<-f.gate
	return f.inner.Fetch(ctx, url)
}
