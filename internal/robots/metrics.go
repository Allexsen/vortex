package robots

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	FetchTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "vortex",
		Subsystem: "robots",
		Name:      "fetch_total",
		Help:      "Total robots.txt fetch attempts.",
	}, []string{"outcome"})

	FetchDurationSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "vortex",
		Subsystem: "robots",
		Name:      "fetch_duration_seconds",
		Help:      "Time taken to fetch a robots.txt file.",
		Buckets:   []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
	})

	CacheResultTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "vortex",
		Subsystem: "robots",
		Name:      "cache_result_total",
		Help:      "Total cache results for robots.txt.",
	}, []string{"result"})

	InflightDedupTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "vortex",
		Subsystem: "robots",
		Name:      "inflight_dedup_total",
		Help:      "Times an inflight robots.txt fetch was reused instead of making a new request.",
	})

	CanCrawlTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "vortex",
		Subsystem: "robots",
		Name:      "can_crawl_total",
		Help:      "Outcomes of CanCrawl checks.",
	}, []string{"result"})
)
