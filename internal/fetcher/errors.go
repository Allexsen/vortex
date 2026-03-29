package fetcher

import (
	"fmt"
	"time"
)

type RateLimitedError struct {
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("rate limited: retry after %v", e.RetryAfter)
}

type RequestError struct {
	StatusCode int
}

func (e *RequestError) Error() string {
	return fmt.Sprintf("request failed with status code %d", e.StatusCode)
}
