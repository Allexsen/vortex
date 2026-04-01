package ratelimit_test

import (
	"context"
	"testing"
	"time"
	"vortex/internal/ratelimit"

	"github.com/redis/go-redis/v9"
)

type mockRedisClient struct {
	cmds []redis.Cmder
	err  error
}

func (m *mockRedisClient) TxPipelined(ctx context.Context, fn func(pipe redis.Pipeliner) error) ([]redis.Cmder, error) {
	return m.cmds, m.err
}

func TestAllow(t *testing.T) {
	tests := []struct {
		name      string
		count     int64
		limit     int
		txErr     error
		cmdErr    error
		wantAllow bool
		wantErr   bool
	}{
		{"within limit", 3, 5, nil, nil, true, false},
		{"at limit", 5, 5, nil, nil, true, false},
		{"exceeds limit", 6, 5, nil, nil, false, false},
		{"redis error", 0, 5, redis.ErrClosed, nil, false, true},
		{"cmd error", 0, 5, nil, redis.ErrInvalidCommand, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := redis.NewIntCmd(context.Background())
			if tt.cmdErr == nil {
				cmd.SetVal(tt.count)
			} else {
				cmd.SetErr(tt.cmdErr)
			}

			mockClient := &mockRedisClient{
				cmds: []redis.Cmder{cmd},
				err:  tt.txErr,
			}

			limiter := ratelimit.NewRedisLimiter(mockClient, "test", tt.limit, time.Minute)
			allow, err := limiter.Allow(context.Background(), "example.com")

			if (err != nil) != tt.wantErr {
				t.Errorf("Allow() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if allow != tt.wantAllow {
				t.Errorf("Allow() = %v, want %v", allow, tt.wantAllow)
			}
		})
	}
}
