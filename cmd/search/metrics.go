package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	SearchRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "vortex",
			Subsystem: "search",
			Name:      "requests_total",
			Help:      "Total number of search requests",
		},
		[]string{"status"},
	)

	SearchRequestDurationSeconds = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "vortex",
			Subsystem: "search",
			Name:      "request_duration_seconds",
			Help:      "Duration of search requests in seconds",
			Buckets:   []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
		},
	)

	SearchEmbedDurationSeconds = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "vortex",
			Subsystem: "search",
			Name:      "embed_duration_seconds",
			Help:      "Duration of embedding operations in seconds",
			Buckets:   []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
		},
	)
)
