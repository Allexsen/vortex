package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"
	"vortex/internal/cache"
	"vortex/internal/cooldown"
	httpFetcher "vortex/internal/fetcher"
	"vortex/internal/models"
	"vortex/internal/parser"
	"vortex/internal/ratelimit"
	robotstxt "vortex/internal/robots"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

type Worker struct {
	conn        *amqp.Connection
	limiter     ratelimit.Limiter
	queue       cooldown.Queue
	robots      *robotstxt.EtiquetteEngine
	fetcher     *httpFetcher.Fetcher
	bloomFilter *cache.BloomFilter

	maxDepth       int
	publishTimeout time.Duration
	taskTimeout    time.Duration
	cooldownTTL    time.Duration

	frontierQueue   string
	processingQueue string
}

func NewWorker(conn *amqp.Connection, limiter ratelimit.Limiter, queue cooldown.Queue,
	robots *robotstxt.EtiquetteEngine, fetcher *httpFetcher.Fetcher, bloomFilter *cache.BloomFilter,
	maxDepth int, publishTimeout time.Duration, taskTimeout time.Duration, cooldownTTL time.Duration,
	frontierQueue string, processingQueue string) *Worker {
	return &Worker{
		conn:            conn,
		limiter:         limiter,
		queue:           queue,
		robots:          robots,
		fetcher:         fetcher,
		bloomFilter:     bloomFilter,
		maxDepth:        maxDepth,
		publishTimeout:  publishTimeout,
		taskTimeout:     taskTimeout,
		cooldownTTL:     cooldownTTL,
		frontierQueue:   frontierQueue,
		processingQueue: processingQueue,
	}
}

func (w *Worker) Run(runCtx context.Context) error {
	ch, err := w.conn.Channel()
	if err != nil {
		return fmt.Errorf("failed to open channel: %w", err)
	}
	defer ch.Close()

	err = ch.Qos(1, 0, false)
	if err != nil {
		return fmt.Errorf("failed to set QoS: %w", err)
	}

	msgs, err := ch.Consume(
		w.frontierQueue, // queue
		"",              // consumer
		false,           // auto-ack
		false,           // exclusive
		false,           // no-local
		false,           // no-wait
		nil,             // args
	)
	if err != nil {
		return fmt.Errorf("failed to start consuming: %w", err)
	}

	slog.Info("Worker started, waiting for messages")
	for msg := range msgs {
		slog.Info("Received a message", "body", msg.Body)

		ctx, cancel := context.WithTimeout(runCtx, w.taskTimeout) // MUST CANCEL MANUALLY; DO NOT DEFER.
		taskResult, err := w.processTask(ctx, msg.Body)
		cancel()

		if err == nil {
			newTasks := taskResult.CrawlTasks
			slog.Info("Task completed successfully", "task_id", taskResult.TraceID, "new_tasks", len(newTasks))

			if len(taskResult.Content) > 0 {
				ctx, cancel := context.WithTimeout(runCtx, w.publishTimeout) // MUST CANCEL MANUALLY; DO NOT DEFER.
				err = w.publishCrawlResult(ctx, ch, taskResult)
				cancel()
				if err != nil {
					slog.Error("Failed to publish crawl result", "task_id", taskResult.TraceID, "error", err)
				}
			}

			for _, t := range newTasks {
				ctx, cancel := context.WithTimeout(runCtx, 5*time.Second) // MUST CANCEL MANUALLY; DO NOT DEFER.
				ok, err := w.bloomFilter.CheckAndSet(ctx, t.URL)
				cancel()
				if err != nil {
					slog.Error("Failed to check bloom filter", "task_id", t.TraceID, "url", t.URL, "error", err)
					continue
				}

				if !ok {
					slog.Debug("URL already seen, skipping", "task_id", t.TraceID, "url", t.URL)
					continue
				}

				taskJSON, err := json.Marshal(t)
				if err != nil {
					slog.Error("Failed to marshal new task", "task_id", t.TraceID, "error", err)
					continue
				}

				ctx, cancel = context.WithTimeout(runCtx, w.publishTimeout) // MUST CANCEL MANUALLY; DO NOT DEFER.
				err = ch.PublishWithContext(ctx,
					"",              // exchange
					w.frontierQueue, // routing key
					false,           // mandatory
					false,           // immediate
					amqp.Publishing{
						ContentType: "application/json",
						Body:        taskJSON,
					},
				)
				cancel()

				if err != nil {
					slog.Error("Failed to publish new task", "task_id", t.TraceID, "error", err)
					continue
				}
			}
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

func (w *Worker) processTask(ctx context.Context, body []byte) (*taskResult, error) {
	var task models.CrawlTask
	err := json.Unmarshal(body, &task)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPermanent, err)
	}

	taskResult := &taskResult{
		TraceID:    task.TraceID,
		TaskURL:    task.URL,
		CrawlTasks: []models.CrawlTask{},
		Content:    "",
	}

	parsedURL, err := url.Parse(task.URL)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPermanent, err)
	}

	allowed, crawlDelay, err := w.robots.CanCrawl(ctx, task.URL)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTransient, err)
	}

	if !allowed {
		slog.Info("Robots.txt disallows URL", "task_id", task.TraceID, "url", task.URL)
		return taskResult, nil
	}

	if crawlDelay > 0 {
		slog.Info("Respecting crawl delay", "task_id", task.TraceID, "delay", crawlDelay, "url", task.URL)
		time.Sleep(crawlDelay)
	}

	domain := parsedURL.Hostname()
	allowed, err = w.limiter.Allow(ctx, domain)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrTransient, err)
	}
	if !allowed {
		slog.Info("Rate limit exceeded for domain", "task_id", task.TraceID, "domain", domain)

		err = w.queue.Push(ctx, task, w.cooldownTTL)
		if err != nil {
			return nil, fmt.Errorf("%w: failed to push to cooldown: %v", ErrTransient, err)
		}

		return taskResult, nil
	}

	page, err := w.fetcher.Fetch(ctx, task.URL)
	if err != nil {
		var rateErr *httpFetcher.RateLimitedError
		if errors.As(err, &rateErr) {
			slog.Info("Rate limited when fetching URL", "task_id", task.TraceID, "url", task.URL, "error", err, "retry_after", rateErr.RetryAfter)
			err = w.queue.Push(ctx, task, rateErr.RetryAfter)
			if err != nil {
				return nil, fmt.Errorf("%w: failed to push to cooldown: %v", ErrTransient, err)
			}
			return taskResult, nil
		} else {
			var reqErr *httpFetcher.RequestError
			if errors.As(err, &reqErr) {
				slog.Warn("Request error for URL", "task_id", task.TraceID, "url", task.URL, "error", err)
				return taskResult, nil // Don't retry on 4xx/5xx errors; just drop the task.
			}
		}

		return nil, fmt.Errorf("%w: failed to fetch URL: %v", ErrTransient, err)
	}
	slog.Info("Successfully fetched URL", "task_id", task.TraceID, "url", task.URL, "size", len(page))

	taskResult.Content, err = parser.ExtractText(page)
	if err != nil {
		slog.Warn("Failed to extract text content", "task_id", task.TraceID, "url", task.URL, "error", err)
		taskResult.Content = ""
	} else {
		slog.Info("Extracted text content", "task_id", task.TraceID, "url", task.URL, "content_length", len(taskResult.Content))
	}

	if task.Depth >= w.maxDepth {
		slog.Info("Max crawl depth reached, not extracting URLs", "task_id", task.TraceID, "url", task.URL, "depth", task.Depth)
		return taskResult, nil
	}

	rawURLs, err := parser.ExtractURLs(page, task.URL)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to extract URLs: %v", ErrTransient, err)
	}
	slog.Info("Extracted URLs", "task_id", task.TraceID, "count", len(rawURLs))

	var urls []string
	for _, u := range rawURLs {
		sanitized, ok := parser.SanitizeURL(u)
		if !ok {
			slog.Debug("Skipping invalid URL", "task_id", task.TraceID, "url", u)
			continue
		}

		urls = append(urls, sanitized)
	}

	seen := make(map[string]struct{})
	for _, u := range urls {
		if _, exists := seen[u]; exists {
			continue
		}

		seen[u] = struct{}{}

		newTask := models.CrawlTask{
			TraceID:    uuid.New().String(),
			URL:        u,
			Attempt:    0,
			EnqueuedAt: time.Now(),
			Depth:      task.Depth + 1,
		}

		taskResult.CrawlTasks = append(taskResult.CrawlTasks, newTask)
	}

	return taskResult, nil
}

func (w *Worker) publishCrawlResult(ctx context.Context, ch *amqp.Channel, taskResult *taskResult) error {
	crawlResult := models.CrawlResult{
		TraceID:   taskResult.TraceID,
		URL:       taskResult.TaskURL,
		Content:   taskResult.Content,
		CreatedAt: time.Now(),
	}

	resultJSON, err := json.Marshal(crawlResult)
	if err != nil {
		return fmt.Errorf("failed to marshal crawl result: %w", err)
	}

	err = ch.PublishWithContext(
		ctx,
		"",
		w.processingQueue,
		false,
		false,
		amqp.Publishing{
			ContentType: "application/json",
			Body:        resultJSON,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to publish crawl result: %w", err)
	}

	return nil
}
