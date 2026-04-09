package robots

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/temoto/robotstxt"
)

var (
	deniedMarker = []byte("ROBOTS_DENIED")
)

type domainEntry struct {
	data []byte
	err  error
	done chan struct{}
}

type EtiquetteEngine struct {
	mu sync.Mutex

	cache     RulesCache
	fetcher   Fetcher
	userAgent string
	inflight  map[string]*domainEntry

	cacheTTL       time.Duration
	deniedCacheTTL time.Duration
}

func NewEtiquetteEngine(cache RulesCache, fetcher Fetcher, userAgent string, cacheTTL, deniedCacheTTL time.Duration) *EtiquetteEngine {
	return &EtiquetteEngine{
		cache:          cache,
		fetcher:        fetcher,
		userAgent:      userAgent,
		inflight:       make(map[string]*domainEntry),
		cacheTTL:       cacheTTL,
		deniedCacheTTL: deniedCacheTTL,
	}
}

func (e *EtiquetteEngine) CanCrawl(ctx context.Context, rawURL string) (bool, time.Duration, error) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		slog.Error("Invalid URL", "url", rawURL, "error", err)
		return false, 0, err
	}
	domain := parsedURL.Hostname()
	path := parsedURL.Path

	e.mu.Lock()
	entry, exists := e.inflight[domain]
	if exists {
		e.mu.Unlock()
		<-entry.done
	} else if !exists {
		entry = &domainEntry{
			data: []byte{},
			err:  nil,
			done: make(chan struct{}),
		}
		e.inflight[domain] = entry
		e.mu.Unlock()

		e.fetchOnce(ctx, domain, parsedURL)
	}

	if entry.err != nil {
		return false, 0, entry.err
	}

	if string(entry.data) == string(deniedMarker) {
		slog.Info("Domain access denied by robots.txt", "domain", domain)
		return false, 0, nil
	}

	rules, err := robotstxt.FromBytes(entry.data)
	if err != nil {
		slog.Warn("Parsing error, defaulting to allow", "domain", domain, "error", err)
		return true, 0, nil
	}

	group := rules.FindGroup(e.userAgent)
	allowed := group.Test(path)
	crawlDelay := group.CrawlDelay
	return allowed, crawlDelay, nil
}

func (e *EtiquetteEngine) setCache(ctx context.Context, domain string, data []byte, ttl time.Duration) {
	err := e.cache.Set(ctx, domain, data, ttl)
	if err != nil {
		slog.Warn("Cache write failed", "domain", domain, "error", err)
	}
}

// fetchOnce deduplicates concurrent robots.txt fetches for the same domain.
// Under burst traffic, multiple goroutines may call CanCrawl for the same domain
// before the cache is populated. The first caller fetches and caches the result;
// others block on the inflight channel and read the result from the shared entry.
func (e *EtiquetteEngine) fetchOnce(ctx context.Context, domain string, parsedURL *url.URL) {
	data, err := e.cache.Get(ctx, domain)
	if err != nil {
		slog.Warn("Cache read failed", "domain", domain, "error", err)
	}

	defer func() {
		e.mu.Lock()
		e.inflight[domain].data = data
		e.inflight[domain].err = err
		close(e.inflight[domain].done)
		delete(e.inflight, domain)
		e.mu.Unlock()
	}()

	if data == nil {
		robotsURL := url.URL{
			Scheme: parsedURL.Scheme,
			Host:   parsedURL.Hostname(),
			Path:   "/robots.txt",
		}

		data, err = e.fetcher.Fetch(ctx, robotsURL.String())
		if err != nil {
			if errors.Is(err, ErrAccessDenied) {
				slog.Info("Access denied", "domain", domain, "error", err)
				data = deniedMarker
				err = nil
				e.setCache(ctx, domain, deniedMarker, e.deniedCacheTTL)
				return
			}

			slog.Error("Transient error", "domain", domain, "error", err)
			return
		}

		if data == nil {
			slog.Info("No robots.txt found", "domain", domain)
			data = []byte{}
		}

		e.setCache(ctx, domain, data, e.cacheTTL)
	}
}
