package worker

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
	"vortex/internal/cooldown"
	"vortex/internal/keys"

	amqp "github.com/rabbitmq/amqp091-go"
)

type Poller struct {
	queue cooldown.Queue
	conn  *amqp.Connection

	pollerInterval time.Duration
}

func NewPoller(queue cooldown.Queue, conn *amqp.Connection, pollerInterval time.Duration) *Poller {
	return &Poller{
		queue:          queue,
		conn:           conn,
		pollerInterval: pollerInterval,
	}
}

func (p *Poller) Run(ctx context.Context) {
	ticker := time.NewTicker(p.pollerInterval)
	defer ticker.Stop()

	ch, err := p.conn.Channel()
	if err != nil {
		slog.Error("Failed to open channel", "error", err)
		return
	}
	defer ch.Close()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Poller shutting down...")
			return
		case <-ticker.C:
			tasks, err := p.queue.PopExpired(ctx)
			if err != nil {
				slog.Error("Failed to pop expired tasks", "error", err)
				continue
			}

			for _, task := range tasks {
				taskJSON, err := json.Marshal(task)
				if err != nil {
					slog.Error("Failed to marshal task", "task_id", task.TraceID, "error", err)
					continue
				}

				err = ch.PublishWithContext(ctx,
					"",                 // exchange
					keys.FrontierQueue, // routing key
					false,              // mandatory
					false,              // immediate
					amqp.Publishing{
						ContentType: "application/json",
						Body:        taskJSON,
					},
				)
				if err != nil {
					slog.Error("Failed to publish task back to frontier", "task_id", task.TraceID, "error", err)
					continue
				}
			}
		}
	}
}
