package main

import (
	"context"
	"encoding/json"
	"os"
	"time"
	"vortex/internal/cache"
	"vortex/internal/config"
	"vortex/internal/infra"
	"vortex/internal/keys"
	"vortex/internal/models"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	amqp "github.com/rabbitmq/amqp091-go"
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
		logger.Error("Failed to open a channel", "error", err)
		os.Exit(1)
	}
	defer ch.Close()

	q, err := ch.QueueDeclare(
		keys.FrontierQueue, // name
		true,               // durable
		false,              // delete when unused
		false,              // exclusive
		false,              // no-wait
		nil,                // arguments
	)
	if err != nil {
		logger.Error("Failed to declare a queue", "error", err)
		os.Exit(1)
	}

	rdb := cache.NewRedisClient(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB, cfg.Redis.PoolSize)
	for i := 1; i <= 3; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), cfg.Worker.RedisTimeout) // MUST CANCEL MANUALLY; DO NOT DEFER
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

	bf := cache.NewBloomFilter(rdb, keys.SeenBloomFilter)

	// TODO: Actual user input for seed URLs instead of hardcoding
	for i := 1; i <= 1; i++ {
		task := models.CrawlTask{
			TraceID: uuid.New().String(),
			// URL:        "https://dummy.url/" + strconv.Itoa(i),
			URL:        "https://go.dev",
			Attempt:    0,
			EnqueuedAt: time.Now(),
			Depth:      0,
		}

		taskJSON, err := json.Marshal(task)
		if err != nil {
			logger.Error("Failed to marshal task", "task_id", task.TraceID, "error", err)
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), cfg.Worker.RedisTimeout) // MUST CANCEL MANUALLY; DO NOT DEFER
		isNew, err := bf.CheckAndSet(ctx, task.URL)
		cancel()
		if err != nil {
			logger.Error("Error checking URL in Bloom filter", "task_id", task.TraceID, "url", task.URL, "error", err)
			continue
		} else if !isNew {
			logger.Info("URL already seen, skipping", "task_id", task.TraceID, "url", task.URL)
			continue
		}

		ctx, cancel = context.WithTimeout(context.Background(), cfg.Crawler.PublishTimeout) // MUST CANCEL MANUALLY; DO NOT DEFER
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
			logger.Error("Failed to publish task", "task_id", task.TraceID, "error", err)
			continue
		}
		logger.Info("Task published", "task_id", task.TraceID, "url", task.URL, "time", task.EnqueuedAt.Format(time.RFC3339))
	}
}
