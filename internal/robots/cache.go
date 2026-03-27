package robots

import (
	"context"
	"time"
)

type RulesCache interface {
	Set(ctx context.Context, domain string, data []byte, ttl time.Duration) error
	Get(ctx context.Context, domain string) ([]byte, error)
}
