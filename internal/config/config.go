package config

import (
	"log/slog"
	"os"
	"strconv"
	"time"
)

type Config struct {
	RabbitMQ RabbitMQConfig
	Redis    RedisConfig
	Worker   WorkerConfig
	Manager  ManagerConfig
	Robots   RobotsConfig
	Fetcher  FetcherConfig
	Search   SearchConfig
}

type RabbitMQConfig struct {
	URL string

	MaxRetries int
	RetryDelay time.Duration
}

type RedisConfig struct {
	Addr     string
	Password string
	DB       int
	PoolSize int

	MaxRetries int
	RetryDelay time.Duration
}

type WorkerConfig struct {
	Count      int
	MaxDepth   int
	MaxRetries int

	TaskTimeout    time.Duration
	RedisTimeout   time.Duration
	PublishTimeout time.Duration
	MetricsPort    string

	RateLimit float64
	RateBurst int

	CooldownTTL    time.Duration
	PollerInterval time.Duration
}

type ManagerConfig struct {
	PollInterval       time.Duration
	ProcessingPauseAt  int
	ProcessingResumeAt int
}

type RobotsConfig struct {
	UserAgent      string
	HTTPUserAgent  string
	HTTPTimeout    time.Duration
	CacheTTL       time.Duration
	DeniedCacheTTL time.Duration
}

type FetcherConfig struct {
	UserAgent   string
	MaxBodySize int
	Timeout     time.Duration
	RetryAfter  time.Duration
}

type SearchConfig struct {
	Port        string
	PostgresURL string
	EmbedderURL string

	Timeout         time.Duration
	ShutdownTimeout time.Duration

	RateLimit int
	RateBurst int

	MaxQueryLength int
	DefaultTopK    int
	MaxTopK        int
}

func Load() (*Config, error) {
	return &Config{
		RabbitMQ: RabbitMQConfig{
			URL:        getString("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
			MaxRetries: getInt("RABBITMQ_MAX_RETRIES", 3),
			RetryDelay: getDuration("RABBITMQ_RETRY_DELAY", 5*time.Second),
		},
		Redis: RedisConfig{
			Addr:       getString("REDIS_ADDR", "localhost:6379"),
			Password:   getString("REDIS_PASSWORD", ""),
			DB:         getInt("REDIS_DB", 0),
			PoolSize:   getInt("REDIS_POOL_SIZE", 10),
			MaxRetries: getInt("REDIS_MAX_RETRIES", 3),
			RetryDelay: getDuration("REDIS_RETRY_DELAY", 5*time.Second),
		},
		Worker: WorkerConfig{
			Count:          getInt("WORKER_COUNT", 50),
			MaxDepth:       getInt("WORKER_MAX_DEPTH", 3),
			MaxRetries:     getInt("WORKER_MAX_RETRIES", 3),
			TaskTimeout:    getDuration("WORKER_TASK_TIMEOUT", 60*time.Second),
			RedisTimeout:   getDuration("WORKER_REDIS_TIMEOUT", 5*time.Second),
			PublishTimeout: getDuration("WORKER_PUBLISH_TIMEOUT", 5*time.Second),
			MetricsPort:    getString("WORKER_METRICS_PORT", "2112"),
			RateLimit:      getFloat64("WORKER_RATE_LIMIT", 1.0),
			RateBurst:      getInt("WORKER_RATE_BURST", 5),
			CooldownTTL:    getDuration("WORKER_COOLDOWN_TTL", 1*time.Second),
			PollerInterval: getDuration("WORKER_POLLER_INTERVAL", 5*time.Second),
		},
		Manager: ManagerConfig{
			PollInterval:       getDuration("MANAGER_POLL_INTERVAL", 2*time.Second),
			ProcessingPauseAt:  getInt("MANAGER_PROCESSING_PAUSE_AT", 5000),
			ProcessingResumeAt: getInt("MANAGER_PROCESSING_RESUME_AT", 2500),
		},
		Robots: RobotsConfig{
			UserAgent:      getString("ROBOTS_USER_AGENT", "VortexBot"),
			HTTPUserAgent:  getString("ROBOTS_HTTP_USER_AGENT", "VortexBot/1.0"),
			HTTPTimeout:    getDuration("ROBOTS_HTTP_TIMEOUT", 10*time.Second),
			CacheTTL:       getDuration("ROBOTS_CACHE_TTL", 24*time.Hour),
			DeniedCacheTTL: getDuration("ROBOTS_DENIED_CACHE_TTL", 2*time.Hour),
		},
		Fetcher: FetcherConfig{
			UserAgent:   getString("FETCHER_USER_AGENT", "VortexBot/1.0"),
			MaxBodySize: getInt("FETCHER_MAX_BODY_SIZE", 10<<20), // 10 MiB
			Timeout:     getDuration("FETCHER_TIMEOUT", 30*time.Second),
			RetryAfter:  getDuration("FETCHER_RETRY_AFTER", 60*time.Second),
		},
		Search: SearchConfig{
			Port:            getString("SEARCH_PORT", "8080"),
			PostgresURL:     getString("POSTGRES_URL", "postgres://vortex:vortex@localhost:5432/vortex"),
			EmbedderURL:     getString("SEARCH_EMBED_URL", "http://embedder:8000/embed"),
			Timeout:         getDuration("SEARCH_TIMEOUT", 10*time.Second),
			ShutdownTimeout: getDuration("SEARCH_SHUTDOWN_TIMEOUT", 15*time.Second),
			RateLimit:       getInt("SEARCH_RATE_LIMIT", 3),
			RateBurst:       getInt("SEARCH_RATE_BURST", 10),
			MaxQueryLength:  getInt("SEARCH_MAX_QUERY_LENGTH", 256),
			DefaultTopK:     getInt("SEARCH_DEFAULT_TOP_K", 10),
			MaxTopK:         getInt("SEARCH_MAX_TOP_K", 50),
		},
	}, nil
}

func getDuration(key string, fallback time.Duration) time.Duration {
	val := os.Getenv(key)
	if val == "" {
		return fallback
	}

	dur, err := time.ParseDuration(val)
	if err != nil {
		slog.Error("Invalid duration, using fallback", "key", key, "error", err, "fallback", fallback)
		return fallback
	}
	return dur
}

func getFloat64(key string, fallback float64) float64 {
	val := os.Getenv(key)
	if val == "" {
		return fallback
	}

	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		slog.Error("Invalid float, using fallback", "key", key, "error", err, "fallback", fallback)
		return fallback
	}
	return f
}

func getInt(key string, fallback int) int {
	val := os.Getenv(key)
	if val == "" {
		return fallback
	}

	i, err := strconv.Atoi(val)
	if err != nil {
		slog.Error("Invalid integer, using fallback", "key", key, "error", err, "fallback", fallback)
		return fallback
	}
	return i
}

func getString(key string, fallback string) string {
	val := os.Getenv(key)
	if val == "" {
		return fallback
	}
	return val
}
