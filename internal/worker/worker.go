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
	robotstxt "vortex/internal/robots"

	amqp "github.com/rabbitmq/amqp091-go"
)

type Worker struct {
	conn    *amqp.Connection
	limiter ratelimit.Limiter
	queue   cooldown.Queue
	robots  *robotstxt.EtiquetteEngine

	taskTimeout time.Duration
}

func NewWorker(conn *amqp.Connection, limiter ratelimit.Limiter, queue cooldown.Queue,
	robots *robotstxt.EtiquetteEngine, taskTimeout time.Duration) *Worker {
	return &Worker{
		conn:        conn,
		limiter:     limiter,
		queue:       queue,
		robots:      robots,
		taskTimeout: taskTimeout,
	}
}

func (w *Worker) Run(ctx context.Context, queueName string) error {
	ch, err := w.conn.Channel()
	if err != nil {
		return fmt.Errorf("failed to open channel: %w", err)
	}
	defer ch.Close()

	err = ch.Qos(1, 0, false)
	if err != nil {
		return fmt.Errorf("failed to set QoS: %w", err)
	}

	q, err := ch.QueueDeclare(
		queueName, // name
		true,      // durable
		false,     // delete when unused
		false,     // exclusive
		false,     // no-wait
		nil,       // arguments
	)
	if err != nil {
		return fmt.Errorf("failed to declare queue: %w", err)
	}

	msgs, err := ch.Consume(
		q.Name, // queue
		"",     // consumer
		false,  // auto-ack
		false,  // exclusive
		false,  // no-local
		false,  // no-wait
		nil,    // args
	)
	if err != nil {
		return fmt.Errorf("failed to start consuming: %w", err)
	}

	log.Println(" [*] Worker started, waiting for messages")
	for msg := range msgs {
		log.Printf("Received a message: %s", msg.Body)

		ctx, cancel := context.WithTimeout(context.Background(), w.taskTimeout) // MUST CANCEL MANUALLY; DO NOT DEFER.
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

	return nil
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

	allowed, crawlDelay, err := w.robots.CanCrawl(ctx, task.URL)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrTransient, err)
	}

	if !allowed {
		log.Printf("Robots.txt disallows %s", task.URL)
		return nil
	}

	if crawlDelay > 0 {
		log.Printf("Respecting crawl delay of %v for %s", crawlDelay, task.URL)
		time.Sleep(crawlDelay)
	}

	domain := parsedURL.Hostname()
	allowed, err = w.limiter.Allow(ctx, domain)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrTransient, err)
	}
	if !allowed {
		log.Printf("Rate limit exceeded for domain %s, pushing to cooldown", domain)

		err = w.queue.Push(ctx, task)
		if err != nil {
			return fmt.Errorf("%w: failed to push to cooldown: %v", ErrTransient, err)
		}

		return nil
	}

	time.Sleep(1 * time.Second) // Simulate work
	return nil
}
