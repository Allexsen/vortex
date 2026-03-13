package worker

import (
	"context"
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
		urls, err := p.queue.PopExpired(ctx)
		logger.FailOnError(err, "Failed to fetch expired URLs from cooldown queue")

		for _, url := range urls {
			err = p.ch.PublishWithContext(ctx,
				"",                        // exchange
				"vortex:frontier:pending", // routing key
				false,                     // mandatory
				false,                     // immediate
				amqp.Publishing{
					ContentType: "text/plain",
					Body:        []byte(url),
				},
			)
			logger.FailOnError(err, "Failed to publish URL back to frontier")
		}

		time.Sleep(5 * time.Second)
	}
}
