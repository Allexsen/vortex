package ratelimit_test

import (
	"context"
	"testing"
	"vortex/internal/ratelimit"

	"github.com/redis/go-redis/v9"
)

type mockRedisClient struct {
	result int64
	err    error
}

func (m *mockRedisClient) Eval(ctx context.Context, script string, keys []string, args ...interface{}) *redis.Cmd {
	cmd := redis.NewCmd(ctx)
	if m.err != nil {
		cmd.SetErr(m.err)
	} else {
		cmd.SetVal(m.result)
	}
	return cmd
}

func TestAllow(t *testing.T) {
	tests := []struct {
		name      string
		allowed   int64
		err       error
		wantAllow bool
		wantErr   bool
	}{
		{"allowed", 1, nil, true, false},
		{"denied", 0, nil, false, false},
		{"redis error", 0, redis.ErrClosed, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &mockRedisClient{
				result: tt.allowed,
				err:    tt.err,
			}

			mockLimiter := ratelimit.NewRedisLimiter(mockClient, "test", 0, 0)
			allow, err := mockLimiter.Allow(context.Background(), "example.com")

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
