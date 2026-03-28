package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"time"
	"vortex/internal/cache"
	"vortex/internal/config"
	"vortex/internal/cooldown"
	"vortex/internal/keys"
	"vortex/internal/ratelimit"
	robotstxt "vortex/internal/robots"
	"vortex/internal/worker"

	"github.com/joho/godotenv"
	amqp "github.com/rabbitmq/amqp091-go"
)

func main() {
	if err := godotenv.Load(); err != nil {
		log.Printf("[WARN] No .env file found, using environment variables")
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("[FATAL] Failed to load configuration: %v", err)
	}

	file, err := os.OpenFile("vortex.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("[FATAL] Failed to open log file: %v", err)
	}
	defer file.Close()
	log.SetOutput(io.MultiWriter(os.Stdout, file))

	var conn *amqp.Connection
	for i := 1; i <= 3; i++ {
		conn, err = amqp.Dial(cfg.RabbitMQ.URL)
		if err == nil {
			log.Println("Connected to RabbitMQ")
			break
		}
		log.Printf("[WARN] RabbitMQ not ready (attempt %d/3): %v. Retrying in 5s...", i, err)
		time.Sleep(5 * time.Second)
	}
	if err != nil {
		log.Fatalf("[FATAL] Failed to connect to RabbitMQ after 3 attempts: %v", err)
	}
	defer conn.Close()

	rdb := cache.NewRedisClient(cfg.Redis)
	for i := 1; i <= 3; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = rdb.Ping(ctx).Err()
		cancel()
		if err == nil {
			log.Println("Connected to Redis")
			break
		}
		log.Printf("[WARN] Redis not ready (attempt %d/3): %v. Retrying in 5s...", i, err)
		time.Sleep(5 * time.Second)
	}
	if err != nil {
		log.Fatalf("[FATAL] Failed to connect to Redis after 3 attempts: %v", err)
	}

	limiter := ratelimit.NewRedisLimiter(rdb, keys.RateLimitPrefix, cfg.Crawler.RateLimit, cfg.Crawler.RateLimitWindow)
	queue := cooldown.NewRedisQueue(rdb, keys.CooldownQueue, cfg.Crawler.CooldownTTL)

	httpClient := &http.Client{Timeout: cfg.Robots.HTTPTimeout}
	fetcher := robotstxt.NewFetcher(httpClient, cfg.Robots.HTTPUserAgent)
	robotsCache := cache.NewRedisCache(rdb, keys.RobotsCachePrefix)
	robots := robotstxt.NewEtiquetteEngine(
		robotsCache,
		fetcher,
		cfg.Robots.UserAgent,
	)

	w := worker.NewWorker(conn, limiter, queue, robots, cfg.Worker.TaskTimeout)
	for range cfg.Worker.Count {
		go func() {
			if err := w.Run(context.Background(), keys.FrontierQueue); err != nil {
				log.Printf("[ERROR] Worker encountered an error: %v", err)
			}
		}()
	}

	p := worker.NewPoller(queue, conn, cfg.Crawler.PollerInterval)
	go p.Run(context.Background())

	var forever chan struct{}

	log.Printf(" [*] Waiting for messages. To exit press CTRL+C")
	<-forever
}
