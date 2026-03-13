package main

import (
	"context"
	"log"
	"time"
	"vortex/internal/cache"
	"vortex/internal/cooldown"
	"vortex/internal/logger"
	"vortex/internal/ratelimit"
	"vortex/internal/worker"

	amqp "github.com/rabbitmq/amqp091-go"
)

func main() {
	conn, err := amqp.Dial("amqp://guest:guest@localhost:5672/")
	logger.FailOnError(err, "Failed to connect to RabbitMQ")
	defer conn.Close()

	ch, err := conn.Channel()
	logger.FailOnError(err, "Failed to open a channel")
	defer ch.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rdb := cache.NewRedisClient()
	err = rdb.Ping(ctx).Err()
	logger.FailOnError(err, "Could not connect to Redis: %v")

	limiter := ratelimit.NewRedisLimiter(rdb, "vortex:limit:", 10, time.Second)
	queue := cooldown.NewRedisQueue(rdb, "vortex:cooldown:urls", 1*time.Second)

	w := worker.NewWorker(ch, limiter, queue)
	msgs, err := w.PrepareStream("vortex:frontier:pending")
	logger.FailOnError(err, "Failed to prepare stream")

	for range 50 {
		go w.Process(msgs)
	}

	p := worker.NewPoller(queue, ch)
	go p.Start(context.Background())

	var forever chan struct{}

	log.Printf(" [*] Waiting for messages. To exit press CTRL+C")
	<-forever
}
