package worker

import (
	"context"
	"encoding/json"
	"errors"
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

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second) // MUST CANCEL MANUALLY; DO NOT DEFER.
		err := w.processTask(ctx, msg.Body)
		cancel()

		if err == nil {
			msg.Ack(false)
			continue
		}

		if errors.Is(err, ErrTransient) {
			log.Printf("[TRANSIENT] Requeuing task: %v", err)
			msg.Nack(false, true)
			continue
		}

		log.Printf("[PERMANENT] Dropping task: %v", err)
		msg.Ack(false)
	}
}

func (w *Worker) processTask(ctx context.Context, body []byte) error {
	var task models.CrawlTask
	err := json.Unmarshal(body, &task)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPermanent, err)
	}

	parsedURL, err := url.Parse(task.URL)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPermanent, err)
	}

	domain := parsedURL.Hostname()
	allowed, err := w.limiter.Allow(ctx, domain)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrTransient, err)
	}
	if !allowed {
		log.Printf("Rate limit exceeded for domain %s, pushing to cooldown", domain)

		err = w.Queue.Push(ctx, task)
		if err != nil {
			return fmt.Errorf("%w: failed to push to cooldown: %v", ErrTransient, err)
		}

		return nil
	}

	time.Sleep(1 * time.Second) // Simulate work
	return nil
}
