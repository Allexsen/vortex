package robots

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/url"
	"time"
)

var (
	deniedMarker = []byte("ROBOTS_DENIED")
)

type EtiquetteEngine struct {
	cache     RulesCache
	fetcher   Fetcher
	userAgent string

	cacheTTL       time.Duration
	deniedCacheTTL time.Duration
}

func NewEtiquetteEngine(cache RulesCache, fetcher Fetcher, userAgent string, cacheTTL, deniedCacheTTL time.Duration) *EtiquetteEngine {
	return &EtiquetteEngine{
		cache:          cache,
		fetcher:        fetcher,
		userAgent:      userAgent,
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

	data, err := e.cache.Get(ctx, domain)
	if err != nil {
		slog.Warn("Cache read failed", "domain", domain, "error", err)
	}

	if data != nil && bytes.Equal(data, deniedMarker) {
		slog.Info("Robots disallowed", "domain", domain)
		return false, 0, nil
	}

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
				e.setCache(ctx, domain, deniedMarker, e.deniedCacheTTL)
				return false, 0, nil
			}

			slog.Error("Transient error", "domain", domain, "error", err)
			return false, 0, err
		}

		if data == nil {
			slog.Info("No robots.txt found", "domain", domain)
			e.setCache(ctx, domain, []byte{}, e.cacheTTL) // Cache empty result
			return true, 0, nil
		} else {
			e.setCache(ctx, domain, data, e.cacheTTL) // Cache valid robots.txt for the specified duration
		}
	}

	rules, err := parseRobotsTxt(data)
	if err != nil {
		slog.Warn("Parsing error, defaulting to allow", "domain", domain, "error", err)
		return true, 0, nil
	}

	allowed := rules.TestAgent(e.userAgent, path)
	crawlDelay := rules.FindGroup(e.userAgent).CrawlDelay
	return allowed, crawlDelay, nil
}

func (e *EtiquetteEngine) setCache(ctx context.Context, domain string, data []byte, ttl time.Duration) {
	err := e.cache.Set(ctx, domain, data, ttl)
	if err != nil {
		slog.Warn("Cache write failed", "domain", domain, "error", err)
	}
}
