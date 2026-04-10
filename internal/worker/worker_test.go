package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
	"vortex/internal/fetcher"
	"vortex/internal/models"
)

// --- Mocks ---

type noopPauser struct{}

func (p noopPauser) WaitIfPaused(ctx context.Context) {}

type mockLimiter struct {
	allow bool
	err   error
}

func (m *mockLimiter) Allow(ctx context.Context, key string) (bool, error) {
	return m.allow, m.err
}

type mockCooldownQueue struct {
	pushed []models.CrawlTask
	err    error
}

func (m *mockCooldownQueue) Push(ctx context.Context, task models.CrawlTask, duration time.Duration) error {
	m.pushed = append(m.pushed, task)
	return m.err
}

func (m *mockCooldownQueue) PopExpired(ctx context.Context) ([]models.CrawlTask, error) {
	return nil, nil
}

type mockEtiquetteEngine struct {
	allowed    bool
	crawlDelay time.Duration
	err        error
}

func (m *mockEtiquetteEngine) CanCrawl(ctx context.Context, url string) (bool, time.Duration, error) {
	return m.allowed, m.crawlDelay, m.err
}

type mockFetcher struct {
	body []byte
	err  error
}

func (m *mockFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	return m.body, m.err
}

type mockBloomFilter struct {
	isNew bool
	err   error
}

func (m *mockBloomFilter) CheckAndSet(ctx context.Context, url string) (bool, error) {
	return m.isNew, m.err
}

// --- Helpers ---

func makeTask(url string, attempt, depth int) models.CrawlTask {
	return models.CrawlTask{
		TraceID:    "test-trace",
		URL:        url,
		Attempt:    attempt,
		Depth:      depth,
		EnqueuedAt: time.Now(),
	}
}

func marshalTask(t *testing.T, task models.CrawlTask) []byte {
	t.Helper()
	data, err := json.Marshal(task)
	if err != nil {
		t.Fatalf("failed to marshal task: %v", err)
	}
	return data
}

func newTestWorker(limiter Limiter, queue CooldownQueue, robots EtiquetteEngine, f Fetcher, bloom BloomFilter) *Worker {
	return NewWorker(
		"test-worker",
		nil,          // conn not used by processTask
		noopPauser{}, // pauser not used by processTask
		limiter, queue, robots, f, bloom,
		3, // maxDepth
		3, // maxRetries
		5*time.Second, 5*time.Second, 30*time.Second, 1*time.Second,
		"frontier", "processing",
	)
}

// --- processTask tests ---

func TestProcessTask(t *testing.T) {
	validHTML := []byte(`<html><body><p>Hello World</p><a href="https://example.com/link1"></a></body></html>`)

	tests := []struct {
		name         string
		task         models.CrawlTask
		robots       *mockEtiquetteEngine
		limiter      *mockLimiter
		fetcher      *mockFetcher
		queue        *mockCooldownQueue
		wantErr      error // sentinel: ErrPermanent or ErrTransient
		wantContent  string
		wantNewTasks bool
	}{
		{
			name:         "success - full pipeline",
			task:         makeTask("https://example.com/page", 0, 0),
			robots:       &mockEtiquetteEngine{allowed: true},
			limiter:      &mockLimiter{allow: true},
			fetcher:      &mockFetcher{body: validHTML},
			queue:        &mockCooldownQueue{},
			wantContent:  "Hello World",
			wantNewTasks: true,
		},
		{
			name:    "max retries exceeded - dropped silently",
			task:    makeTask("https://example.com/page", 3, 0),
			robots:  &mockEtiquetteEngine{allowed: true},
			limiter: &mockLimiter{allow: true},
			fetcher: &mockFetcher{body: validHTML},
			queue:   &mockCooldownQueue{},
		},
		{
			name:    "robots disallows",
			task:    makeTask("https://example.com/page", 0, 0),
			robots:  &mockEtiquetteEngine{allowed: false},
			limiter: &mockLimiter{allow: true},
			fetcher: &mockFetcher{body: validHTML},
			queue:   &mockCooldownQueue{},
		},
		{
			name:    "robots error - transient",
			task:    makeTask("https://example.com/page", 0, 0),
			robots:  &mockEtiquetteEngine{err: errors.New("timeout")},
			limiter: &mockLimiter{allow: true},
			fetcher: &mockFetcher{body: validHTML},
			queue:   &mockCooldownQueue{},
			wantErr: ErrTransient,
		},
		{
			name:    "rate limited - pushed to cooldown",
			task:    makeTask("https://example.com/page", 0, 0),
			robots:  &mockEtiquetteEngine{allowed: true},
			limiter: &mockLimiter{allow: false},
			fetcher: &mockFetcher{body: validHTML},
			queue:   &mockCooldownQueue{},
		},
		{
			name:    "rate limiter error - transient",
			task:    makeTask("https://example.com/page", 0, 0),
			robots:  &mockEtiquetteEngine{allowed: true},
			limiter: &mockLimiter{err: errors.New("redis down")},
			fetcher: &mockFetcher{body: validHTML},
			queue:   &mockCooldownQueue{},
			wantErr: ErrTransient,
		},
		{
			name:    "cooldown push error - transient",
			task:    makeTask("https://example.com/page", 0, 0),
			robots:  &mockEtiquetteEngine{allowed: true},
			limiter: &mockLimiter{allow: false},
			fetcher: &mockFetcher{body: validHTML},
			queue:   &mockCooldownQueue{err: errors.New("redis down")},
			wantErr: ErrTransient,
		},
		{
			name:    "fetch 429 - pushed to cooldown",
			task:    makeTask("https://example.com/page", 0, 0),
			robots:  &mockEtiquetteEngine{allowed: true},
			limiter: &mockLimiter{allow: true},
			fetcher: &mockFetcher{err: &fetcher.RateLimitedError{RetryAfter: 30 * time.Second}},
			queue:   &mockCooldownQueue{},
		},
		{
			name:    "fetch 4xx/5xx - dropped silently",
			task:    makeTask("https://example.com/page", 0, 0),
			robots:  &mockEtiquetteEngine{allowed: true},
			limiter: &mockLimiter{allow: true},
			fetcher: &mockFetcher{err: &fetcher.RequestError{StatusCode: 404}},
			queue:   &mockCooldownQueue{},
		},
		{
			name:    "fetch generic error - transient",
			task:    makeTask("https://example.com/page", 0, 0),
			robots:  &mockEtiquetteEngine{allowed: true},
			limiter: &mockLimiter{allow: true},
			fetcher: &mockFetcher{err: errors.New("connection reset")},
			queue:   &mockCooldownQueue{},
			wantErr: ErrTransient,
		},
		{
			name:        "max depth - content extracted but no URLs",
			task:        makeTask("https://example.com/page", 0, 3),
			robots:      &mockEtiquetteEngine{allowed: true},
			limiter:     &mockLimiter{allow: true},
			fetcher:     &mockFetcher{body: validHTML},
			queue:       &mockCooldownQueue{},
			wantContent: "Hello World",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newTestWorker(tt.limiter, tt.queue, tt.robots, tt.fetcher, &mockBloomFilter{isNew: true})
			body := marshalTask(t, tt.task)

			result, err := w.processTask(context.Background(), body)

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected %v, got %v", tt.wantErr, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantContent != "" && result.Content != tt.wantContent {
				t.Errorf("Content = %q, want %q", result.Content, tt.wantContent)
			}

			if tt.wantNewTasks && len(result.NewCrawlTasks) == 0 {
				t.Error("expected new crawl tasks, got none")
			}

			if !tt.wantNewTasks && len(result.NewCrawlTasks) != 0 {
				t.Errorf("expected no new tasks, got %d", len(result.NewCrawlTasks))
			}
		})
	}
}

func TestProcessTaskInvalidJSON(t *testing.T) {
	w := newTestWorker(
		&mockLimiter{allow: true},
		&mockCooldownQueue{},
		&mockEtiquetteEngine{allowed: true},
		&mockFetcher{body: []byte("<html></html>")},
		&mockBloomFilter{isNew: true},
	)

	result, err := w.processTask(context.Background(), []byte("not json"))

	if !errors.Is(err, ErrPermanent) {
		t.Fatalf("expected ErrPermanent, got %v", err)
	}
	if result != nil {
		t.Error("expected nil result for unmarshal failure")
	}
}

func TestProcessTaskRateLimitedCooldownPushFails(t *testing.T) {
	w := newTestWorker(
		&mockLimiter{allow: true},
		&mockCooldownQueue{err: errors.New("redis down")},
		&mockEtiquetteEngine{allowed: true},
		&mockFetcher{err: &fetcher.RateLimitedError{RetryAfter: 30 * time.Second}},
		&mockBloomFilter{isNew: true},
	)

	task := makeTask("https://example.com/page", 0, 0)
	body := marshalTask(t, task)

	_, err := w.processTask(context.Background(), body)
	if !errors.Is(err, ErrTransient) {
		t.Fatalf("expected ErrTransient when cooldown push fails after 429, got %v", err)
	}
}

func TestProcessTaskRateLimitPushedToCooldown(t *testing.T) {
	queue := &mockCooldownQueue{}
	w := newTestWorker(
		&mockLimiter{allow: false},
		queue,
		&mockEtiquetteEngine{allowed: true},
		&mockFetcher{body: []byte("<html></html>")},
		&mockBloomFilter{isNew: true},
	)

	task := makeTask("https://example.com/page", 0, 0)
	body := marshalTask(t, task)

	_, err := w.processTask(context.Background(), body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(queue.pushed) != 1 {
		t.Fatalf("expected 1 task pushed to cooldown, got %d", len(queue.pushed))
	}
	if queue.pushed[0].URL != "https://example.com/page" {
		t.Errorf("pushed task URL = %q, want %q", queue.pushed[0].URL, "https://example.com/page")
	}
}

func TestProcessTaskFetch429PushedToCooldown(t *testing.T) {
	queue := &mockCooldownQueue{}
	w := newTestWorker(
		&mockLimiter{allow: true},
		queue,
		&mockEtiquetteEngine{allowed: true},
		&mockFetcher{err: &fetcher.RateLimitedError{RetryAfter: 45 * time.Second}},
		&mockBloomFilter{isNew: true},
	)

	task := makeTask("https://example.com/page", 0, 0)
	body := marshalTask(t, task)

	_, err := w.processTask(context.Background(), body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(queue.pushed) != 1 {
		t.Fatalf("expected 1 task pushed to cooldown, got %d", len(queue.pushed))
	}
}

// --- buildNewTasks tests ---

func TestBuildNewTasks(t *testing.T) {
	tests := []struct {
		name     string
		urls     []string
		depth    int
		wantURLs []string
	}{
		{
			name:     "valid URLs",
			urls:     []string{"https://example.com/a", "https://example.com/b"},
			depth:    1,
			wantURLs: []string{"https://example.com/a", "https://example.com/b"},
		},
		{
			name:     "deduplicates URLs",
			urls:     []string{"https://example.com/a", "https://example.com/a"},
			depth:    0,
			wantURLs: []string{"https://example.com/a"},
		},
		{
			name:     "filters invalid URLs",
			urls:     []string{"https://example.com/a", "ftp://bad.com", "javascript:alert(1)"},
			depth:    0,
			wantURLs: []string{"https://example.com/a"},
		},
		{
			name:     "strips fragments then deduplicates",
			urls:     []string{"https://example.com/a#frag", "https://example.com/a"},
			depth:    0,
			wantURLs: []string{"https://example.com/a"},
		},
		{
			name:     "empty input",
			urls:     []string{},
			depth:    0,
			wantURLs: nil,
		},
		{
			name:     "nil input",
			urls:     nil,
			depth:    0,
			wantURLs: nil,
		},
		{
			name:     "all invalid",
			urls:     []string{"ftp://bad.com", "mailto:a@b.com"},
			depth:    0,
			wantURLs: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tasks := buildNewTasks(tt.urls, "parent-trace", tt.depth)

			if len(tasks) != len(tt.wantURLs) {
				t.Fatalf("got %d tasks, want %d", len(tasks), len(tt.wantURLs))
			}

			for i, task := range tasks {
				if task.URL != tt.wantURLs[i] {
					t.Errorf("task[%d].URL = %q, want %q", i, task.URL, tt.wantURLs[i])
				}
				if task.Depth != tt.depth+1 {
					t.Errorf("task[%d].Depth = %d, want %d", i, task.Depth, tt.depth+1)
				}
				if task.Attempt != 0 {
					t.Errorf("task[%d].Attempt = %d, want 0", i, task.Attempt)
				}
				if task.TraceID == "" {
					t.Errorf("task[%d].TraceID is empty", i)
				}
			}
		})
	}
}
