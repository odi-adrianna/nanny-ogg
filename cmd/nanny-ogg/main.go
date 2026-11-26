// Command nanny-ogg runs the Nanny Ogg meal-planning tool server: the household
// profile and portion math plus the Mealie recipe book and meal-plan calendar,
// exposed as an OpenAPI tool server for Open WebUI.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/odi-adrianna/nanny-ogg/internal/mealie"
	"github.com/odi-adrianna/nanny-ogg/internal/plan"
	"github.com/odi-adrianna/nanny-ogg/internal/server"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	baseURL := os.Getenv("MEALIE_BASE_URL")
	token := os.Getenv("MEALIE_API_TOKEN")
	if baseURL == "" || token == "" {
		logger.Error("MEALIE_BASE_URL and MEALIE_API_TOKEN are required")
		os.Exit(1)
	}

	prof, err := plan.Load(os.Getenv("PROFILE_FILE"))
	if err != nil {
		logger.Error("load profile", "err", err)
		os.Exit(1)
	}

	loc, err := time.LoadLocation(envOr("TZ", "Europe/Warsaw"))
	if err != nil {
		logger.Error("load time zone", "err", err)
		os.Exit(1)
	}

	cfg := server.Config{
		APIKey:      os.Getenv("TOOLS_API_KEY"),
		AllowOrigin: envOr("CORS_ALLOW_ORIGIN", "*"),
		Location:    loc,
	}

	addr := ":" + envOr("PORT", "8080")
	srv := &http.Server{
		Addr:              addr,
		Handler:           server.New(logger, mealie.New(baseURL, token), prof, cfg),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "err", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "err", err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
