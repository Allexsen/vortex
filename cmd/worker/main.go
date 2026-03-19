package main

import (
	"context"
	"io"
	"log"
	"os"
	"time"
	"vortex/internal/cache"
	"vortex/internal/cooldown"
	"vortex/internal/ratelimit"
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

	ch, err := conn.Channel()
	if err != nil {
		log.Fatalf("[FATAL] Failed to open a channel: %v", err)
	}
	defer ch.Close()

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

	limiter := ratelimit.NewRedisLimiter(rdb, "vortex:limit:", 10, time.Second)
	queue := cooldown.NewRedisQueue(rdb, "vortex:cooldown:urls", 1*time.Second)

	w := worker.NewWorker(ch, limiter, queue)
	msgs, err := w.PrepareStream("vortex:frontier:pending")
	if err != nil {
		log.Fatalf("[FATAL] Failed to prepare message stream: %v", err)
	}

	for range 50 {
		go w.Process(msgs)
	}

	p := worker.NewPoller(queue, ch)
	go p.Start(context.Background())

	var forever chan struct{}

	log.Printf(" [*] Waiting for messages. To exit press CTRL+C")
	<-forever
}
