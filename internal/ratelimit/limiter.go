package ratelimit

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

const tokenBucketScript = `
	local key = KEYS[1]           -- the Redis hash key, e.g. "ratelimit:example.com"
	local rate = tonumber(ARGV[1]) -- tokens added per second
	local burst = tonumber(ARGV[2]) -- max tokens (bucket capacity)

	local now = tonumber(redis.call('TIME')[1]) -- current unix timestamp from Redis clock

	local fields = redis.call('HMGET', key, 'tokens', 'last')
	local tokens = tonumber(fields[1])
	local last = tonumber(fields[2])

	if tokens == nil then
		-- first request for this domain: start with a full bucket minus 1
		tokens = burst - 1
		redis.call('HSET', key, 'tokens', tokens, 'last', now)
		redis.call('EXPIRE', key, math.ceil(burst / rate) * 2)
		return 1
	end

	-- refill: add tokens for elapsed time, cap at burst
	local elapsed = now - last
	tokens = math.min(burst, tokens + elapsed * rate)

	if tokens >= 1 then
		tokens = tokens - 1
		redis.call('HSET', key, 'tokens', tokens, 'last', now)
		redis.call('EXPIRE', key, math.ceil(burst / rate) * 2)
		return 1
	end

	-- denied: still update last so we don't over-accumulate on next call
	redis.call('HSET', key, 'tokens', tokens, 'last', now)
	redis.call('EXPIRE', key, math.ceil(burst / rate) * 2)
	return 0
`

type RedisClient interface {
	Eval(ctx context.Context, script string, keys []string, args ...interface{}) *redis.Cmd
}

type RedisLimiter struct {
	client RedisClient
	prefix string
	rate   float64
	burst  int
}

func NewRedisLimiter(client RedisClient, prefix string, rate float64, burst int) *RedisLimiter {
	return &RedisLimiter{
		client: client,
		prefix: prefix,
		rate:   rate,
		burst:  burst,
	}
}

func (l *RedisLimiter) Allow(ctx context.Context, domain string) (bool, error) {
	key := fmt.Sprintf("%s:%s", l.prefix, domain)

	result, err := l.client.Eval(ctx, tokenBucketScript, []string{key}, l.rate, l.burst).Int64()

	if err != nil {
		return false, err
	}

	if result == 0 {
		LimitedTotal.Inc()
		return false, nil
	}

	return true, nil
}
