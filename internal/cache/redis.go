package cache

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisCache struct {
	client *redis.Client
	prefix string
}

func NewRedisCache(client *redis.Client, prefix string) *RedisCache {
	return &RedisCache{
		client: client,
		prefix: prefix,
	}
}

func NewRedisClient(addr, password string, db, poolSize int) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     addr,     // Redis server address
		Password: password, // no password set
		DB:       db,       // use default DB
		PoolSize: poolSize, // set pool size
	})
}

func (c *RedisCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return c.client.Set(ctx, c.prefix+key, value, ttl).Err()
}

func (c *RedisCache) Get(ctx context.Context, key string) ([]byte, error) {
	data, err := c.client.Get(ctx, c.prefix+key).Bytes()
	if err == redis.Nil {
		return nil, nil // Cache miss
	}
	return data, err
}
