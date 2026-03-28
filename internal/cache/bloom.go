package cache

import (
	"context"

	"github.com/redis/go-redis/v9"
)

type BloomFilter struct {
	rdb *redis.Client
	key string
}

func NewBloomFilter(rdb *redis.Client, key string) *BloomFilter {
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
