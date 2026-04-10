package robots

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

type RobotsFetcher struct {
	client    *http.Client
	userAgent string
}

func NewFetcher(client *http.Client, userAgent string) *RobotsFetcher {
	return &RobotsFetcher{
		client:    client,
		userAgent: userAgent,
	}
}

func (f *RobotsFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	start := time.Now()
	defer func() {
		FetchDurationSeconds.Observe(time.Since(start).Seconds())
	}()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", f.userAgent)
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		FetchTotal.WithLabelValues("not_found").Inc()
		return nil, nil
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized:
		FetchTotal.WithLabelValues("access_denied").Inc()
		return nil, fmt.Errorf("%w: %d", ErrAccessDenied, resp.StatusCode)
	case resp.StatusCode >= http.StatusInternalServerError:
		FetchTotal.WithLabelValues("server_error").Inc()
		return nil, fmt.Errorf("%w: %d", ErrServerError, resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		FetchTotal.WithLabelValues("unexpected").Inc()
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	FetchTotal.WithLabelValues("success").Inc()
	return io.ReadAll(resp.Body)
}
