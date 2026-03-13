package cooldown

import (
	"context"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

type Queue interface {
	Push(context.Context, string) error
	PopExpired(context.Context) ([]string, error)
}

type RedisQueue struct {
	client   *redis.Client
	key      string
	duration time.Duration
}

func NewRedisQueue(client *redis.Client, key string, duration time.Duration) *RedisQueue {
	return &RedisQueue{
		client:   client,
		key:      key,
		duration: duration,
	}
}

func (q *RedisQueue) Push(ctx context.Context, url string) error {
	score := float64(time.Now().Add(q.duration).Unix()) // cooldown duration
	return q.client.ZAdd(ctx, q.key, redis.Z{
		Score:  score,
		Member: url,
	}).Err()
}

func (q *RedisQueue) PopExpired(ctx context.Context) ([]string, error) {
	now := float64(time.Now().Unix())
	urls, err := q.client.ZRangeArgs(ctx, redis.ZRangeArgs{
		Key:     q.key,
		Start:   "-inf",
		Stop:    strconv.FormatFloat(now, 'f', -1, 64),
		ByScore: true,
	}).Result()
	if err != nil {
		return nil, err
	}

	if len(urls) == 0 {
		return nil, nil
	}

	members := make([]any, len(urls))
	for i, u := range urls {
		members[i] = u
	}

	_, err = q.client.ZRem(ctx, q.key, members...).Result()
	if err != nil {
		return nil, err
	}

	return urls, nil
}
