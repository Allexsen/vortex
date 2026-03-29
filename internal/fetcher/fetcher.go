package fetcher

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"time"
)

type Fetcher struct {
	client    *http.Client
	userAgent string
}

func NewFetcher(timeout time.Duration, userAgent string) *Fetcher {
	return &Fetcher{
		client: &http.Client{
			Timeout: timeout,
		},
		userAgent: userAgent,
	}
}

func (f *Fetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("User-Agent", f.userAgent)

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		retryAfterStr := resp.Header.Get("Retry-After")
		retryAfter, err := strconv.Atoi(retryAfterStr)
		if err != nil {
			return nil, &RateLimitedError{RetryAfter: 60 * time.Second} // Default to 1 minute
		}

		return nil, &RateLimitedError{RetryAfter: time.Duration(retryAfter) * time.Second}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &RequestError{StatusCode: resp.StatusCode}
	}

	return io.ReadAll(resp.Body)
}
