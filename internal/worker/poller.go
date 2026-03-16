package worker

import (
	"context"
	"encoding/json"
	"time"
	"vortex/internal/cooldown"
	"vortex/internal/logger"

	amqp "github.com/rabbitmq/amqp091-go"
)

type Poller struct {
	queue cooldown.Queue
	ch    *amqp.Channel
}

func NewPoller(queue cooldown.Queue, ch *amqp.Channel) *Poller {
	return &Poller{
		queue: queue,
		ch:    ch,
	}
}

func (p *Poller) Start(ctx context.Context) {
	for {
		tasks, err := p.queue.PopExpired(ctx)
		logger.FailOnError(err, "Failed to fetch expired URLs from cooldown queue")

		for _, task := range tasks {
			taskJSON, err := json.Marshal(task)
			logger.FailOnError(err, "Failed to marshal task")

			err = p.ch.PublishWithContext(ctx,
				"",                        // exchange
				"vortex:frontier:pending", // routing key
				false,                     // mandatory
				false,                     // immediate
				amqp.Publishing{
					ContentType: "application/json",
					Body:        taskJSON,
				},
			)
			logger.FailOnError(err, "Failed to publish URL back to frontier")
		}

		time.Sleep(5 * time.Second)
	}
}
