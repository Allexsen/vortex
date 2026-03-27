package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"time"
	"vortex/internal/cache"
	"vortex/internal/cooldown"
	"vortex/internal/ratelimit"
	robotstxt "vortex/internal/robots"
	"vortex/internal/worker"

	amqp "github.com/rabbitmq/amqp091-go"
)

func main() {
	file, err := os.OpenFile("vortex.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatalf("[FATAL] Failed to open log file: %v", err)
	}
	defer file.Close()
	log.SetOutput(io.MultiWriter(os.Stdout, file))

	var conn *amqp.Connection
	for i := 1; i <= 3; i++ {
		conn, err = amqp.Dial("amqp://guest:guest@localhost:5672/")
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

	rdb := cache.NewRedisClient()
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

	limiter := ratelimit.NewRedisLimiter(rdb, "vortex:limit:", 1, time.Second)
	queue := cooldown.NewRedisQueue(rdb, "vortex:cooldown:urls", 1*time.Second)

	httpClient := &http.Client{Timeout: 10 * time.Second}
	fetcher := robotstxt.NewFetcher(httpClient, "VortexBot/1.0")
	robotsCache := cache.NewRedisCache(rdb, "vortex:robots:")
	robots := robotstxt.NewEtiquetteEngine(
		robotsCache,
		fetcher,
		"VortexBot",
	)

	w := worker.NewWorker(conn, limiter, queue, robots)
	for range 50 {
		go func() {
			if err := w.Run(context.Background(), "vortex:frontier:pending"); err != nil {
				log.Printf("[ERROR] Worker encountered an error: %v", err)
			}
		}()
	}

	p := worker.NewPoller(queue, conn)
	go p.Run(context.Background())

	var forever chan struct{}

	log.Printf(" [*] Waiting for messages. To exit press CTRL+C")
	<-forever
}
