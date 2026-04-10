package worker

import (
	"context"
	"time"
	"vortex/internal/models"
)

type EtiquetteEngine interface {
	CanCrawl(ctx context.Context, url string) (bool, time.Duration, error)
}

type Pauser interface {
	WaitIfPaused(ctx context.Context)
}

type Fetcher interface {
	Fetch(ctx context.Context, url string) ([]byte, error)
}

type BloomFilter interface {
	CheckAndSet(ctx context.Context, url string) (bool, error)
}

type Limiter interface {
	Allow(ctx context.Context, key string) (bool, error)
}

type CooldownQueue interface {
	Push(context.Context, models.CrawlTask, time.Duration) error
	PopExpired(context.Context) ([]models.CrawlTask, error)
}
