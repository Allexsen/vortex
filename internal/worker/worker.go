package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"time"
	"vortex/internal/cooldown"
	"vortex/internal/models"
	"vortex/internal/ratelimit"

	amqp "github.com/rabbitmq/amqp091-go"
)

type Worker struct {
	ch      *amqp.Channel
	limiter ratelimit.Limiter
	Queue   cooldown.Queue
}

func NewWorker(ch *amqp.Channel, limiter ratelimit.Limiter, queue cooldown.Queue) *Worker {
	return &Worker{
		ch:      ch,
		limiter: limiter,
		Queue:   queue,
	}
}

func (w *Worker) PrepareStream(queueName string) (<-chan amqp.Delivery, error) {
	q, err := w.ch.QueueDeclare(
		queueName, // name
		true,      // durable
		false,     // delete when unused
		false,     // exclusive
		false,     // no-wait
		nil,       // arguments
	)
	if err != nil {
		return nil, err
	}
	msgs, err := w.ch.Consume(
		q.Name, // queue
		"",     // consumer
		false,  // auto-ack
		false,  // exclusive
		false,  // no-local
		false,  // no-wait
		nil,    // args
	)
	if err != nil {
		return nil, err
	}
	return msgs, nil
}

func (w *Worker) Process(msgs <-chan amqp.Delivery) {
	log.Println(" [*] Waiting for messages. To exit press CTRL+C")
	for msg := range msgs {
		log.Printf("Received a message: %s", msg.Body)

		var task models.CrawlTask
		err := json.Unmarshal(msg.Body, &task)
		if err != nil {
			HandleError(fmt.Errorf("%w: %v", ErrPermanent, err), msg)
			continue
		}

		parsedURL, err := url.Parse(task.URL)
		if err != nil {
			HandleError(fmt.Errorf("%w: %v", ErrPermanent, err), msg)
			continue
		}

		domain := parsedURL.Hostname()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second) // MUST CANCEL MANUALLY; DO NOT DEFER.
		allowed, err := w.limiter.Allow(ctx, domain)
		cancel()
		if err != nil {
			HandleError(fmt.Errorf("%w: %v", ErrPermanent, err), msg)
			continue
		}

		if !allowed {
			log.Printf("Rate limit exceeded for domain %s, delaying URL", domain)

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second) // MUST CANCEL MANUALLY; DO NOT DEFER.
			err = w.Queue.Push(ctx, task)
			cancel()
			if err != nil {
				HandleError(fmt.Errorf("%w: %v", ErrPermanent, err), msg)
				continue
			}
			msg.Ack(false)
			continue
		}

		time.Sleep(1 * time.Second) // Simulate work
		msg.Ack(false)
	}
}
