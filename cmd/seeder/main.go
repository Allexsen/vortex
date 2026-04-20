package main

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
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
	cleanupFunc, err := infra.SetupLogger("seeder")
	if err != nil {
		slog.Error("Failed to set up logger", "error", err)
		os.Exit(1)
	}
	defer cleanupFunc()

	if err := godotenv.Load(); err != nil {
		slog.Warn("No .env file found, using environment variables")
	}

	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	conn, ch, err := infra.SetupRabbitMQ(cfg.RabbitMQ.URL, cfg.RabbitMQ.MaxRetries, cfg.RabbitMQ.RetryDelay)
	if err != nil {
		slog.Error("Failed to set up RabbitMQ", "error", err)
		os.Exit(1)
	}
	defer conn.Close()
	defer ch.Close()

	err = infra.DeclareWithDLQ(ch, keys.FrontierQueue, keys.FrontierDLQ, keys.FrontierDLQRoutingKey, keys.DeadLetterExchange)
	if err != nil {
		slog.Error("Failed to declare frontier queue with DLQ", "error", err)
		os.Exit(1)
	}

	rdb, err := infra.SetupRedis(cfg.Redis.Addr, cfg.Redis.Password, cfg.Redis.DB, cfg.Redis.PoolSize,
		cfg.Worker.RedisTimeout, cfg.Redis.MaxRetries, cfg.Redis.RetryDelay)
	if err != nil {
		slog.Error("Failed to set up Redis", "error", err)
		os.Exit(1)
	}

	bf := cache.NewBloomFilter(rdb, keys.SeenBloomFilter)

	seedURLs, err := getSeederDomains()
	if err != nil {
		slog.Error("Failed to get seed URLs", "error", err)
		os.Exit(1)
	}

	for _, url := range seedURLs {
		task := models.CrawlTask{
			TraceID:    uuid.New().String(),
			URL:        url,
			Attempt:    0,
			EnqueuedAt: time.Now(),
			Depth:      0,
		}

		taskJSON, err := json.Marshal(task)
		if err != nil {
			slog.Error("Failed to marshal task", "task_id", task.TraceID, "error", err)
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), cfg.Worker.RedisTimeout) // MUST CANCEL MANUALLY; DO NOT DEFER
		isNew, err := bf.CheckAndSet(ctx, task.URL)
		cancel()
		if err != nil {
			slog.Error("Error checking URL in Bloom filter", "task_id", task.TraceID, "url", task.URL, "error", err)
			continue
		} else if !isNew {
			slog.Info("URL already seen, skipping", "task_id", task.TraceID, "url", task.URL)
			continue
		}

		ctx, cancel = context.WithTimeout(context.Background(), cfg.Worker.PublishTimeout) // MUST CANCEL MANUALLY; DO NOT DEFER
		err = ch.PublishWithContext(ctx,
			"",                 // exchange
			keys.FrontierQueue, // routing key
			false,              // mandatory
			false,              // immediate
			amqp.Publishing{
				DeliveryMode: amqp.Persistent,
				ContentType:  "application/json",
				Body:         taskJSON,
			},
		)
		cancel()

		if err != nil {
			slog.Error("Failed to publish task", "task_id", task.TraceID, "error", err)
			continue
		}
		slog.Info("Task published", "task_id", task.TraceID, "url", task.URL, "time", task.EnqueuedAt.Format(time.RFC3339))
	}
}

//go:embed seeds.txt
var seedsPayload string

// getSeederDomains reads the embedded text file line by line.
func getSeederDomains() ([]string, error) {
	var urls []string

	scanner := bufio.NewScanner(strings.NewReader(seedsPayload))

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		urls = append(urls, line)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return urls, nil
}
