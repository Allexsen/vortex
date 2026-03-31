package main

import (
	"context"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"
	"vortex/internal/cache"
	"vortex/internal/config"
	"vortex/internal/cooldown"
	httpFetcher "vortex/internal/fetcher"
	"vortex/internal/keys"
	"vortex/internal/ratelimit"
	robotstxt "vortex/internal/robots"
	"vortex/internal/worker"

	"github.com/joho/godotenv"
	amqp "github.com/rabbitmq/amqp091-go"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	const logDir = "logs"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		logger.Error("Failed to create log directory", "error", err)
		os.Exit(1)
	}

	logPath := filepath.Join(logDir, "vortex.log")
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0666)
	if err != nil {
		logger.Error("Failed to open log file", "error", err)
		os.Exit(1)
	}
	defer file.Close()

	multiWriter := io.MultiWriter(os.Stdout, file)
	log.SetOutput(multiWriter)
	logger = slog.New(slog.NewJSONHandler(multiWriter, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := godotenv.Load(); err != nil {
		logger.Warn("No .env file found, using environment variables")
	}

	cfg, err := config.Load()
	if err != nil {
		logger.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	var conn *amqp.Connection
	for i := 1; i <= 3; i++ {
		conn, err = amqp.Dial(cfg.RabbitMQ.URL)
		if err == nil {
			logger.Info("Connected to RabbitMQ")
			break
		}
		logger.Warn("RabbitMQ not ready.. attempting to reconnect in 5s", "attempt", i, "error", err)
		time.Sleep(5 * time.Second)
	}
	if err != nil {
		logger.Error("Failed to connect to RabbitMQ after 3 attempts", "error", err)
		os.Exit(1)
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		logger.Error("Failed to open amqp channel", "error", err)
		os.Exit(1)
	}
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

	rdb := cache.NewRedisClient(cfg.Redis)
	for i := 1; i <= 3; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = rdb.Ping(ctx).Err()
		cancel()
		if err == nil {
			logger.Info("Connected to Redis")
			break
		}
		logger.Warn("Redis not ready.. attempting to reconnect in 5s", "attempt", i, "error", err)
		time.Sleep(5 * time.Second)
	}
	if err != nil {
		logger.Error("Failed to connect to Redis after 3 attempts", "error", err)
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
	)

	fetcher := httpFetcher.NewFetcher(cfg.Fetcher.Timeout, cfg.Fetcher.UserAgent)
	bloomFilter := cache.NewBloomFilter(rdb, keys.SeenBloomFilter)

	w := worker.NewWorker(conn, limiter, queue, robots, fetcher, bloomFilter,
		cfg.Crawler.MaxDepth, cfg.Crawler.PublishTimeout, cfg.Worker.TaskTimeout, cfg.Crawler.CooldownTTL,
		keys.FrontierQueue, keys.ProcessingQueue,
	)
	for range cfg.Worker.Count {
		go func() {
			if err := w.Run(context.Background()); err != nil {
				logger.Error("Worker encountered an error", "error", err)
			}
		}()
	}

	p := worker.NewPoller(queue, conn, cfg.Crawler.PollerInterval)
	go p.Run(context.Background())

	var forever chan struct{}

	logger.Info("Waiting for messages. To exit press CTRL+C")
	<-forever
}
