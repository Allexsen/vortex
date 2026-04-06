package cooldown

import (
	"context"
	"encoding/json"
	"strconv"
	"time"
	"vortex/internal/models"

	"github.com/redis/go-redis/v9"
)

var (
	popExpiredScript = redis.NewScript(`
	local items = redis.call('ZRANGEBYSCORE', KEYS[1], '-inf', ARGV[1])
	if #items > 0 then
		redis.call('ZREM', KEYS[1], unpack(items))
	end
	return items
	`)
)

type RedisQueue struct {
	client *redis.Client
	key    string
}

func NewRedisQueue(client *redis.Client, key string) *RedisQueue {
	return &RedisQueue{
		client: client,
		key:    key,
	}
}

func (q *RedisQueue) Push(ctx context.Context, task models.CrawlTask, duration time.Duration) error {
	taskJSON, err := json.Marshal(task)
	if err != nil {
		return err
	}

	score := float64(time.Now().Add(duration).Unix()) // cooldown duration
	err = q.client.ZAdd(ctx, q.key, redis.Z{
		Score:  score,
		Member: taskJSON,
	}).Err()
	if err != nil {
		return err
	}
	CooldownPushesTotal.Inc()

	return nil
}

func (q *RedisQueue) PopExpired(ctx context.Context) ([]models.CrawlTask, error) {
	now := strconv.FormatFloat(float64(time.Now().Unix()), 'f', -1, 64)
	result, err := popExpiredScript.Run(ctx, q.client, []string{q.key}, now).Result()
	if err != nil {
		return nil, err
	}

	items, ok := result.([]interface{})
	if !ok {
		return nil, nil
	}

	var tasks []models.CrawlTask
	for _, item := range items {
		itemStr, ok := item.(string)
		if !ok {
			continue
		}

		var task models.CrawlTask
		if err := json.Unmarshal([]byte(itemStr), &task); err != nil {
			continue
		}
		tasks = append(tasks, task)
	}

	return tasks, nil
}
