package worker

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
)

type RedisClient interface {
	Get(ctx context.Context, key string) *redis.StringCmd
}

type Manager struct {
	gate atomic.Pointer[chan struct{}]

	rdb          RedisClient
	redisTimeout time.Duration

	conn               *amqp.Connection
	controlKey         string
	frontierQueue      string
	processingQueue    string
	pollInterval       time.Duration
	processingPauseAt  int
	processingResumeAt int
}

func NewManager(rdb RedisClient, conn *amqp.Connection,
	controlKey, frontierQueue, processingQueue string,
	redisTimeout, pollInterval time.Duration,
	processingPauseAt, processingResumeAt int) *Manager {
	return &Manager{
		rdb:                rdb,
		redisTimeout:       redisTimeout,
		conn:               conn,
		controlKey:         controlKey,
		frontierQueue:      frontierQueue,
		processingQueue:    processingQueue,
		pollInterval:       pollInterval,
		processingPauseAt:  processingPauseAt,
		processingResumeAt: processingResumeAt,
	}
}

func (m *Manager) Run(ctx context.Context) {
	ticker := time.NewTicker(m.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.tick(ctx)
		}
	}
}

func (m *Manager) WaitIfPaused(ctx context.Context) {
	gate := m.gate.Load()
	if gate == nil {
		return // Not paused
	}
	select {
	case <-*gate:
		return // Resumed
	case <-ctx.Done():
		return // Context cancelled
	}
}

func (m *Manager) tick(ctx context.Context) {
	// Check for control messages
	redisCtx, cancel := context.WithTimeout(ctx, m.redisTimeout)
	defer cancel()

	val, err := m.rdb.Get(redisCtx, m.controlKey).Result()
	if err != nil && err != redis.Nil {
		slog.Error("manager: failed to read control key", "error", err)
		return
	}

	switch val {
	case "pause":
		if m.gate.Load() == nil {
			m.pause("manual")
			slog.Info("manager: paused (manual control)")
		}
	case "resume":
		if m.gate.Load() != nil {
			m.resume("manual")
			slog.Info("manager: resumed (manual control)")
		}
	default:
		if val != "" {
			slog.Warn("manager: unknown control command; defaulting to auto mode", "command", val)
		}
		// No manual control, switch based on queue length
		ch, err := m.conn.Channel()
		if err != nil {
			slog.Error("manager: failed to open AMQP channel", "error", err)
			return
		}
		defer ch.Close()

		qFrontier, err := ch.QueueDeclarePassive(m.frontierQueue, true, false, false, false, nil)
		if err != nil {
			slog.Error("manager: frontier queue inspect failed", "error", err)
			return
		}

		qProcessing, err := ch.QueueDeclarePassive(m.processingQueue, true, false, false, false, nil)
		if err != nil {
			slog.Error("manager: processing queue inspect failed", "error", err)
			return
		}

		frontierDepth := qFrontier.Messages
		FrontierQueueDepth.Set(float64(frontierDepth))

		processingDepth := qProcessing.Messages
		ProcessingQueueDepth.Set(float64(processingDepth))

		paused := m.gate.Load() != nil

		switch {
		case !paused && processingDepth >= m.processingPauseAt:
			m.pause("auto")
			slog.Info("manager: paused (auto)", "depth", processingDepth, "threshold", m.processingPauseAt)
		case paused && processingDepth <= m.processingResumeAt:
			m.resume("auto")
			slog.Info("manager: resumed (auto)", "depth", processingDepth, "threshold", m.processingResumeAt)
		}
	}
}

// NOT THREAD-SAFE: Call only from the manager's main loop
func (m *Manager) pause(reason string) {
	ManagerThrottleTotal.WithLabelValues("pause", reason).Inc()
	ManagerPaused.Set(1)

	gate := make(chan struct{})
	m.gate.Store(&gate)
}

func (m *Manager) resume(reason string) {
	ManagerThrottleTotal.WithLabelValues("resume", reason).Inc()
	ManagerPaused.Set(0)

	oldGate := m.gate.Swap(nil)
	if oldGate != nil {
		close(*oldGate)
	}
}
