package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"time"
	"vortex/internal/cache"
	"vortex/internal/config"
	"vortex/internal/keys"
	"vortex/internal/models"

	"github.com/google/uuid"
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

	rdb := cache.NewRedisClient(cfg.Redis)
	for i := 1; i <= 3; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) // MUST CANCEL MANUALLY; DO NOT DEFER
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
	for i := 1; i <= 1; i++ {
		uuid := uuid.New().String()
		task := models.CrawlTask{
			TraceID: uuid,
			// URL:        "https://dummy.url/" + strconv.Itoa(i),
			URL:        "https://allexsen.github.io/portfolio/",
			Attempt:    0,
			EnqueuedAt: time.Now(),
		}

		taskJSON, err := json.Marshal(task)
		if err != nil {
			logger.Error("Failed to marshal task", "task_id", task.TraceID, "error", err)
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) // MUST CANCEL MANUALLY; DO NOT DEFER
		isNew, err := bf.CheckAndSet(ctx, task.URL)
		cancel()
		if err != nil {
			logger.Error("Error checking URL in Bloom filter", "task_id", task.TraceID, "url", task.URL, "error", err)
			continue
		} else if !isNew {
			logger.Info("URL already seen, skipping", "task_id", task.TraceID, "url", task.URL)
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
			logger.Error("Failed to publish task", "task_id", task.TraceID, "error", err)
			continue
		}
		logger.Info("Task published", "task_id", task.TraceID, "url", task.URL, "time", task.EnqueuedAt.Format(time.RFC3339))
	}
}
