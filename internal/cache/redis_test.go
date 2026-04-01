package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type mockCacheClient struct {
	store  map[string]string
	getErr error
	setErr error
}

func newMockCacheClient() *mockCacheClient {
	return &mockCacheClient{store: make(map[string]string)}
}

func (m *mockCacheClient) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd {
	cmd := redis.NewStatusCmd(context.Background())
	if m.setErr != nil {
		cmd.SetErr(m.setErr)
		return cmd
	}
	m.store[key] = string(value.([]byte))
	cmd.SetVal("OK")
	return cmd
}

func (m *mockCacheClient) Get(ctx context.Context, key string) *redis.StringCmd {
	cmd := redis.NewStringCmd(context.Background())
	if m.getErr != nil {
		cmd.SetErr(m.getErr)
		return cmd
	}
	val, ok := m.store[key]
	if !ok {
		cmd.SetErr(redis.Nil)
		return cmd
	}
	cmd.SetVal(val)
	return cmd
}

func TestRedisCacheSet(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   []byte
		setErr  error
		wantErr bool
	}{
		{
			name:  "success",
			key:   "mykey",
			value: []byte("myvalue"),
		},
		{
			name:    "redis error",
			key:     "mykey",
			value:   []byte("myvalue"),
			setErr:  errors.New("connection refused"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := newMockCacheClient()
			mock.setErr = tt.setErr
			cache := NewRedisCache(mock, "prefix:")

			err := cache.Set(context.Background(), tt.key, tt.value, time.Minute)

			if (err != nil) != tt.wantErr {
				t.Fatalf("Set() error = %v, wantErr %v", err, tt.wantErr)
			}

			if !tt.wantErr {
				stored, ok := mock.store["prefix:"+tt.key]
				if !ok {
					t.Fatal("expected key to be stored")
				}
				if stored != string(tt.value) {
					t.Errorf("stored value = %q, want %q", stored, string(tt.value))
				}
			}
		})
	}
}

func TestRedisCacheGet(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		seed    map[string]string
		getErr  error
		want    []byte
		wantErr bool
	}{
		{
			name: "cache hit",
			key:  "mykey",
			seed: map[string]string{"prefix:mykey": "myvalue"},
			want: []byte("myvalue"),
		},
		{
			name: "cache miss returns nil nil",
			key:  "missing",
			seed: map[string]string{},
			want: nil,
		},
		{
			name:    "redis error",
			key:     "mykey",
			getErr:  errors.New("connection refused"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := newMockCacheClient()
			if tt.seed != nil {
				mock.store = tt.seed
			}
			mock.getErr = tt.getErr
			cache := NewRedisCache(mock, "prefix:")

			data, err := cache.Get(context.Background(), tt.key)

			if (err != nil) != tt.wantErr {
				t.Fatalf("Get() error = %v, wantErr %v", err, tt.wantErr)
			}

			if !tt.wantErr {
				if tt.want == nil && data != nil {
					t.Errorf("Get() = %q, want nil", data)
				} else if tt.want != nil && string(data) != string(tt.want) {
					t.Errorf("Get() = %q, want %q", data, tt.want)
				}
			}
		})
	}
}

func TestRedisCachePrefixApplied(t *testing.T) {
	mock := newMockCacheClient()
	cache := NewRedisCache(mock, "robots:")

	cache.Set(context.Background(), "example.com", []byte("data"), time.Minute)

	if _, ok := mock.store["robots:example.com"]; !ok {
		t.Error("expected key with prefix 'robots:example.com'")
	}
}
