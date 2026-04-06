package worker

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	PagesCrawledTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "vortex",
		Subsystem: "worker",
		Name:      "pages_crawled_total",
		Help:      "Total pages successfully fetched.",
	})

	URLsDiscoveredTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "vortex",
		Subsystem: "worker",
		Name:      "urls_discovered_total",
		Help:      "Total unique URLs discovered.",
	})

	PageProcessSeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "vortex",
		Subsystem: "worker",
		Name:      "page_process_seconds",
		Help:      "Time taken to process a page in seconds.",
		Buckets:   []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	})

	TasksProcessedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "vortex",
		Subsystem: "worker",
		Name:      "tasks_processed_total",
		Help:      "Total tasks processed, labeled by status.",
	}, []string{"status"})

	CooldownRepublishedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "vortex",
		Subsystem: "worker",
		Name:      "cooldown_republished_total",
		Help:      "Total tasks republished from cooldown queue.",
	})
)
