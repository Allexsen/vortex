package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"
	"vortex/internal/cooldown"
	httpFetcher "vortex/internal/fetcher"
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
	fetcher *httpFetcher.Fetcher

	taskTimeout time.Duration
	cooldownTTL time.Duration
}

func NewWorker(conn *amqp.Connection, limiter ratelimit.Limiter, queue cooldown.Queue,
	robots *robotstxt.EtiquetteEngine, fetcher *httpFetcher.Fetcher,
	taskTimeout time.Duration, cooldownTTL time.Duration) *Worker {
	return &Worker{
		conn:        conn,
		limiter:     limiter,
		queue:       queue,
		robots:      robots,
		fetcher:     fetcher,
		taskTimeout: taskTimeout,
		cooldownTTL: cooldownTTL,
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

	slog.Info("Worker started, waiting for messages")
	for msg := range msgs {
		slog.Info("Received a message", "body", msg.Body)

		ctx, cancel := context.WithTimeout(context.Background(), w.taskTimeout) // MUST CANCEL MANUALLY; DO NOT DEFER.
		err := w.processTask(ctx, msg.Body)
		cancel()

		if err == nil {
			msg.Ack(false)
			continue
		}

		if errors.Is(err, ErrTransient) {
			slog.Warn("Requeuing task", "error", err)
			msg.Nack(false, true)
			continue
		}

		slog.Error("Dropping task", "error", err)
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
		slog.Info("Robots.txt disallows URL", "task_id", task.TraceID, "url", task.URL)
		return nil
	}

	if crawlDelay > 0 {
		slog.Info("Respecting crawl delay", "task_id", task.TraceID, "delay", crawlDelay, "url", task.URL)
		time.Sleep(crawlDelay)
	}

	domain := parsedURL.Hostname()
	allowed, err = w.limiter.Allow(ctx, domain)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrTransient, err)
	}
	if !allowed {
		slog.Info("Rate limit exceeded for domain", "task_id", task.TraceID, "domain", domain)

		err = w.queue.Push(ctx, task, w.cooldownTTL)
		if err != nil {
			return fmt.Errorf("%w: failed to push to cooldown: %v", ErrTransient, err)
		}

		return nil
	}

	page, err := w.fetcher.Fetch(ctx, task.URL)
	if err != nil {
		var rateErr *httpFetcher.RateLimitedError
		if errors.As(err, &rateErr) {
			slog.Info("Rate limited when fetching URL", "task_id", task.TraceID, "url", task.URL, "error", err, "retry_after", rateErr.RetryAfter)
			err = w.queue.Push(ctx, task, rateErr.RetryAfter)
			if err != nil {
				return fmt.Errorf("%w: failed to push to cooldown: %v", ErrTransient, err)
			}
			return nil
		} else {
			var reqErr *httpFetcher.RequestError
			if errors.As(err, &reqErr) {
				slog.Warn("Request error for URL", "task_id", task.TraceID, "url", task.URL, "error", err)
				return nil // Don't retry on 4xx/5xx errors; just drop the task.
			}
		}

		return fmt.Errorf("%w: failed to fetch URL: %v", ErrTransient, err)
	}

	slog.Info("Successfully fetched URL", "task_id", task.TraceID, "url", task.URL, "size", len(page))

	return nil
}
