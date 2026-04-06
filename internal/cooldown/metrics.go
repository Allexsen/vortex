package cooldown

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	CooldownPushesTotal = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "vortex",
		Subsystem: "cooldown",
		Name:      "pushes_total",
		Help:      "Total tasks pushed to cooldown queue.",
	})
)
