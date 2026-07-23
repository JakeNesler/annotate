package main

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/JakeNesler/annotate/internal/review"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	webRoot := os.Getenv("WEB_ROOT")
	if webRoot == "" {
		webRoot = "."
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	server := &http.Server{
		Addr:              ":" + port,
		Handler:           review.NewServer(review.NewStore(24*time.Hour, 200), webRoot, logger).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	logger.Info("review server listening", "address", server.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("review server stopped", "error", err)
		os.Exit(1)
	}
}
