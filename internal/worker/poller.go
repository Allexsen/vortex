package worker

import (
	"context"
	"encoding/json"
	"log"
	"time"
	"vortex/internal/cooldown"

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
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("Poller shutting down...")
			return
		case <-ticker.C:
			tasks, err := p.queue.PopExpired(ctx)
			if err != nil {
				log.Printf("[ERROR] Failed to pop expired tasks: %v", err)
				continue
			}

			for _, task := range tasks {
				taskJSON, err := json.Marshal(task)
				if err != nil {
					log.Printf("[ERROR] Failed to marshal task: %v", err)
					continue
				}

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
				if err != nil {
					log.Printf("[ERROR] Failed to publish task back to frontier: %v", err)
					continue
				}
			}
		}
	}
}
