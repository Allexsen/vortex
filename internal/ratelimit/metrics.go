package ratelimit

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	LimitedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "vortex",
		Subsystem: "ratelimit",
		Name:      "limited_total",
		Help:      "Total tasks rejected due to rate limiting.",
	})
)
