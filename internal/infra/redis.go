package infra

import (
	"context"
	"fmt"
	"log/slog"
	"time"
	"vortex/internal/cache"

	"github.com/redis/go-redis/v9"
)

func SetupRedis(addr, password string, db, poolSize int, timeout time.Duration, maxRetries int, retryDelay time.Duration) (*redis.Client, error) {
	var err error
	rdb := cache.NewRedisClient(addr, password, db, poolSize)
	for i := 1; i <= maxRetries; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), timeout) // MUST CANCEL MANUALLY; DO NOT DEFER
		err = rdb.Ping(ctx).Err()
		cancel()
		if err == nil {
			slog.Info("Connected to Redis")
			return rdb, nil
		}

		slog.Warn(fmt.Sprintf("Redis not ready.. attempting to reconnect in %v", retryDelay), "attempt", i, "error", err)
		time.Sleep(retryDelay)
	}

	slog.Error(fmt.Sprintf("Failed to connect to Redis after %d attempts", maxRetries), "error", err)
	return nil, err
}
