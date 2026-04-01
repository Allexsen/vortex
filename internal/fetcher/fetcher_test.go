package fetcher_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
	"vortex/internal/fetcher"
)

type mockHTTPClient struct {
	resp *http.Response
	err  error
}

func (m *mockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return m.resp, m.err
}

func TestFetch(t *testing.T) {
	tests := []struct {
		name           string
		url            string
		resp           *http.Response
		clientErr      error
		wantBody       string
		wantErr        bool
		wantRateLimit  bool
		wantRetryAfter time.Duration
		wantReqErr     bool
		wantStatusCode int
	}{
		{
			name: "success",
			url:  "https://example.com",
			resp: &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader("hello world")),
			},
			wantBody: "hello world",
		},
		{
			name:      "client error",
			url:       "https://example.com",
			clientErr: errors.New("connection refused"),
			wantErr:   true,
		},
		{
			name: "429 with retry-after header",
			url:  "https://example.com",
			resp: &http.Response{
				StatusCode: 429,
				Header:     http.Header{"Retry-After": []string{"30"}},
				Body:       io.NopCloser(strings.NewReader("")),
			},
			wantErr:        true,
			wantRateLimit:  true,
			wantRetryAfter: 30 * time.Second,
		},
		{
			name: "429 without retry-after header",
			url:  "https://example.com",
			resp: &http.Response{
				StatusCode: 429,
				Header:     http.Header{},
				Body:       io.NopCloser(strings.NewReader("")),
			},
			wantErr:        true,
			wantRateLimit:  true,
			wantRetryAfter: 60 * time.Second,
		},
		{
			name: "429 with invalid retry-after",
			url:  "https://example.com",
			resp: &http.Response{
				StatusCode: 429,
				Header:     http.Header{"Retry-After": []string{"not-a-number"}},
				Body:       io.NopCloser(strings.NewReader("")),
			},
			wantErr:        true,
			wantRateLimit:  true,
			wantRetryAfter: 60 * time.Second,
		},
		{
			name: "404 not found",
			url:  "https://example.com",
			resp: &http.Response{
				StatusCode: 404,
				Body:       io.NopCloser(strings.NewReader("")),
			},
			wantErr:        true,
			wantReqErr:     true,
			wantStatusCode: 404,
		},
		{
			name: "500 server error",
			url:  "https://example.com",
			resp: &http.Response{
				StatusCode: 500,
				Body:       io.NopCloser(strings.NewReader("")),
			},
			wantErr:        true,
			wantReqErr:     true,
			wantStatusCode: 500,
		},
		{
			name: "301 redirect treated as error",
			url:  "https://example.com",
			resp: &http.Response{
				StatusCode: 301,
				Body:       io.NopCloser(strings.NewReader("")),
			},
			wantErr:        true,
			wantReqErr:     true,
			wantStatusCode: 301,
		},
		{
			name:    "invalid URL",
			url:     "://",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockHTTPClient{resp: tt.resp, err: tt.clientErr}
			f := fetcher.NewFetcher(mock, "TestBot/1.0")

			body, err := f.Fetch(context.Background(), tt.url)

			if (err != nil) != tt.wantErr {
				t.Fatalf("Fetch() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantRateLimit {
				var rateErr *fetcher.RateLimitedError
				if !errors.As(err, &rateErr) {
					t.Fatalf("expected RateLimitedError, got %T: %v", err, err)
				}
				if rateErr.RetryAfter != tt.wantRetryAfter {
					t.Errorf("RetryAfter = %v, want %v", rateErr.RetryAfter, tt.wantRetryAfter)
				}
				return
			}

			if tt.wantReqErr {
				var reqErr *fetcher.RequestError
				if !errors.As(err, &reqErr) {
					t.Fatalf("expected RequestError, got %T: %v", err, err)
				}
				if reqErr.StatusCode != tt.wantStatusCode {
					t.Errorf("StatusCode = %d, want %d", reqErr.StatusCode, tt.wantStatusCode)
				}
				return
			}

			if !tt.wantErr && string(body) != tt.wantBody {
				t.Errorf("Fetch() body = %q, want %q", string(body), tt.wantBody)
			}
		})
	}
}

func TestFetchSetsUserAgent(t *testing.T) {
	var capturedUA string
	mock := &mockHTTPClient{
		resp: &http.Response{
			StatusCode: 200,
			Body:       io.NopCloser(strings.NewReader("")),
		},
	}

	// Override Do to capture the request
	f := fetcher.NewFetcher(&uaCapturingClient{
		inner:      mock,
		capturedUA: &capturedUA,
	}, "VortexBot/1.0")

	f.Fetch(context.Background(), "https://example.com")

	if capturedUA != "VortexBot/1.0" {
		t.Errorf("User-Agent = %q, want %q", capturedUA, "VortexBot/1.0")
	}
}

type uaCapturingClient struct {
	inner      *mockHTTPClient
	capturedUA *string
}

func (c *uaCapturingClient) Do(req *http.Request) (*http.Response, error) {
	*c.capturedUA = req.Header.Get("User-Agent")
	return c.inner.Do(req)
}
