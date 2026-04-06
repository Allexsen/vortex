package fetcher

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	FetchErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "vortex",
		Subsystem: "fetcher",
		Name:      "errors_total",
		Help:      "Total fetch errors, labeled by error type.",
	}, []string{"error_type"})

	FetchLatencySeconds = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "vortex",
		Subsystem: "fetcher",
		Name:      "latency_seconds",
		Help:      "Latency of fetch operations in seconds.",
		Buckets:   []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	})
)
