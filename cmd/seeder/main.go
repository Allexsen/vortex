package main

import (
	"context"
	"encoding/json"
	"log"
	"strconv"
	"time"
	"vortex/internal/cache"
	"vortex/internal/logger"
	"vortex/internal/models"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

func main() {
	conn, err := amqp.Dial("amqp://guest:guest@localhost:5672/")
	logger.FailOnError(err, "Failed to connect to RabbitMQ")
	defer conn.Close()

	ch, err := conn.Channel()
	logger.FailOnError(err, "Failed to open a channel")
	defer ch.Close()

	q, err := ch.QueueDeclare(
		"vortex:frontier:pending", // name
		true,                      // durable
		false,                     // delete when unused
		false,                     // exclusive
		false,                     // no-wait
		nil,                       // arguments
	)
	logger.FailOnError(err, "Failed to declare a queue")

	rdb := cache.NewRedisClient()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = rdb.Ping(ctx).Err()
	logger.FailOnError(err, "Could not connect to Redis: %v")

	for i := 1; i <= 100; i++ {
		uuid := uuid.New().String()
		task := models.CrawlTask{
			TraceID:    uuid,
			URL:        "dummy.url/" + strconv.Itoa(i),
			Attempt:    0,
			EnqueuedAt: time.Now(),
		}

		taskJSON, err := json.Marshal(task)
		if err != nil {
			logger.FailOnError(err, "Error marshaling task: %v")
			continue
		}

		if isNew, err := cache.IsNewURL(ctx, rdb, task.URL); err != nil {
			logger.FailOnError(err, "Error checking URL in Bloom filter: %v")
			continue
		} else if !isNew {
			log.Printf("URL already seen, skipping: %s", taskJSON)
			continue
		}

		err = ch.PublishWithContext(ctx,
			"",     // exchange
			q.Name, // routing key
			false,  // mandatory
			false,  // immediate
			amqp.Publishing{
				ContentType: "application/json",
				Body:        taskJSON,
			},
		)

		logger.FailOnError(err, "Failed to publish a message")
		log.Printf(" [x] Sent %s", taskJSON)
	}
}
