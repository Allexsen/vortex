package robots

import (
	"context"
	"fmt"
	"io"
	"net/http"
)

type Fetcher struct {
	client    *http.Client
	userAgent string
}

func NewFetcher(client *http.Client, userAgent string) *Fetcher {
	return &Fetcher{
		client:    client,
		userAgent: userAgent,
	}
}

func (f *Fetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
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
		return nil, nil
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized:
		return nil, fmt.Errorf("%w: %d", ErrAccessDenied, resp.StatusCode)
	case resp.StatusCode >= http.StatusInternalServerError:
		return nil, fmt.Errorf("%w: %d", ErrServerError, resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}
