package main

import (
	"context"
	"fmt"
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
	const logDir = "logs"
	logger, cleanupFunc, err := infra.SetupLogger(logDir)
	if err != nil {
		logger.Error("Failed to set up logger", "error", err)
		os.Exit(1)
	}
	defer cleanupFunc()

	if err := godotenv.Load(); err != nil {
		logger.Warn("No .env file found, using environment variables")
	}

	cfg, err := config.Load()
	if err != nil {
		logger.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	conn, ch, err := infra.SetupRabbitMQ(cfg.RabbitMQ.URL)
	if err != nil {
		logger.Error("Failed to set up RabbitMQ", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			logger.Error("Failed to gracefully close RabbitMQ connection", "error", err)
		}
	}()
	defer func() {
		if err := ch.Close(); err != nil {
			logger.Error("Failed to gracefully close RabbitMQ channel", "error", err)
		}
	}()

	err = infra.DeclareWithDLQ(ch, keys.FrontierQueue, keys.FrontierDLQ, keys.FrontierDLQRoutingKey, keys.DeadLetterExchange)
	if err != nil {
		logger.Error("Failed to declare frontier queue with DLQ", "error", err)
		os.Exit(1)
	}

	err = infra.DeclareWithDLQ(ch, keys.ProcessingQueue, keys.ProcessingDLQ, keys.ProcessingDLQRoutingKey, keys.DeadLetterExchange)
	if err != nil {
		logger.Error("Failed to declare processing queue with DLQ", "error", err)
		os.Exit(1)
	}

	rdb, err := infra.SetupRedis(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB, cfg.Redis.PoolSize, cfg.Worker.RedisTimeout)
	if err != nil {
		logger.Error("Failed to set up Redis", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := rdb.Close(); err != nil {
			logger.Error("Failed to gracefully close Redis client", "error", err)
		}
	}()

	limiter := ratelimit.NewRedisLimiter(rdb, keys.RateLimitPrefix, cfg.Crawler.RateLimit, cfg.Crawler.RateLimitWindow)
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
	fetcher := httpFetcher.NewFetcher(httpClientWithTimeout, cfg.Fetcher.UserAgent)
	bloomFilter := cache.NewBloomFilter(rdb, keys.SeenBloomFilter)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	wg := &sync.WaitGroup{}
	infra.StartMetricsServer(ctx, wg, cfg.Worker.MetricsPort)

	wg.Add(cfg.Worker.Count)
	for i := 1; i <= cfg.Worker.Count; i++ {
		go func() {
			defer wg.Done()
			w := worker.NewWorker(fmt.Sprintf("worker-%d", i),
				conn, limiter, queue, robots, fetcher, bloomFilter,
				cfg.Crawler.MaxDepth, cfg.Worker.MaxRetries,
				cfg.Crawler.PublishTimeout, cfg.Worker.RedisTimeout, cfg.Worker.TaskTimeout, cfg.Crawler.CooldownTTL,
				keys.FrontierQueue, keys.ProcessingQueue,
			)
			if err := w.Run(ctx); err != nil {
				logger.Error("Worker encountered an error", "error", err)
			} else {
				logger.Info("Worker stopped gracefully")
			}
		}()
	}

	wg.Add(1)
	p := worker.NewPoller(queue, conn, cfg.Crawler.PollerInterval)
	go func() {
		defer wg.Done()
		p.Run(ctx)
		logger.Info("Poller stopped gracefully")

	}()

	logger.Info("Waiting for messages. To exit press CTRL+C")

	wg.Wait()
	logger.Info("All workers stopped, exiting")
}
