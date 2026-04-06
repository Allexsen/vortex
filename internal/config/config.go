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
	Robots   RobotsConfig
	Crawler  CrawlerConfig
	Fetcher  FetcherConfig
	Search   SearchConfig
}

type RabbitMQConfig struct {
	URL string
}

type RedisConfig struct {
	Addr     string
	Password string
	DB       int
	PoolSize int
}

type WorkerConfig struct {
	Count        int
	MaxRetries   int
	TaskTimeout  time.Duration
	RedisTimeout time.Duration
	MetricsPort  string
}

type RobotsConfig struct {
	UserAgent      string
	HTTPUserAgent  string
	HTTPTimeout    time.Duration
	CacheTTL       time.Duration
	DeniedCacheTTL time.Duration
}

type CrawlerConfig struct {
	MaxDepth        int
	RateLimit       int
	RateLimitWindow time.Duration
	CooldownTTL     time.Duration
	PollerInterval  time.Duration
	PublishTimeout  time.Duration
}

type FetcherConfig struct {
	Timeout   time.Duration
	UserAgent string
}

type SearchConfig struct {
	PostgresURL string
	EmbedderURL string
	Port        string
	Timeout     time.Duration
}

func Load() (*Config, error) {
	return &Config{
		RabbitMQ: RabbitMQConfig{
			URL: getString("RABBITMQ_URL", "amqp://guest:guest@localhost:5672/"),
		},
		Redis: RedisConfig{
			Addr:     getString("REDIS_ADDR", "localhost:6379"),
			Password: getString("REDIS_PASSWORD", ""),
			DB:       getInt("REDIS_DB", 0),
			PoolSize: getInt("REDIS_POOL_SIZE", 10),
		},
		Worker: WorkerConfig{
			Count:        getInt("WORKER_COUNT", 50),
			MaxRetries:   getInt("WORKER_MAX_RETRIES", 3),
			TaskTimeout:  getDuration("WORKER_TASK_TIMEOUT", 60*time.Second),
			RedisTimeout: getDuration("WORKER_REDIS_TIMEOUT", 5*time.Second),
			MetricsPort:  getString("WORKER_METRICS_PORT", "2112"),
		},
		Robots: RobotsConfig{
			UserAgent:      getString("ROBOTS_USER_AGENT", "VortexBot"),
			HTTPUserAgent:  getString("ROBOTS_HTTP_USER_AGENT", "VortexBot/1.0"),
			HTTPTimeout:    getDuration("ROBOTS_HTTP_TIMEOUT", 10*time.Second),
			CacheTTL:       getDuration("ROBOTS_CACHE_TTL", 24*time.Hour),
			DeniedCacheTTL: getDuration("ROBOTS_DENIED_CACHE_TTL", 2*time.Hour),
		},
		Crawler: CrawlerConfig{
			MaxDepth:        getInt("CRAWLER_MAX_DEPTH", 3),
			RateLimit:       getInt("CRAWLER_RATE_LIMIT", 1),
			RateLimitWindow: getDuration("CRAWLER_RATE_LIMIT_WINDOW", 1*time.Second),
			CooldownTTL:     getDuration("CRAWLER_COOLDOWN_TTL", 1*time.Second),
			PollerInterval:  getDuration("CRAWLER_POLLER_INTERVAL", 5*time.Second),
			PublishTimeout:  getDuration("CRAWLER_PUBLISH_TIMEOUT", 5*time.Second),
		},
		Fetcher: FetcherConfig{
			Timeout:   getDuration("FETCHER_TIMEOUT", 30*time.Second),
			UserAgent: getString("FETCHER_USER_AGENT", "VortexBot/1.0"),
		},
		Search: SearchConfig{
			PostgresURL: getString("POSTGRES_URL", "postgres://vortex:vortex@localhost:5432/vortex"),
			EmbedderURL: getString("SEARCH_EMBED_URL", "http://embedder:8000/embed"),
			Port:        getString("SEARCH_PORT", "8080"),
			Timeout:     getDuration("SEARCH_TIMEOUT", 10*time.Second),
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
