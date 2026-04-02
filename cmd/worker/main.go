package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
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
	logger, err := infra.SetupLogger(logDir)
	if err != nil {
		logger.Error("Failed to set up logger", "error", err)
		os.Exit(1)
	}

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
	defer conn.Close()
	defer ch.Close()

	_, err = ch.QueueDeclare(
		keys.FrontierQueue, // name
		true,               // durable
		false,              // delete when unused
		false,              // exclusive
		false,              // no-wait
		nil,                // arguments
	)
	if err != nil {
		logger.Error("Failed to declare frontier queue", "error", err)
		os.Exit(1)
	}

	_, err = ch.QueueDeclare(
		keys.ProcessingQueue,
		true,  // durable
		false, // delete when unused
		false, // exclusive
		false, // no-wait
		nil,   // arguments
	)

	if err != nil {
		logger.Error("Failed to declare processing queue", "error", err)
		os.Exit(1)
	}

	rdb, err := infra.SetupRedis(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB, cfg.Redis.PoolSize, cfg.Worker.RedisTimeout)
	if err != nil {
		logger.Error("Failed to set up Redis", "error", err)
		os.Exit(1)
	}

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

	for i := 1; i <= cfg.Worker.Count; i++ {
		go func() {
			w := worker.NewWorker(fmt.Sprintf("worker-%d", i),
				conn, limiter, queue, robots, fetcher, bloomFilter,
				cfg.Crawler.MaxDepth, cfg.Worker.MaxRetries,
				cfg.Crawler.PublishTimeout, cfg.Worker.RedisTimeout, cfg.Worker.TaskTimeout, cfg.Crawler.CooldownTTL,
				keys.FrontierQueue, keys.ProcessingQueue,
			)
			if err := w.Run(context.Background()); err != nil {
				logger.Error("Worker encountered an error", "error", err)
			}
		}()
	}

	p := worker.NewPoller(queue, conn, cfg.Crawler.PollerInterval)
	go p.Run(context.Background())

	logger.Info("Waiting for messages. To exit press CTRL+C")
	select {}
}
