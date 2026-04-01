package cache

import (
	"context"
	"errors"
	"testing"

	"github.com/redis/go-redis/v9"
)

type mockBloomClient struct {
	val interface{}
	err error
}

func (m *mockBloomClient) Do(ctx context.Context, args ...interface{}) *redis.Cmd {
	cmd := redis.NewCmd(context.Background())
	if m.err != nil {
		cmd.SetErr(m.err)
	} else {
		cmd.SetVal(m.val)
	}
	return cmd
}

func TestCheckAndSet(t *testing.T) {
	tests := []struct {
		name    string
		val     interface{}
		err     error
		wantNew bool
		wantErr bool
	}{
		{
			name:    "new URL - returns true",
			val:     true,
			wantNew: true,
		},
		{
			name:    "already seen - returns false",
			val:     false,
			wantNew: false,
		},
		{
			name:    "redis error",
			err:     errors.New("connection refused"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockBloomClient{val: tt.val, err: tt.err}
			bf := NewBloomFilter(mock, "test:bloom")

			isNew, err := bf.CheckAndSet(context.Background(), "https://example.com")

			if (err != nil) != tt.wantErr {
				t.Fatalf("CheckAndSet() error = %v, wantErr %v", err, tt.wantErr)
			}

			if !tt.wantErr && isNew != tt.wantNew {
				t.Errorf("CheckAndSet() = %v, want %v", isNew, tt.wantNew)
			}
		})
	}
}
