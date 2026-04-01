package robots

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRobotsFetcherFetch(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantBody   string
		wantNil    bool
		wantErr    error // sentinel to check with errors.Is
		wantAnyErr bool  // for non-sentinel errors
	}{
		{
			name:       "200 returns body",
			statusCode: 200,
			body:       "User-agent: *\nDisallow: /private",
			wantBody:   "User-agent: *\nDisallow: /private",
		},
		{
			name:       "404 returns nil nil",
			statusCode: 404,
			wantNil:    true,
		},
		{
			name:       "403 returns ErrAccessDenied",
			statusCode: 403,
			wantErr:    ErrAccessDenied,
		},
		{
			name:       "401 returns ErrAccessDenied",
			statusCode: 401,
			wantErr:    ErrAccessDenied,
		},
		{
			name:       "500 returns ErrServerError",
			statusCode: 500,
			wantErr:    ErrServerError,
		},
		{
			name:       "503 returns ErrServerError",
			statusCode: 503,
			wantErr:    ErrServerError,
		},
		{
			name:       "302 returns unexpected status error",
			statusCode: 302,
			wantAnyErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				if tt.body != "" {
					io.WriteString(w, tt.body)
				}
			}))
			defer server.Close()

			f := NewFetcher(server.Client(), "TestBot")
			data, err := f.Fetch(context.Background(), server.URL+"/robots.txt")

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}

			if tt.wantAnyErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.wantNil {
				if data != nil {
					t.Errorf("expected nil data, got %q", data)
				}
				return
			}

			if string(data) != tt.wantBody {
				t.Errorf("body = %q, want %q", string(data), tt.wantBody)
			}
		})
	}
}

func TestRobotsFetcherSetsUserAgent(t *testing.T) {
	var capturedUA string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedUA = r.Header.Get("User-Agent")
		w.WriteHeader(200)
	}))
	defer server.Close()

	f := NewFetcher(server.Client(), "VortexBot/1.0")
	f.Fetch(context.Background(), server.URL+"/robots.txt")

	if capturedUA != "VortexBot/1.0" {
		t.Errorf("User-Agent = %q, want %q", capturedUA, "VortexBot/1.0")
	}
}

func TestRobotsFetcherClientError(t *testing.T) {
	f := NewFetcher(&http.Client{}, "TestBot")
	// Use an unreachable URL to trigger a client-level error
	_, err := f.Fetch(context.Background(), "http://127.0.0.1:0/robots.txt")
	if err == nil {
		t.Fatal("expected error for unreachable server, got nil")
	}
}
