package infra

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

func SetupLogger(logDir string) (*slog.Logger, func(), error) {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := os.MkdirAll(logDir, 0755); err != nil {
		return logger, func() {}, err
	}

	logPath := filepath.Join(logDir, "vortex.log")
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0666)
	if err != nil {
		return logger, func() {}, err
	}

	cleanupFunc := func() {
		if err := file.Close(); err != nil {
			logger.Error("Failed to close log file", "error", err)
		}
	}

	multiWriter := io.MultiWriter(os.Stdout, file)
	logger = slog.New(slog.NewJSONHandler(multiWriter, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	return logger, cleanupFunc, nil
}
