package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
	"vortex/internal/config"
	"vortex/internal/infra"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/pgvector/pgvector-go"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Server struct {
	db          *pgxpool.Pool
	embedderURL string
	timeout     time.Duration
	logger      *slog.Logger
}

type SearchResult struct {
	ChunkText string  `json:"chunk_text"`
	URL       string  `json:"url"`
	Distance  float64 `json:"distance"`
}

func main() {
	const logDir = "logs"
	logger, cleanupFunc, err := infra.SetupLogger(logDir)
	if err != nil {
		logger.Error("Failed to set up logger", "error", err)
		os.Exit(1)
	}
	defer cleanupFunc()

	if err := godotenv.Load(); err != nil {
		logger.Warn("No .env file found, using environment variables")
	}

	cfg, err := config.Load()
	if err != nil {
		logger.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	conn, err := pgxpool.New(ctx, cfg.Search.PostgresURL)
	if err != nil {
		logger.Error("failed to connect to Postgres", "error", err)
		os.Exit(1)
	}
	defer conn.Close()

	mux := http.NewServeMux()

	server := &Server{db: conn, embedderURL: cfg.Search.EmbedderURL, timeout: cfg.Search.Timeout, logger: logger}
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("GET /health", server.healthHandler)

	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.Dir("cmd/search/static"))))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "cmd/search/static/index.html")
	})

	limiter := NewIPRateLimiter(float64(cfg.Search.RateLimit), cfg.Search.RateBurst)
	mux.HandleFunc("GET /search", limiter.Middleware(server.searchHandler))

	httpServer := &http.Server{Addr: ":" + cfg.Search.Port, Handler: mux}

	go func() {
		<-ctx.Done()
		logger.Info("Shutting down search server...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		httpServer.Shutdown(shutdownCtx)
	}()

	logger.Info("Starting search server", "port", cfg.Search.Port)
	if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
		logger.Error("Failed to start search server", "error", err)
		os.Exit(1)
	}
	logger.Info("Search server stopped")
}

func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := s.db.Ping(ctx); err != nil {
		s.logger.Error("Health check failed", "error", err)
		http.Error(w, "postgres unavailable", http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (s *Server) searchHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() {
		SearchRequestDurationSeconds.Observe(time.Since(start).Seconds())
	}()

	q := r.URL.Query().Get("q")
	if q == "" {
		SearchRequestsTotal.WithLabelValues("error").Inc()
		http.Error(w, "missing query parameter 'q'", http.StatusBadRequest)
		return
	}
	if len(q) > 2000 {
		SearchRequestsTotal.WithLabelValues("error").Inc()
		http.Error(w, "query too long", http.StatusBadRequest)
		return
	}

	s.logger.Info("Received search request", "query", q)

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()

	embedding, err := s.embed(ctx, q)
	if err != nil {
		SearchRequestsTotal.WithLabelValues("error").Inc()
		s.logger.Error("Failed to get embedding", "error", err)
		http.Error(w, "failed to get embedding", http.StatusInternalServerError)
		return
	}

	limit := 10
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil {
			limit = l
			if limit < 1 {
				limit = 1
			} else if limit > 30 {
				limit = 30
			}
		}
	}

	results, err := s.search(ctx, embedding, limit)
	if err != nil {
		SearchRequestsTotal.WithLabelValues("error").Inc()
		s.logger.Error("Failed to search database", "error", err)
		http.Error(w, "failed to search database", http.StatusInternalServerError)
		return
	}

	SearchRequestsTotal.WithLabelValues("success").Inc()
	s.logger.Info("Search completed", "query", q, "results", len(results))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(results)
}

func (s *Server) embed(ctx context.Context, text string) ([]float32, error) {
	embedStart := time.Now()
	defer func() {
		SearchEmbedDurationSeconds.Observe(time.Since(embedStart).Seconds())
	}()

	reqJSON, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", s.embedderURL, bytes.NewReader(reqJSON))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	var embedResp struct {
		Embedding []float64 `json:"embedding"`
	}
	err = json.NewDecoder(resp.Body).Decode(&embedResp)
	if err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	embedding := make([]float32, len(embedResp.Embedding))
	for i, v := range embedResp.Embedding {
		embedding[i] = float32(v)
	}

	return embedding, nil
}

func (s *Server) search(ctx context.Context, embedding []float32, limit int) ([]SearchResult, error) {
	vec := pgvector.NewVector(embedding)

	pgQuery := `
		SELECT c.chunk_text, a.url, c.embedding <=> $1 AS distance
		FROM chunks c
		JOIN articles a ON a.id = c.article_id
		ORDER BY c.embedding <=> $1
		LIMIT $2;
	`

	rows, err := s.db.Query(ctx, pgQuery, vec, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query database: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var chunkText, url string
		var distance float64
		err := rows.Scan(&chunkText, &url, &distance)
		if err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		results = append(results, SearchResult{
			ChunkText: chunkText,
			URL:       url,
			Distance:  distance,
		})
	}
	return results, nil
}
