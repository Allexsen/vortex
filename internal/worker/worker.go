package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"
	httpFetcher "vortex/internal/fetcher"
	"vortex/internal/models"
	"vortex/internal/parser"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

type Worker struct {
	id string

	conn        *amqp.Connection
	pauser      Pauser
	limiter     Limiter
	queue       CooldownQueue
	robots      EtiquetteEngine
	fetcher     Fetcher
	bloomFilter BloomFilter

	maxDepth       int
	maxRetries     int
	publishTimeout time.Duration
	redisTimeout   time.Duration
	taskTimeout    time.Duration
	cooldownTTL    time.Duration

	frontierQueue   string
	processingQueue string
}

func NewWorker(id string, conn *amqp.Connection, pauser Pauser, limiter Limiter, queue CooldownQueue,
	robots EtiquetteEngine, fetcher Fetcher, bloomFilter BloomFilter,
	maxDepth, maxRetries int,
	publishTimeout, redisTimeout, taskTimeout, cooldownTTL time.Duration,
	frontierQueue, processingQueue string) *Worker {
	return &Worker{
		id:              id,
		conn:            conn,
		pauser:          pauser,
		limiter:         limiter,
		queue:           queue,
		robots:          robots,
		fetcher:         fetcher,
		bloomFilter:     bloomFilter,
		maxDepth:        maxDepth,
		maxRetries:      maxRetries,
		publishTimeout:  publishTimeout,
		redisTimeout:    redisTimeout,
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

	slog.Info("Worker started, waiting for messages", "worker_id", w.id)
	for {
		select {
		case <-runCtx.Done():
			slog.Info("Worker received shutdown signal", "worker_id", w.id)
			return nil
		case msg, ok := <-msgs:
			if !ok {
				slog.Info("Message channel closed", "worker_id", w.id)
				return nil
			}

			w.pauser.WaitIfPaused(runCtx) // Check if we should pause processing before handling the message

			slog.Info("Received a message", "worker_id", w.id)

			ctx, cancel := context.WithTimeout(runCtx, w.taskTimeout) // MUST CANCEL MANUALLY; DO NOT DEFER.
			taskResult, err := w.processTask(ctx, msg.Body)
			cancel()

			if err != nil {
				if errors.Is(err, ErrTransient) {
					TasksProcessedTotal.WithLabelValues("transient_error").Inc()

					slog.Warn("Requeuing task", "worker_id", w.id, "error", err)
					taskResult.Task.Attempt++
					err = w.publish(runCtx, ch, w.frontierQueue, taskResult.Task)
					if err != nil {
						slog.Error("Failed to requeue task", "worker_id", w.id, "task_id", taskResult.Task.TraceID, "error", err)
					}
					msg.Ack(false)
					continue
				}

				TasksProcessedTotal.WithLabelValues("dropped").Inc()
				if taskResult != nil {
					slog.Warn("Dropping task due to permanent error", "worker_id", w.id, "task_id", taskResult.Task.TraceID, "url", taskResult.Task.URL, "error", err)
				} else {
					slog.Warn("Dropping task due to permanent error and failed to parse task details", "worker_id", w.id, "error", err)
				}
				msg.Nack(false, false)
				continue
			}

			TasksProcessedTotal.WithLabelValues("success").Inc()

			newTasks := taskResult.NewCrawlTasks
			slog.Info("Task completed successfully", "worker_id", w.id, "task_id", taskResult.Task.TraceID, "new_tasks", len(newTasks))

			if len(taskResult.Content) > 0 {
				err = w.publishCrawlResult(runCtx, ch, taskResult)
				if err != nil {
					slog.Error("Failed to publish crawl result", "worker_id", w.id, "task_id", taskResult.Task.TraceID, "error", err)
				}
			}

			for _, t := range newTasks {
				ctx, cancel := context.WithTimeout(runCtx, w.redisTimeout) // MUST CANCEL MANUALLY; DO NOT DEFER.
				ok, err := w.bloomFilter.CheckAndSet(ctx, t.URL)
				cancel()
				if err != nil {
					slog.Error("Failed to check bloom filter", "worker_id", w.id, "task_id", t.TraceID, "url", t.URL, "error", err)
					continue
				}

				if !ok {
					slog.Debug("URL already seen, skipping", "worker_id", w.id, "task_id", t.TraceID, "url", t.URL)
					continue
				}

				err = w.publish(runCtx, ch, w.frontierQueue, t)
				if err != nil {
					slog.Error("Failed to publish new task", "worker_id", w.id, "task_id", t.TraceID, "url", t.URL, "error", err)
					continue
				}
			}
			slog.Info("Finished processing message", "worker_id", w.id, "task_id", taskResult.Task.TraceID)
			msg.Ack(false)
		}
	}
}

func (w *Worker) processTask(ctx context.Context, body []byte) (*taskResult, error) {
	startProcess := time.Now()
	defer func() {
		PageProcessSeconds.Observe(time.Since(startProcess).Seconds())
	}()

	var task models.CrawlTask
	err := json.Unmarshal(body, &task)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPermanent, err)
	}

	taskResult := &taskResult{
		Task:          task,
		NewCrawlTasks: []models.CrawlTask{},
		Content:       "",
	}

	if task.Attempt >= w.maxRetries {
		slog.Warn("Max retry attempts reached, dropping task", "task_id", task.TraceID, "url", task.URL, "attempts", task.Attempt)
		return taskResult, nil
	}

	parsedURL, err := url.Parse(task.URL)
	if err != nil {
		return taskResult, fmt.Errorf("%w: %v", ErrPermanent, err)
	}

	allowed, crawlDelay, err := w.robots.CanCrawl(ctx, task.URL)
	if err != nil {
		return taskResult, fmt.Errorf("%w: %v", ErrTransient, err)
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
		return taskResult, fmt.Errorf("%w: %v", ErrTransient, err)
	}
	if !allowed {
		slog.Info("Rate limit exceeded for domain", "task_id", task.TraceID, "domain", domain)

		err = w.queue.Push(ctx, task, w.cooldownTTL)
		if err != nil {
			return taskResult, fmt.Errorf("%w: failed to push to cooldown: %v", ErrTransient, err)
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
				return taskResult, fmt.Errorf("%w: failed to push to cooldown: %v", ErrTransient, err)
			}

			return taskResult, nil
		} else {
			var reqErr *httpFetcher.RequestError
			if errors.As(err, &reqErr) {
				slog.Warn("Request error for URL", "task_id", task.TraceID, "url", task.URL, "error", err)
				return taskResult, nil // Don't retry on 4xx/5xx errors; just drop the task.
			}
		}

		slog.Warn("Failed to fetch URL", "task_id", task.TraceID, "url", task.URL, "error", err)
		return taskResult, fmt.Errorf("%w: failed to fetch URL: %v", ErrTransient, err)
	}
	PagesCrawledTotal.Inc()
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
		return taskResult, fmt.Errorf("%w: failed to extract URLs: %v", ErrTransient, err)
	}
	slog.Info("Extracted URLs", "task_id", task.TraceID, "count", len(rawURLs))

	taskResult.NewCrawlTasks = buildNewTasks(rawURLs, task.TraceID, task.Depth)
	slog.Info("Built new crawl tasks", "task_id", task.TraceID, "new_tasks", len(taskResult.NewCrawlTasks))

	return taskResult, nil
}

func (w *Worker) publishCrawlResult(runCtx context.Context, ch *amqp.Channel, taskResult *taskResult) error {
	crawlResult := models.CrawlResult{
		TraceID:   taskResult.Task.TraceID,
		URL:       taskResult.Task.URL,
		Content:   taskResult.Content,
		CreatedAt: time.Now(),
	}

	return w.publish(runCtx, ch, w.processingQueue, crawlResult)
}

func (w *Worker) publish(runCtx context.Context, ch *amqp.Channel, queue string, payload any) error {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	ctx, cancel := context.WithTimeout(runCtx, w.publishTimeout)
	defer cancel()
	err = ch.PublishWithContext(ctx,
		"",    // exchange
		queue, // routing key
		false, // mandatory
		false, // immediate
		amqp.Publishing{
			DeliveryMode: amqp.Persistent,
			ContentType:  "application/json",
			Body:         payloadJSON,
		},
	)

	if err != nil {
		return fmt.Errorf("failed to publish message: %w", err)
	}

	return nil
}

func buildNewTasks(rawURLs []string, parentTraceID string, depth int) []models.CrawlTask {
	var urls []string
	for _, u := range rawURLs {
		sanitized, ok := parser.SanitizeURL(u)
		if !ok {
			slog.Debug("Skipping invalid URL", "task_id", parentTraceID, "url", u)
			continue
		}

		urls = append(urls, sanitized)
	}

	var newTasks []models.CrawlTask
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
			Depth:      depth + 1,
		}

		newTasks = append(newTasks, newTask)
	}

	URLsDiscoveredTotal.Add(float64(len(newTasks)))
	return newTasks
}
