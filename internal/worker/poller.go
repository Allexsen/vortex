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
	conn  *amqp.Connection
}

func NewPoller(queue cooldown.Queue, conn *amqp.Connection) *Poller {
	return &Poller{
		queue: queue,
		conn:  conn,
	}
}

func (p *Poller) Run(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	ch, err := p.conn.Channel()
	if err != nil {
		log.Fatalf("[FATAL] Failed to open channel: %v", err)
	}
	defer ch.Close()

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

				err = ch.PublishWithContext(ctx,
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
