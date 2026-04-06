package infra

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func StartMetricsServer(ctx context.Context, wg *sync.WaitGroup, port string) {
	metricsMux := http.NewServeMux()
	metricsServer := &http.Server{
		Addr:              ":" + port,
		Handler:           metricsMux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	metricsMux.Handle("/metrics", promhttp.Handler())
	wg.Add(1)
	go func() {
		defer wg.Done()
		slog.Info("Starting metrics server", "port", port)
		if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("Failed to start metrics server", "error", err)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		slog.Info("Shutting down metrics server...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := metricsServer.Shutdown(shutdownCtx); err != nil {
			slog.Error("Failed to gracefully shut down metrics server", "error", err)
		} else {
			slog.Info("Metrics server stopped gracefully")
		}
	}()
}
