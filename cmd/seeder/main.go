package main

import (
	"context"
	"log"
	"strconv"
	"time"
	"vortex/internal/cache"
	"vortex/internal/logger"

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
		url := "dummy.url/" + strconv.Itoa(i)
		if isNew, err := cache.IsNewURL(ctx, rdb, url); err != nil {
			logger.FailOnError(err, "Error checking URL in Bloom filter: %v")
			continue
		} else if !isNew {
			log.Printf("URL already seen, skipping: %s", url)
			continue
		}

		err = ch.PublishWithContext(ctx,
			"",     // exchange
			q.Name, // routing key
			false,  // mandatory
			false,  // immediate
			amqp.Publishing{
				ContentType: "text/plain",
				Body:        []byte(url),
			},
		)

		logger.FailOnError(err, "Failed to publish a message")
		log.Printf(" [x] Sent %s", url)
	}
}
