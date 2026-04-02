package infra

import (
	"context"
	"log/slog"
	"time"
	"vortex/internal/cache"

	"github.com/redis/go-redis/v9"
)

func SetupRedis(addr, password string, db, poolSize int, timeout time.Duration) (*redis.Client, error) {
	var err error
	rdb := cache.NewRedisClient(addr, password, db, poolSize)
	for i := 1; i <= 3; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), timeout) // MUST CANCEL MANUALLY; DO NOT DEFER
		err = rdb.Ping(ctx).Err()
		cancel()
		if err == nil {
			slog.Info("Connected to Redis")
			return rdb, nil
		}

		slog.Warn("Redis not ready.. attempting to reconnect in 5s", "attempt", i, "error", err)
		time.Sleep(5 * time.Second)
	}

	slog.Error("Failed to connect to Redis after 3 attempts", "error", err)
	return nil, err
}
