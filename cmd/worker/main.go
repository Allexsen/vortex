package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"vortex/internal/cache"
	"vortex/internal/config"
	"vortex/internal/cooldown"
	httpFetcher "vortex/internal/fetcher"
	"vortex/internal/infra"
	"vortex/internal/keys"
	"vortex/internal/ratelimit"
	robotstxt "vortex/internal/robots"
	"vortex/internal/worker"

	"github.com/joho/godotenv"
)

func main() {
	cleanupFunc, err := infra.SetupLogger("worker")
	if err != nil {
		slog.Error("Failed to set up logger", "error", err)
		os.Exit(1)
	}
	defer cleanupFunc()

	if err := godotenv.Load(); err != nil {
		slog.Warn("No .env file found, using environment variables")
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	conn, ch, err := infra.SetupRabbitMQ(cfg.RabbitMQ.URL, cfg.RabbitMQ.MaxRetries, cfg.RabbitMQ.RetryDelay)
	if err != nil {
		slog.Error("Failed to set up RabbitMQ", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			slog.Error("Failed to gracefully close RabbitMQ connection", "error", err)
		}
	}()
	defer func() {
		if err := ch.Close(); err != nil {
			slog.Error("Failed to gracefully close RabbitMQ channel", "error", err)
		}
	}()

	err = infra.DeclareWithDLQ(ch, keys.FrontierQueue, keys.FrontierDLQ, keys.FrontierDLQRoutingKey, keys.DeadLetterExchange)
	if err != nil {
		slog.Error("Failed to declare frontier queue with DLQ", "error", err)
		os.Exit(1)
	}

	err = infra.DeclareWithDLQ(ch, keys.ProcessingQueue, keys.ProcessingDLQ, keys.ProcessingDLQRoutingKey, keys.DeadLetterExchange)
	if err != nil {
		slog.Error("Failed to declare processing queue with DLQ", "error", err)
		os.Exit(1)
	}

	rdb, err := infra.SetupRedis(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB, cfg.Redis.PoolSize,
		cfg.Worker.RedisTimeout, cfg.Redis.MaxRetries, cfg.Redis.RetryDelay)
	if err != nil {
		slog.Error("Failed to set up Redis", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := rdb.Close(); err != nil {
			slog.Error("Failed to gracefully close Redis client", "error", err)
		}
	}()

	limiter := ratelimit.NewRedisLimiter(rdb, keys.RateLimitPrefix, cfg.Worker.RateLimit, cfg.Worker.RateBurst)
	queue := cooldown.NewRedisQueue(rdb, keys.CooldownQueue)

	httpClient := &http.Client{Timeout: cfg.Robots.HTTPTimeout}
	robotsFetcher := robotstxt.NewFetcher(httpClient, cfg.Robots.HTTPUserAgent)
	robotsCache := cache.NewRedisCache(rdb, keys.RobotsCachePrefix)
	robots := robotstxt.NewEtiquetteEngine(
		robotsCache,
		robotsFetcher,
		cfg.Robots.UserAgent,
		cfg.Robots.CacheTTL,
		cfg.Robots.DeniedCacheTTL,
	)

	httpClientWithTimeout := &http.Client{Timeout: cfg.Fetcher.Timeout}
	fetcher := httpFetcher.NewFetcher(httpClientWithTimeout, cfg.Fetcher.UserAgent, cfg.Fetcher.MaxBodySize, cfg.Fetcher.RetryAfter)
	bloomFilter := cache.NewBloomFilter(rdb, keys.SeenBloomFilter)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	wg := &sync.WaitGroup{}
	infra.StartMetricsServer(ctx, wg, cfg.Worker.MetricsPort)

	manager := worker.NewManager(
		rdb, conn, keys.ControlCrawler, keys.FrontierQueue, keys.ProcessingQueue,
		cfg.Worker.RedisTimeout, cfg.Manager.PollInterval,
		cfg.Manager.ProcessingPauseAt, cfg.Manager.ProcessingResumeAt,
	)

	wg.Add(1)
	go func() {
		defer wg.Done()
		manager.Run(ctx)
		slog.Info("Manager stopped gracefully")
	}()

	wg.Add(cfg.Worker.Count)
	for i := 1; i <= cfg.Worker.Count; i++ {
		go func() {
			defer wg.Done()
			w := worker.NewWorker(fmt.Sprintf("worker-%d", i),
				conn, manager, limiter, queue, robots, fetcher, bloomFilter,
				cfg.Worker.MaxDepth, cfg.Worker.MaxRetries,
				cfg.Worker.PublishTimeout, cfg.Worker.RedisTimeout, cfg.Worker.TaskTimeout, cfg.Worker.CooldownTTL,
				keys.FrontierQueue, keys.ProcessingQueue,
			)
			if err := w.Run(ctx); err != nil {
				slog.Error("Worker encountered an error", "error", err)
			} else {
				slog.Info("Worker stopped gracefully")
			}
		}()
	}

	wg.Add(1)
	p := worker.NewPoller(queue, conn, manager, cfg.Worker.PollerInterval)
	go func() {
		defer wg.Done()
		p.Run(ctx)
		slog.Info("Poller stopped gracefully")

	}()

	slog.Info("Waiting for messages. To exit press CTRL+C")

	wg.Wait()
	slog.Info("All workers stopped, exiting")
}
