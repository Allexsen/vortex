package cooldown

import (
	"context"
	"encoding/json"
	"strconv"
	"time"
	"vortex/internal/models"

	"github.com/redis/go-redis/v9"
)

type Queue interface {
	Push(context.Context, models.CrawlTask) error
	PopExpired(context.Context) ([]models.CrawlTask, error)
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

func (q *RedisQueue) Push(ctx context.Context, task models.CrawlTask) error {
	taskJSON, err := json.Marshal(task)
	if err != nil {
		return err
	}

	score := float64(time.Now().Add(q.duration).Unix()) // cooldown duration
	return q.client.ZAdd(ctx, q.key, redis.Z{
		Score:  score,
		Member: taskJSON,
	}).Err()
}

func (q *RedisQueue) PopExpired(ctx context.Context) ([]models.CrawlTask, error) {
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

	var tasks []models.CrawlTask
	for _, u := range urls {
		var task models.CrawlTask
		if err := json.Unmarshal([]byte(u), &task); err != nil {
			continue
		}
		tasks = append(tasks, task)
	}

	members := make([]any, len(urls))
	for i, u := range urls {
		members[i] = u
	}

	_, err = q.client.ZRem(ctx, q.key, members...).Result()
	if err != nil {
		return nil, err
	}

	return tasks, nil
}
