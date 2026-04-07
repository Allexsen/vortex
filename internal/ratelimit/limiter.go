package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisClient interface {
	TxPipelined(ctx context.Context, fn func(pipe redis.Pipeliner) error) ([]redis.Cmder, error)
}

type RedisLimiter struct {
	client RedisClient
	prefix string
	limit  int
	window time.Duration
}

func NewRedisLimiter(client RedisClient, prefix string, limit int, window time.Duration) *RedisLimiter {
	return &RedisLimiter{
		client: client,
		prefix: prefix,
		limit:  limit,
		window: window,
	}
}

func (l *RedisLimiter) Allow(ctx context.Context, key string) (bool, error) {
	redisKey := fmt.Sprintf("%s:%s:%d", l.prefix, key, time.Now().Unix())

	cmds, err := l.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Incr(ctx, redisKey)
		pipe.Expire(ctx, redisKey, l.window)
		return nil
	})
	if err != nil {
		return false, err
	}

	count, err := cmds[0].(*redis.IntCmd).Result()
	if err != nil {
		return false, err
	}

	allowed := count <= int64(l.limit)
	if !allowed {
		LimitedTotal.Inc()
	}

	return allowed, nil
}
