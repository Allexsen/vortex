package cache

import (
	"context"

	"github.com/redis/go-redis/v9"
)

type BloomClient interface {
	Do(ctx context.Context, args ...interface{}) *redis.Cmd
}

type BloomFilter struct {
	rdb BloomClient
	key string
}

func NewBloomFilter(rdb BloomClient, key string) *BloomFilter {
	return &BloomFilter{
		rdb: rdb,
		key: key,
	}
}

func (bf *BloomFilter) CheckAndSet(ctx context.Context, url string) (bool, error) {
	resp, err := bf.rdb.Do(ctx, "BF.ADD", bf.key, url).Bool()
	if err != nil {
		return false, err
	}

	return resp, nil
}
