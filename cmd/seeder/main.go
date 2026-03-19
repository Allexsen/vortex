package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"
	"vortex/internal/cache"
	"vortex/internal/models"

	"github.com/google/uuid"
	amqp "github.com/rabbitmq/amqp091-go"
)

func main() {
	const logDir = "logs"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Fatalf("[FATAL] Failed to create log directory: %v", err)
	}

	logPath := filepath.Join(logDir, "vortex.log")
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0666)
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

	q, err := ch.QueueDeclare(
		"vortex:frontier:pending", // name
		true,                      // durable
		false,                     // delete when unused
		false,                     // exclusive
		false,                     // no-wait
		nil,                       // arguments
	)
	if err != nil {
		log.Fatalf("[FATAL] Failed to declare a queue: %v", err)
	}

	rdb := cache.NewRedisClient()
	for i := 1; i <= 3; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) // MUST CANCEL MANUALLY; DO NOT DEFER
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

	for i := 1; i <= 100; i++ {
		uuid := uuid.New().String()
		task := models.CrawlTask{
			TraceID:    uuid,
			URL:        "https://dummy.url/" + strconv.Itoa(i),
			Attempt:    0,
			EnqueuedAt: time.Now(),
		}

		taskJSON, err := json.Marshal(task)
		if err != nil {
			log.Printf("[ERROR] Failed to marshal task: %v", err)
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) // MUST CANCEL MANUALLY; DO NOT DEFER
		isNew, err := cache.CheckAndSetURL(ctx, rdb, task.URL)
		cancel()
		if err != nil {
			log.Printf("[ERROR] Error checking URL in Bloom filter: %v", err)
			continue
		} else if !isNew {
			log.Printf("URL already seen, skipping: %s", taskJSON)
			continue
		}

		ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second) // MUST CANCEL MANUALLY; DO NOT DEFER
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
		cancel()

		if err != nil {
			log.Printf("[ERROR] Failed to publish task: %v", err)
			continue
		}
		log.Printf(" [x] Sent %s", taskJSON)
	}
}
