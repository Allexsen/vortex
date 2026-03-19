package cache

import (
	"context"

	"github.com/redis/go-redis/v9"
)

func CheckAndSetURL(ctx context.Context, rdb *redis.Client, url string) (bool, error) {
	resp, err := rdb.Do(ctx, "BF.ADD", "vortex:seen:urls", url).Bool()
	if err != nil {
		return false, err
	}

	return resp, nil
}
