package robots

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/url"
	"time"
)

var (
	deniedMarker = []byte("ROBOTS_DENIED")
)

type EtiquetteEngine struct {
	cache     RulesCache
	fetcher   *Fetcher
	userAgent string
}

func NewEtiquetteEngine(cache RulesCache, fetcher *Fetcher, userAgent string) *EtiquetteEngine {
	return &EtiquetteEngine{
		cache:     cache,
		fetcher:   fetcher,
		userAgent: userAgent,
	}
}

func (e *EtiquetteEngine) CanCrawl(ctx context.Context, rawURL string) (bool, time.Duration, error) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		log.Printf("Error parsing URL: %s", err)
		return false, 0, err
	}
	domain := parsedURL.Hostname()
	path := parsedURL.Path

	data, err := e.cache.Get(ctx, domain)
	if err != nil {
		log.Printf("Cache read failed for %s: %v", domain, err)
	}

	if data != nil && bytes.Equal(data, deniedMarker) {
		log.Printf("Robots disallowed for %s", domain)
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
				log.Printf("Access denied for %s: %v", domain, err)
				e.setCache(ctx, domain, deniedMarker, 2*time.Hour) // Cache denial for 2 hours
				return false, 0, nil
			}

			log.Printf("Transient error for %s: %v", domain, err)
			return false, 0, err
		}

		if data == nil {
			log.Printf("No robots.txt found for %s, allowing crawl", domain)
			e.setCache(ctx, domain, []byte{}, 24*time.Hour) // Cache empty result
			return true, 0, nil
		} else {
			e.setCache(ctx, domain, data, 24*time.Hour) // Cache valid robots.txt for 24 hours
		}
	}

	rules, err := parseRobotsTxt(data)
	if err != nil {
		log.Printf("Error parsing robots.txt for %s: %v", domain, err)
		return true, 0, nil
	}

	allowed := rules.TestAgent(e.userAgent, path)
	crawlDelay := rules.FindGroup(e.userAgent).CrawlDelay
	return allowed, crawlDelay, nil
}

func (e *EtiquetteEngine) setCache(ctx context.Context, domain string, data []byte, ttl time.Duration) {
	err := e.cache.Set(ctx, domain, data, ttl)
	if err != nil {
		log.Printf("Cache write failed for %s: %v", domain, err)
	}
}
